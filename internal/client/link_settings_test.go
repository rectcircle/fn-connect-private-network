package client

import (
	"context"
	"net/netip"
	"os"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestLinkSettingsPreserveIdentityAndRejectUnknownSchema(t *testing.T) {
	store := configuredManagerStore(t)
	before, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	links, err := store.loadLinks("home-nas")
	if err != nil || !slices.Equal(links, defaultLinks()) {
		t.Fatal(links, err)
	}
	wanted := []LinkPreference{{"fn-connect", false}, {"ipv6", true}}
	if err := store.saveLinks("home-nas", wanted); err != nil {
		t.Fatal(err)
	}
	if got, err := store.loadLinks("home-nas"); err != nil || !slices.Equal(got, wanted) {
		t.Fatal(got, err)
	}
	if got, err := store.loadLinks("other-nas"); err != nil || !slices.Equal(got, defaultLinks()) {
		t.Fatal(got, err)
	}
	after, _ := os.ReadFile(store.path)
	if string(before) != string(after) {
		t.Fatal("link settings changed identity/configuration")
	}
	if err := store.saveLinks("home-nas", []LinkPreference{{"local", false}, {"ipv6", true}}); err == nil {
		t.Fatal("local was editable")
	}
	if err := os.WriteFile(store.path+".links", []byte(`{"version":99,"nas":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.saveLinks("home-nas", defaultLinks()); err == nil {
		t.Fatal("overwrote a future schema")
	}
}
func TestLinkPolicyAndForcedTestSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		links   []LinkPreference
		force   string
		local   bool
		want    model.ClientState
		wantErr bool
	}{
		{"relay first", []LinkPreference{{"fn-connect", true}, {"ipv6", true}}, "", false, model.ClientRelay, false},
		{"ipv6 disabled", []LinkPreference{{"ipv6", false}, {"fn-connect", true}}, "", false, model.ClientRelay, false},
		{"local fixed first", []LinkPreference{{"fn-connect", true}, {"ipv6", true}}, "", true, model.ClientLocal, false},
		{"only local unavailable", []LinkPreference{{"ipv6", false}, {"fn-connect", false}}, "", false, model.ClientReconnecting, true},
		{"force relay bypasses local and disabled", []LinkPreference{{"ipv6", true}, {"fn-connect", false}}, "fn-connect", true, model.ClientRelay, false},
		{"force ipv6 never falls back", defaultLinks(), "ipv6", false, model.ClientReconnecting, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, p, n, d, _ := coordinationFixture(t)
				p.reachable.Store(tc.local)
				if tc.force != "ipv6" {
					d.discover = func(context.Context) (Discovery, error) { return Discovery{IPv6: []string{"240e::1"}}, nil }
					m.ipv6RouteChecker = func(context.Context, netip.Addr, int) bool { return true }
				}
				if err := m.store.saveLinks("home-nas", tc.links); err != nil {
					t.Fatal(err)
				}
				ctx := t.Context()
				if tc.force != "" {
					ctx = context.WithValue(ctx, forcedLinkKey{}, tc.force)
				}
				cfg, _ := m.store.Load()
				err := m.connectWithConfig(ctx, *cfg)
				if (err != nil) != tc.wantErr || m.Status().State != tc.want {
					t.Fatalf("state=%s err=%v", m.Status().State, err)
				}
				if tc.force == "ipv6" && m.bridge.(*coordinatedBridge).started.Load() != 0 {
					t.Fatal("IPv6 test fell back to relay")
				}
				if tc.force == "fn-connect" && !slices.Contains(m.lastPlan.AllowedIPs, "192.168.71.2/32") {
					t.Fatal("LAN test lacks temporary NAS host route")
				}
				if tc.want == model.ClientLocal && n.applies.Load() != 0 {
					t.Fatal("local selection applied a tunnel")
				}
			})
		})
	}
}
