package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type dnsNetwork interface {
	ApplyDNS(context.Context, model.DNSPlan) error
	RenewDNS(context.Context, model.DNSPlan) error
	RemoveDNS(context.Context, model.DNSPlan) error
}

func (n IPCPrivilegedNetwork) ApplyDNS(ctx context.Context, p model.DNSPlan) error {
	return n.Client.Call(ctx, privileged.MethodApplyDNS, p, nil)
}
func (n IPCPrivilegedNetwork) RenewDNS(ctx context.Context, p model.DNSPlan) error {
	return n.Client.Call(ctx, privileged.MethodRenewDNS, p, nil)
}
func (n IPCPrivilegedNetwork) RemoveDNS(ctx context.Context, p model.DNSPlan) error {
	return n.Client.Call(ctx, privileged.MethodRemoveDNS, p, nil)
}

type clientDNS struct {
	lifecycle   sync.Mutex
	closed      bool
	retryAt     time.Time
	retryDelay  time.Duration
	mu          sync.Mutex
	forwarder   *dns.Forwarder
	plan        model.DNSPlan
	identity    string
	closeRemote func()
}

func (m *Manager) setDNSStatus(state, suffix string, err error) {
	m.mu.Lock()
	next := model.DNSStatus{State: state, Suffix: suffix, LastError: model.PublicError(err)}
	changed := m.status.DNS.State != next.State || m.status.DNS.Suffix != suffix || !sameDNSError(m.status.DNS.LastError, next.LastError)
	m.status.DNS = next
	m.mu.Unlock()
	if changed {
		m.statusChanges.Notify()
	}
}
func sameDNSError(a, b *model.Error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Code == b.Code && a.Message == b.Message
}
func (m *Manager) stopDNS(ctx context.Context) error {
	m.dns.lifecycle.Lock()
	defer m.dns.lifecycle.Unlock()
	return m.stopDNSLocked(ctx)
}
func (m *Manager) stopDNSLocked(ctx context.Context) error {
	m.dns.mu.Lock()
	defer m.dns.mu.Unlock()
	if m.dns.forwarder == nil {
		return nil
	}
	if network, ok := m.privileged.(dnsNetwork); ok {
		if err := network.RemoveDNS(ctx, m.dns.plan); err != nil {
			return err
		}
	}
	m.dns.forwarder.Close()
	if m.dns.closeRemote != nil {
		m.dns.closeRemote()
		m.dns.closeRemote = nil
	}
	m.dns.forwarder = nil
	m.dns.identity = ""
	return nil
}
func (m *Manager) runDNS(ctx context.Context) {
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := m.stopDNS(cleanup); err != nil {
			m.logger.Error("remove DNS rules", "error", err)
		}
	}()
	// Reconcile on existing connection/configuration notifications. A slow refresh
	// also catches OS changes that do not produce a connection status event.
	nextRefresh := time.Time{}
	for ctx.Err() == nil {
		generation := m.statusChanges.Current()
		m.reconcileDNS(ctx)
		if nextRefresh.IsZero() || time.Now().After(nextRefresh) {
			nextRefresh = time.Now().Add(30 * time.Second)
		}
		for ctx.Err() == nil {
			waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, changed := m.statusChanges.Wait(waitCtx, generation)
			cancel()
			if changed || time.Now().After(nextRefresh) || !m.renewDNS(ctx) {
				break
			}
		}
	}

}

// Lease renewal does not reload configuration, probe interfaces or inspect OS rules.
func (m *Manager) renewDNS(ctx context.Context) bool {
	m.dns.lifecycle.Lock()
	defer m.dns.lifecycle.Unlock()
	if m.dns.closed || m.dns.forwarder == nil {
		return false
	}
	network, ok := m.privileged.(dnsNetwork)
	if !ok {
		return false
	}
	renewCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := network.RenewDNS(renewCtx, m.dns.plan); err != nil {
		m.setDNSStatus("degraded", m.dns.plan.Suffix, err)
		return false
	}
	return true
}

func (m *Manager) reconcileDNS(ctx context.Context) {
	m.dns.lifecycle.Lock()
	defer m.dns.lifecycle.Unlock()
	if m.dns.closed {
		return
	}
	status := m.Status()
	active := status.State == model.ClientLocal || status.State == model.ClientDirect || status.State == model.ClientRelay
	if !active {
		if err := m.stopDNSLocked(ctx); err != nil {
			m.setDNSStatus("degraded", "", err)
		} else {
			m.setDNSStatus("inactive", "", nil)
		}
		return
	}
	network, ok := m.privileged.(dnsNetwork)
	if !ok {
		return
	}
	if m.dns.forwarder == nil && time.Now().Before(m.dns.retryAt) {
		return
	}
	defer func() {
		if m.dns.forwarder != nil {
			m.dns.retryDelay = 0
			m.dns.retryAt = time.Time{}
			return
		}
		m.dns.retryDelay = min(30*time.Second, max(time.Second, m.dns.retryDelay*2))
		m.dns.retryAt = time.Now().Add(m.dns.retryDelay)
	}()
	config, err := m.store.Load()
	if err != nil || config == nil {
		m.setDNSStatus("degraded", "", err)
		return
	}
	suffix := config.Configuration.DNSSuffix
	if suffix == "" {
		suffix = dns.DefaultSuffix(config.FNID)
	}
	if err := dns.ValidateSuffix(suffix); err != nil {
		m.setDNSStatus("degraded", suffix, err)
		return
	}
	privateAPIConfiguration, found, _, loadErr := m.store.LoadPrivateAPIConfig(config.FNID)
	if loadErr != nil || !found {
		cleanupErr := m.stopDNSLocked(ctx)
		m.setDNSStatus("degraded", suffix, model.WrapError(model.ErrorUnavailable, "NAS private DNS endpoints unavailable", true, errors.Join(loadErr, cleanupErr)))
		return
	}
	encoded, _ := json.Marshal(privateAPIConfiguration.Endpoints)
	transportIdentity := sha256.Sum256(encoded)
	identity := fmt.Sprint(config.FNID, "|", config.DeviceID, "|", suffix, "|", config.Configuration.DNSRevision, "|", status.Path, "|", m.networkFingerprint(), "|", fmt.Sprintf("%x", transportIdentity))
	m.dns.mu.Lock()
	same := m.dns.forwarder != nil && m.dns.identity == identity
	plan := m.dns.plan
	m.dns.mu.Unlock()
	if same {
		applyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := network.ApplyDNS(applyCtx, plan)
		cancel()
		if err != nil {
			m.setDNSStatus("degraded", suffix, err)
		} else {
			m.mu.RLock()
			state := m.status.DNS.State
			m.mu.RUnlock()
			if state != "degraded" {
				m.setDNSStatus("ready", suffix, nil)
			}
		}
		return
	}
	if err = m.stopDNSLocked(ctx); err != nil {
		m.setDNSStatus("degraded", suffix, err)
		return
	}

	resolver, err := newPrivateDNSClient(privateAPIConfiguration.Endpoints, func(address string) bool { return m.dnsAddressAllowed(config, address) }, m.dnsHTTPClient)
	if err != nil {
		m.setDNSStatus("degraded", suffix, err)
		return
	}
	installed := false
	defer func() {
		if !installed {
			resolver.Close()
		}
	}()
	lookup := func(queryCtx context.Context, q model.DNSQuery) (model.DNSAnswer, error) {
		active, e := m.store.Load()
		if e != nil {
			return model.DNSAnswer{}, e
		}
		if active == nil || !active.AutoConnect || active.FNID != config.FNID || active.DeviceID != config.DeviceID {
			return model.DNSAnswer{}, model.NewError(model.ErrorCanceled, "DNS connection is no longer active", false)
		}
		answer, e := resolver.QueryDNS(queryCtx, q)
		if e != nil {
			m.setDNSStatus("degraded", suffix, e)
			if version.IsFailure(e) {
				m.RecheckNetwork()
			}
		} else {
			m.setDNSStatus("ready", suffix, nil)
		}
		return answer, e
	}

	allowed := func(address string) bool { return m.dnsAddressAllowed(config, address) }

	m.setDNSStatus("preparing", suffix, nil)

	var f *dns.Forwarder
	var port uint16
	host := "127.0.0.1"
	if runtime.GOOS == "windows" {
		f, host, port, err = dns.ListenLoopback(plan.Address, suffix, lookup, allowed)
	} else {
		f, port, err = dns.Listen("127.0.0.1:0", suffix, lookup, allowed)
	}
	if err != nil {
		m.setDNSStatus("degraded", suffix, err)
		return
	}
	var generation [16]byte
	if _, err = rand.Read(generation[:]); err != nil {
		f.Close()
		m.setDNSStatus("degraded", suffix, err)
		return
	}

	plan = model.DNSPlan{Suffix: suffix, Address: host, Port: port, Generation: hex.EncodeToString(generation[:])}
	if err = network.ApplyDNS(ctx, plan); err != nil {
		f.Close()
		m.setDNSStatus("degraded", suffix, err)
		return
	}
	m.dns.mu.Lock()
	m.dns.forwarder = f
	m.dns.plan = plan
	m.dns.identity = identity
	m.dns.closeRemote = resolver.Close
	installed = true
	m.dns.mu.Unlock()
	m.setDNSStatus("ready", suffix, nil)
}

func (m *Manager) dnsAddressAllowed(config *LocalConfig, address string) bool {
	current := m.Status()
	if current.State != model.ClientLocal && current.State != model.ClientDirect && current.State != model.ClientRelay {
		return false
	}
	snapshot, err := m.probe.Snapshot()
	if err != nil {
		return false
	}
	latest, err := m.store.Load()
	if err != nil || latest == nil || !latest.AutoConnect || latest.FNID != config.FNID || latest.DeviceID != config.DeviceID {
		return false
	}
	prefixes := routedPrefixes(latest.Configuration, snapshot.Prefixes)
	if current.State == model.ClientLocal {
		prefixes = nil
		for _, prefix := range snapshot.Prefixes {
			prefixes = append(prefixes, prefix.String())
		}
		return dns.AllowedAddress(address, prefixes) && dns.AllowedAddress(address, latest.Configuration.AllowedIPs)
	}
	return dns.AllowedAddress(address, prefixes)
}
