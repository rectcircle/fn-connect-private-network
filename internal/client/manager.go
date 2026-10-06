package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

type Discoverer interface {
	Discover(context.Context, string) (Discovery, error)
}

type RemoteService interface {
	Bootstrap(context.Context) (Bootstrap, error)
	RegisterDevice(context.Context, string, string) (model.DeviceRegistration, error)
	Configuration(context.Context, string) (model.ClientConfiguration, error)
	LocalProbeConfiguration(
		context.Context,
		string,
	) (model.LocalProbeConfiguration, error)
}

type ConfigurationWatchResult struct {
	Changed       bool                      `json:"changed"`
	Cursor        string                    `json:"cursor"`
	Configuration model.ClientConfiguration `json:"configuration,omitempty"`
}

type ConfigurationWatcher interface {
	WatchConfiguration(
		context.Context,
		string,
		string,
	) (ConfigurationWatchResult, error)
}

type RemoteFactory func(string, []Cookie, func([]Cookie) error) (RemoteService, error)

type PrivilegedNetwork interface {
	Apply(context.Context, model.ClientPlan) (model.ClientPrivilegedStatus, error)
	Status(context.Context) (model.ClientPrivilegedStatus, error)
	Remove(context.Context) error
}

type PrivilegedLifecycleWatcher interface {
	WatchLifecycle(
		context.Context,
		uint64,
	) (privileged.WatchResult[privileged.ClientStatus], error)
}

type Bridge interface {
	Start(context.Context, string, CookieProvider, ...func([]Cookie, []Cookie) error) (string, error)
	BindPeer(uint16) error
	Stop()
	Connected() bool
	Events() <-chan error
}

type LocalProbe interface {
	Reachable(
		context.Context,
		model.LocalProbeConfiguration,
		string,
		NetworkSnapshot,
	) (bool, error)
	Snapshot() (NetworkSnapshot, error)
}

type NetworkMonitor interface {
	Events(context.Context) <-chan model.NetworkChange
}

type NetworkSnapshot struct {
	InterfaceName  string
	InterfaceIndex int
	Prefixes       []netip.Prefix
	Addresses      []netip.Addr
	HasPublicIPv6  bool
	IPv6Networks   []string
}

func (s NetworkSnapshot) Fingerprint() string {
	values := []string{s.InterfaceName, strconv.Itoa(s.InterfaceIndex)}
	for _, prefix := range s.Prefixes {
		values = append(values, prefix.String())
	}
	values = append(values, strconv.FormatBool(s.HasPublicIPv6))
	values = append(values, s.IPv6Networks...)
	slices.Sort(values[2:])
	return strings.Join(values, "\n")
}

type ManagerOptions struct {
	Store            *ConfigStore
	Discoverer       Discoverer
	Remote           RemoteFactory
	Privileged       PrivilegedNetwork
	Bridge           Bridge
	Probe            LocalProbe
	Monitor          NetworkMonitor
	Authenticator    NativeAuthenticator
	DeviceName       string
	Logger           *slog.Logger
	HandshakeTimeout time.Duration
	// IPv6RouteChecker reports whether the current device can reach an IPv6
	// target address. When nil, the default UDP route-lookup probe is used.
	IPv6RouteChecker func(context.Context, netip.Addr, int) bool
}

type Manager struct {
	mu               sync.RWMutex
	operation        sync.Mutex
	store            *ConfigStore
	discoverer       Discoverer
	remote           RemoteFactory
	privileged       PrivilegedNetwork
	bridge           Bridge
	probe            LocalProbe
	monitor          NetworkMonitor
	authenticator    NativeAuthenticator
	deviceName       string
	logger           *slog.Logger
	handshakeTimeout time.Duration
	directRetryAfter time.Time
	direct           model.DirectDiagnostics
	localProbeConfig *model.LocalProbeConfiguration
	ipv6RouteChecker func(context.Context, netip.Addr, int) bool
	status           model.ClientStatus
	maintenanceError bool
	maintenancePhase string
	statusChanges    *notify.Change
	watchCancel      context.CancelFunc
	// ignoreNetworkChangesUntil absorbs the network-change events that our own
	// tunnel bring-up triggers (creating a utun interface / adding routes changes
	// State:/Network/Global/IPv4|IPv6, which the monitor surfaces as a primary
	// network change). Without this guard, every successful reconnect immediately
	// fires another reconnect ~2ms later. Only physical-network changes where the
	// fingerprint actually differs still reconnect during this window.
	ignoreNetworkChangesUntil time.Time
}

func NewManager(options ManagerOptions) (*Manager, error) {
	if options.Store == nil ||
		options.Discoverer == nil ||
		options.Remote == nil ||
		options.Privileged == nil ||
		options.Bridge == nil ||
		options.Probe == nil {
		return nil, errors.New("client manager dependencies are required")
	}
	deviceName := options.DeviceName
	if deviceName == "" {
		deviceName, _ = os.Hostname()
	}
	if deviceName == "" {
		deviceName = "Mac"
	}
	managerLogger := options.Logger
	if managerLogger == nil {
		managerLogger = slog.Default()
	}
	handshakeTimeout := options.HandshakeTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	authenticator := options.Authenticator
	if authenticator == nil {
		authenticator = NativeSessionClient{}
	}
	ipv6RouteChecker := options.IPv6RouteChecker
	if ipv6RouteChecker == nil {
		ipv6RouteChecker = hasIPv6RouteTo
	}
	return &Manager{
		store:            options.Store,
		discoverer:       options.Discoverer,
		remote:           options.Remote,
		privileged:       options.Privileged,
		bridge:           options.Bridge,
		probe:            options.Probe,
		monitor:          options.Monitor,
		authenticator:    authenticator,
		deviceName:       deviceName,
		logger:           managerLogger,
		handshakeTimeout: handshakeTimeout,
		ipv6RouteChecker: ipv6RouteChecker,
		statusChanges:    notify.New(),
		status: model.ClientStatus{
			State:     model.ClientUnconfigured,
			UpdatedAt: time.Now().UTC(),
		},
	}, nil
}

// Start prepares status only; IPC readiness must not depend on credentials or networking.
func (m *Manager) Start() error {
	config, err := m.store.Load()
	if err != nil {
		return err
	}
	if config == nil {
		m.setStatus(model.ClientUnconfigured, "", "", nil)
		m.logger.Info("client configuration absent")
		return nil
	}
	m.setStatus(model.ClientPaused, "", "", nil)
	m.logger.Info("client configuration loaded", "fn_id", config.FNID,
		"device_id", config.DeviceID, "auto_connect", config.AutoConnect)
	return nil
}

func (m *Manager) Authorize(
	ctx context.Context,
	fnID string,
	cookies []Cookie,
) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	return m.authorize(ctx, fnID, cookies)
}

func (m *Manager) AuthorizeNative(
	ctx context.Context,
	fnID string,
	username string,
	password string,
) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	username = strings.TrimSpace(username)
	if err := validateLoginInput(username, password); err != nil {
		return err
	}
	var deviceID string
	if existing, found, loadErr := m.store.LoadNativeSession(fnID); loadErr != nil {
		return m.fail(loadErr)
	} else if found && existing.Username == username {
		deviceID = existing.DeviceID
	}
	session, err := m.authenticator.Login(ctx, fnID, username, password, deviceID, m.deviceName)
	if err != nil {
		return m.fail(err)
	}
	if err := m.store.SaveNativeSession(session); err != nil {
		return m.fail(err)
	}
	if err := m.store.SaveNativeGatewayCookies(fnID, session.Token); err != nil {
		return m.fail(err)
	}
	cookies, err := m.store.LoadCookies(fnID)
	if err != nil {
		return m.fail(err)
	}
	return m.authorize(ctx, fnID, cookies)
}

func (m *Manager) authorize(
	ctx context.Context,
	fnID string,
	cookies []Cookie,
) error {
	fnID, err := normalizeFNID(fnID)
	if err != nil {
		return model.WrapError(model.ErrorInvalidArgument, "invalid FN ID", false, err)
	}
	if len(cookies) == 0 {
		return model.NewError(
			model.ErrorAuthRequired,
			"FN Connect cookies are required",
			false,
		)
	}
	logger := logging.FromContext(ctx, m.logger).With("fn_id", fnID)
	logger.Info("authorization validation started")
	m.cancelConfigurationWatch()
	m.setStatus(model.ClientAuthorizing, "", "", nil)
	existing, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	privateKey, err := m.store.EnsureWireGuardKey(fnID)
	if err != nil {
		return m.fail(err)
	}
	candidateCookies := append([]Cookie(nil), cookies...)
	remote, err := m.remote(fnID, candidateCookies, func(updated []Cookie) error {
		candidateCookies = append(candidateCookies[:0], updated...)
		return nil
	})
	if err != nil {
		return m.fail(err)
	}
	bootstrap, err := remote.Bootstrap(ctx)
	if err != nil {
		return m.fail(err)
	}
	if !bootstrap.Administrator {
		return m.fail(model.NewError(
			model.ErrorPermissionDenied,
			"administrator access is required to register this device",
			false,
		))
	}
	logger.Info("gateway session validated", "administrator", true)
	registrationReason := "no_saved_device"
	if existing != nil && existing.FNID != fnID {
		registrationReason = "different_server"
	}
	if existing != nil && existing.FNID == fnID && existing.DeviceID != "" &&
		existing.PublicKey != privateKey.PublicKey().String() {
		registrationReason = "identity_changed"
		logger.Warn("saved device identity changed", "previous_device_id", existing.DeviceID)
	}
	if existing != nil &&
		existing.FNID == fnID &&
		existing.DeviceID != "" &&
		existing.PublicKey == privateKey.PublicKey().String() {
		configuration, err := remote.Configuration(ctx, existing.DeviceID)
		if err != nil && model.AsError(err).Code != model.ErrorDeviceRevoked {
			return m.fail(err)
		}
		if err == nil {
			configuration, _, err = selectClientConfiguration(
				existing.Configuration,
				configuration,
			)
			if err != nil {
				return m.fail(err)
			}
			existing.Configuration = configuration
			existing.AutoConnect = true
			if err := m.store.SaveCookies(fnID, candidateCookies); err != nil {
				return m.fail(err)
			}
			if err := m.store.Save(*existing); err != nil {
				return m.fail(err)
			}
			logger.Info("existing device reused", "device_id", existing.DeviceID,
				"address", configuration.ClientAddress)
			logger.Info("authorization saved", "device_id", existing.DeviceID)
			return m.connectWithConfig(ctx, *existing)
		}
		registrationReason = "device_revoked"
	}
	publicKey := privateKey.PublicKey().String()
	logger.Info("device registration requested", "reason", registrationReason, "device_name", model.SafeText(m.deviceName))
	registration, err := remote.RegisterDevice(
		ctx,
		m.deviceName,
		publicKey,
	)
	if err != nil {
		return m.fail(err)
	}
	registration, err = wgconfig.NormalizeDeviceRegistration(registration, publicKey)
	if err != nil {
		return m.fail(model.WrapError(
			model.ErrorFailedPrecondition,
			"server returned an invalid device registration",
			false,
			err,
		))
	}
	configuration, _, err := selectClientConfiguration(
		model.ClientConfiguration{},
		registration.Configuration,
	)
	if err != nil {
		return m.fail(err)
	}
	config := LocalConfig{
		Version:       localConfigVersion,
		FNID:          fnID,
		DeviceID:      registration.Device.ID,
		PublicKey:     publicKey,
		Configuration: configuration,
		AutoConnect:   true,
	}
	if err := m.store.SaveCookies(fnID, candidateCookies); err != nil {
		return m.fail(err)
	}
	if err := m.store.Save(config); err != nil {
		return m.fail(err)
	}
	logger.Info("device registration completed", "device_id", config.DeviceID,
		"address", configuration.ClientAddress)
	logger.Info("authorization saved", "device_id", config.DeviceID)
	return m.connectWithConfig(ctx, config)
}

func (m *Manager) Connect(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	m.directRetryAfter = time.Time{}
	return m.connect(ctx)
}

func (m *Manager) connect(ctx context.Context) error {
	m.cancelConfigurationWatch()
	config, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	if config == nil || config.DeviceID == "" {
		err := model.NewError(
			model.ErrorAuthRequired,
			"FN Connect authorization is required",
			false,
		)
		return m.fail(err)
	}
	config.AutoConnect = true
	if err := m.store.Save(*config); err != nil {
		return m.fail(err)
	}
	return m.connectWithConfig(ctx, *config)
}

func (m *Manager) Disconnect(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	return m.disconnect(ctx)
}

func (m *Manager) disconnect(ctx context.Context) error {
	logger := logging.FromContext(ctx, m.logger)
	logger.Info("client disconnect requested")
	m.setDirectDiagnostics(model.DirectDiagnostics{})
	config, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	if config != nil {
		config.AutoConnect = false
		if err := m.store.Save(*config); err != nil {
			return m.fail(err)
		}
	}
	m.cancelConfigurationWatch()
	m.bridge.Stop()
	m.localProbeConfig = nil
	if err := m.privileged.Remove(ctx); err != nil {
		typed := model.AsError(err)
		m.logger.Error(
			"disconnect network cleanup failed",
			"code",
			typed.Code,
			"retryable",
			typed.Retryable,
			"error",
			err,
		)
		state := model.ClientPaused
		if config == nil {
			state = model.ClientUnconfigured
		}
		m.setStatus(state, "", "", typed)
		return err
	}
	if config != nil {
		m.setStatus(model.ClientPaused, "", "", nil)
	} else {
		m.setStatus(model.ClientUnconfigured, "", "", nil)
	}
	logger.Info("client disconnected", "state", m.Status().State)
	return nil
}

func (m *Manager) convergeStoppedNetwork(
	ctx context.Context,
	config LocalConfig,
	status model.ClientStatus,
) {
	network, statusErr := m.privileged.Status(ctx)
	if statusErr != nil {
		m.recordStoppedCleanupError(config, status, statusErr)
		return
	}
	if !network.Active && !network.Degraded {
		if status.State == model.ClientPaused && status.LastError != nil {
			m.setStatus(
				model.ClientPaused,
				"",
				"",
				nil,
			)
		}
		return
	}
	if err := m.privileged.Remove(ctx); err != nil {
		m.recordStoppedCleanupError(config, status, err)
		return
	}
	if status.State == model.ClientPaused {
		m.setStatus(
			model.ClientPaused,
			"",
			"",
			nil,
		)
	}
}

func (m *Manager) recordStoppedCleanupError(
	config LocalConfig,
	status model.ClientStatus,
	err error,
) {
	typed := model.AsError(err)
	if status.LastError == nil ||
		status.LastError.Code != typed.Code ||
		status.LastError.Message != typed.Message {
		m.logger.Error(
			"converge stopped network cleanup",
			"code",
			typed.Code,
			"retryable",
			typed.Retryable,
			"error",
			err,
		)
	}
	if status.State == model.ClientPaused {
		m.setStatus(
			model.ClientPaused,
			"",
			"",
			typed,
		)
	}
}

func (m *Manager) Retry(ctx context.Context) error {
	return m.Connect(ctx)
}

func (m *Manager) Logout(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	config, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	if err := m.disconnect(ctx); err != nil {
		return err
	}
	if config != nil {
		if err := errors.Join(
			m.store.ClearCookies(config.FNID),
			m.store.ClearNativeSession(config.FNID),
		); err != nil {
			return m.fail(err)
		}
		logging.FromContext(ctx, m.logger).Info("client logged out",
			"fn_id", config.FNID, "device_id", config.DeviceID, "identity_retained", true)
		m.setStatus(
			model.ClientAuthRequired,
			"",
			"",
			nil,
		)
	}
	return nil
}

func (m *Manager) Forget(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	m.cancelConfigurationWatch()
	m.setDirectDiagnostics(model.DirectDiagnostics{})
	config, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	m.bridge.Stop()
	m.localProbeConfig = nil
	if err := m.privileged.Remove(ctx); err != nil {
		return m.fail(err)
	}
	if config != nil {
		if err := m.store.Forget(config.FNID); err != nil {
			return m.fail(err)
		}
		logging.FromContext(ctx, m.logger).Info("client identity forgotten",
			"fn_id", config.FNID, "device_id", config.DeviceID)
	}
	m.setStatus(model.ClientUnconfigured, "", "", nil)
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	m.cancelConfigurationWatch()
	m.bridge.Stop()
	m.localProbeConfig = nil
	return m.privileged.Remove(ctx)
}

func (m *Manager) Run(ctx context.Context) {
	m.resume(ctx)
	if ctx.Err() != nil {
		return
	}
	previous := m.networkFingerprint()
	maintenanceTicker := time.NewTicker(60 * time.Second)
	defer maintenanceTicker.Stop()
	bridgeEvents := m.bridge.Events()
	privilegedEvents := m.privilegedLifecycleEvents(ctx)
	go m.watchConfiguration(ctx)
	var networkEvents <-chan model.NetworkChange
	if m.monitor != nil {
		networkEvents = m.monitor.Events(ctx)
	}
	var debounce <-chan time.Time
	var pendingNetworkChange model.NetworkChange
	var readinessEvents <-chan networkReadinessResult
	var stopReadiness func()
	// startReadiness starts a network-readiness wait unless one is already in
	// progress. Reusing an in-flight wait prevents a physical network change that
	// is reported multiple times (macOS emits several SCDynamicStore callbacks for
	// one switch) from repeatedly tearing down and restarting the wait, which
	// would otherwise cause several reconnect cycles within seconds. The in-flight
	// probe re-evaluates `networkReady` on every tick, so it automatically reflects
	// the latest network state and restores the link once ready.
	startReadiness := func() {
		if readinessEvents != nil {
			return
		}
		readinessEvents, stopReadiness = m.beginNetworkReadiness(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case result := <-readinessEvents:
			readinessEvents = nil
			if stopReadiness != nil {
				stopReadiness()
				stopReadiness = nil
			}
			if result.ready {
				m.restoreAfterNetworkReady(ctx)
			} else {
				m.markNetworkUnavailable()
			}
		case err := <-bridgeEvents:
			if err == nil {
				m.mu.RLock()
				relayError := m.maintenanceError && m.maintenancePhase == "relay"
				m.mu.RUnlock()
				if relayError {
					m.clearMaintenanceError()
				}
				continue
			}
			if model.AsError(err).Code == model.ErrorAuthRequired {
				m.operation.Lock()
				config, loadErr := m.store.Load()
				recovered := false
				if loadErr == nil && config != nil {
					recovered, loadErr = m.recoverNativeSession(ctx, config.FNID, true)
				}
				if loadErr == nil && recovered {
					m.logger.Info("relay credentials recovered; reconnecting", "fn_id", config.FNID)
					m.bridge.Stop()
					if cleanupErr := m.privileged.Remove(ctx); cleanupErr != nil {
						loadErr = cleanupErr
					} else {
						loadErr = m.reconnect(ctx, *config)
					}
				}
				m.operation.Unlock()
				if loadErr == nil && recovered {
					continue
				}
				if loadErr != nil {
					err = loadErr
				}
			}
			if model.AsError(err).Retryable {
				m.recordMaintenanceError("relay", err)
				continue
			}
			m.operation.Lock()
			m.bridge.Stop()
			m.localProbeConfig = nil
			if cleanupErr := m.privileged.Remove(ctx); cleanupErr != nil {
				m.logger.Error(
					"clean up network after relay failure",
					"code",
					model.AsError(cleanupErr).Code,
					"error",
					cleanupErr,
				)
			}
			_ = m.fail(err)
			m.operation.Unlock()
		case change, ok := <-networkEvents:
			if !ok {
				networkEvents = nil
				continue
			}
			pendingNetworkChange.PrimaryNetworkChanged =
				pendingNetworkChange.PrimaryNetworkChanged ||
					change.PrimaryNetworkChanged
			debounce = time.After(time.Second)
		case <-debounce:
			debounce = nil
			change := pendingNetworkChange
			pendingNetworkChange = model.NetworkChange{}
			if m.handleNetworkChange(ctx, &previous, change.PrimaryNetworkChanged) {
				startReadiness()
			}
		case <-maintenanceTicker.C:
			m.maintainHealth(ctx)
			if m.handleNetworkChange(ctx, &previous, false) {
				startReadiness()
			}
		case _, ok := <-privilegedEvents:
			if !ok {
				privilegedEvents = nil
				continue
			}
			m.maintainPrivileged(ctx)
		}
	}
}

func (m *Manager) resume(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil || m.Status().State != model.ClientPaused {
		return
	}
	// Re-read after taking the operation lock so a concurrent disconnect wins.
	config, err := m.store.Load()
	if err != nil {
		_ = m.fail(err)
		return
	}
	if config == nil {
		return
	}
	if config.AutoConnect {
		m.logger.Info("automatic connection requested", "fn_id", config.FNID, "device_id", config.DeviceID)
		_ = m.connect(ctx)
	} else {
		m.convergeStoppedNetwork(ctx, *config, m.Status())
	}
}

func (m *Manager) watchConfiguration(ctx context.Context) {
	var cursor string
	var retryDelay time.Duration
	nativeRecoveryAttempted := false
	for ctx.Err() == nil {
		statusGeneration := m.statusChanges.Current()
		m.operation.Lock()
		config, err := m.store.Load()
		if err != nil {
			m.recordMaintenanceError("load_configuration", err)
			m.operation.Unlock()
			if !m.waitConfigurationRetry(ctx, &retryDelay, err) {
				return
			}
			continue
		}
		if !canAutoConnect(config, m.Status()) {
			cursor = ""
			m.operation.Unlock()
			m.statusChanges.Wait(ctx, statusGeneration)
			continue
		}
		cookies, err := m.store.LoadCookies(config.FNID)
		if err != nil {
			m.recordMaintenanceError("load_cookies", err)
			m.operation.Unlock()
			if !m.waitConfigurationRetry(ctx, &retryDelay, err) {
				return
			}
			continue
		}
		if len(cookies) == 0 {
			_ = m.fail(model.NewError(
				model.ErrorAuthRequired, "FN Connect authorization is required", false,
			))
			m.operation.Unlock()
			m.statusChanges.Wait(ctx, statusGeneration)
			continue
		}
		var updatedCookies []Cookie
		remote, err := m.remote(
			config.FNID,
			cookies,
			func(updated []Cookie) error {
				updatedCookies = slices.Clone(updated)
				if updatedCookies == nil {
					updatedCookies = []Cookie{}
				}
				return nil
			},
		)
		if err != nil {
			m.recordMaintenanceError("create_remote", err)
			m.operation.Unlock()
			if !m.waitConfigurationRetry(ctx, &retryDelay, err) {
				return
			}
			continue
		}
		watcher, ok := remote.(ConfigurationWatcher)
		if !ok {
			m.operation.Unlock()
			return
		}
		watchContext, cancel := context.WithCancel(ctx)
		m.watchCancel = cancel
		m.operation.Unlock()

		result, err := watcher.WatchConfiguration(watchContext, config.DeviceID, cursor)

		m.operation.Lock()
		current, loadErr := m.store.Load()
		if loadErr != nil {
			m.cancelConfigurationWatch()
			m.recordMaintenanceError("load_configuration", loadErr)
			m.operation.Unlock()
			if !m.waitConfigurationRetry(ctx, &retryDelay, loadErr) {
				return
			}
			continue
		}
		obsolete := watchContext.Err() != nil ||
			!canAutoConnect(current, m.Status()) ||
			current.FNID != config.FNID || current.DeviceID != config.DeviceID
		m.cancelConfigurationWatch()
		if obsolete {
			m.operation.Unlock()
			cursor = ""
			retryDelay = 0
			continue
		}
		if updatedCookies != nil {
			err = errors.Join(err, m.store.MergeCookies(config.FNID, cookies, updatedCookies))
		}
		if err != nil && model.AsError(err).Code == model.ErrorAuthRequired {
			expected := cookies
			if updatedCookies != nil {
				expected = updatedCookies
			}
			latestCookies, loadErr := m.store.LoadCookies(config.FNID)
			if loadErr == nil && len(latestCookies) > 0 && !slices.Equal(expected, latestCookies) {
				err = model.WithOperation(model.NewError(model.ErrorUnavailable,
					"gateway credentials changed during configuration watch; retrying", true), "configuration.watch")
			}
		}
		if err != nil && model.AsError(err).Code == model.ErrorAuthRequired && !nativeRecoveryAttempted {
			recovered, recoverErr := m.recoverNativeSession(ctx, config.FNID, true)
			if recoverErr == nil && recovered {
				m.logger.Info("configuration watch credentials recovered", "fn_id", config.FNID)
				nativeRecoveryAttempted = true
				m.operation.Unlock()
				cursor = ""
				retryDelay = 0
				continue
			}
			if recoverErr != nil {
				err = recoverErr
			}
		}
		if err == nil {
			nativeRecoveryAttempted = false
			if result.Changed {
				err = m.applyWatchedConfiguration(ctx, result.Configuration)
			} else {
				m.clearMaintenanceError()
			}
		}
		if err != nil {
			typed := model.AsError(err)
			if typed.Code == model.ErrorAuthRequired || typed.Code == model.ErrorDeviceRevoked {
				_ = m.fail(err)
				m.bridge.Stop()
				m.convergeStoppedNetwork(ctx, *current, m.Status())
			} else {
				m.recordMaintenanceError("watch_configuration", err)
			}
		}
		m.operation.Unlock()
		if err == nil {
			cursor = result.Cursor
			retryDelay = 0
			continue
		}
		if !m.waitConfigurationRetry(ctx, &retryDelay, err) {
			return
		}
	}
}

func (m *Manager) cancelConfigurationWatch() {
	if m.watchCancel != nil {
		m.watchCancel()
		m.watchCancel = nil
	}
}

func canAutoConnect(config *LocalConfig, status model.ClientStatus) bool {
	return config != nil && config.DeviceID != "" && config.AutoConnect &&
		status.State != model.ClientAuthRequired && status.State != model.ClientAuthorizing &&
		(status.State != model.ClientError || status.LastError != nil && status.LastError.Retryable)
}

func (m *Manager) waitConfigurationRetry(
	ctx context.Context,
	retryDelay *time.Duration,
	err error,
) bool {
	if *retryDelay == 0 {
		*retryDelay = time.Second
	} else if *retryDelay < 30*time.Second {
		*retryDelay = min(*retryDelay*2, 30*time.Second)
	}
	m.logger.Warn(
		"watch server configuration",
		append(logging.ErrorAttrs(err), "retry_in", *retryDelay)...,
	)
	timer := time.NewTimer(*retryDelay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) applyWatchedConfiguration(
	ctx context.Context,
	latest model.ClientConfiguration,
) error {
	config, err := m.store.Load()
	if err != nil {
		m.recordMaintenanceError("load_configuration", err)
		return err
	}
	if !canAutoConnect(config, m.Status()) {
		return nil
	}
	normalized, changed, err := selectClientConfiguration(
		config.Configuration,
		latest,
	)
	if err != nil {
		m.recordMaintenanceError("select_configuration", err)
		return err
	}
	if !changed {
		m.clearMaintenanceError()
		return nil
	}
	config.Configuration = normalized
	if err := m.store.Save(*config); err != nil {
		m.recordMaintenanceError("save_configuration", err)
		return err
	}
	m.clearMaintenanceError()
	m.logger.Info("client reconnect requested", "reason", "configuration_changed", "device_id", config.DeviceID)
	_ = m.reconnect(ctx, *config)
	return nil
}

// Caller holds operation; background recovery never changes the user's intent.
func (m *Manager) reconnect(ctx context.Context, config LocalConfig) error {
	if !canAutoConnect(&config, m.Status()) {
		return nil
	}
	m.cancelConfigurationWatch()
	reconnectContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return m.connectWithKnownConfiguration(reconnectContext, config)
}

func (m *Manager) privilegedLifecycleEvents(ctx context.Context) <-chan struct{} {
	watcher, ok := m.privileged.(PrivilegedLifecycleWatcher)
	if !ok {
		return nil
	}
	events := make(chan struct{}, 1)
	go func() {
		defer close(events)
		var generation uint64
		retryDelay := time.Second
		for ctx.Err() == nil {
			result, err := watcher.WatchLifecycle(ctx, generation)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				m.logger.Error("watch privileged lifecycle failed", logging.ErrorAttrs(
					model.WithOperation(err, "privileged.watch_lifecycle"))...)
				select {
				case events <- struct{}{}:
				default:
				}
				timer := time.NewTimer(retryDelay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if retryDelay < 5*time.Second {
					retryDelay = min(2*retryDelay, 5*time.Second)
				}
				generation = 0
				continue
			}
			retryDelay = time.Second
			generation = result.Generation
			if result.Changed {
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events
}

func (m *Manager) maintainPrivileged(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	status := m.Status()
	config, err := m.store.Load()
	if err != nil {
		m.recordMaintenanceError("load_configuration", err)
		return
	}
	if !canAutoConnect(config, status) {
		if config != nil {
			m.convergeStoppedNetwork(ctx, *config, status)
		}
		return
	}
	if status.State == model.ClientLocal {
		return
	}
	statusContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	network, err := m.privileged.Status(statusContext)
	cancel()
	if err != nil {
		m.setStatus(
			model.ClientReconnecting,
			status.Path,
			status.Interface,
			model.PublicError(err),
		)
		return
	}
	if network.Active && !network.Degraded &&
		(status.State == model.ClientDirect || status.State == model.ClientRelay) {
		return
	}
	m.setStatus(
		model.ClientReconnecting,
		status.Path,
		status.Interface,
		model.NewError(
			model.ErrorUnavailable,
			"privileged network state was reset",
			true,
		),
	)
	m.logger.Info("client reconnect requested", "reason", "privileged_reset", "device_id", config.DeviceID)
	_ = m.reconnect(ctx, *config)
}

func (m *Manager) maintainHealth(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	config, err := m.store.Load()
	if err != nil {
		m.recordMaintenanceError("load_configuration", err)
		return
	}
	if config == nil {
		return
	}
	status := m.Status()
	if !canAutoConnect(config, status) {
		m.convergeStoppedNetwork(ctx, *config, status)
		return
	}
	if status.State == model.ClientReconnecting ||
		status.State == model.ClientPaused || status.State == model.ClientUnconfigured {
		m.logger.Info("client reconnect requested", "reason", "retry_state", "device_id", config.DeviceID)
		_ = m.reconnect(ctx, *config)
		return
	}

	switch status.State {
	case model.ClientDirect, model.ClientRelay:
		networkStatus, statusErr := m.privileged.Status(ctx)
		healthErr := clientNetworkHealthError(
			status.State,
			networkStatus,
			statusErr,
			m.bridge.Connected(),
		)
		if statusErr == nil {
			m.mu.Lock()
			changed := m.status.MTU != networkStatus.MTU ||
				!timesEqual(m.status.LastHandshake, networkStatus.LastHandshake)
			if changed {
				m.status.LastHandshake = networkStatus.LastHandshake
				m.status.MTU = networkStatus.MTU
				m.status.UpdatedAt = time.Now().UTC()
			}
			m.mu.Unlock()
			if changed {
				m.statusChanges.Notify()
			}
		}
		if healthErr != nil {
			m.logger.Warn("client network unhealthy", logging.ErrorAttrs(healthErr)...)
			if status.State == model.ClientDirect {
				m.directRetryAfter = time.Now().Add(5 * time.Minute)
			}
			m.setStatus(
				model.ClientReconnecting,
				status.Path,
				status.Interface,
				model.PublicError(healthErr),
			)
			m.logger.Info("client reconnect requested", "reason", "network_unhealthy", "device_id", config.DeviceID)
			_ = m.reconnect(ctx, *config)
			return
		}
	case model.ClientLocal:
		snapshot, snapshotErr := m.probe.Snapshot()
		reachable := false
		probeConfiguration := cloneLocalProbeConfiguration(m.localProbeConfig)
		if probeConfiguration != nil && snapshotErr == nil {
			var probeErr error
			reachable, probeErr = m.probe.Reachable(
				ctx,
				*probeConfiguration,
				config.DeviceID,
				snapshot,
			)
			if probeErr != nil {
				m.logger.Warn("local path check failed", logging.ErrorAttrs(probeErr)...)
			}
			if reachable {
				m.localProbeConfig = probeConfiguration
			}
		}
		if !reachable {
			m.logger.Info("client reconnect requested", "reason", "local_path_lost", "device_id", config.DeviceID)
			m.setStatus(
				model.ClientReconnecting,
				"local",
				"",
				nil,
			)
			_ = m.reconnect(ctx, *config)
			return
		}
	}

	if status.State == model.ClientRelay &&
		!m.directRetryAfter.IsZero() &&
		time.Now().After(m.directRetryAfter) {
		// Address discovery alone must not tear down a working relay.
		snapshot, snapshotErr := m.probe.Snapshot()
		discovery, discoveryErr := m.discoverer.Discover(ctx, config.FNID)
		if snapshotErr != nil || discoveryErr != nil ||
			discovery.ForbidPublicIPv6 || len(discovery.DirectIPv6Candidates()) == 0 {
			m.directRetryAfter = time.Now().Add(5 * time.Minute)
			retryAfter := m.directRetryAfter
			m.mu.RLock()
			direct := m.direct
			direct.LocalPublicIPv6 = snapshotErr == nil && snapshot.HasPublicIPv6
			m.mu.RUnlock()
			direct.RetryAfter = &retryAfter
			m.setDirectDiagnostics(direct)
			if err := errors.Join(snapshotErr, discoveryErr); err != nil {
				m.logger.Warn("background direct discovery failed", logging.ErrorAttrs(err)...)
			}
			return
		}
		m.directRetryAfter = time.Time{}
		m.logger.Info("client reconnect requested", "reason", "direct_retry", "device_id", config.DeviceID)
		_ = m.reconnect(ctx, *config)
	}
}

// networkReadinessInterval controls how often connectivity is re-probed while
// waiting for the physical network to become ready after a change.
const networkReadinessInterval = time.Second

// networkReadinessTimeout bounds how long the client waits for the network to
// become ready after a change. If it never becomes ready in this window, the
// client reports an explicit "network unavailable" error instead of repeatedly
// trying to build links against a network that is not up yet, and recovers
// automatically when the next network change reports the network as ready.
const networkReadinessTimeout = 20 * time.Second

// networkChangeQuietWindow absorbs the network-change events produced by our own
// tunnel bring-up (utun creation / route changes) right after a successful
// reconnect. Without this window a successful reconnect is immediately followed
// by another reconnect triggered by the very events it caused.
const networkChangeQuietWindow = 3 * time.Second

type networkReadinessResult struct {
	ready bool
}

// handleNetworkChange decides whether a reconnection is needed after the
// physical network changed. It does not immediately rebuild the tunnel:
// rebuilding before the network is actually ready only wastes the whole connect
// path on requests/probes that cannot reach the peer yet (e.g. a local-probe
// configuration request that hangs until its 30s HTTP timeout). Instead it
// returns true to let Run() gate the reconnect behind a network-readiness check.
func (m *Manager) handleNetworkChange(
	ctx context.Context,
	previous *string,
	primaryNetworkChanged bool,
) bool {
	current := m.networkFingerprint()
	if current == *previous {
		// The physical network fingerprint is unchanged. A primary-network-change
		// event with no change in the fingerprint is most likely self-induced by our
		// own tunnel bring-up (utun creation / route changes), not a real network
		// switch. Absorb it inside the post-bring-up quiet window so a successful
		// reconnect does not immediately schedule another one.
		m.mu.RLock()
		fingerprintUnchanged := time.Now().Before(m.ignoreNetworkChangesUntil)
		m.mu.RUnlock()
		if !primaryNetworkChanged || fingerprintUnchanged {
			return false
		}
	}
	*previous = current
	m.operation.Lock()
	defer m.operation.Unlock()
	m.directRetryAfter = time.Time{}
	config, err := m.store.Load()
	if err != nil {
		m.recordMaintenanceError("load_configuration", err)
		return false
	}
	if !canAutoConnect(config, m.Status()) {
		return false
	}
	m.logger.Info("client reconnect pending; waiting for network readiness",
		"reason", "physical_network_changed", "device_id", config.DeviceID)
	return true
}

// beginNetworkReadiness starts a background readiness probe and returns its
// result channel, plus a cancel function to abandon the wait (e.g. when an even
// newer network change arrives). The caller must eventually cancel the returned
// function once the result is consumed or superseded.
func (m *Manager) beginNetworkReadiness(
	ctx context.Context,
) (<-chan networkReadinessResult, func()) {
	readinessContext, cancel := context.WithCancel(ctx)
	result := make(chan networkReadinessResult, 1)
	go m.waitNetworkReadiness(readinessContext, result)
	return result, cancel
}

func (m *Manager) waitNetworkReadiness(
	readinessContext context.Context,
	result chan<- networkReadinessResult,
) {
	ticker := time.NewTicker(networkReadinessInterval)
	defer ticker.Stop()
	deadline := time.NewTimer(networkReadinessTimeout)
	defer deadline.Stop()
	emit := func(ready bool) {
		if readinessContext.Err() != nil {
			return
		}
		select {
		case result <- networkReadinessResult{ready: ready}:
		default:
		}
	}
	for {
		if m.networkReady(readinessContext) {
			emit(true)
			return
		}
		select {
		case <-readinessContext.Done():
			return
		case <-deadline.C:
			emit(false)
			return
		case <-ticker.C:
		}
	}
}

// networkReady reports whether the physical network is ready enough to build a
// connection. LAN-first: if cached local-probe config exists and the local
// gateway is reachable on the current interface, the network is considered
// ready (a fast LOCAL path exists). Otherwise fall back to probing the public
// network via an existing FN Connect API call.
func (m *Manager) networkReady(ctx context.Context) bool {
	config, err := m.store.Load()
	if err == nil && config != nil {
		probeConfiguration := cloneLocalProbeConfiguration(m.localProbeConfig)
		snapshot, snapshotErr := m.probe.Snapshot()
		if probeConfiguration != nil && snapshotErr == nil {
			reachable, probeErr := m.probe.Reachable(
				ctx,
				*probeConfiguration,
				config.DeviceID,
				snapshot,
			)
			if probeErr == nil && reachable {
				return true
			}
		}
	}
	return m.publicNetworkReachable(ctx)
}

// publicNetworkReachable reports whether the public network (and the FN Connect
// discovery endpoint) is reachable, by reusing the existing public discovery API
// as a real connectivity probe. Unlike the authenticated gateway APIs, discovery
// needs no cookies, so it never fails for credential reasons and only reflects
// whether the public link can reach fnos. A response with a real HTTP status
// (even non-2xx) means the request was delivered over the public link, so the
// network is up. A network-layer failure (HTTPStatus == 0) is the only case
// treated as "not ready". The call is bounded by a short timeout so a half-up
// network does not stall the readiness wait.
func (m *Manager) publicNetworkReachable(ctx context.Context) bool {
	config, err := m.store.Load()
	if err != nil || config == nil {
		return false
	}
	probeContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err = m.discoverer.Discover(probeContext, config.FNID)
	if err == nil {
		return true
	}
	// A response with a real HTTP status (even 401/5xx/login page) proves the
	// public link delivered the request. Only HTTPStatus == 0 means the request
	// never reached fnos (network-layer failure), i.e. public network not ready.
	return model.AsError(err).HTTPStatus > 0
}

// restoreAfterNetworkReady rebuilds the tunnel now that the network is ready.
// It is called from Run() only after a network-readiness wait reported success.
func (m *Manager) restoreAfterNetworkReady(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	config, err := m.store.Load()
	if err != nil || !canAutoConnect(config, m.Status()) {
		return
	}
	m.logger.Info("network ready; client reconnect requested", "reason", "network_ready", "device_id", config.DeviceID)
	_ = m.reconnect(ctx, *config)
}

// markNetworkUnavailable reports an explicit "network unavailable" error state
// after the readiness wait timed out, instead of rebuilding links blindly over
// an unavailable network. The next network change that becomes ready will
// trigger restoreAfterNetworkReady automatically.
func (m *Manager) markNetworkUnavailable() {
	m.logger.Warn("network did not become ready; pausing reconnect until the network recovers")
	m.setStatus(
		model.ClientReconnecting,
		"",
		"",
		model.NewError(
			model.ErrorUnavailable,
			"网络不可达，等待网络恢复后自动重连",
			true,
		),
	)
}

func (m *Manager) Status() model.ClientStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneStatus(m.status)
}

func (m *Manager) WatchStatus(
	ctx context.Context,
	after uint64,
) StatusWatchResult {
	generation, changed := m.statusChanges.Wait(ctx, after)
	return StatusWatchResult{
		Changed:    changed,
		Generation: generation,
		Status:     m.Status(),
	}
}

func (m *Manager) Diagnose(ctx context.Context) model.ClientDiagnostics {
	status := m.Status()
	networkStatus, err := m.privileged.Status(ctx)
	snapshot, snapshotErr := m.probe.Snapshot()
	fingerprint := ""
	if snapshotErr == nil {
		fingerprint = snapshot.Fingerprint()
	}
	lanOverlap := false
	config, configErr := m.store.Load()
	if configErr == nil && config != nil && snapshotErr == nil {
		lanOverlap = configurationHasLANOverlap(
			config.Configuration,
			snapshot.Prefixes,
		)
	}
	mtu := status.MTU
	if err == nil && networkStatus.MTU > 0 {
		mtu = networkStatus.MTU
	}
	networkInterface := status.Interface
	if err == nil && networkStatus.Interface != "" {
		networkInterface = networkStatus.Interface
	}
	diagnostics := model.ClientDiagnostics{
		Status:              status,
		PrivilegedAvailable: err == nil,
		PrivilegedActive:    err == nil && networkStatus.Active,
		PrivilegedDegraded:  err == nil && networkStatus.Degraded,
		NetworkInterface:    networkInterface,
		NetworkFingerprint:  fingerprint,
		MTU:                 mtu,
		LANOverlap:          lanOverlap,
		RoutePolicy:         "overlay-only-on-lan-overlap",
		CheckedAt:           time.Now().UTC(),
	}
	m.mu.RLock()
	diagnostics.Direct = m.direct
	m.mu.RUnlock()
	diagnostics.Direct.LocalPublicIPv6 = snapshot.HasPublicIPv6
	if configErr == nil && config != nil {
		diagnostics.FNID = config.FNID
		diagnostics.ClientAddress = config.Configuration.ClientAddress
		if session, found, err := m.store.LoadNativeSession(config.FNID); err == nil && found {
			diagnostics.Username = session.Username
		}
	}
	if networkStatus.LastHandshake != nil {
		diagnostics.HandshakeAge = time.Since(*networkStatus.LastHandshake).
			Round(time.Second).
			String()
	}
	for _, issue := range []*model.Error{
		model.WithOperation(err, "diagnose.privileged"),
		model.WithOperation(snapshotErr, "diagnose.network"),
		model.WithOperation(configErr, "diagnose.configuration"),
	} {
		if issue != nil {
			public := model.PublicError(issue)
			diagnostics.Errors = append(diagnostics.Errors, public)
			if diagnostics.Status.LastError == nil {
				diagnostics.Status.LastError = public
			}
		}
	}
	return diagnostics
}

func (m *Manager) connectWithConfig(
	ctx context.Context,
	config LocalConfig,
) error {
	return m.connectWithOptionalConfiguration(ctx, config, nil)
}

func (m *Manager) connectWithKnownConfiguration(
	ctx context.Context,
	config LocalConfig,
) error {
	configuration := config.Configuration
	return m.connectWithOptionalConfiguration(
		ctx,
		config,
		&configuration,
	)
}

func (m *Manager) connectWithOptionalConfiguration(
	ctx context.Context,
	config LocalConfig,
	known *model.ClientConfiguration,
) error {
	connectionStarted := time.Now()
	m.setDirectDiagnostics(model.DirectDiagnostics{Reason: "checking"})
	logger := logging.FromContext(ctx, m.logger).With("fn_id", config.FNID, "device_id", config.DeviceID)
	logger.Info("client connection started")
	m.setStatus(
		model.ClientProbing,
		"",
		"",
		nil,
	)
	m.bridge.Stop()
	m.localProbeConfig = nil
	if err := m.privileged.Remove(ctx); err != nil {
		return m.fail(err)
	}
	cookies, err := m.store.LoadCookies(config.FNID)
	if err != nil {
		return m.fail(err)
	}
	if len(cookies) == 0 {
		return m.fail(model.NewError(
			model.ErrorAuthRequired,
			"FN Connect authorization is required",
			false,
		))
	}
	remote, err := m.remote(
		config.FNID,
		cookies,
		func(updated []Cookie) error {
			err := m.store.MergeCookies(config.FNID, cookies, updated)
			cookies = slices.Clone(updated)
			return err
		},
	)
	if err != nil {
		return m.fail(err)
	}
	latest := config.Configuration
	if known == nil {
		logger.Info("device configuration requested")
		latest, err = remote.Configuration(ctx, config.DeviceID)
		if err != nil {
			// Deferred session recovery: only spend a WebSocket round trip when the
			// gateway actually rejected our token, then retry the configuration once.
			if model.AsError(err).Code == model.ErrorAuthRequired {
				recoveryStarted := time.Now()
				if recovered, recoverErr := m.recoverNativeSession(ctx, config.FNID, true); recoverErr != nil {
					return m.fail(recoverErr)
				} else if recovered {
					logger.Warn("gateway token invalid; native session recovered, retrying configuration",
						"recover_ms", time.Since(recoveryStarted).Milliseconds())
					latest, err = remote.Configuration(ctx, config.DeviceID)
				}
			}
			if err != nil {
				return m.fail(err)
			}
		}
	} else {
		latest = *known
	}
	configuration, changed, err := selectClientConfiguration(
		config.Configuration,
		latest,
	)
	if err != nil {
		return m.fail(err)
	}
	if changed {
		config.Configuration = configuration
		if err := m.store.Save(config); err != nil {
			return m.fail(err)
		}
	}
	logger.Info("device configuration ready", "address", configuration.ClientAddress,
		"route_count", len(configuration.AllowedIPs), "changed", changed)

	networkSnapshot, err := m.probe.Snapshot()
	if err != nil {
		return m.fail(err)
	}
	logger.Info("local probe started")
	// The local-probe configuration is only an optimization to detect an on-LAN
	// path to the gateway. Fetching it must not block the reconnect critical path
	// when the physical network just changed and the WAN/uplink is not ready yet;
	// otherwise the default 30s HTTP client timeout stalls reconnection by ~30s.
	// Bound this request with a short timeout so the connect flow falls through to
	// discovery promptly instead of waiting for the full HTTP client timeout.
	probeContext, cancelProbe := context.WithTimeout(ctx, 3*time.Second)
	probeConfiguration, probeErr := remote.LocalProbeConfiguration(
		probeContext,
		config.DeviceID,
	)
	cancelProbe()
	local := false
	if probeErr == nil {
		local, err = m.probe.Reachable(
			ctx,
			probeConfiguration,
			config.DeviceID,
			networkSnapshot,
		)
		if err != nil {
			logger.Warn("probe local server", logging.ErrorAttrs(err)...)
		}
	} else {
		logger.Warn("get local probe configuration", logging.ErrorAttrs(probeErr)...)
	}
	logger.Info("local probe completed", "reachable", local)
	if local {
		m.setDirectDiagnostics(model.DirectDiagnostics{Reason: "local_network", LocalPublicIPv6: networkSnapshot.HasPublicIPv6})
		m.localProbeConfig = cloneLocalProbeConfiguration(&probeConfiguration)
		m.setStatus(
			model.ClientLocal,
			"local",
			"",
			nil,
		)
		logger.Info("client connected", "state", model.ClientLocal, "path", "local",
			"elapsed_ms", time.Since(connectionStarted).Milliseconds())
		return nil
	}
	m.localProbeConfig = nil
	logger.Info("FN Connect discovery started")
	discovery, err := m.discoverer.Discover(ctx, config.FNID)
	if err != nil {
		if ctx.Err() != nil {
			return m.fail(ctx.Err())
		}
		logger.Warn("FN Connect discovery failed; using known relay address", logging.ErrorAttrs(err)...)
	}
	candidates := discovery.DirectIPv6Candidates()
	direct := model.DirectDiagnostics{
		LocalPublicIPv6: networkSnapshot.HasPublicIPv6,
		CandidateCount:  len(candidates),
	}
	switch {
	case err != nil:
		direct.Reason = "discovery_failed"
		direct.LastError = model.PublicError(err)
	case discovery.ForbidPublicIPv6:
		direct.Reason = "server_disabled"
	case len(candidates) == 0:
		direct.Reason = "no_server_ipv6"
	case !m.ipv6RouteChecker(ctx, candidates[0], int(config.Configuration.ListenPort)):
		// Only probe IPv6 when the current device actually has an IPv6 uplink,
		// otherwise fall straight through to relay instead of failing a handshake.
		direct.Reason = "no_local_ipv6"
	case time.Now().Before(m.directRetryAfter):
		direct.Reason = "cooldown"
		retryAfter := m.directRetryAfter
		direct.RetryAfter = &retryAfter
	default:
		direct.Reason = "probing"
	}
	m.setDirectDiagnostics(direct)
	logger.Info("FN Connect discovery completed", "ipv6_candidates", len(candidates),
		"local_public_ipv6", networkSnapshot.HasPublicIPv6, "ipv6_forbidden", discovery.ForbidPublicIPv6)
	allowedIPs := routedPrefixes(
		config.Configuration,
		networkSnapshot.Prefixes,
	)
	logger.Info("client routes selected", "route_count", len(allowedIPs),
		"lan_overlap", configurationHasLANOverlap(config.Configuration, networkSnapshot.Prefixes))
	privateKey, err := m.store.EnsureWireGuardKey(config.FNID)
	if err != nil {
		return m.fail(err)
	}
	if direct.Reason == "probing" {
		// Bound the complete direct phase so many stale addresses cannot starve relay.
		directContext, cancelDirect := context.WithTimeout(ctx, 20*time.Second)
		defer cancelDirect()
		for _, address := range candidates {
			if directContext.Err() != nil {
				break
			}
			plan := clientPlan(
				config.Configuration,
				privateKey.String(),
				"direct",
				net.JoinHostPort(address.String(), strconv.Itoa(int(config.Configuration.ListenPort))),
				allowedIPs,
				1280,
			)
			direct.AttemptCount++
			direct.Endpoint = plan.Endpoint
			m.setDirectDiagnostics(direct)
			logger.Info("direct connection attempted", "endpoint", plan.Endpoint)
			started := time.Now()
			status, applyErr := m.applyPlan(ctx, plan)
			if applyErr != nil {
				return m.fail(applyErr)
			}
			status, handshakeErr := m.waitForHandshake(directContext, status, started)
			if handshakeErr != nil {
				direct.LastError = model.PublicError(model.WithOperation(handshakeErr, "direct.handshake"))
				logger.Warn("direct connection failed; trying next path", logging.ErrorAttrs(
					model.WithOperation(handshakeErr, "direct.handshake"))...)
				if cleanupErr := m.privileged.Remove(ctx); cleanupErr != nil {
					return m.fail(errors.Join(handshakeErr, cleanupErr))
				}
				continue
			}
			m.bridge.Stop()
			direct.Reason = "connected"
			direct.LastError = nil
			m.directRetryAfter = time.Time{}
			m.setDirectDiagnostics(direct)
			m.setNetworkStatus(
				model.ClientDirect,
				"ipv6",
				status,
			)
			logger.Info("client connected", "state", model.ClientDirect, "path", "ipv6",
				"interface", status.Interface, "mtu", status.MTU,
				"elapsed_ms", time.Since(connectionStarted).Milliseconds())
			return nil
		}
		if ctx.Err() != nil {
			return m.fail(ctx.Err())
		}
		direct.Reason = "handshake_failed"
	}
	if direct.Reason != "server_disabled" {
		// Also revisit empty/stale discovery results while the physical network is unchanged.
		m.directRetryAfter = time.Now().Add(5 * time.Minute)
		retryAfter := m.directRetryAfter
		direct.RetryAfter = &retryAfter
	}
	m.setDirectDiagnostics(direct)
	logger.Info("direct path unavailable", "reason", direct.Reason, "attempts", direct.AttemptCount,
		"endpoint", direct.Endpoint, "retry_after", direct.RetryAfter)

	relayURL, err := relayURLFromDiscovery(config.FNID, discovery)
	if err != nil {
		return m.fail(err)
	}
	logger.Info("relay path selected", "relay_url", model.SafeURL(relayURL))
	endpoint, err := m.bridge.Start(ctx, relayURL, func() ([]Cookie, error) {
		return m.store.LoadCookies(config.FNID)
	}, func(before, after []Cookie) error {
		return m.store.MergeCookies(config.FNID, before, after)
	})
	if err != nil {
		return m.fail(model.NormalizeError(
			err,
			model.ErrorUnavailable,
			"FN Connect relay is unavailable",
			true,
		))
	}
	plan := clientPlan(
		config.Configuration,
		privateKey.String(),
		"relay",
		endpoint,
		allowedIPs,
		1280,
	)
	started := time.Now()
	status, err := m.applyPlan(ctx, plan)
	if err != nil {
		m.bridge.Stop()
		return m.fail(err)
	}
	if err := m.bridge.BindPeer(status.ListenPort); err != nil {
		m.bridge.Stop()
		cleanupErr := m.privileged.Remove(ctx)
		return m.fail(errors.Join(err, cleanupErr))
	}
	status, err = m.waitForHandshake(ctx, status, started)
	if err != nil {
		m.bridge.Stop()
		cleanupErr := m.privileged.Remove(ctx)
		return m.fail(errors.Join(err, cleanupErr))
	}
	m.setNetworkStatus(
		model.ClientRelay,
		"fn-connect",
		status,
	)
	logger.Info("client connected", "state", model.ClientRelay, "path", "fn-connect",
		"interface", status.Interface, "mtu", status.MTU,
		"elapsed_ms", time.Since(connectionStarted).Milliseconds())
	return nil
}

func hasIPv6RouteTo(ctx context.Context, address netip.Addr, port int) bool {
	if !address.Is6() || address.Is4In6() {
		return false
	}
	target := net.JoinHostPort(address.String(), strconv.Itoa(port))
	dialContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(dialContext, "udp6", target)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}

func (m *Manager) setDirectDiagnostics(direct model.DirectDiagnostics) {
	m.mu.Lock()
	changed := !m.direct.EqualTo(direct)
	m.direct = direct
	m.mu.Unlock()
	if changed {
		m.statusChanges.Notify()
	}
}

// recoverNativeSession refreshes the native session only when force is set.
// Session recovery is deferred on demand: callers refresh only after a request
// is rejected as InvalidToken, instead of wasting a WebSocket round trip on
// every reconnect. With force=false it only ensures the gateway cookie is
// written from the currently stored (potentially still valid) token.
func (m *Manager) recoverNativeSession(ctx context.Context, fnID string, force bool) (bool, error) {
	session, found, err := m.store.LoadNativeSession(fnID)
	if err != nil || !found {
		return found, err
	}
	if force {
		session, err = m.authenticator.Recover(ctx, session, m.deviceName)
		if err != nil {
			return true, err
		}
		if err := m.store.SaveNativeSession(session); err != nil {
			return true, err
		}
	}
	if err := m.store.SaveNativeGatewayCookies(fnID, session.Token); err != nil {
		return true, err
	}
	return true, nil
}

func selectClientConfiguration(
	current model.ClientConfiguration,
	received model.ClientConfiguration,
) (model.ClientConfiguration, bool, error) {
	received, err := wgconfig.NormalizeClientConfiguration(received)
	if err != nil {
		return model.ClientConfiguration{}, false, model.WithOperation(
			model.WrapError(model.ErrorProtocol, "server returned an invalid WireGuard configuration", false, err), "configuration.validate")
	}
	if current.DeviceID == "" {
		return received, true, nil
	}
	current, err = wgconfig.NormalizeClientConfiguration(current)
	if err != nil {
		return model.ClientConfiguration{}, false, model.WithOperation(
			model.WrapError(model.ErrorFailedPrecondition, "stored WireGuard configuration is invalid", false, err), "configuration.validate")
	}
	if clientConfigurationsEqual(current, received) {
		return current, false, nil
	}
	return received, true, nil
}

func clientConfigurationsEqual(
	first model.ClientConfiguration,
	second model.ClientConfiguration,
) bool {
	return first.DeviceID == second.DeviceID &&
		first.ServerPublicKey == second.ServerPublicKey &&
		first.ServerAddress == second.ServerAddress &&
		first.ClientAddress == second.ClientAddress &&
		first.ListenPort == second.ListenPort &&
		slices.Equal(first.AllowedIPs, second.AllowedIPs)
}

func clientPlan(
	config model.ClientConfiguration,
	privateKey string,
	mode string,
	endpoint string,
	allowedIPs []string,
	mtu int,
) model.ClientPlan {
	return model.ClientPlan{
		Mode:            mode,
		PrivateKey:      privateKey,
		ClientAddress:   config.ClientAddress,
		ServerPublicKey: config.ServerPublicKey,
		Endpoint:        endpoint,
		AllowedIPs:      slices.Clone(allowedIPs),
		MTU:             mtu,
	}
}

func (m *Manager) applyPlan(
	ctx context.Context,
	plan model.ClientPlan,
) (model.ClientPrivilegedStatus, error) {
	logger := logging.FromContext(ctx, m.logger)
	logger.Info("client network apply requested", "mode", plan.Mode, "address", plan.ClientAddress,
		"route_count", len(plan.AllowedIPs), "mtu", plan.MTU)
	status, err := m.privileged.Apply(ctx, plan)
	if err == nil && status.LastError != nil {
		err = status.LastError
	}
	if err != nil {
		return status, model.WithOperation(err, "network.apply."+plan.Mode)
	}
	logger.Info("client network applied", "mode", plan.Mode, "interface", status.Interface,
		"listen_port", status.ListenPort, "mtu", status.MTU)
	return status, nil
}

func (m *Manager) waitForHandshake(
	ctx context.Context,
	status model.ClientPrivilegedStatus,
	started time.Time,
) (model.ClientPrivilegedStatus, error) {
	logger := logging.FromContext(ctx, m.logger)
	logger.Info("WireGuard handshake waiting", "interface", status.Interface)
	deadline := time.NewTimer(m.handshakeTimeout)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	slowWarning := time.NewTimer(2 * time.Second)
	defer slowWarning.Stop()
	for {
		if status.LastHandshake != nil && !status.LastHandshake.Before(started) {
			logger.Info("WireGuard handshake confirmed", "interface", status.Interface,
				"handshake_at", *status.LastHandshake, "elapsed_ms", time.Since(started).Milliseconds())
			return status, nil
		}
		select {
		case <-ctx.Done():
			return status, model.AsError(ctx.Err())
		case <-deadline.C:
			return status, model.NewError(
				model.ErrorTimeout,
				"WireGuard handshake timed out",
				true,
			)
		case <-slowWarning.C:
			logger.Warn("WireGuard handshake still pending", "interface", status.Interface,
				"elapsed_ms", time.Since(started).Milliseconds())
		case <-ticker.C:
			next, err := m.privileged.Status(ctx)
			if err != nil {
				return status, err
			}
			status = next
		}
	}
}

func (m *Manager) setNetworkStatus(
	state model.ClientState,
	path string,
	network model.ClientPrivilegedStatus,
) {
	m.mu.Lock()
	m.status = model.ClientStatus{
		State:         state,
		Path:          path,
		Interface:     network.Interface,
		MTU:           network.MTU,
		LastHandshake: network.LastHandshake,
		UpdatedAt:     time.Now().UTC(),
	}
	// The tunnel interface was just brought up. Absorb the network-change events
	// this triggers for a short window so it does not spawn a second reconnect.
	m.ignoreNetworkChangesUntil = time.Now().Add(networkChangeQuietWindow)
	m.maintenanceError = false
	m.mu.Unlock()
	m.statusChanges.Notify()
}

func clientNetworkHealthError(
	state model.ClientState,
	network model.ClientPrivilegedStatus,
	statusErr error,
	bridgeConnected bool,
) error {
	if statusErr != nil {
		return statusErr
	}
	if !network.Active || network.Degraded {
		return model.NewError(
			model.ErrorUnavailable,
			"client privileged network is inactive or degraded",
			true,
		)
	}
	if network.LastHandshake == nil {
		return model.NewError(
			model.ErrorUnavailable,
			"WireGuard handshake is unavailable",
			true,
		)
	}
	if time.Since(*network.LastHandshake) > 180*time.Second {
		return model.NewError(
			model.ErrorTimeout,
			"WireGuard handshake is stale",
			true,
		)
	}
	if state == model.ClientRelay && !bridgeConnected {
		return model.NewError(
			model.ErrorUnavailable,
			"FN Connect relay is disconnected",
			true,
		)
	}
	return nil
}

func cloneLocalProbeConfiguration(
	configuration *model.LocalProbeConfiguration,
) *model.LocalProbeConfiguration {
	if configuration == nil {
		return nil
	}
	cloned := *configuration
	cloned.Endpoints = slices.Clone(configuration.Endpoints)
	return &cloned
}

func configurationHasLANOverlap(
	config model.ClientConfiguration,
	localPrefixes []netip.Prefix,
) bool {
	server, err := netip.ParsePrefix(config.ServerAddress)
	if err != nil {
		return false
	}
	for _, value := range config.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			continue
		}
		prefix = prefix.Masked()
		if prefix.Contains(server.Addr()) {
			continue
		}
		if overlapsAny(prefix, localPrefixes) {
			return true
		}
	}
	return false
}

func routedPrefixes(
	config model.ClientConfiguration,
	localPrefixes []netip.Prefix,
) []string {
	server, _ := netip.ParsePrefix(config.ServerAddress)
	result := []string{netip.PrefixFrom(server.Addr(), 32).String()}
	for _, value := range config.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			continue
		}
		prefix = prefix.Masked()
		if prefix.Contains(server.Addr()) {
			continue
		}
		if !overlapsAny(prefix, localPrefixes) {
			result = appendUnique(result, prefix.String())
		}
	}
	return result
}

func overlapsAny(prefix netip.Prefix, candidates []netip.Prefix) bool {
	for _, candidate := range candidates {
		if prefix.Addr().BitLen() == candidate.Addr().BitLen() &&
			(prefix.Contains(candidate.Addr()) || candidate.Contains(prefix.Addr())) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

func relayURLFromDiscovery(fnID string, discovery Discovery) (string, error) {
	host := ""
	if len(discovery.FN) > 0 {
		host = discovery.FN[0]
	}
	if host == "" {
		normalized, err := normalizeFNID(fnID)
		if err != nil {
			return "", err
		}
		host = normalized + ".fnos.net"
	}
	parsed, err := url.Parse("wss://" + host)
	if err != nil || parsed.Host == "" {
		return "", model.WithOperation(model.NewError(model.ErrorProtocol, "invalid FN Connect relay endpoint", false), "discovery.relay_endpoint")
	}
	parsed.Path = gatewayApplicationPath + "/relay/v1/wireguard"
	return parsed.String(), nil
}

func (m *Manager) fail(err error) error {
	typed := model.AsError(err)
	if typed.Code == model.ErrorCanceled {
		m.logger.Debug("client operation canceled", logging.ErrorAttrs(err)...)
	} else {
		m.logger.Error("client operation failed", logging.ErrorAttrs(err)...)
	}
	state := model.ClientError
	switch {
	case typed.Code == model.ErrorAuthRequired ||
		typed.Code == model.ErrorDeviceRevoked:
		m.cancelConfigurationWatch()
		state = model.ClientAuthRequired
		// A terminal auth state must not keep stale "probing" diagnostics.
		m.directPruneToIdle()
	case typed.Retryable:
		state = model.ClientReconnecting
	case typed.Code == model.ErrorPermissionDenied:
		state = model.ClientPaused
		m.directPruneToIdle()
	}
	m.setStatus(state, "", "", typed)
	return err
}

// directPruneToIdle clears transient probing diagnostics for terminal states so the
// UI no longer shows "检测中" while the client is paused or requires login.
func (m *Manager) directPruneToIdle() {
	m.mu.Lock()
	m.direct = model.DirectDiagnostics{}
	m.mu.Unlock()
	m.statusChanges.Notify()
}

func (m *Manager) setStatus(
	state model.ClientState,
	path string,
	interfaceName string,
	lastError *model.Error,
) {
	m.mu.Lock()
	m.status = model.ClientStatus{
		State:     state,
		Path:      path,
		Interface: interfaceName,
		LastError: model.PublicError(lastError),
		UpdatedAt: time.Now().UTC(),
	}
	m.maintenanceError = false
	m.mu.Unlock()
	m.statusChanges.Notify()
}

func (m *Manager) recordMaintenanceError(phase string, err error) {
	public := model.PublicError(err)
	m.mu.Lock()
	previous := m.status.LastError
	changed := previous == nil ||
		previous.Code != public.Code ||
		previous.Message != public.Message ||
		previous.Detail != public.Detail || previous.Operation != public.Operation ||
		previous.HTTPStatus != public.HTTPStatus || previous.RemoteCode != public.RemoteCode
	m.status.LastError = public
	m.status.UpdatedAt = time.Now().UTC()
	m.maintenanceError = true
	m.maintenancePhase = phase
	m.mu.Unlock()
	if changed {
		m.statusChanges.Notify()
		m.logger.Error("synchronize client configuration", append(logging.ErrorAttrs(err), "phase", phase)...)
	}
}

func (m *Manager) clearMaintenanceError() {
	m.mu.Lock()
	if !m.maintenanceError {
		m.mu.Unlock()
		return
	}
	phase := m.maintenancePhase
	m.status.LastError = nil
	m.status.UpdatedAt = time.Now().UTC()
	m.maintenanceError = false
	m.maintenancePhase = ""
	m.mu.Unlock()
	m.logger.Info("client maintenance recovered", "phase", phase)
	m.statusChanges.Notify()
}

func timesEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == second
	}
	return first.Equal(*second)
}

func (m *Manager) networkFingerprint() string {
	snapshot, err := m.probe.Snapshot()
	if err != nil {
		return ""
	}
	return snapshot.Fingerprint()
}

type IPCPrivilegedNetwork struct {
	Client ipc.Client
}

func (n IPCPrivilegedNetwork) Apply(
	ctx context.Context,
	plan model.ClientPlan,
) (model.ClientPrivilegedStatus, error) {
	var status model.ClientPrivilegedStatus
	err := n.Client.Call(ctx, privileged.MethodApply, plan, &status)
	return status, err
}

func (n IPCPrivilegedNetwork) Status(
	ctx context.Context,
) (model.ClientPrivilegedStatus, error) {
	var response privileged.ClientStatus
	if err := n.Client.Call(ctx, privileged.MethodStatus, nil, &response); err != nil {
		return model.ClientPrivilegedStatus{}, err
	}
	if !response.Available {
		return model.ClientPrivilegedStatus{}, model.NewError(
			model.ErrorUnavailable,
			"client privileged network engine is unavailable",
			true,
		)
	}
	if response.Network.LastError != nil {
		return response.Network, response.Network.LastError
	}
	return response.Network, nil
}

func (n IPCPrivilegedNetwork) Remove(ctx context.Context) error {
	return n.Client.Call(ctx, privileged.MethodRemove, nil, nil)
}

func (n IPCPrivilegedNetwork) WatchLifecycle(
	ctx context.Context,
	after uint64,
) (privileged.WatchResult[privileged.ClientStatus], error) {
	client := n.Client
	client.Timeout = 5*time.Minute + 10*time.Second
	var result privileged.WatchResult[privileged.ClientStatus]
	err := client.Call(
		ctx,
		privileged.MethodWatchLifecycle,
		privileged.WatchRequest{After: after},
		&result,
	)
	return result, err
}

type SystemLocalProbe struct {
	HTTPClient *http.Client
}

// localProbeTimeout bounds each candidate end-to-end attempt. Endpoints are
// probed concurrently and fail fast on connect errors, so a LAN that is not
// locally reachable is rejected after ~300ms instead of blocking the reconnect.
const localProbeTimeout = 300 * time.Millisecond

func (p SystemLocalProbe) Reachable(
	ctx context.Context,
	configuration model.LocalProbeConfiguration,
	deviceID string,
	snapshot NetworkSnapshot,
) (bool, error) {
	key, err := base64.RawURLEncoding.DecodeString(configuration.Key)
	if err != nil || len(key) != sha256.Size {
		return false, errors.New("invalid local probe key")
	}
	if deviceID == "" {
		return false, errors.New("local probe device ID is required")
	}
	nonce := make([]byte, sha256.Size)
	if _, err := rand.Read(nonce); err != nil {
		return false, fmt.Errorf("generate local probe nonce: %w", err)
	}
	input := model.LocalProbeRequest{
		DeviceID: deviceID,
		Nonce:    base64.RawURLEncoding.EncodeToString(nonce),
	}
	body, err := json.Marshal(input)
	if err != nil {
		return false, err
	}
	expected := hmac.New(sha256.New, key)
	_, _ = expected.Write([]byte(model.LocalProbeDomain))
	_, _ = expected.Write(nonce)
	expectedProof := expected.Sum(nil)

	var endpoints []string
	for _, endpoint := range configuration.Endpoints {
		host, port, splitErr := net.SplitHostPort(endpoint)
		if splitErr != nil {
			continue
		}
		address, parseErr := netip.ParseAddr(host)
		if parseErr != nil || !address.Is4() || !address.IsPrivate() {
			continue
		}
		if value, parseErr := strconv.ParseUint(port, 10, 16); parseErr != nil || value == 0 {
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	if len(endpoints) == 0 {
		return false, nil
	}

	probeContext, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, len(endpoints))
	for _, endpoint := range endpoints {
		go func(endpoint string) {
			ok, probeErr := probeLocalEndpoint(probeContext, p.HTTPClient, snapshot.InterfaceIndex,
				endpoint, body, expectedProof)
			results <- result{ok: ok, err: probeErr}
		}(endpoint)
	}
	var failures []error
	for range endpoints {
		select {
		case <-ctx.Done():
			return false, model.WithOperation(model.NormalizeError(ctx.Err(), model.ErrorUnavailable,
				"local probe canceled", true), "local_probe.request")
		case res := <-results:
			if res.ok {
				cancel()
				return true, nil
			}
			if res.err != nil {
				failures = append(failures, res.err)
			}
		}
	}
	if len(failures) > 0 {
		return false, model.WithOperation(model.NormalizeError(errors.Join(failures...), model.ErrorUnavailable,
			"no local probe endpoint succeeded", true), "local_probe.request")
	}
	return false, nil
}

func probeLocalEndpoint(
	ctx context.Context,
	httpClient *http.Client,
	interfaceIndex int,
	endpoint string,
	body []byte,
	expectedProof []byte,
) (bool, error) {
	client := httpClient
	var transport *http.Transport
	if client == nil {
		transport = localProbeTransport(interfaceIndex)
		client = &http.Client{
			Timeout:   localProbeTimeout,
			Transport: transport,
			CheckRedirect: func(
				*http.Request,
				[]*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		}
	}
	target := url.URL{
		Scheme: "http",
		Host:   endpoint,
		Path:   "/probe",
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		target.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return false, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if transport != nil {
		transport.CloseIdleConnections()
	}
	if err != nil {
		return false, err
	}
	if response.StatusCode != http.StatusOK {
		failure := HTTPResponseError(response, "local_probe.request", nil)
		response.Body.Close()
		return false, failure
	}
	data, readErr := io.ReadAll(io.LimitReader(
		response.Body,
		maxRemoteResponseSize+1,
	))
	response.Body.Close()
	if readErr != nil || len(data) > maxRemoteResponseSize {
		if readErr == nil {
			readErr = errors.New("local probe response exceeds size limit")
		}
		return false, readErr
	}
	var probe model.LocalProbeResponse
	decodeErr := model.DecodeStrict(data, &probe)
	proof, proofErr := base64.RawURLEncoding.DecodeString(probe.Proof)
	if decodeErr == nil && proofErr == nil && hmac.Equal(proof, expectedProof) {
		return true, nil
	}
	return false, model.NewError(model.ErrorProtocol, "local probe returned an invalid identity proof", false)
}

func localProbeTransport(interfaceIndex int) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(
		ctx context.Context,
		network string,
		address string,
	) (net.Conn, error) {
		dialer := net.Dialer{
			Timeout: localProbeTimeout,
			Control: func(_, _ string, raw syscall.RawConn) error {
				var bindErr error
				if err := raw.Control(func(fd uintptr) {
					bindErr = bindSocketToInterface(fd, interfaceIndex)
				}); err != nil {
					return err
				}
				return bindErr
			},
		}
		return dialer.DialContext(ctx, network, address)
	}
	return transport
}

func (SystemLocalProbe) Snapshot() (NetworkSnapshot, error) {
	defaultInterface, err := defaultPhysicalInterface()
	if err != nil {
		return NetworkSnapshot{}, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return NetworkSnapshot{}, err
	}
	return networkSnapshot(interfaces, defaultInterface, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	}), nil
}

func networkSnapshot(
	interfaces []net.Interface,
	defaultInterface string,
	addresses func(net.Interface) ([]net.Addr, error),
) NetworkSnapshot {
	snapshot := NetworkSnapshot{}
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 ||
			networkInterface.Flags&net.FlagLoopback != 0 ||
			!usablePhysicalInterface(
				networkInterface.Name,
				defaultInterface,
			) {
			continue
		}
		primary := networkInterface.Name == defaultInterface ||
			defaultInterface == "" && snapshot.InterfaceName == ""
		if primary {
			snapshot.InterfaceName = networkInterface.Name
			snapshot.InterfaceIndex = networkInterface.Index
		}
		values, err := addresses(networkInterface)
		if err != nil {
			continue
		}
		for _, value := range values {
			prefix, err := netip.ParsePrefix(value.String())
			if err != nil {
				continue
			}
			if primary && prefix.Addr().Is4() {
				snapshot.Prefixes = append(snapshot.Prefixes, prefix.Masked())
				snapshot.Addresses = append(snapshot.Addresses, prefix.Addr())
			}
			if isPublicIPv6Address(prefix.Addr()) {
				snapshot.HasPublicIPv6 = true
				// Prefixes detect renumbering without reconnecting on temporary-address rotation.
				snapshot.IPv6Networks = appendUnique(snapshot.IPv6Networks,
					networkInterface.Name+":"+prefix.Masked().String())
			}
		}
	}
	slices.Sort(snapshot.IPv6Networks)
	return snapshot
}

func defaultPhysicalInterface() (string, error) {
	connection, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return "", nil
	}
	defer connection.Close()
	local := connection.LocalAddr().(*net.UDPAddr).IP
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, value := range addresses {
			prefix, parseErr := netip.ParsePrefix(value.String())
			if parseErr == nil && prefix.Addr().String() == local.String() {
				return networkInterface.Name, nil
			}
		}
	}
	return "", errors.New("default physical interface was not found")
}

func usablePhysicalInterface(name, defaultInterface string) bool {
	if name == defaultInterface && strings.HasPrefix(name, "bridge") {
		return true
	}
	return !isVirtualInterface(name)
}

func isVirtualInterface(name string) bool {
	for _, prefix := range []string{
		"utun", "lo", "bridge", "awdl", "llw", "gif", "stf", "vbox", "vmnet",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func isPublicIPv6Address(address netip.Addr) bool {
	return wgconfig.IsPublicIPv6(address)
}
