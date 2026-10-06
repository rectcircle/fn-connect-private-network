package client

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestDirectCandidatesIncludeGlobalInterfaceAddresses(t *testing.T) {
	discovery := Discovery{
		PublicIPv6: []string{"240e::1", " 240e::1 ", "invalid", "2001:db8::1"},
		IPv6:       []string{"240e::2", "240e::1", "fe80::1%en0", "fd00::1", "::ffff:192.168.1.1", "240e::3%en0"},
	}
	want := []netip.Addr{netip.MustParseAddr("240e::1"), netip.MustParseAddr("240e::2")}
	if got := discovery.DirectIPv6Candidates(); !slices.Equal(got, want) {
		t.Fatalf("candidates=%v", got)
	}
}

func TestIPv6SnapshotIsIndependentOfIPv4DefaultInterface(t *testing.T) {
	interfaces := []net.Interface{
		{Index: 1, Name: "en0", Flags: net.FlagUp},
		{Index: 2, Name: "en5", Flags: net.FlagUp},
		{Index: 3, Name: "utun9", Flags: net.FlagUp},
		{Index: 4, Name: "en6"},
	}
	addrs := map[string][]string{
		"en0":   {"192.168.1.2/24", "fe80::1/64"},
		"en5":   {"240e:1::1/64", "240e:1::2/64"},
		"utun9": {"240e:2::1/64"}, "en6": {"240e:3::1/64"},
	}
	read := func(iface net.Interface) ([]net.Addr, error) {
		var result []net.Addr
		for _, value := range addrs[iface.Name] {
			address, prefix, _ := net.ParseCIDR(value)
			prefix.IP = address
			result = append(result, prefix)
		}
		return result, nil
	}
	snapshot := networkSnapshot(interfaces, "en0", read)
	if !snapshot.HasPublicIPv6 || snapshot.InterfaceName != "en0" ||
		!slices.Equal(snapshot.IPv6Networks, []string{"en5:240e:1::/64"}) ||
		!slices.Equal(snapshot.Prefixes, []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}) {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	addrs["en5"] = []string{"240e:1::3/64"}
	if networkSnapshot(interfaces, "en0", read).Fingerprint() != snapshot.Fingerprint() {
		t.Fatal("temporary address rotation changed network identity")
	}
	addrs["en5"] = []string{"240e:4::1/64"}
	if networkSnapshot(interfaces, "en0", read).Fingerprint() == snapshot.Fingerprint() {
		t.Fatal("IPv6 prefix change was invisible")
	}
	delete(addrs, "en5")
	if networkSnapshot(interfaces, "en0", read).HasPublicIPv6 {
		t.Fatal("virtual or down interface incorrectly enabled IPv6")
	}
}

func TestDirectSelectionAndFallbackReasons(t *testing.T) {
	for _, test := range []struct {
		name             string
		discovery        Discovery
		noLocal, blocked bool
		ipv6Route        bool
		discoveryErr     error
		reason           string
		state            model.ClientState
	}{
		{name: "interface_global", ipv6Route: true, discovery: Discovery{IPv6: []string{"240e::1"}}, reason: "connected", state: model.ClientDirect},
		{name: "no_candidates", ipv6Route: true, reason: "no_server_ipv6", state: model.ClientRelay},
		{name: "no_local_ipv6", noLocal: true, ipv6Route: false, discovery: Discovery{PublicIPv6: []string{"240e::1"}}, reason: "no_local_ipv6", state: model.ClientRelay},
		{name: "no_local_global_address_still_probes", noLocal: true, ipv6Route: true, discovery: Discovery{IPv6: []string{"240e::1"}}, reason: "connected", state: model.ClientDirect},
		{name: "disabled", ipv6Route: true, discovery: Discovery{ForbidPublicIPv6: true, IPv6: []string{"240e::1"}}, reason: "server_disabled", state: model.ClientRelay},
		{name: "udp_blocked", ipv6Route: true, blocked: true, discovery: Discovery{IPv6: []string{"240e::1"}}, reason: "handshake_failed", state: model.ClientRelay},
		{name: "discovery_failed", ipv6Route: true, discoveryErr: model.NewError(model.ErrorUnavailable, "discovery unavailable", true), reason: "discovery_failed", state: model.ClientRelay},
	} {
		t.Run(test.name, func(t *testing.T) {
			network := &fakePrivilegedNetwork{}
			if test.blocked {
				network.failHandshakeEndpoints = map[string]bool{"[240e::1]:51820": true}
			}
			probe := fakeLocalProbe{addresses: []netip.Addr{netip.MustParseAddr("240e::2")}}
			if test.noLocal {
				probe.addresses = nil
			}
			manager, err := NewManager(ManagerOptions{
				Store:      configuredManagerStore(t),
				Discoverer: fakeDiscoverer{result: test.discovery, err: test.discoveryErr},
				Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
					return &fakeRemoteService{configuration: managerClientConfiguration()}, nil
				},
				Privileged: network, Bridge: &fakeBridge{endpoint: "127.0.0.1:51821"},
				Probe:      probe, HandshakeTimeout: time.Millisecond,
				IPv6RouteChecker: func(context.Context, netip.Addr, int) bool { return test.ipv6Route },
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			diagnostic := manager.Diagnose(context.Background())
			if diagnostic.Direct.Reason != test.reason || diagnostic.Status.State != test.state ||
				diagnostic.FNID != "home-nas" || diagnostic.ClientAddress == "" {
				t.Fatalf("diagnostic=%+v", diagnostic)
			}
			if test.blocked {
				if diagnostic.Direct.LastError == nil || diagnostic.Direct.RetryAfter == nil {
					t.Fatal("UDP failure lacks diagnostic or retry time")
				}
				delete(network.failHandshakeEndpoints, "[240e::1]:51820")
				if err := manager.Retry(context.Background()); err != nil {
					t.Fatal(err)
				}
				if manager.Status().State != model.ClientDirect {
					t.Fatal("manual retry did not bypass cooldown")
				}
			}
			if test.reason == "no_server_ipv6" {
				manager.discoverer = fakeDiscoverer{result: Discovery{IPv6: []string{"240e::1"}}}
				manager.directRetryAfter = time.Now().Add(-time.Second)
				manager.maintainHealth(context.Background())
				if manager.Status().State != model.ClientDirect {
					t.Fatal("empty discovery was never revisited")
				}
			}
		})
	}
}
