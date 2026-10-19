package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"testing"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type dnsTestNetwork struct {
	fakePrivilegedNetwork
	mu                         sync.Mutex
	plan                       model.DNSPlan
	applies, removes, renewals int
}

func (n *dnsTestNetwork) ApplyDNS(_ context.Context, p model.DNSPlan) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.plan = p
	n.applies++
	return nil
}
func (n *dnsTestNetwork) RenewDNS(context.Context, model.DNSPlan) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.renewals++
	return nil
}
func (n *dnsTestNetwork) RemoveDNS(context.Context, model.DNSPlan) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.removes++
	return nil
}

func TestDNSAutomaticallyAppliesOnAllPathsAndCleansOnDisconnect(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		state       model.ClientState
		localPrefix string
	}{
		{"local-on-link", model.ClientLocal, "192.168.71.0/24"},
		{"local-vm-nat", model.ClientLocal, "10.211.55.0/24"},
		{"direct", model.ClientDirect, "10.77.0.0/24"},
		{"relay", model.ClientRelay, "10.77.0.0/24"},
	} {
		state := scenario.state
		t.Run(scenario.name, func(t *testing.T) {
			store := configuredManagerStore(t)
			network := &dnsTestNetwork{}
			key := bytes.Repeat([]byte{4}, 32)
			if e := store.SavePrivateAPIConfig("home-nas", model.PrivateAPIConfiguration{Endpoints: []string{"192.168.71.2:54790"}, Key: base64.RawURLEncoding.EncodeToString(key)}); e != nil {
				t.Fatal(e)
			}
			client := privateDNSHTTPClient(t)
			prefixes := []netip.Prefix{netip.MustParsePrefix(scenario.localPrefix)}
			m, e := NewManager(ManagerOptions{DNSHTTPClient: client, Store: store, Discoverer: fakeDiscoverer{}, Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
				t.Fatal("DNS must not use the FN Connect gateway")
				return nil, nil
			}, Privileged: network, Bridge: &fakeBridge{}, Probe: fakeLocalProbe{reachable: state == model.ClientLocal, prefixes: prefixes}})
			if e != nil {
				t.Fatal(e)
			}
			m.setStatus(state, string(state), "", nil)
			m.reconcileDNS(t.Context())
			defer m.stopDNS(context.Background())
			if status := m.Status().DNS; status.State != "ready" || status.Suffix != "home-nas.fncpn" {
				t.Fatalf("%+v", status)
			}
			if !m.renewDNS(t.Context()) || network.applies != 1 || network.renewals != 1 {
				t.Fatal("lease renewal reapplied system rules")
			}
			q := new(wire.Msg)
			q.SetQuestion("abc.local.home-nas.fncpn.", wire.TypeA)
			answer, _, e := (&wire.Client{}).Exchange(q, net.JoinHostPort(network.plan.Address, strconv.Itoa(int(network.plan.Port))))
			if e != nil || len(answer.Answer) != 1 {
				t.Fatalf("%+v %v", answer, e)
			}
			cfg, _ := store.Load()
			cfg.Configuration.DNSSuffix = "custom.fncpn"
			if e = store.Save(*cfg); e != nil {
				t.Fatal(e)
			}
			m.reconcileDNS(t.Context())
			if m.Status().DNS.Suffix != "custom.fncpn" || network.removes != 1 {
				t.Fatal("custom suffix not reconciled")
			}
			if e = m.Disconnect(t.Context()); e != nil {
				t.Fatal(e)
			}
			if network.removes != 2 {
				t.Fatal("disconnect did not remove DNS")
			}
		})
	}
}

// Exercises private HTTP without sending packets outside the test.
func privateDNSHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "http" || r.URL.Host != "192.168.71.2:54790" || r.URL.Path != model.DNSQueryPath {
			t.Errorf("unexpected target %s", r.URL)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("DNS leaked login credentials")
		}
		var input model.DNSQuery
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := model.DecodeStrict(data, &input); err != nil {
			t.Fatalf("DNS request contains extra protocol fields: %v", err)
		}
		answer := model.DNSAnswer{Name: input.Name, Type: input.Type, Result: "answer", Source: "mdns", Addresses: []string{"192.168.71.20"}}
		data, _ = json.Marshal(answer)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
	})}
}
func TestClientSchemaTwoUpgradePreservesIntentAndSecrets(t *testing.T) {
	store := configuredManagerStore(t)
	before, _ := store.Load()
	before.Version = 2
	data, e := json.Marshal(before)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(store.path, data, 0600); e != nil {
		t.Fatal(e)
	}
	keyBefore, e := store.EnsureWireGuardKey(before.FNID)
	if e != nil {
		t.Fatal(e)
	}
	upgraded, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	if upgraded.Version != localConfigVersion || upgraded.FNID != before.FNID || upgraded.DeviceID != before.DeviceID || upgraded.AutoConnect != before.AutoConnect {
		t.Fatalf("identity or intent changed: %+v", upgraded)
	}
	keyAfter, e := store.EnsureWireGuardKey(before.FNID)
	if e != nil || keyAfter != keyBefore {
		t.Fatal("private identity changed")
	}
	backup, e := os.ReadFile(store.path + ".schema-2.backup")
	if e != nil || string(backup) != string(data) {
		t.Fatal("missing exact rollback backup")
	}
}

func TestPrivateDNSNeedsNoDeviceKey(t *testing.T) {
	c, err := newPrivateDNSClient([]string{"192.168.71.2:54790"}, func(string) bool { return true }, privateDNSHTTPClient(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, name := range []string{"abc", ""} {
		if _, err = c.QueryDNS(t.Context(), model.DNSQuery{Name: name, Type: "A"}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestPrivateDNSRejectsRouteConflictWithoutSendingRequest(t *testing.T) {
	c, e := newPrivateDNSClient([]string{"192.168.71.2:54790"}, func(string) bool { return false }, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("queried conflicting LAN IP"); return nil, nil })})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.QueryDNS(t.Context(), model.DNSQuery{Name: "abc", Type: "A"}); e == nil || model.AsError(e).Code != model.ErrorUnavailable {
		t.Fatal(e)
	}
}

func TestDNSAddressBoundariesAcrossPaths(t *testing.T) {
	for _, state := range []model.ClientState{model.ClientLocal, model.ClientDirect, model.ClientRelay, model.ClientPaused} {
		for _, prefix := range []string{"192.168.71.0/24", "10.211.55.0/24"} {
			t.Run(string(state)+"/"+prefix, func(t *testing.T) {
				store := configuredManagerStore(t)
				m, err := NewManager(ManagerOptions{Store: store, Discoverer: fakeDiscoverer{}, Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) { return &fakeRemoteService{}, nil }, Privileged: &dnsTestNetwork{}, Bridge: &fakeBridge{}, Probe: fakeLocalProbe{prefixes: []netip.Prefix{netip.MustParsePrefix(prefix)}}})
				if err != nil {
					t.Fatal(err)
				}
				m.setStatus(state, string(state), "", nil)
				config, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				wantAllowed := state == model.ClientLocal || ((state == model.ClientDirect || state == model.ClientRelay) && prefix != "192.168.71.0/24")
				if got := m.dnsAddressAllowed(config, "192.168.71.2"); got != wantAllowed {
					t.Fatalf("NAS permission = %v, want %v", got, wantAllowed)
				}
				for _, address := range []string{"192.168.72.2", "10.211.55.3", "8.8.8.8", "127.0.0.1", "169.254.1.1", "::1"} {
					if m.dnsAddressAllowed(config, address) {
						t.Fatalf("accepted out-of-scope %s", address)
					}
				}
				config.AutoConnect = false
				if err := store.Save(*config); err != nil {
					t.Fatal(err)
				}
				if m.dnsAddressAllowed(config, "192.168.71.2") {
					t.Fatal("paused connection still permits DNS")
				}
			})
		}
	}
}
