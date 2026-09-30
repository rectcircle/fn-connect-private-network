package linux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const defaultInterfaceName = "fncpn0"

type keyStore interface {
	LoadOrCreate() (wgtypes.Key, error)
}

type deviceManager interface {
	Available() bool
	Apply(context.Context, string, string, wgtypes.Key, model.ServerPlan) error
	Remove(context.Context, string, string) error
	PeerStatus(context.Context, string) ([]model.PrivilegedPeerStatus, error)
}

type forwardingManager interface {
	Available() bool
	Enable(context.Context) (bool, error)
	Set(context.Context, bool) error
}

type firewallManager interface {
	Available() bool
	Apply(context.Context, string, string, model.ServerPlan) error
	Remove(context.Context, string) error
}

type persistedServerState struct {
	Version       int    `json:"version"`
	OwnerToken    string `json:"ownerToken"`
	InterfaceName string `json:"interfaceName"`
	FirewallTable string `json:"firewallTable"`
}

type serverStateStore interface {
	Load() (*persistedServerState, error)
	Save(persistedServerState) error
	Clear() error
}

type ServerEngine struct {
	mu            sync.Mutex
	interfaceName string
	keys          keyStore
	device        deviceManager
	forwarding    forwardingManager
	firewall      firewallManager
	state         serverStateStore
	active        bool
	ownerToken    string
	firewallTable string
	publicKey     string
	listenPort    uint16
	degraded      bool
	logger        *slog.Logger
}

func NewServerEngine(
	interfaceName string,
	keys keyStore,
	device deviceManager,
	forwarding forwardingManager,
	firewall firewallManager,
	state serverStateStore,
	loggers ...*slog.Logger,
) *ServerEngine {
	if interfaceName == "" {
		interfaceName = defaultInterfaceName
	}
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &ServerEngine{
		interfaceName: interfaceName,
		keys:          keys,
		device:        device,
		forwarding:    forwarding,
		firewall:      firewall,
		state:         state,
		logger:        logger,
	}
}

func (e *ServerEngine) Available() bool {
	return e != nil &&
		e.keys != nil &&
		e.device != nil &&
		e.device.Available() &&
		e.forwarding != nil &&
		e.forwarding.Available() &&
		e.firewall != nil &&
		e.firewall.Available() &&
		e.state != nil
}

func (e *ServerEngine) Status() model.ServerPrivilegedStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	status := model.ServerPrivilegedStatus{
		Active:     e.active,
		Interface:  e.interfaceName,
		PublicKey:  e.publicKey,
		ListenPort: e.listenPort,
		Degraded:   e.degraded,
	}
	if e.active {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		peers, err := e.device.PeerStatus(ctx, e.interfaceName)
		cancel()
		if err == nil {
			status.Peers = peers
		} else {
			status.Degraded = true
			e.logger.Error("read WireGuard peer status", "error", err)
		}
	}
	return status
}

func (e *ServerEngine) Recover(ctx context.Context) error {
	if !e.Available() {
		return unavailableServerError()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	state, err := e.state.Load()
	if err != nil || state == nil {
		return err
	}
	var cleanupErrors []error
	if err := e.firewall.Remove(ctx, state.FirewallTable); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove stale firewall: %w", err))
	}
	if err := e.device.Remove(ctx, state.InterfaceName, state.OwnerToken); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove stale WireGuard device: %w", err))
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		e.degraded = true
		return err
	}
	if err := e.state.Clear(); err != nil {
		return err
	}
	e.resetLocked()
	return nil
}

func (e *ServerEngine) Apply(ctx context.Context, plan model.ServerPlan) error {
	normalized, err := wgconfig.NormalizeServerPlan(plan)
	if err != nil {
		return err
	}
	if !e.Available() {
		return unavailableServerError()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.degraded {
		if err := e.removeLocked(ctx); err != nil {
			return err
		}
	}
	if e.active {
		return e.updateLocked(ctx, normalized)
	}
	e.degraded = false
	return e.applyFreshLocked(ctx, normalized)
}

func (e *ServerEngine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.removeLocked(ctx)
}

func (e *ServerEngine) applyFreshLocked(
	ctx context.Context,
	plan model.ServerPlan,
) error {
	privateKey, err := e.keys.LoadOrCreate()
	if err != nil {
		return err
	}
	if planPeerKey(plan, privateKey.PublicKey().String()) != "" {
		return errors.New("server key must differ from every peer key")
	}
	ownerToken, err := newOwnerToken()
	if err != nil {
		return fmt.Errorf("generate network owner token: %w", err)
	}
	firewallTable := "fncpn_" + ownerToken[:12]
	journal := persistedServerState{
		Version:       serverStateVersion,
		OwnerToken:    ownerToken,
		InterfaceName: e.interfaceName,
		FirewallTable: firewallTable,
	}
	if err := e.state.Save(journal); err != nil {
		return fmt.Errorf("persist network cleanup intent: %w", err)
	}
	if err := e.device.Apply(ctx, e.interfaceName, ownerToken, privateKey, plan); err != nil {
		cleanupErr := e.device.Remove(
			context.Background(),
			e.interfaceName,
			ownerToken,
		)
		return e.rollbackFailure("apply WireGuard device", err, cleanupErr)
	}
	_, err = e.forwarding.Enable(ctx)
	if err != nil {
		cleanupErr := e.device.Remove(context.Background(), e.interfaceName, ownerToken)
		return e.rollbackFailure("enable IP forwarding", err, cleanupErr)
	}
	if err := e.firewall.Apply(ctx, firewallTable, e.interfaceName, plan); err != nil {
		firewallErr := e.firewall.Remove(context.Background(), firewallTable)
		deviceErr := e.device.Remove(context.Background(), e.interfaceName, ownerToken)
		return e.rollbackFailure(
			"apply firewall",
			err,
			errors.Join(firewallErr, deviceErr),
		)
	}
	e.active = true
	e.ownerToken = ownerToken
	e.firewallTable = firewallTable
	e.publicKey = privateKey.PublicKey().String()
	e.listenPort = plan.ListenPort
	e.degraded = false
	return nil
}

func (e *ServerEngine) updateLocked(
	ctx context.Context,
	plan model.ServerPlan,
) error {
	privateKey, err := e.keys.LoadOrCreate()
	if err != nil {
		return err
	}
	if err := e.device.Apply(
		ctx,
		e.interfaceName,
		e.ownerToken,
		privateKey,
		plan,
	); err != nil {
		return e.updateFailure("update WireGuard device", err)
	}
	if err := e.forwarding.Set(ctx, true); err != nil {
		return e.updateFailure("restore IP forwarding", err)
	}
	if err := e.firewall.Apply(
		ctx,
		e.firewallTable,
		e.interfaceName,
		plan,
	); err != nil {
		return e.updateFailure("update firewall", err)
	}
	e.active = true
	e.publicKey = privateKey.PublicKey().String()
	e.listenPort = plan.ListenPort
	e.degraded = false
	return nil
}

func (e *ServerEngine) removeLocked(ctx context.Context) error {
	journal, loadErr := e.state.Load()
	if loadErr != nil {
		return loadErr
	}
	ownerToken := e.ownerToken
	firewallTable := e.firewallTable
	interfaceName := e.interfaceName
	if journal != nil {
		ownerToken = journal.OwnerToken
		firewallTable = journal.FirewallTable
		interfaceName = journal.InterfaceName
	}
	if ownerToken == "" || firewallTable == "" {
		if err := e.state.Clear(); err != nil {
			return err
		}
		e.resetLocked()
		return nil
	}
	var cleanupErrors []error
	if err := e.firewall.Remove(ctx, firewallTable); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove firewall: %w", err))
	}
	if err := e.device.Remove(ctx, interfaceName, ownerToken); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove WireGuard device: %w", err))
	}
	if err := errors.Join(cleanupErrors...); err != nil {
		e.degraded = true
		return err
	}
	if err := e.state.Clear(); err != nil {
		return err
	}
	e.resetLocked()
	return nil
}

func (e *ServerEngine) resetLocked() {
	e.active = false
	e.ownerToken = ""
	e.firewallTable = ""
	e.publicKey = ""
	e.listenPort = 0
	e.degraded = false
}

func planPeerKey(plan model.ServerPlan, key string) string {
	for _, peer := range plan.Peers {
		if peer.PublicKey == key {
			return key
		}
	}
	return ""
}

func serverWireGuardConfigDiff(
	device *wgtypes.Device,
	privateKey wgtypes.Key,
	plan model.ServerPlan,
) (wgtypes.Config, bool, error) {
	var config wgtypes.Config
	changed := false
	if device == nil || device.PublicKey != privateKey.PublicKey() {
		config.PrivateKey = &privateKey
		changed = true
	}
	if device == nil || device.ListenPort != int(plan.ListenPort) {
		listenPort := int(plan.ListenPort)
		config.ListenPort = &listenPort
		changed = true
	}
	if device == nil || device.FirewallMark != 0 {
		firewallMark := 0
		config.FirewallMark = &firewallMark
		changed = true
	}

	actual := make(map[wgtypes.Key]wgtypes.Peer)
	if device != nil {
		actual = make(map[wgtypes.Key]wgtypes.Peer, len(device.Peers))
		for _, peer := range device.Peers {
			actual[peer.PublicKey] = peer
		}
	}
	expected := make(map[wgtypes.Key]struct{}, len(plan.Peers))
	var zeroKey wgtypes.Key
	var zeroKeepalive time.Duration
	for _, peer := range plan.Peers {
		publicKey, err := wgtypes.ParseKey(peer.PublicKey)
		if err != nil {
			return wgtypes.Config{}, false, err
		}
		_, allowedIP, err := net.ParseCIDR(peer.Address)
		if err != nil {
			return wgtypes.Config{}, false, err
		}
		expected[publicKey] = struct{}{}
		current, exists := actual[publicKey]
		update := wgtypes.PeerConfig{PublicKey: publicKey}
		if !exists ||
			len(current.AllowedIPs) != 1 ||
			current.AllowedIPs[0].String() != peer.Address {
			update.ReplaceAllowedIPs = true
			update.AllowedIPs = []net.IPNet{*allowedIP}
		}
		if exists && current.PresharedKey != zeroKey {
			update.PresharedKey = &zeroKey
		}
		if exists && current.PersistentKeepaliveInterval != 0 {
			update.PersistentKeepaliveInterval = &zeroKeepalive
		}
		if !exists ||
			update.ReplaceAllowedIPs ||
			update.PresharedKey != nil ||
			update.PersistentKeepaliveInterval != nil {
			config.Peers = append(config.Peers, update)
			changed = true
		}
	}
	for publicKey := range actual {
		if _, ok := expected[publicKey]; !ok {
			config.Peers = append(config.Peers, wgtypes.PeerConfig{
				PublicKey: publicKey,
				Remove:    true,
			})
			changed = true
		}
	}
	return config, changed, nil
}

func wrapRollback(component string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("restore previous %s: %w", component, err)
}

func unavailableServerError() error {
	return model.NewError(
		model.ErrorUnavailable,
		"Linux server network configuration is unavailable",
		false,
	)
}

func newOwnerToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (e *ServerEngine) rollbackFailure(
	operation string,
	cause error,
	rollback error,
) error {
	if rollback == nil {
		if err := e.state.Clear(); err != nil {
			rollback = err
		}
	} else {
		e.degraded = true
	}
	result := errors.Join(
		fmt.Errorf("%s: %w", operation, cause),
		wrapRollback("network resources", rollback),
	)
	e.logger.Error(
		"server network operation failed",
		"operation",
		operation,
		"error",
		result,
	)
	return result
}

func (e *ServerEngine) updateFailure(operation string, cause error) error {
	e.degraded = true
	result := fmt.Errorf("%s: %w", operation, cause)
	e.logger.Error(
		"server network update failed",
		"operation",
		operation,
		"error",
		result,
	)
	return result
}
