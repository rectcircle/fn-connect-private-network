package client

import (
	"context"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
)

type localAccessState int

const (
	localAccessAllowed     localAccessState = 1
	localAccessWaiting     localAccessState = 2
	localAccessUnavailable localAccessState = 3
	localAccessWaitTimeout                  = 15 * time.Second
)

type localAccessWatcher interface {
	WatchLocalAccess(context.Context, string) (<-chan localAccessState, func())
}

func (m *Manager) setLocalAccess(state string) {
	m.mu.Lock()
	changed := m.status.LocalNetworkAccess != state
	m.status.LocalNetworkAccess = state
	m.mu.Unlock()
	if changed {
		m.statusChanges.Notify()
	}
}

// ContinueRemoteConnection releases only the permission wait. It never enables
// AutoConnect, and the coordinator still owns cancellation and paused intent.
func (m *Manager) ContinueRemoteConnection() {
	m.mu.Lock()
	if m.localAccessSkip != nil {
		select {
		case m.localAccessSkip <- struct{}{}:
		default:
		}
	}
	m.mu.Unlock()
}

func (m *Manager) waitForLocalAccess(ctx context.Context, remote RemoteService, config LocalConfig) {
	watcher, ok := m.probe.(localAccessWatcher)
	if !ok {
		return
	}
	cfg := cloneLocalProbeConfiguration(m.localProbeConfig)
	if cfg == nil {
		fetch, cancel := context.WithTimeout(ctx, configurationFetchTimeout)
		fresh, err := remote.LocalProbeConfiguration(fetch, config.DeviceID)
		cancel()
		if err != nil {
			return
		}
		cfg = &fresh
	}
	if len(cfg.Endpoints) == 0 {
		return
	}
	endpoint := cfg.Endpoints[0]
	identity := endpoint + "|" + m.networkFingerprint()
	if m.localAccessEndpoint != identity || m.Status().LocalNetworkAccess == "unavailable" {
		if m.localAccessCancel != nil {
			m.localAccessCancel()
			<-m.localAccessReady
		}
		lifetime := m.localAccessContext
		if lifetime == nil {
			lifetime = ctx
		}
		m.setLocalAccess("checking")
		logging.FromContext(ctx, m.logger).Info("local network access observation started", "operation", "local_access.start")
		events, cancel := watcher.WatchLocalAccess(lifetime, endpoint)
		m.localAccessCancel, m.localAccessEndpoint = cancel, identity
		m.localAccessReady = make(chan struct{})
		ready := m.localAccessReady
		go func() {
			defer close(ready)
			for state := range events {
				switch state {
				case localAccessWaiting:
					m.setLocalAccess("waiting")
					m.logger.Info("local network access waiting", "operation", "local_access.wait")
				case localAccessAllowed:
					m.setLocalAccess("allowed")
					m.logger.Info("local network access available", "operation", "local_access.ready")
					m.RecheckNetwork()
					return
				case localAccessUnavailable:
					m.setLocalAccess("unavailable")
					m.logger.Info("local network endpoint unavailable", "operation", "local_access.unavailable")
					return
				}
			}
		}()
	}
	ready := m.localAccessReady
	// A normal unreachable endpoint must not cause a 15-second permission wait.
	// Allow the native connection a short window to report the specific policy.
	initial := time.NewTimer(localEndpointTimeout)
	defer initial.Stop()
	select {
	case <-ctx.Done():
		return
	case <-ready:
		return
	case <-initial.C:
	}
	if m.Status().LocalNetworkAccess != "waiting" {
		return
	}
	m.mu.Lock()
	m.status.LocalNetworkWaiting = true
	m.mu.Unlock()
	m.statusChanges.Notify()
	defer func() { m.mu.Lock(); m.status.LocalNetworkWaiting = false; m.mu.Unlock(); m.statusChanges.Notify() }()
	m.mu.Lock()
	skip := make(chan struct{}, 1)
	m.localAccessSkip = skip
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.localAccessSkip = nil; m.mu.Unlock() }()
	timer := time.NewTimer(localAccessWaitTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-ready:
	case <-skip:
		logging.FromContext(ctx, m.logger).Info("local network permission wait skipped")
	case <-timer.C:
		logging.FromContext(ctx, m.logger).Info("local network permission wait timed out")
	}
}

// Called under operation, after the coordinator cancels/joins any active wait.
func (m *Manager) stopLocalAccess() {
	if m.localAccessCancel != nil {
		m.localAccessCancel()
		<-m.localAccessReady
	}
	m.localAccessCancel = nil
	m.localAccessReady = nil
	m.localAccessEndpoint = ""
	m.setLocalAccess("")
}
