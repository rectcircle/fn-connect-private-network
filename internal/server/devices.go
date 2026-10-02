package server

import (
	"context"
	"slices"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	networkSnapshotMaxAge = 15 * time.Second
	deviceIdleTimeout     = 90 * time.Second
)

// Observation state stays in memory, separate from the registered device record.
type deviceActivity struct {
	received uint64
	present  bool
	since    time.Time
	lastSeen time.Time
}

type deviceConnectionState string

const (
	deviceConnected    deviceConnectionState = "connected"
	deviceDisconnected deviceConnectionState = "disconnected"
	deviceUnknown      deviceConnectionState = "unknown"
)

type adminDevice struct {
	model.Device
	ConnectionState deviceConnectionState `json:"connectionState"`
}

func networkObservable(snapshot model.ServerNetworkSnapshot, now time.Time) bool {
	return snapshot.Fresh && !snapshot.RefreshedAt.IsZero() &&
		!now.Before(snapshot.RefreshedAt) && now.Sub(snapshot.RefreshedAt) <= networkSnapshotMaxAge &&
		snapshot.LastError == nil && snapshot.Network.LastError == nil &&
		snapshot.Network.Active && !snapshot.Network.Degraded
}

func observeDeviceActivity(previous, current serverSnapshot) map[string]deviceActivity {
	now := current.network.RefreshedAt
	if !networkObservable(current.network, now) {
		return nil
	}
	continuous := networkObservable(previous.network, now) &&
		previous.network.Network.Interface == current.network.Network.Interface &&
		previous.network.Network.PublicKey == current.network.Network.PublicKey
	peers := make(map[string]model.PrivilegedPeerStatus, len(current.network.Network.Peers))
	for _, peer := range current.network.Network.Peers {
		peers[peer.PublicKey] = peer
	}
	observations := make(map[string]deviceActivity, len(current.state.Devices))
	for _, device := range current.state.Devices {
		peer, present := peers[device.PublicKey]
		activity, exists := previous.activity[device.PublicKey]
		if !continuous || !exists || present != activity.present || peer.ReceiveBytes < activity.received {
			activity = deviceActivity{since: now}
		} else if peer.ReceiveBytes > activity.received {
			activity.lastSeen = now
		}
		activity.received, activity.present = peer.ReceiveBytes, present
		observations[device.PublicKey] = activity
	}
	return observations
}

func deviceConnection(publicKey string, view serverSnapshot, now time.Time) deviceConnectionState {
	if !networkObservable(view.network, now) {
		return deviceUnknown
	}
	activity, exists := view.activity[publicKey]
	if !exists {
		return deviceUnknown
	}
	if !activity.lastSeen.IsZero() {
		if now.Sub(activity.lastSeen) <= deviceIdleTimeout {
			return deviceConnected
		}
	} else if now.Sub(activity.since) <= deviceIdleTimeout {
		return deviceUnknown
	}
	return deviceDisconnected
}

func deviceViews(view serverSnapshot, now time.Time) []adminDevice {
	current := slices.Clone(view.state.Devices)
	for index := range current {
		current[index].LastHandshake = nil
		current[index].ReceiveBytes, current[index].TransmitBytes = 0, 0
	}
	if view.network.Fresh {
		applyPeerStatus(current, view.network.Network.Peers)
	}
	devices := make([]adminDevice, 0, len(current))
	for _, device := range current {
		devices = append(devices, adminDevice{
			Device: device, ConnectionState: deviceConnection(device.PublicKey, view, now),
		})
	}
	return devices
}

func (s *Service) DeleteDevice(ctx context.Context, id string) (failure error) {
	defer func() {
		if failure != nil {
			failure = model.WithOperation(failure, "device.delete")
		}
	}()
	s.operation.Lock()
	defer s.operation.Unlock()
	current := s.Snapshot()
	index := slices.IndexFunc(current.Devices, func(device model.Device) bool { return device.ID == id })
	if index < 0 {
		return model.NewError(model.ErrorNotFound, "device was not found", false)
	}
	if s.network == nil {
		return model.NewError(model.ErrorUnavailable, "device connection state is unavailable", true)
	}
	// A cached admin snapshot cannot authorize a destructive operation.
	statusContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	status, err := s.network.Status(statusContext)
	cancel()
	if err == nil && status.LastError != nil {
		err = status.LastError
	}
	network := s.networkResult(status, err)
	s.publish(serverSnapshot{state: current, network: network})
	if err != nil {
		return err
	}
	device := current.Devices[index]
	switch deviceConnection(device.PublicKey, s.snapshot(), time.Now()) {
	case deviceConnected:
		return model.NewError(model.ErrorFailedPrecondition,
			"device is online; disconnect it and wait for 90 seconds without receive activity before deleting", false)
	case deviceUnknown:
		return model.NewError(model.ErrorUnavailable, "device connection state is unknown; deletion is not allowed", true)
	}
	next := cloneState(current)
	next.Devices = slices.Delete(next.Devices, index, index+1)
	if err := s.commit(ctx, next); err != nil {
		return err
	}
	logging.FromContext(ctx, s.logger).Info("device deleted", "device_id", id,
		"device_name", model.SafeText(device.Name), "address", device.OverlayAddress)
	return nil
}
