package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
)

type Network interface {
	Apply(context.Context, model.ServerState) (model.ServerPrivilegedStatus, error)
	Status(context.Context) (model.ServerPrivilegedStatus, error)
}

// Service owns every business write and publishes one immutable state/network view.
type Service struct {
	operation     sync.Mutex
	store         *Store
	network       Network
	logger        *slog.Logger
	view          atomic.Pointer[serverSnapshot]
	configChanges *notify.Change
	adminChanges  *notify.Change
}

func OpenService(dir string, network Network, logger *slog.Logger) (*Service, error) {
	store, state, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	service := &Service{
		store: store, network: network, logger: logger,
		configChanges: notify.New(), adminChanges: notify.New(),
	}
	service.view.Store(&serverSnapshot{state: state})
	return service, nil
}

func (s *Service) Snapshot() model.ServerState {
	return cloneState(s.view.Load().state)
}

func (s *Service) snapshot() serverSnapshot {
	current := s.view.Load()
	view := serverSnapshot{
		state: cloneState(current.state), network: cloneServerNetworkSnapshot(current.network),
		activity: maps.Clone(current.activity),
	}
	if view.network.Fresh && !view.network.RefreshedAt.IsZero() &&
		time.Since(view.network.RefreshedAt) > networkSnapshotMaxAge {
		view.network.Fresh = false
		view.network.LastError = model.NewError(
			model.ErrorUnavailable, "server network status is stale", true,
		)
	}
	return view
}

func (s *Service) NetworkSnapshot() model.ServerNetworkSnapshot {
	return s.snapshot().network
}

func (s *Service) publish(view serverSnapshot) {
	previous := s.view.Load()
	view.activity = observeDeviceActivity(*previous, view)
	s.view.Store(&view)
	for _, device := range view.state.Devices {
		before := deviceConnection(device.PublicKey, *previous, previous.network.RefreshedAt)
		after := deviceConnection(device.PublicKey, view, view.network.RefreshedAt)
		if before != after {
			s.logger.Info("device connection state changed", "device_id", device.ID,
				"previous_state", before, "state", after, "idle_timeout", deviceIdleTimeout.String())
		}
	}
	if configurationSnapshotCursor(*previous) != configurationSnapshotCursor(view) {
		s.configChanges.Notify()
	}
	if semanticSnapshotCursor(*previous) != semanticSnapshotCursor(view) {
		s.adminChanges.Notify()
	}
}

func (s *Service) apply(ctx context.Context, state model.ServerState) (model.ServerNetworkSnapshot, error) {
	if s.network == nil {
		return model.ServerNetworkSnapshot{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, err := s.network.Apply(ctx, state)
	return s.networkResult(status, err), err
}

func (s *Service) networkResult(status model.ServerPrivilegedStatus, err error) model.ServerNetworkSnapshot {
	previous := s.view.Load().network
	snapshot := model.ServerNetworkSnapshot{
		Network: status, Fresh: err == nil, RefreshedAt: time.Now().UTC(),
	}
	if err != nil {
		snapshot.Network = cloneServerNetworkSnapshot(s.view.Load().network).Network
		snapshot.LastError = model.PublicError(err)
		s.logger.Error("server network operation failed", logging.ErrorAttrs(err)...)
	} else if !previous.Fresh || previous.Network.Active != status.Active ||
		previous.Network.Degraded != status.Degraded || previous.Network.Interface != status.Interface ||
		previous.Network.ListenPort != status.ListenPort || len(previous.Network.Peers) != len(status.Peers) {
		s.logger.Info("server network status changed", "active", status.Active, "degraded", status.Degraded,
			"interface", status.Interface, "listen_port", status.ListenPort, "peer_count", len(status.Peers))
	}
	return snapshot
}

func (s *Service) commit(ctx context.Context, next model.ServerState) error {
	previous := s.Snapshot()
	network, err := s.apply(ctx, next)
	if err != nil {
		rollback, rollbackErr := s.apply(context.WithoutCancel(ctx), previous)
		s.publish(serverSnapshot{state: previous, network: rollback})
		return errors.Join(err, rollbackErr)
	}
	committed, err := s.store.save(next)
	if committed {
		s.publish(serverSnapshot{state: next, network: network})
		return err
	}
	rollback, rollbackErr := s.apply(context.WithoutCancel(ctx), previous)
	s.publish(serverSnapshot{state: previous, network: rollback})
	return errors.Join(err, rollbackErr)
}

func (s *Service) Reconcile(ctx context.Context) error {
	s.operation.Lock()
	defer s.operation.Unlock()
	state := s.Snapshot()
	network, err := s.apply(ctx, state)
	s.publish(serverSnapshot{state: state, network: network})
	return err
}

func (s *Service) refreshNetwork(ctx context.Context) {
	s.operation.Lock()
	defer s.operation.Unlock()
	statusContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	status, err := s.network.Status(statusContext)
	cancel()
	network := s.networkResult(status, err)
	state := s.Snapshot()
	if err == nil && (!status.Active || status.Degraded) {
		network, _ = s.apply(ctx, state)
	}
	s.publish(serverSnapshot{state: state, network: network})
}

func (s *Service) Run(ctx context.Context) {
	if s.network == nil {
		return
	}
	s.refreshNetwork(ctx)
	statusTicker := time.NewTicker(5 * time.Second)
	defer statusTicker.Stop()
	reconcileTicker := time.NewTicker(time.Minute)
	defer reconcileTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-statusTicker.C:
			s.refreshNetwork(ctx)
		case <-reconcileTicker.C:
			_ = s.Reconcile(ctx)
		}
	}
}

func (s *Service) createDevice(ctx context.Context, name, publicKey string) (model.Device, bool, error) {
	current := s.Snapshot()
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return model.Device{}, false, model.NewError(
			model.ErrorInvalidArgument, "device name must contain 1-64 bytes", false,
		)
	}
	publicKey = strings.TrimSpace(publicKey)
	if err := ValidatePublicKey(publicKey); err != nil {
		return model.Device{}, false, model.WrapError(
			model.ErrorInvalidArgument, "invalid device public key", false, err,
		)
	}
	for _, device := range current.Devices {
		if device.PublicKey == publicKey {
			return device, false, nil
		}
	}
	address, err := allocateLowestAddress(current.Settings.OverlayCIDR, current.Devices)
	if err != nil {
		return model.Device{}, false, model.WrapError(
			model.ErrorFailedPrecondition, "overlay address pool is exhausted", false, err,
		)
	}
	id, err := newID()
	if err != nil {
		return model.Device{}, false, fmt.Errorf("generate device ID: %w", err)
	}
	now := time.Now().UTC()
	device := model.Device{
		ID: id, Name: name, PublicKey: publicKey, OverlayAddress: address,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	current.Devices = append(current.Devices, device)
	if err := s.commit(ctx, current); err != nil {
		return model.Device{}, false, err
	}
	return device, true, nil
}

func (s *Service) RegisterDevice(ctx context.Context, name, publicKey string) (model.DeviceRegistration, bool, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	device, created, err := s.createDevice(ctx, name, publicKey)
	if err != nil {
		return model.DeviceRegistration{}, false, err
	}
	configuration, err := s.clientConfiguration(device.ID)
	if err != nil {
		return model.DeviceRegistration{}, false, err
	}
	logging.FromContext(ctx, s.logger).Info("device registration completed",
		"device_id", device.ID, "device_name", model.SafeText(device.Name),
		"address", device.OverlayAddress, "created", created)
	return model.DeviceRegistration{Device: device, Configuration: configuration}, created, nil
}

func (s *Service) clientConfiguration(deviceID string) (model.ClientConfiguration, error) {
	view := s.snapshot()
	if !view.network.Fresh {
		return model.ClientConfiguration{}, model.WrapError(
			model.ErrorUnavailable, "server network status is stale", true, view.network.LastError,
		)
	}
	device, err := FindDevice(view.state, deviceID)
	if err != nil {
		return model.ClientConfiguration{}, model.NewError(
			model.ErrorDeviceRevoked, "device was not found", false,
		)
	}
	return ClientConfiguration(view.state, device, view.network.Network)
}

func (s *Service) UpdateNetworks(ctx context.Context, update NetworkUpdate) (model.ServerState, error) {
	s.operation.Lock()
	defer s.operation.Unlock()
	current := s.Snapshot()
	next := cloneState(current)
	if update.OverlayCIDR == nil && update.ListenPort == nil && update.LANCIDRs == nil {
		return model.ServerState{}, model.NewError(model.ErrorInvalidArgument, "network update is empty", false)
	}
	if update.OverlayCIDR != nil {
		next.Settings.OverlayCIDR = *update.OverlayCIDR
	}
	if update.ListenPort != nil {
		next.Settings.ListenPort = *update.ListenPort
	}
	if update.LANCIDRs != nil {
		next.Settings.LANCIDRs = slices.Clone(*update.LANCIDRs)
	}
	if err := ValidateSettings(next.Settings); err != nil {
		return model.ServerState{}, model.WrapError(model.ErrorInvalidArgument, "invalid network settings", false, err)
	}
	overlayChanged := next.Settings.OverlayCIDR != current.Settings.OverlayCIDR
	if !overlayChanged && next.Settings.ListenPort == current.Settings.ListenPort &&
		slices.Equal(next.Settings.LANCIDRs, current.Settings.LANCIDRs) {
		return current, nil
	}
	if overlayChanged {
		addresses, err := AllocateAddresses(next.Settings.OverlayCIDR, len(next.Devices))
		if err != nil {
			return model.ServerState{}, model.WrapError(
				model.ErrorFailedPrecondition, "overlay cannot accommodate registered devices", false, err,
			)
		}
		now := time.Now().UTC()
		for index := range next.Devices {
			next.Devices[index].OverlayAddress = addresses[index]
			next.Devices[index].UpdatedAt = now
		}
	}
	if err := s.commit(ctx, next); err != nil {
		return model.ServerState{}, err
	}
	logging.FromContext(ctx, s.logger).Info("server network settings updated",
		"overlay_cidr", next.Settings.OverlayCIDR, "listen_port", next.Settings.ListenPort,
		"lan_cidrs", next.Settings.LANCIDRs)
	return cloneState(next), nil
}
