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

type PhysicalLink struct {
	Name      string
	Index     int
	Prefixes  []netip.Prefix
	Addresses []netip.Addr
	Identity  string
}

type NetworkSnapshot struct {
	Links          []PhysicalLink
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
	for _, address := range s.Addresses {
		if address.Is4() {
			values = append(values, "address:"+address.String())
		}
	}
	for _, link := range s.Links {
		values = append(values, "link:"+link.Name+":"+strconv.Itoa(link.Index)+":"+link.Identity)
		for _, prefix := range link.Prefixes {
			values = append(values, link.Name+":"+prefix.String())
		}
		for _, address := range link.Addresses {
			if address.Is4() {
				values = append(values, link.Name+":"+address.String())
			}
		}
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
	WebAuthenticator AdminWebAuthenticator
	AdminWebProbe    func(context.Context, string, string) error
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
	webAuthenticator AdminWebAuthenticator
	adminWebProbe    func(context.Context, string, string) error
	deviceName       string
	logger           *slog.Logger
	handshakeTimeout time.Duration
	directRetryAfter time.Time
	direct           model.DirectDiagnostics
	localProbeConfig *model.LocalProbeConfiguration
	// nasAddress is the NAS's LAN IPv4 extracted from the local-probe endpoints.
	// It is retained even when the LAN probe itself fails: once the VPN tunnel
	// is up this address is reachable, so it is shown on the client's details
	// page regardless of which path (local/direct/relay) was selected.
	nasAddress       string
	ipv6RouteChecker func(context.Context, netip.Addr, int) bool
	status           model.ClientStatus
	maintenanceError bool
	maintenancePhase string
	statusChanges    *notify.Change
	watchCancel      context.CancelFunc
	adminProxy       *AdminProxy
	coordinatorMu    sync.Mutex
	coordinator      *connectionCoordinator
	// The following connection evidence is owned by operation.
	lastPlan               *model.ClientPlan
	lastNetworkFingerprint string
	// reconnectRetryAfter holds the earliest time a full reconnect (and its
	// address discovery) may run again after a recent connect failure. It protects
	// fnos.net from a reconnect/rediscovery storm when the NAS is unreachable.
	reconnectRetryAfter time.Time
	// reconnectBackoff is the next backoff interval to apply on the next failed
	// reconnect; it doubles up to maxReconnectBackoff and resets when connect
	// succeeds or a network-ready event fires.
	reconnectBackoff time.Duration
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
	webAuthenticator := options.WebAuthenticator
	if authenticator == nil {
		authenticator = NativeSessionClient{}
	}
	if webAuthenticator == nil {
		webAuthenticator, _ = authenticator.(AdminWebAuthenticator)
	}
	adminWebProbe := options.AdminWebProbe
	if adminWebProbe == nil {
		adminWebProbe = probeAdminWebSession
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
		webAuthenticator: webAuthenticator,
		adminWebProbe:    adminWebProbe,
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

// loadLocalProbeCache restores a previously persisted LAN probe configuration so
// the LOCAL path can be established without first contacting the public gateway.
// It is a best-effort hint: an absent or invalid cache is ignored. It must only
// be called once the IPC/privileged secret path is available (i.e. during
// connect), never from Start — the daemon may not have its privileged IPC ready
// yet and reading a secret would block Startup.
func (m *Manager) loadLocalProbeCache(fnID string) {
	cfg, found, _, err := m.store.LoadLocalProbeConfig(fnID)
	if err != nil {
		m.logger.Warn("load cached local probe configuration", "fn_id", fnID, "error", err)
		return
	}
	if !found {
		return
	}
	m.mu.Lock()
	m.localProbeConfig = &cfg
	m.mu.Unlock()
	m.logger.Info("cached local probe configuration loaded", "fn_id", fnID,
		"endpoint_count", len(cfg.Endpoints))
}

// persistLocalProbeConfig writes the local-probe cache to disk. Failures are
// logged but never fatal: the cache is an optimization, so a stale cache still
// falls back to fetching from the gateway.
func (m *Manager) persistLocalProbeConfig(fnID string, cfg model.LocalProbeConfiguration) {
	if err := m.store.SaveLocalProbeConfig(fnID, cfg); err != nil {
		m.logger.Warn("save cached local probe configuration", "fn_id", fnID, "error", err)
	}
}

func (m *Manager) Authorize(
	ctx context.Context,
	fnID string,
	cookies []Cookie,
) error {
	defer m.allowConnections()
	m.suspendConnections()
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
	defer m.allowConnections()
	m.suspendConnections()
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
	if err := m.authorize(ctx, fnID, cookies); err != nil {
		return err
	}
	m.refreshAdminWebSession(ctx, fnID, username, password, session.DeviceID)
	return nil
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
	// Non-administrator fnOS accounts may also register a device and establish a
	// network connection. Administrator status is retained separately and only
	// gates access to the admin-only management webview.
	logger.Info("gateway session validated", "administrator", bootstrap.Administrator)
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
			existing.Administrator = bootstrap.Administrator
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
		Administrator: bootstrap.Administrator,
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
	if coordinator := m.connectionCoordinator(); coordinator != nil {
		return coordinator.manual(ctx)
	}
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	attempt, cancel := context.WithTimeout(ctx, connectionAttemptTimeout)
	defer cancel()
	m.directRetryAfter = time.Time{}
	err := m.connect(attempt)
	if errors.Is(err, context.DeadlineExceeded) {
		err = m.fail(err)
	}
	m.noteReconnectOutcome(err)
	return err
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
	// A manual recheck of an unchanged healthy tunnel needs path discovery, not
	// another blocking configuration fetch. The configuration watch continues
	// to deliver changes; unhealthy or changed networks retain the full fetch.
	if isValidClientConfiguration(config.Configuration) && m.lastPlan != nil {
		if snapshot, snapshotErr := m.probe.Snapshot(); snapshotErr == nil && m.keepCurrentPlan(ctx, *m.lastPlan, snapshot) {
			return m.connectWithKnownConfiguration(ctx, *config)
		}
	}
	return m.connectWithConfig(ctx, *config)
}

func (m *Manager) Disconnect(ctx context.Context) error {
	m.suspendConnections()
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
	m.lastPlan = nil
	m.lastNetworkFingerprint = ""
	if err := m.removeNetwork(ctx); err != nil {
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
	network, statusErr := m.networkStatus(ctx)
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
	if err := m.removeNetwork(ctx); err != nil {
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
	m.suspendConnections()
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
			m.store.ClearAdminWebSession(config.FNID),
		); err != nil {
			return m.fail(err)
		}
		m.closeAdminProxy()
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
	m.suspendConnections()
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
	m.lastPlan = nil
	m.lastNetworkFingerprint = ""
	if err := m.removeNetwork(ctx); err != nil {
		return m.fail(err)
	}
	if config != nil {
		if err := m.store.Forget(config.FNID); err != nil {
			return m.fail(err)
		}
		m.closeAdminProxy()
		logging.FromContext(ctx, m.logger).Info("client identity forgotten",
			"fn_id", config.FNID, "device_id", config.DeviceID)
	}
	m.setStatus(model.ClientUnconfigured, "", "", nil)
	return nil
}

func (m *Manager) Close(ctx context.Context) error {
	m.suspendConnections()
	m.operation.Lock()
	defer m.operation.Unlock()
	m.cancelConfigurationWatch()
	m.bridge.Stop()
	m.localProbeConfig = nil
	m.lastPlan = nil
	m.lastNetworkFingerprint = ""
	m.mu.Lock()
	proxy := m.adminProxy
	m.adminProxy = nil
	m.mu.Unlock()
	if proxy != nil {
		closeErr := proxy.Close()
		if closeErr != nil {
			return closeErr
		}
	}
	return m.removeNetwork(ctx)
}

func (m *Manager) closeAdminProxy() {
	m.mu.Lock()
	proxy := m.adminProxy
	m.adminProxy = nil
	m.mu.Unlock()
	if proxy != nil {
		if err := proxy.Close(); err != nil {
			m.logger.Warn("close admin proxy", "error", err)
		}
	}
}

func (m *Manager) Run(ctx context.Context) {
	m.runCoordinator(ctx)
}

// handleRelayFailure runs in the coordinator worker, never in its event loop.
func (m *Manager) handleRelayFailure(ctx context.Context, err error) {
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil {
		return
	}
	config, loadErr := m.store.Load()
	if loadErr != nil || !canAutoConnect(config, m.Status()) {
		return
	}
	if model.AsError(err).Code == model.ErrorAuthRequired {
		recovered, recoverErr := m.recoverNativeSession(ctx, config.FNID, true)
		if recoverErr == nil && recovered {
			_ = m.reconnect(ctx, *config)
			return
		}
		if recoverErr != nil {
			err = recoverErr
		}
	}
	if model.AsError(err).Retryable {
		m.recordMaintenanceError("relay", err)
		return
	}
	m.bridge.Stop()
	if cleanupErr := m.removeNetwork(ctx); cleanupErr != nil {
		m.logger.Error("clean up network after relay failure", "error", cleanupErr)
	}
	_ = m.fail(err)
}

func (m *Manager) resume(ctx context.Context) error {
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if m.Status().State != model.ClientPaused {
		return nil
	}
	config, err := m.store.Load()
	if err != nil {
		return m.fail(err)
	}
	if config == nil {
		return nil
	}
	if config.AutoConnect {
		m.logger.Info("automatic connection requested", "fn_id", config.FNID, "device_id", config.DeviceID)
		err := m.connect(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			err = m.fail(err)
		}
		m.noteReconnectOutcome(err)
		return err
	}
	m.convergeStoppedNetwork(ctx, *config, m.Status())
	return nil
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
		if err != nil && model.AsError(err).Code == model.ErrorAuthRequired {
			// One recovery per rejected credential sequence. A second rejection
			// after recovery is terminal; polls while recovery is pending just wait.
			if m.authenticationRecoveryInProgress() || !nativeRecoveryAttempted && m.scheduleAuthenticationRecovery() {
				nativeRecoveryAttempted = true
				err = model.NewError(model.ErrorUnavailable, "gateway authentication recovery pending", true)
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
	if !m.scheduleConnection(workReconcile) {
		_ = m.reconnect(ctx, *config)
	}
	return nil
}

// Caller holds operation; background recovery never changes the user's intent.
func (m *Manager) reconnect(ctx context.Context, config LocalConfig) error {
	if !canAutoConnect(&config, m.Status()) {
		return nil
	}
	m.mu.RLock()
	retryAt := m.reconnectRetryAfter
	m.mu.RUnlock()
	if !retryAt.IsZero() && time.Now().Before(retryAt) {
		// Still in cooldown after a recent connect failure (NAS unreachable or
		// gateway rate-limiting). Skip rebuilding the tunnel — and therefore skip
		// re-running address discovery — so a flapping privileged/network event
		// source cannot spam fnos.net until it returns HTTP 429.
		return nil
	}
	m.cancelConfigurationWatch()
	reconnectContext, cancel := context.WithTimeout(ctx, connectionAttemptTimeout)
	defer cancel()
	err := m.connectWithKnownConfiguration(reconnectContext, config)
	if errors.Is(err, context.DeadlineExceeded) {
		err = m.fail(err)
	}
	m.noteReconnectOutcome(err)
	return err
}

// noteReconnectOutcome grows the reconnect backoff whenever a full reconnect
// fails with a retryable error, and resets it on success. On each failed attempt
// the next reconnect is gated behind the growing interval (capped at
// maxReconnectBackoff), which is what stops the sub-second rediscovery storm.
func (m *Manager) noteReconnectOutcome(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.reconnectBackoff = 0
		m.reconnectRetryAfter = time.Time{}
		return
	}
	if errors.Is(err, context.Canceled) || !model.AsError(err).Retryable {
		return
	}
	delay := m.reconnectBackoff
	if delay == 0 {
		delay = time.Second
	}
	m.reconnectRetryAfter = time.Now().Add(delay)
	if m.reconnectBackoff == 0 {
		m.reconnectBackoff = 2 * time.Second
	} else {
		m.reconnectBackoff *= 2
	}
	if m.reconnectBackoff > maxReconnectBackoff {
		m.reconnectBackoff = maxReconnectBackoff
	}
}

// resetReconnectCooldown clears the reconnect backoff so a recovery path (e.g.
// the physical network becoming ready again) can reconnect immediately.
func (m *Manager) resetReconnectCooldown() {
	m.mu.Lock()
	m.reconnectBackoff = 0
	m.reconnectRetryAfter = time.Time{}
	m.mu.Unlock()
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

// localProbeHealthChecks is how many consecutive failed LAN probe checks mark an
// established LOCAL path as lost. A single transient probe timeout must not tear
// down a working LAN link: doing so forces a costly reconnect that re-fetches
// the local-probe config over the public gateway (which itself may be flaky).
const localProbeHealthChecks = 2

// localProbeHealthRetryInterval is the small wait between consecutive probe
// checks inside a single maintainHealth pass.
const localProbeHealthRetryInterval = 300 * time.Millisecond

// localPathStillHealthy re-probes the NAS on the LAN a few times before
// declaring the LOCAL path lost, so one transient timeout does not drop an
// otherwise healthy direct path. It returns true on the first successful probe
// (refreshing the cached config) and false only after localProbeHealthChecks
// consecutive failures.
func (m *Manager) localPathStillHealthy(ctx context.Context, config *LocalConfig) bool {
	snapshot, snapshotErr := m.probe.Snapshot()
	if snapshotErr != nil {
		return false
	}
	for attempt := 0; attempt < localProbeHealthChecks; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(localProbeHealthRetryInterval):
			}
		}
		probeConfiguration := cloneLocalProbeConfiguration(m.localProbeConfig)
		if probeConfiguration == nil {
			return false
		}
		reachable, probeErr := m.probe.Reachable(
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
			return true
		}
	}
	return false
}

func (m *Manager) maintainPrivileged(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil {
		return
	}
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
	network, err := m.networkStatus(ctx)
	if ctx.Err() != nil {
		return
	}
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
	if ctx.Err() != nil {
		return
	}
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

	if (status.State == model.ClientDirect || status.State == model.ClientRelay) && m.tryLocalUpgrade(ctx, *config) {
		return
	}
	if ctx.Err() != nil {
		return
	}

	switch status.State {
	case model.ClientDirect, model.ClientRelay:
		networkStatus, statusErr := m.networkStatus(ctx)
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
		if healthErr != nil && status.State == model.ClientRelay && statusErr == nil &&
			networkStatus.Active && !networkStatus.Degraded && !m.bridge.Connected() {
			m.recordMaintenanceError("relay", healthErr)
			return
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
		if !m.localPathStillHealthy(ctx, config) {
			if ctx.Err() != nil {
				return
			}
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
		discoveryContext, cancelDiscovery := context.WithTimeout(ctx, discoveryTimeout)
		discovery, discoveryErr := m.discoverer.Discover(discoveryContext, config.FNID)
		cancelDiscovery()
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
		attempt := context.WithValue(ctx, discoveryEvidenceKey{}, discoveryEvidence{discovery, nil})
		_ = m.reconnect(attempt, *config)
	}
}

// Connection budgets are shared by manual and automatic attempts.
const (
	connectionAttemptTimeout  = 20 * time.Second
	discoveryTimeout          = 3 * time.Second
	directPhaseTimeout        = 6 * time.Second
	privilegedCallTimeout     = 2 * time.Second
	maxReconnectBackoff       = 60 * time.Second
	configurationFetchTimeout = 3 * time.Second
)

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
	networkStatus, err := m.networkStatus(ctx)
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
	diagnostics.NASAddress = m.nasAddress
	m.mu.RUnlock()
	diagnostics.Direct.LocalPublicIPv6 = snapshot.HasPublicIPv6
	if configErr == nil && config != nil {
		diagnostics.FNID = config.FNID
		diagnostics.ClientAddress = config.Configuration.ClientAddress
		diagnostics.Administrator = config.Administrator
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

// AdminProxyURL starts (or reuses) the local reverse proxy that fronts the NAS
// management UI. It reads only the independent Web session; the CLI session and
// its gateway cookie remain owned by the tunnel path.
func (m *Manager) AdminProxyURL(ctx context.Context) (string, error) {
	// The management UI uses its dedicated Web token, not the CLI tunnel token.
	config, err := m.store.Load()
	if err != nil {
		return "", err
	}
	if config == nil || config.FNID == "" {
		return "", errors.New("no matched NAS configured")
	}
	if !config.Administrator {
		return "", model.WrapError(
			model.ErrorPermissionDenied,
			"only administrator accounts can access the management page",
			false, nil,
		)
	}
	session, found, err := m.store.LoadAdminWebSession(config.FNID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", model.WithOperation(model.NewError(
			model.ErrorAuthRequired, "fnOS management session is unavailable; sign in again", false,
		), "admin_proxy.probe")
	}
	if err := m.adminWebProbe(ctx, config.FNID, session.Token); err != nil {
		if model.AsError(err).Code == model.ErrorAuthRequired {
			_ = m.store.ClearAdminWebSession(config.FNID)
			m.closeAdminProxy()
		}
		return "", err
	}
	m.mu.Lock()
	proxy := m.adminProxy
	m.mu.Unlock()
	if proxy == nil {
		proxy = &AdminProxy{
			ListenAddress: "127.0.0.1:0",
			FNIDProvider: func() (string, error) {
				config, err := m.store.Load()
				if err != nil {
					return "", err
				}
				if config == nil || config.FNID == "" {
					return "", errors.New("no matched NAS configured")
				}
				return config.FNID, nil
			},
			CookieProvider: func(fnID string) ([]Cookie, error) {
				session, found, err := m.store.LoadAdminWebSession(fnID)
				if err != nil || !found {
					return nil, err
				}
				return nativeGatewayCookies(fnID, session.Token), nil
			},
			Logger: m.logger,
		}
		m.mu.Lock()
		if m.adminProxy == nil {
			m.adminProxy = proxy
		} else {
			proxy = m.adminProxy
		}
		m.mu.Unlock()
	}
	base, err := proxy.Start()
	if err != nil {
		return "", err
	}
	return base.String(), nil
}

func probeAdminWebSession(ctx context.Context, fnID string, token string) error {
	target := "https://" + fnID + ".fnos.net/app/fncpn/api/v1/admin/snapshot"
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:               nil,
			TLSHandshakeTimeout: 10 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return probeAdminWebSessionWithClient(ctx, client, target, token)
}

func probeAdminWebSessionWithClient(
	ctx context.Context,
	client *http.Client,
	target string,
	token string,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return model.WithOperation(err, "admin_proxy.probe")
	}
	request.Header.Set("Cookie", (&http.Cookie{Name: "mode", Value: "relay"}).String()+
		"; "+(&http.Cookie{Name: "fnos-token", Value: token}).String())
	response, err := client.Do(request)
	if err != nil {
		return model.WithOperation(model.NormalizeError(
			err, model.ErrorUnavailable, "fnOS management session probe failed", true,
		), "admin_proxy.probe")
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
	if readErr != nil {
		return model.WithOperation(model.NormalizeError(
			readErr, model.ErrorUnavailable, "read fnOS management session probe", true,
		), "admin_proxy.probe")
	}
	if authErr := gatewayAuthenticationError(response, data); authErr != nil {
		authErr.Operation = "admin_proxy.probe"
		authErr.HTTPStatus = response.StatusCode
		return authErr
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return nil
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return HTTPResponseError(response, "admin_proxy.probe", nil)
}

func (m *Manager) refreshAdminWebSession(
	ctx context.Context,
	fnID string,
	username string,
	password string,
	nativeDeviceID string,
) {
	if previous, found, err := m.store.LoadAdminWebSession(fnID); err != nil {
		m.logger.Warn("admin auth: load previous Web session failed",
			"fn_id", fnID, "error", err)
	} else if found && previous.Username != strings.TrimSpace(username) {
		if err := m.store.ClearAdminWebSession(fnID); err != nil {
			m.logger.Warn("admin auth: clear previous Web session failed",
				"fn_id", fnID, "error", err)
		}
	}
	if m.webAuthenticator == nil {
		return
	}
	webSession, err := m.webAuthenticator.WebLogin(
		ctx, fnID, username, password,
		adminWebDeviceID(nativeDeviceID), m.deviceName,
	)
	if err != nil {
		m.logger.Warn("admin auth: encrypted Web token login unavailable",
			"fn_id", fnID, "error", err)
		return
	}
	if err := m.store.SaveAdminWebSession(webSession); err != nil {
		m.logger.Warn("admin auth: save Web session failed",
			"fn_id", fnID, "error", err)
		return
	}
	m.logger.Info("admin auth: Web session saved",
		"fn_id", fnID, "token_present", webSession.Token != "")
}

func adminWebDeviceID(nativeDeviceID string) string {
	digest := sha256.Sum256([]byte(nativeDeviceID + "\x00admin-web"))
	return fmt.Sprintf("fncpn-web-%x", digest[:16])
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
	attemptContext, cancelAttempt := context.WithTimeout(ctx, connectionAttemptTimeout)
	defer cancelAttempt()
	ctx = attemptContext
	connectionStarted := time.Now()
	m.setDirectDiagnostics(model.DirectDiagnostics{Reason: "checking"})
	logger := logging.FromContext(ctx, m.logger).With("fn_id", config.FNID, "device_id", config.DeviceID)
	logger.Info("client connection started")
	initialStatus := m.Status()
	if initialStatus.State != model.ClientLocal && initialStatus.State != model.ClientDirect && initialStatus.State != model.ClientRelay {
		m.setStatus(model.ClientProbing, "", "", nil)
	}
	if ctx.Err() != nil {
		return ctx.Err()
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
	networkSnapshot, err := m.probe.Snapshot()
	if err != nil {
		return m.fail(err)
	}
	logger.Info("local probe started")
	selection := m.selectLocal(ctx, remote, config, networkSnapshot)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	local := selection.reachable
	probeConfiguration := selection.configuration
	observedEndpoints := selection.endpoints
	logger.Info("local probe completed", "reachable", local)
	// Persist the NAS LAN IPv4 from every endpoint observed (cache + gateway
	// refresh). It stays reachable once the tunnel is up regardless of which
	// transport path (LOCAL / IPv6 DIRECT / relay) was actually selected, so the
	// UI always shows the NAS IPv4 on detail view after a successful connection.
	m.mu.Lock()
	m.nasAddress = primaryNASAddress(observedEndpoints)
	m.mu.Unlock()
	var discoveryResults chan discoveryEvidence
	if !local {
		discoveryResults = make(chan discoveryEvidence, 1)
		discoveryContext, cancelDiscovery := context.WithCancel(ctx)
		defer cancelDiscovery()
		go func() { discoveryResults <- m.discoverForAttempt(discoveryContext, config.FNID) }()
	}
	latest := config.Configuration
	if known == nil && !(local && isValidClientConfiguration(config.Configuration)) {
		logger.Info("device configuration requested")
		// Bound the gateway fetch so a slow/unreachable public gateway cannot hold a
		// manual "重新检测" or reconnect for the full 30s http.Client timeout. A fetch
		// that times out (or fails for any non-auth reason) falls back to the
		// previously known local configuration, if any, so an on-LAN reconnect is not
		// blocked by an unreachable gateway. Configuration refreshes are additionally
		// driven by the background WatchConfiguration loop.
		fetchContext, cancelFetch := context.WithTimeout(ctx, configurationFetchTimeout)
		latest, err = remote.Configuration(fetchContext, config.DeviceID)
		cancelFetch()
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
					cookies, err = m.store.LoadCookies(config.FNID)
					if err == nil {
						remote, err = m.remote(
							config.FNID,
							cookies,
							func(updated []Cookie) error {
								mergeErr := m.store.MergeCookies(config.FNID, cookies, updated)
								cookies = slices.Clone(updated)
								return mergeErr
							},
						)
					}
					var retryCanceler context.CancelFunc
					fetchContext, retryCanceler = context.WithTimeout(ctx, configurationFetchTimeout)
					if err == nil {
						latest, err = remote.Configuration(fetchContext, config.DeviceID)
					}
					retryCanceler()
				}
			}
			if err != nil {
				// The gateway is unreachable (timeout/network). Reuse any usable
				// local configuration so a manual retry on the NAS LAN still comes
				// up even when the public gateway is slow or down.
				if isValidClientConfiguration(config.Configuration) {
					logger.Warn("using known local configuration after gateway fetch failed",
						"error", err, "address", config.Configuration.ClientAddress)
					latest = config.Configuration
					err = nil
				} else {
					return m.fail(err)
				}
			}
		}
	} else if known != nil {
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

	if local {
		if err := m.establishLocal(ctx, config, networkSnapshot, probeConfiguration); err != nil {
			return err
		}
		logger.Info("client connected", "state", model.ClientLocal, "path", "local", "elapsed_ms", time.Since(connectionStarted).Milliseconds())
		return nil
	}
	logger.Info("FN Connect discovery started")
	evidence := <-discoveryResults
	discovery, err := evidence.value, evidence.err
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
		directContext, cancelDirect := context.WithTimeout(ctx, directBudget(ctx))
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
			if m.keepCurrentPlan(ctx, plan, networkSnapshot) {
				direct.Reason = "connected"
				m.setDirectDiagnostics(direct)
				return nil
			}
			if directContext.Err() != nil {
				break
			}
			if err := m.replaceNetwork(directContext); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				break
			}
			status, applyErr := m.applyPlan(directContext, plan)
			if applyErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				direct.LastError = model.PublicError(applyErr)
				break
			}
			candidateContext, cancelCandidate := context.WithTimeout(directContext, 3*time.Second)
			status, handshakeErr := m.waitForHandshake(candidateContext, status, started)
			cancelCandidate()
			if handshakeErr != nil {
				direct.LastError = model.PublicError(model.WithOperation(handshakeErr, "direct.handshake"))
				logger.Warn("direct connection failed; trying next path", logging.ErrorAttrs(
					model.WithOperation(handshakeErr, "direct.handshake"))...)
				// The next candidate/fallback owns replacement and cleanup; doing it
				// here as well would remove the same network twice.
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := m.commitConnection(ctx, func() {
				m.lastPlan = &plan
				m.lastNetworkFingerprint = networkSnapshot.Fingerprint()
				direct.Reason = "connected"
				direct.LastError = nil
				m.directRetryAfter = time.Time{}
				m.setDirectDiagnostics(direct)
				m.setNetworkStatus(
					model.ClientDirect,
					"ipv6",
					status,
				)
			}); err != nil {
				return err
			}
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
	// A same-plan manual check must preserve a healthy relay bridge and tunnel.
	if m.lastPlan != nil && m.lastPlan.Mode == "relay" {
		plan := clientPlan(config.Configuration, privateKey.String(), "relay", m.lastPlan.Endpoint, allowedIPs, 1280)
		if m.keepCurrentPlan(ctx, plan, networkSnapshot) {
			return nil
		}
	}
	relayContext, cancelRelay := context.WithTimeout(ctx, relayPhaseTimeout)
	defer cancelRelay()
	if err := m.replaceNetwork(relayContext); err != nil {
		return m.fail(err)
	}
	logger.Info("relay path selected", "relay_url", model.SafeURL(relayURL))
	endpoint, err := m.bridge.Start(relayContext, relayURL, func() ([]Cookie, error) {
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
	status, err := m.applyPlan(relayContext, plan)
	if err != nil {
		m.bridge.Stop()
		return m.fail(err)
	}
	if err := m.bridge.BindPeer(status.ListenPort); err != nil {
		m.bridge.Stop()
		cleanupErr := m.removeNetwork(ctx)
		return m.fail(errors.Join(err, cleanupErr))
	}
	status, err = m.waitForHandshake(relayContext, status, started)
	if err != nil {
		m.bridge.Stop()
		cleanupErr := m.removeNetwork(ctx)
		return m.fail(errors.Join(err, cleanupErr))
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := m.commitConnection(ctx, func() {
		m.lastPlan = &plan
		m.lastNetworkFingerprint = networkSnapshot.Fingerprint()
		m.setNetworkStatus(
			model.ClientRelay,
			"fn-connect",
			status,
		)
	}); err != nil {
		return err
	}
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
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, m.handshakeTimeout)
	defer cancelHandshake()
	ctx = handshakeContext
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
			next, err := m.networkStatus(ctx)
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

// isValidClientConfiguration reports whether a previously known client
// configuration is usable enough to establish the tunnel when the gateway fetch
// is unreachable. It needs at least the WireGuard keys/addresses to build the
// link; an empty configuration cannot be reused.
func isValidClientConfiguration(config model.ClientConfiguration) bool {
	return config.ServerPublicKey != "" &&
		config.ServerAddress != "" &&
		config.ClientAddress != ""
}

// primaryNASAddress returns the first private IPv4 host from the local-probe
// endpoints. These are the NAS gateway LAN addresses that become reachable once
// the VPN tunnel is up. Returns an empty string when no private IPv4 endpoint
// is present.
func primaryNASAddress(endpoints []string) string {
	for _, endpoint := range endpoints {
		host, _, splitErr := net.SplitHostPort(endpoint)
		if splitErr != nil {
			continue
		}
		address, parseErr := netip.ParseAddr(host)
		if parseErr != nil || !address.Is4() || !address.IsPrivate() {
			continue
		}
		return host
	}
	return ""
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

// reportedConnectionError preserves the cause while marking that its failure
// has already been logged and committed to status by an inner operation.
type reportedConnectionError struct{ error }

func (e *reportedConnectionError) Unwrap() error { return e.error }

func (m *Manager) fail(err error) error {
	var reported *reportedConnectionError
	if errors.As(err, &reported) {
		return err
	}
	typed := model.AsError(err)
	if typed.Code == model.ErrorCanceled {
		m.logger.Debug("client operation canceled", logging.ErrorAttrs(err)...)
		return err
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
	return &reportedConnectionError{err}
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
			ok, probeErr := probeLocalEndpoint(probeContext, p.HTTPClient, snapshot.interfaceForEndpoint(endpoint),
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
			Timeout:   localEndpointTimeout,
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
			Timeout: localEndpointTimeout,
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
	snapshot := networkSnapshot(interfaces, defaultInterface, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	})
	enrichPhysicalNetwork(&snapshot)
	return snapshot, nil
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
		link := PhysicalLink{Name: networkInterface.Name, Index: networkInterface.Index}
		values, err := addresses(networkInterface)
		if err != nil {
			continue
		}
		for _, value := range values {
			prefix, err := netip.ParsePrefix(value.String())
			if err != nil {
				continue
			}
			if prefix.Addr().Is4() {
				link.Prefixes = append(link.Prefixes, prefix.Masked())
				link.Addresses = append(link.Addresses, prefix.Addr())
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
		if len(link.Addresses) > 0 {
			snapshot.Links = append(snapshot.Links, link)
		}
	}
	slices.Sort(snapshot.IPv6Networks)
	return snapshot
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
