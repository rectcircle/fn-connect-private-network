package client

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

const (
	localSelectionTimeout = 2 * time.Second
	localRetryInterval    = 200 * time.Millisecond
	localEndpointTimeout  = 600 * time.Millisecond
	relayPhaseTimeout     = 8 * time.Second
)

func (s NetworkSnapshot) interfaceForEndpoint(endpoint string) int {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return s.InterfaceIndex
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return s.InterfaceIndex
	}
	bestIndex, bestBits := s.InterfaceIndex, -1
	for _, link := range s.Links {
		for _, prefix := range link.Prefixes {
			if prefix.Contains(address) && prefix.Bits() > bestBits {
				bestIndex, bestBits = link.Index, prefix.Bits()
			}
		}
	}
	return bestIndex
}

// probeLocal retries unknown/transient outcomes for one bounded selection
// window. Subnet equality is never evidence that the authenticated probe passed.
func (m *Manager) probeLocal(ctx context.Context, cfg model.LocalProbeConfiguration, deviceID string, snapshot NetworkSnapshot) (bool, error) {
	stage, cancel := context.WithTimeout(ctx, localSelectionTimeout)
	defer cancel()
	for {
		ok, err := m.probe.Reachable(stage, cfg, deviceID, snapshot)
		if version.IsFailure(err) {
			return false, err
		}
		if stage.Err() != nil {
			return false, nil
		}
		if ok {
			return true, nil
		}
		if len(cfg.Endpoints) == 0 || err != nil && model.AsError(err).Code == model.ErrorProtocol {
			return false, nil
		}
		timer := time.NewTimer(localRetryInterval)
		select {
		case <-stage.Done():
			timer.Stop()
			return false, nil
		case <-timer.C:
		}
	}
}

type localSelection struct {
	err           error
	configuration model.LocalProbeConfiguration
	endpoints     []string
	reachable     bool
}

// Cache validation and endpoint refresh overlap. Cache hits need no WAN, while
// a stale cache does not postpone discovering a new NAS address by two seconds.
// Refresh and retries share one selection deadline, rather than adding their
// individual timeouts. All goroutines are joined before returning, including
// cookie persistence.
func (m *Manager) selectLocal(ctx context.Context, remote RemoteService, config LocalConfig, snapshot NetworkSnapshot) localSelection {
	if m.localProbeConfig == nil {
		m.loadLocalProbeCache(config.FNID)
	}
	cached := cloneLocalProbeConfiguration(m.localProbeConfig)
	stage, cancel := context.WithTimeout(ctx, localSelectionTimeout)
	defer cancel()
	type result struct {
		cfg   model.LocalProbeConfiguration
		ok    bool
		fresh bool
		err   error
	}
	count := 1
	results := make(chan result, 2)
	if cached != nil {
		count++
		go func() {
			ok, err := m.probeLocal(stage, *cached, config.DeviceID, snapshot)
			results <- result{cfg: *cached, ok: ok, err: err}
		}()
	}
	go func() {
		if cached != nil {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-stage.Done():
				timer.Stop()
				results <- result{}
				return
			case <-timer.C:
			}
		}
		fetch, stop := context.WithTimeout(stage, configurationFetchTimeout)
		fresh, err := remote.LocalProbeConfiguration(fetch, config.DeviceID)
		stop()
		if err != nil {
			if stage.Err() == nil {
				logging.FromContext(ctx, m.logger).Warn("get local probe configuration", "error", err)
			}
			results <- result{err: err}
			return
		}
		ok, err := m.probeLocal(stage, fresh, config.DeviceID, snapshot)
		results <- result{cfg: fresh, ok: ok, err: err, fresh: true}
	}()
	selection := localSelection{}
	for i := 0; i < count; i++ {
		res := <-results
		if version.IsFailure(res.err) {
			selection.err = res.err
		}
		selection.endpoints = append(selection.endpoints, res.cfg.Endpoints...)
		if res.fresh && len(res.cfg.Endpoints) > 0 && ctx.Err() == nil {
			// Persist authenticated endpoint metadata even on a remote network, so
			// subsequent LOCAL promotion can use it without the public gateway.
			m.localProbeConfig = cloneLocalProbeConfiguration(&res.cfg)
			m.persistLocalProbeConfig(config.FNID, res.cfg)
		}
		if res.ok && !selection.reachable {
			selection.configuration, selection.reachable = res.cfg, true
			cancel()
		}
	}
	return selection
}

func (m *Manager) establishLocal(ctx context.Context, config LocalConfig, snapshot NetworkSnapshot, cfg model.LocalProbeConfiguration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Status().State != model.ClientLocal {
		m.bridge.Stop()
		if err := m.removeNetwork(ctx); err != nil {
			return m.fail(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.persistLocalProbeConfig(config.FNID, cfg)
	return m.commitConnection(ctx, func() {
		m.lastPlan = nil
		m.lastNetworkFingerprint = snapshot.Fingerprint()
		m.localProbeConfig = cloneLocalProbeConfiguration(&cfg)
		m.setDirectDiagnostics(model.DirectDiagnostics{Reason: "local_network", LocalPublicIPv6: snapshot.HasPublicIPv6})
		m.setStatus(model.ClientLocal, "local", "", nil)
	})
}

// Called only by the serialized worker. A failed upgrade never tears down a
// healthy DIRECT/RELAY connection; the normal health check remains independent.
func (m *Manager) tryLocalUpgrade(ctx context.Context, config LocalConfig) bool {
	if m.localProbeConfig == nil {
		m.loadLocalProbeCache(config.FNID)
	}
	cached := cloneLocalProbeConfiguration(m.localProbeConfig)
	if cached == nil {
		return false
	}
	snapshot, err := m.probe.Snapshot()
	if err != nil {
		return false
	}
	probe, stop := context.WithTimeout(ctx, localEndpointTimeout)
	ok, probeErr := m.probe.Reachable(probe, *cached, config.DeviceID, snapshot)
	stop()
	if version.IsFailure(probeErr) {
		_ = m.fail(probeErr)
		return true
	}
	if !ok || ctx.Err() != nil {
		return false
	}
	return m.establishLocal(ctx, config, snapshot, *cached) == nil
}

func sameClientPlan(a, b model.ClientPlan) bool {
	return a.Mode == b.Mode && a.PrivateKey == b.PrivateKey && a.ClientAddress == b.ClientAddress &&
		a.ServerPublicKey == b.ServerPublicKey && a.Endpoint == b.Endpoint && a.MTU == b.MTU && slices.Equal(a.AllowedIPs, b.AllowedIPs)
}

func (m *Manager) keepCurrentPlan(ctx context.Context, plan model.ClientPlan, snapshot NetworkSnapshot) bool {
	if m.lastPlan == nil || !sameClientPlan(*m.lastPlan, plan) || m.lastNetworkFingerprint != snapshot.Fingerprint() {
		return false
	}
	status := m.Status()
	if status.State != model.ClientDirect && status.State != model.ClientRelay {
		return false
	}
	network, err := m.networkStatus(ctx)
	return ctx.Err() == nil && clientNetworkHealthError(status.State, network, err, m.bridge.Connected()) == nil
}

func (m *Manager) replaceNetwork(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.bridge.Stop()
	if err := m.removeNetwork(ctx); err != nil {
		return err
	}
	m.lastPlan = nil
	if err := ctx.Err(); err != nil {
		return err
	}
	m.setStatus(model.ClientProbing, "", "", nil)
	return nil
}

func directBudget(ctx context.Context) time.Duration {
	budget := directPhaseTimeout
	if deadline, ok := ctx.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-relayPhaseTimeout-privilegedCallTimeout)
	}
	return max(budget, 0)
}

// Discovery evidence lives for exactly one evaluation. Background DIRECT
// upgrades pass their result through the same path rather than issuing it twice.
type discoveryEvidence struct {
	value Discovery
	err   error
}
type discoveryEvidenceKey struct{}

func (m *Manager) discoverForAttempt(ctx context.Context, fnID string) discoveryEvidence {
	if evidence, ok := ctx.Value(discoveryEvidenceKey{}).(discoveryEvidence); ok {
		return evidence
	}
	stage, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	value, err := m.discoverer.Discover(stage, fnID)
	return discoveryEvidence{value, err}
}
