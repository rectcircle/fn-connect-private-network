//go:build windows

package windows

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.org/x/sys/windows"
)

type ClientEngine struct {
	mu             sync.Mutex
	runtime        *WireGuardRuntime
	network        *NetworkConfigurator
	logger         *slog.Logger
	interfaceName  string
	ownerSID       string
	ownerSession   uint32
	degraded       bool
	powerResumes   atomic.Uint64
	notify         func()
	once           sync.Once
	statusSnapshot atomic.Pointer[model.ClientPrivilegedStatus]
}

var consoleSessionChanges = make(chan struct{}, 1)
var activeClientEngine atomic.Pointer[ClientEngine]

func NewClientEngine(logger *slog.Logger, stateDirectory string) *ClientEngine {
	if logger == nil {
		logger = slog.Default()
	}
	engine := &ClientEngine{runtime: NewWireGuardRuntime(logger), network: NewNetworkConfigurator(stateDirectory), logger: logger}
	activeClientEngine.Store(engine)
	return engine
}

// NotifyConsoleSessionChange wakes the service-side owner check immediately
// after SCM reports a Windows session event. The one-slot buffer coalesces
// bursts such as console connect/disconnect during a fast user switch.
func NotifyConsoleSessionChange() {
	select {
	case consoleSessionChanges <- struct{}{}:
	default:
	}
}

func (e *ClientEngine) Available() bool {
	return e != nil && e.runtime != nil && e.network != nil && e.network.Available()
}

func (e *ClientEngine) SetLifecycleNotifier(notify func()) {
	e.mu.Lock()
	e.notify = notify
	pendingResume := e.powerResumes.Load() > 0
	e.mu.Unlock()
	if pendingResume && notify != nil {
		notify()
	}
}

// NotifyPowerResume publishes an SCM power-resume event through the existing
// privileged lifecycle pipe so the user daemon can immediately check tunnel
// health across the process boundary.
func (e *ClientEngine) NotifyPowerResume() {
	e.mu.Lock()
	e.powerResumes.Add(1)
	notify := e.notify
	e.mu.Unlock()
	if notify != nil {
		notify()
	}
}

// PowerResumeGeneration is included in privileged lifecycle responses, allowing
// the user daemon to distinguish resume events from ordinary service changes.
func (e *ClientEngine) PowerResumeGeneration() uint64 {
	return e.powerResumes.Load()
}

func NotifyPowerResume() {
	if engine := activeClientEngine.Load(); engine != nil {
		engine.NotifyPowerResume()
	}
}

func (e *ClientEngine) Status() model.ClientPrivilegedStatus {
	// Driver calls can take seconds. Report the last completed operation while
	// a mutation owns the lock instead of timing out unrelated health requests.
	if !e.mu.TryLock() {
		if status := e.statusSnapshot.Load(); status != nil {
			return *status
		}
		return model.ClientPrivilegedStatus{}
	}
	defer e.mu.Unlock()
	status := e.runtime.Status()
	status.Degraded = status.Degraded || e.degraded
	e.statusSnapshot.Store(&status)
	return status
}

func (e *ClientEngine) publishStatusLocked() {
	var status model.ClientPrivilegedStatus
	if e.runtime != nil {
		status = e.runtime.Status()
	}
	status.Degraded = status.Degraded || e.degraded
	e.statusSnapshot.Store(&status)
}

func (e *ClientEngine) Recover(ctx context.Context) error {
	if !e.Available() {
		return model.NewError(model.ErrorUnavailable, "Windows Wintun network engine is unavailable", false)
	}
	if err := e.network.Recover(ctx); err != nil {
		e.mu.Lock()
		e.degraded = true
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	e.degraded = false
	e.mu.Unlock()
	e.once.Do(func() { go e.monitorConsoleSession(ctx) })
	return nil
}

func (e *ClientEngine) Apply(ctx context.Context, plan model.ClientPlan) error {
	normalized, err := wgconfig.NormalizeClientPlan(plan)
	if err != nil {
		return model.WrapError(model.ErrorInvalidArgument, "invalid client network plan", false, err)
	}
	if !e.Available() {
		return model.NewError(model.ErrorUnavailable, "Windows Wintun network engine is unavailable", false)
	}
	identity, ok := peer.IdentityFromContext(ctx)
	if !ok {
		return model.NewError(model.ErrorPermissionDenied, "only the active local console session may configure the tunnel", false)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishStatusLocked()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	console, consoleSID := windowsConsoleIdentity()
	if !authorizedConsolePeer(identity, consoleSID, console) {
		return model.NewError(model.ErrorPermissionDenied, "only the active local console session may configure the tunnel", false)
	}
	if e.degraded || e.interfaceName != "" || e.runtime.Status().Active {
		if err := e.cleanupLocked(ctx); err != nil {
			return err
		}
	}
	started := time.Now()
	name, err := e.runtime.Start(ctx, normalized)
	e.logger.Info("Windows tunnel runtime setup finished", "elapsed_ms", time.Since(started).Milliseconds(), "error", err)
	if err != nil {
		return err
	}
	e.interfaceName = name
	started = time.Now()
	networkErr := e.network.Apply(ctx, name, normalized)
	e.logger.Info("Windows addresses and routes setup finished", "elapsed_ms", time.Since(started).Milliseconds(), "error", networkErr)
	if err := networkErr; err != nil {
		cleanupErr := e.cleanupLocked(context.Background())
		return errors.Join(err, cleanupErr)
	}
	console, consoleSID = windowsConsoleIdentity()
	if !authorizedConsolePeer(identity, consoleSID, console) {
		cleanupErr := e.cleanupLocked(context.Background())
		return errors.Join(
			model.NewError(model.ErrorPermissionDenied, "local console session changed while applying the tunnel", false),
			cleanupErr,
		)
	}
	e.ownerSID, e.ownerSession, e.degraded = identity.SID, identity.SessionID, false
	return nil
}

func (e *ClientEngine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer e.publishStatusLocked()
	if err := ctx.Err(); err != nil {
		return err
	}
	return e.cleanupLocked(ctx)
}

func (e *ClientEngine) cleanupLocked(ctx context.Context) error {
	started := time.Now()
	defer func() {
		e.publishStatusLocked()
		if e.logger != nil {
			e.logger.Info("Windows tunnel cleanup finished", "elapsed_ms", time.Since(started).Milliseconds(), "degraded", e.degraded)
		}
	}()
	name := e.interfaceName
	if name == "" && e.runtime != nil {
		name = e.runtime.Status().Interface
	}
	var err error
	if name != "" {
		err = e.network.Remove(ctx, name)
	}
	if err != nil {
		e.degraded = true
		return err
	}
	if e.runtime != nil {
		e.runtime.Stop()
	}
	e.interfaceName, e.ownerSID, e.ownerSession, e.degraded = "", "", 0, false
	return nil
}

func (e *ClientEngine) monitorConsoleSession(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	monitorConsoleSessionEvents(ctx, ticker.C, consoleSessionChanges, e.reconcileConsoleOwner)
}

func monitorConsoleSessionEvents(
	ctx context.Context,
	poll <-chan time.Time,
	events <-chan struct{},
	reconcile func(),
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-poll:
			reconcile()
		case <-events:
			reconcile()
		}
	}
}

func (e *ClientEngine) reconcileConsoleOwner() {
	e.reconcileConsoleOwnerWith(windowsConsoleIdentity)
}

func (e *ClientEngine) reconcileConsoleOwnerWith(identity func() (uint32, string)) {
	// Keep identity lookup, ownership comparison, and cleanup in one critical
	// section. Otherwise a console switch can race with a successful Apply from
	// the new owner and let a stale check remove the new owner's healthy tunnel.
	e.mu.Lock()
	if e.interfaceName == "" {
		e.mu.Unlock()
		return
	}
	console, consoleSID := identity()
	if sameConsoleOwner(e.ownerSID, e.ownerSession, consoleSID, console) {
		e.mu.Unlock()
		return
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := e.cleanupLocked(cleanup)
	cancel()
	notify := e.notify
	e.mu.Unlock()
	if err != nil {
		e.logger.Error("remove tunnel after console session changed", "error", err)
		return
	}
	if notify != nil {
		notify()
	}
}

func windowsConsoleIdentity() (uint32, string) {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == 0xffffffff {
		return session, ""
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(session, &token); err != nil {
		return session, ""
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return session, ""
	}
	return session, user.User.Sid.String()
}

func sameConsoleOwner(ownerSID string, ownerSession uint32, consoleSID string, consoleSession uint32) bool {
	return ownerSID != "" && consoleSID != "" && ownerSID == consoleSID && ownerSession != 0xffffffff && ownerSession == consoleSession
}

func authorizedConsolePeer(identity ipc.PeerIdentity, consoleSID string, consoleSession uint32) bool {
	return sameConsoleOwner(identity.SID, identity.SessionID, consoleSID, consoleSession)
}

var _ privileged.ClientEngine = (*ClientEngine)(nil)
