package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type connectionWork uint8

const (
	workStartup connectionWork = 1 << iota
	workManual
	workReconcile
	workHealth
	workPrivileged
	workRelay
	workAuthRecovery
)

func (w connectionWork) String() string {
	var reasons []string
	for _, entry := range []struct {
		work connectionWork
		name string
	}{
		{workStartup, "startup"}, {workManual, "manual"}, {workReconcile, "network_or_configuration"},
		{workHealth, "health_or_retry"}, {workPrivileged, "privileged"}, {workRelay, "relay"}, {workAuthRecovery, "authentication"},
	} {
		if w&entry.work != 0 {
			reasons = append(reasons, entry.name)
		}
	}
	return strings.Join(reasons, ",")
}

// The coordinator owns scheduling. A single cancellable worker owns network
// mutations under operation. Notification producers never wait for that lock.
type connectionCoordinator struct {
	manager       *Manager
	mu            sync.Mutex
	wake          chan struct{}
	done          chan struct{}
	pending       connectionWork
	waiters       []chan error
	activeWaiters []chan error
	active        connectionWork
	activeContext context.Context
	activeEpoch   uint64
	cancel        context.CancelFunc
	epoch         uint64
	suspended     bool
	stopped       bool
	relayError    error
}

type connectionResult struct {
	epoch uint64
	err   error
}

func (m *Manager) connectionCoordinator() *connectionCoordinator {
	m.coordinatorMu.Lock()
	defer m.coordinatorMu.Unlock()
	return m.coordinator
}

func (m *Manager) scheduleConnection(work connectionWork) bool {
	c := m.connectionCoordinator()
	if c == nil {
		return false
	}
	c.submit(work)
	return true
}

func (c *connectionCoordinator) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}
func (c *connectionCoordinator) submit(work connectionWork) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.suspended {
		return
	}
	// A connection already verifies the privileged state; notifications generated
	// by its own Remove/Apply do not require another maintenance pass.
	if work == workPrivileged && c.active&(workStartup|workManual|workReconcile) != 0 {
		return
	}
	c.pending |= work
	c.signal()
}

func (c *connectionCoordinator) manual(ctx context.Context) error {
	waiter := make(chan error, 1)
	config, _ := c.manager.store.Load()
	status := c.manager.Status()
	canJoinAutomatic := canAutoConnect(config, status) &&
		(status.State == model.ClientProbing || status.State == model.ClientLocal || status.State == model.ClientDirect || status.State == model.ClientRelay)
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return context.Canceled
	}
	c.suspended = false
	joinable := c.active&workManual != 0 || canJoinAutomatic &&
		(c.active&workReconcile != 0 || c.active&workStartup != 0 && status.State == model.ClientProbing)
	if joinable && c.activeEpoch == c.epoch && c.activeContext != nil && c.activeContext.Err() == nil {
		c.activeWaiters = append(c.activeWaiters, waiter)
		c.manager.logger.Debug("connection request merged", "network_generation", c.epoch)
	} else {
		c.pending |= workManual
		c.waiters = append(c.waiters, waiter)
		c.signal()
	}
	c.mu.Unlock()
	select {
	case err := <-waiter:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return context.Canceled
	}
}

func notifyConnectionWaiters(waiters []chan error, err error) {
	for _, waiter := range waiters {
		waiter <- err
	}
}

func (m *Manager) suspendConnections() {
	if c := m.connectionCoordinator(); c != nil {
		c.mu.Lock()
		c.epoch++
		c.suspended = true
		c.pending = 0
		if c.cancel != nil {
			c.cancel()
		}
		notifyConnectionWaiters(c.waiters, context.Canceled)
		notifyConnectionWaiters(c.activeWaiters, context.Canceled)
		c.waiters, c.activeWaiters = nil, nil
		c.mu.Unlock()
	}
}

func (m *Manager) allowConnections() {
	if c := m.connectionCoordinator(); c != nil {
		c.mu.Lock()
		c.suspended = false
		c.mu.Unlock()
		c.submit(workHealth)
	}
}

func (c *connectionCoordinator) networkChanged() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped || c.suspended {
		return
	}
	c.epoch++
	if c.cancel != nil {
		c.cancel()
	}
	// Manual callers await convergence on the latest network, not an obsolete attempt.
	c.waiters = append(c.waiters, c.activeWaiters...)
	c.activeWaiters = nil
	c.pending |= workReconcile | (c.active & workManual)
	c.signal()
}

func (m *Manager) scheduleAuthenticationRecovery() bool {
	if c := m.connectionCoordinator(); c != nil {
		c.submit(workAuthRecovery)
		return true
	}
	return false
}

func (m *Manager) authenticationRecoveryInProgress() bool {
	c := m.connectionCoordinator()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return (c.pending|c.active)&workAuthRecovery != 0
}

func (m *Manager) recoverGatewayAuthentication(ctx context.Context) {
	m.operation.Lock()
	defer m.operation.Unlock()
	if ctx.Err() != nil {
		return
	}
	config, err := m.store.Load()
	if err != nil || !canAutoConnect(config, m.Status()) {
		return
	}
	recovered, err := m.recoverNativeSession(ctx, config.FNID, true)
	if ctx.Err() != nil {
		return
	}
	if err == nil && recovered {
		return
	}
	if err == nil {
		err = model.NewError(model.ErrorAuthRequired, "FN Connect authorization is required", false)
	}
	if model.AsError(err).Retryable {
		m.recordMaintenanceError("authentication", err)
		return
	}
	_ = m.fail(err)
	m.bridge.Stop()
	m.convergeStoppedNetwork(ctx, *config, m.Status())
}

func (m *Manager) runCoordinator(ctx context.Context) {
	c := &connectionCoordinator{manager: m, wake: make(chan struct{}, 1), done: make(chan struct{}), pending: workStartup}
	m.coordinatorMu.Lock()
	if m.coordinator != nil {
		m.coordinatorMu.Unlock()
		return
	}
	m.coordinator = c
	m.coordinatorMu.Unlock()
	defer func() {
		c.mu.Lock()
		c.stopped = true
		if c.cancel != nil {
			c.cancel()
		}
		notifyConnectionWaiters(c.waiters, context.Canceled)
		notifyConnectionWaiters(c.activeWaiters, context.Canceled)
		c.waiters, c.activeWaiters = nil, nil
		close(c.done)
		c.mu.Unlock()
		m.coordinatorMu.Lock()
		m.coordinator = nil
		m.coordinatorMu.Unlock()
	}()

	var networkEvents <-chan model.NetworkChange
	if m.monitor != nil {
		networkEvents = m.monitor.Events(ctx)
	}
	bridgeEvents := m.bridge.Events()
	privilegedEvents := m.privilegedLifecycleEvents(ctx)
	previous := m.networkFingerprint()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	health := time.NewTicker(15 * time.Second)
	defer health.Stop()
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer retry.Stop()
	var retryEvents <-chan time.Time
	var settle *time.Timer
	var settleEvents <-chan time.Time
	defer func() {
		if settle != nil {
			settle.Stop()
		}
	}()
	results := make(chan connectionResult, 1)
	running := false
	watchStarted := false
	promotionChecks := 0
	var attempt uint64
	c.signal()
	observe := func() {
		snapshot, err := m.probe.Snapshot()
		if err != nil {
			return
		} // An observation failure is not an empty network.
		current := snapshot.Fingerprint()
		if current != previous {
			previous = current
			promotionChecks = 0
			m.resetReconnectCooldown()
			c.networkChanged()
		}
	}
	armRetry := func() {
		retry.Stop()
		retryEvents = nil
		config, err := m.store.Load()
		status := m.Status()
		if err != nil || !canAutoConnect(config, status) {
			return
		}
		m.mu.RLock()
		at := m.reconnectRetryAfter
		m.mu.RUnlock()
		delay := time.Duration(0)
		if !at.IsZero() {
			delay = max(time.Until(at), time.Millisecond)
		} else if status.State == model.ClientReconnecting {
			delay = time.Second
		} else if status.State == model.ClientDirect || status.State == model.ClientRelay {
			promotionChecks++
			delay = 5 * time.Second // Short-term LOCAL promotion after a network switch.
			if promotionChecks > 3 {
				delay = 30 * time.Second
			}
		}
		if delay > 0 {
			retry.Reset(delay)
			retryEvents = retry.C
		}
	}
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			if c.cancel != nil {
				c.cancel()
			}
			c.mu.Unlock()
			// A worker may be doing bounded rollback. Join it before daemon shutdown.
			if running {
				<-results
			}
			return
		case <-c.wake:
			if running {
				continue
			}
			c.mu.Lock()
			work := c.pending
			if c.suspended {
				work = 0
			}
			if work == 0 {
				c.mu.Unlock()
				continue
			}
			c.pending = 0
			c.active = work
			c.activeWaiters, c.waiters = c.waiters, nil
			epoch := c.epoch
			attempt++
			budget := connectionAttemptTimeout
			if _, ok := m.probe.(localAccessWatcher); ok {
				budget += localAccessWaitTimeout + configurationFetchTimeout + localEndpointTimeout
			}
			workerCtx, cancel := context.WithTimeout(ctx, budget)
			workerCtx = logging.WithLogger(workerCtx, m.logger.With("attempt_id", fmt.Sprint(attempt), "network_generation", epoch, "trigger", work.String()))
			workerCtx = context.WithValue(workerCtx, connectionAttemptKey{}, connectionAttemptIdentity{c, epoch})
			c.cancel = cancel
			c.activeContext, c.activeEpoch = workerCtx, epoch
			relayErr := c.relayError
			c.mu.Unlock()
			running = true
			go func() {
				err := m.runConnectionWork(workerCtx, work, relayErr)
				cancel()
				results <- connectionResult{epoch: epoch, err: err}
			}()
		case result := <-results:
			running = false
			if !watchStarted {
				watchStarted = true
				go m.watchConfiguration(ctx)
			}
			c.mu.Lock()
			if result.epoch == c.epoch {
				notifyConnectionWaiters(c.activeWaiters, result.err)
			}
			c.activeWaiters = nil
			c.active = 0
			c.cancel = nil
			c.activeContext = nil
			c.mu.Unlock()
			armRetry()
			c.signal()
		case _, ok := <-networkEvents:
			if !ok {
				networkEvents = nil
				continue
			}
			// Fixed leading-edge window: continuous notifications cannot postpone
			// observation indefinitely. Periodic polling also repairs lost OS events.
			if settleEvents == nil {
				if settle == nil {
					settle = time.NewTimer(250 * time.Millisecond)
				} else {
					settle.Reset(250 * time.Millisecond)
				}
				settleEvents = settle.C
			}
		case <-settleEvents:
			settleEvents = nil
			observe()
		case <-poll.C:
			observe()
		case <-health.C:
			c.submit(workHealth)
		case <-retryEvents:
			retryEvents = nil
			c.submit(workHealth)
		case _, ok := <-privilegedEvents:
			if !ok {
				privilegedEvents = nil
				continue
			}
			c.submit(workPrivileged)
		case err, ok := <-bridgeEvents:
			if !ok {
				bridgeEvents = nil
				continue
			}
			if err == nil {
				m.clearMaintenanceError()
				continue
			}
			c.mu.Lock()
			c.relayError = err
			c.mu.Unlock()
			c.submit(workRelay)
		}
	}
}

func (m *Manager) runConnectionWork(ctx context.Context, work connectionWork, relayErr error) error {
	started := time.Now()
	logger := logging.FromContext(ctx, m.logger)
	logger.Info("connection evaluation started")
	defer func() {
		logger.Info("connection evaluation finished", "elapsed_ms", time.Since(started).Milliseconds(), "state", m.Status().State)
	}()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if work&workAuthRecovery != 0 {
		m.recoverGatewayAuthentication(ctx)
	}
	if work&workManual != 0 {
		m.operation.Lock()
		defer m.operation.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		m.directRetryAfter = time.Time{}
		m.resetReconnectCooldown()
		err := m.connect(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			err = m.fail(err)
		}
		m.noteReconnectOutcome(err)
		return err
	}
	if work&workStartup != 0 {
		if err := m.resume(ctx); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if work&workRelay != 0 && relayErr != nil {
		m.handleRelayFailure(ctx, relayErr)
	}
	if work&workReconcile != 0 {
		m.operation.Lock()
		defer m.operation.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		config, err := m.store.Load()
		if err != nil {
			return err
		}
		if !canAutoConnect(config, m.Status()) {
			return nil
		}
		m.directRetryAfter = time.Time{}
		return m.reconnect(ctx, *config)
	}
	if work&workPrivileged != 0 {
		m.maintainPrivileged(ctx)
	}
	if work&workHealth != 0 && ctx.Err() == nil {
		m.maintainHealth(ctx)
	}
	return ctx.Err()
}

func (m *Manager) networkStatus(ctx context.Context) (model.ClientPrivilegedStatus, error) {
	stage, cancel := context.WithTimeout(ctx, privilegedCallTimeout)
	defer cancel()
	return m.privileged.Status(stage)
}

func (m *Manager) removeNetwork(ctx context.Context) error {
	stage, cancel := context.WithTimeout(ctx, privilegedCallTimeout)
	defer cancel()
	return m.privileged.Remove(stage)
}

type connectionAttemptKey struct{}
type connectionAttemptIdentity struct {
	coordinator *connectionCoordinator
	epoch       uint64
}

// Publishing connection evidence is atomic with respect to intent/network
// invalidation. A late success from a canceled generation cannot become current.
func (m *Manager) commitConnection(ctx context.Context, publish func()) error {
	if identity, ok := ctx.Value(connectionAttemptKey{}).(connectionAttemptIdentity); ok {
		identity.coordinator.mu.Lock()
		defer identity.coordinator.mu.Unlock()
		if identity.epoch != identity.coordinator.epoch || identity.coordinator.stopped {
			return context.Canceled
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	publish()
	return nil
}

// RecheckNetwork is an automatic notification, unlike Retry/Connect. It never
// enables AutoConnect and is ignored by a suspended coordinator.
func (m *Manager) RecheckNetwork() {
	m.scheduleConnection(workHealth)
}
