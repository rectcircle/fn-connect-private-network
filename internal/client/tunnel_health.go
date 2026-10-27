package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Check the existing authenticated NAS endpoint through the tunnel, rather than
// treating the age of a WireGuard key exchange as a connectivity test.
func (m *Manager) tunnelHealthError(ctx context.Context, config *LocalConfig) error {
	configuration := clonePrivateAPIConfiguration(m.privateAPIConfig)
	if configuration == nil {
		// An unavailable legacy/private API must not add a server requirement.
		return nil
	}
	// Only probe destinations installed in the tunnel; LAN-overlap exclusions
	// must not turn into false failures or successful physical-LAN probes.
	if m.lastPlan != nil {
		endpoints := configuration.Endpoints[:0]
		for _, endpoint := range configuration.Endpoints {
			host, _, _ := net.SplitHostPort(endpoint)
			ip, err := netip.ParseAddr(host)
			if err != nil {
				continue
			}
			for _, route := range m.lastPlan.AllowedIPs {
				prefix, err := netip.ParsePrefix(route)
				if err == nil && prefix.Contains(ip) {
					endpoints = append(endpoints, endpoint)
					break
				}
			}
		}
		configuration.Endpoints = endpoints
		if len(endpoints) == 0 {
			return nil
		}
	}
	client := m.dnsHTTPClient // iOS supplies its provider-owned tunnel transport.
	if client == nil {
		address, err := netip.ParsePrefix(config.Configuration.ClientAddress)
		if err != nil {
			return err
		}
		transport := &http.Transport{
			// Binding the VPN source prevents a successful physical-LAN bypass.
			DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IP(address.Addr().AsSlice())}, Timeout: localEndpointTimeout}).DialContext,
		}
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport}
	}
	cloned := *client
	cloned.Jar = nil
	cloned.Timeout = localEndpointTimeout
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	probe := SystemLocalProbe{HTTPClient: &cloned}
	var failure error
	for attempt := 0; attempt < localProbeHealthChecks; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(localProbeHealthRetryInterval):
			}
		}
		check, cancel := context.WithTimeout(ctx, localEndpointTimeout)
		ok, err := probe.Reachable(check, *configuration, config.DeviceID, NetworkSnapshot{})
		cancel()
		if ok || isAdmissionFailure(err) {
			return err
		}
		failure = err
	}
	if failure == nil {
		failure = errors.New("NAS tunnel probe failed")
	}
	return model.WithOperation(model.NormalizeError(failure, model.ErrorUnavailable, "NAS tunnel probe failed", true), "tunnel_probe")
}
