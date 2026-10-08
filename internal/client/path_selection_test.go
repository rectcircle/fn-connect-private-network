package client

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestLocalSelectionWaitsForLANWarmupBeforeIPv6(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, d, _ := coordinationFixture(t)
		p.reach = func(_ context.Context, call int32) bool { return call >= 4 }
		d.discover = func(context.Context) (Discovery, error) { return Discovery{IPv6: []string{"240e::1"}}, nil }
		result := make(chan error, 1)
		go func() { result <- m.Connect(t.Context()) }()
		advanceCoordinator(700 * time.Millisecond)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if m.Status().State != model.ClientLocal || n.applies.Load() != 0 || d.calls.Load() != 0 {
			t.Fatal("transient LAN failure selected a tunnel")
		}
	})
}

func TestManualCheckKeepsHealthyRelayPlan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, d, _ := coordinationFixture(t)
		p.reachable.Store(false)
		m.localProbeConfig = &model.LocalProbeConfiguration{}
		if err := m.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		removes, applies := n.removes.Load(), n.applies.Load()
		if err := m.Retry(t.Context()); err != nil {
			t.Fatal(err)
		}
		if n.removes.Load() != removes || n.applies.Load() != applies || d.calls.Load() != 2 {
			t.Fatal("manual check rebuilt an unchanged healthy relay")
		}
	})
}

func TestDirectAndRelayUpgradeToLocalWithoutNetworkEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, state := range []model.ClientState{model.ClientDirect, model.ClientRelay} {
			m, _, n, _, _ := coordinationFixture(t)
			now := time.Now()
			n.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", LastHandshake: &now}
			m.setStatus(state, "existing", "utun9", nil)
			m.maintainHealth(t.Context())
			if m.Status().State != model.ClientLocal || n.removes.Load() != 1 {
				t.Fatalf("%s was not promoted to LOCAL", state)
			}
		}
	})
}

func TestLocalProbeUsesActualPrefixesAndPhysicalInterface(t *testing.T) {
	snapshot := NetworkSnapshot{InterfaceIndex: 1, Links: []PhysicalLink{
		{Name: "en7", Index: 7, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}},
		{Name: "en5", Index: 5, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.0.0/23")}},
	}}
	for _, test := range []struct {
		endpoint string
		index    int
	}{
		{"10.1.99.2:54790", 7}, {"192.168.1.2:54790", 5}, {"10.2.0.1:54790", 1},
	} {
		if got := snapshot.interfaceForEndpoint(test.endpoint); got != test.index {
			t.Fatalf("%s bound to interface %d, want %d", test.endpoint, got, test.index)
		}
	}
}

func TestDirectBudgetReservesRelayAndApplySharesDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, d, _ := coordinationFixture(t)
		p.reachable.Store(false)
		m.localProbeConfig = &model.LocalProbeConfiguration{}
		d.discover = func(context.Context) (Discovery, error) {
			return Discovery{IPv6: []string{"240e::1", "240e::2", "240e::3"}}, nil
		}
		m.ipv6RouteChecker = func(context.Context, netip.Addr, int) bool { return true }
		var canceled atomic.Bool
		n.apply = func(ctx context.Context, plan model.ClientPlan) (model.ClientPrivilegedStatus, error) {
			if plan.Mode == "direct" {
				<-ctx.Done()
				canceled.Store(true)
				return model.ClientPrivilegedStatus{}, ctx.Err()
			}
			now := time.Now()
			return model.ClientPrivilegedStatus{Active: true, Interface: "utun9", ListenPort: 49152, LastHandshake: &now}, nil
		}
		started := time.Now()
		if err := m.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !canceled.Load() || m.Status().State != model.ClientRelay || time.Since(started) > connectionAttemptTimeout {
			t.Fatal("DIRECT starved fallback or ignored its phase deadline")
		}
		short, stop := context.WithTimeout(t.Context(), relayPhaseTimeout)
		defer stop()
		if directBudget(short) != 0 {
			t.Fatal("DIRECT consumed relay reserve")
		}
	})
}

func TestHandshakeStatusCannotOutliveHandshakeBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		m.handshakeTimeout = 300 * time.Millisecond
		m.privileged = blockingStatusNetwork{}
		started := time.Now()
		if _, err := m.waitForHandshake(t.Context(), model.ClientPrivilegedStatus{}, started); err == nil {
			t.Fatal("blocking status unexpectedly succeeded")
		}
		if elapsed := time.Since(started); elapsed > m.handshakeTimeout {
			t.Fatalf("status blocked handshake for %v", elapsed)
		}
	})
}

type blockingStatusNetwork struct{}

func (blockingStatusNetwork) Status(ctx context.Context) (model.ClientPrivilegedStatus, error) {
	<-ctx.Done()
	return model.ClientPrivilegedStatus{}, ctx.Err()
}
func (blockingStatusNetwork) Apply(context.Context, model.ClientPlan) (model.ClientPrivilegedStatus, error) {
	return model.ClientPrivilegedStatus{}, nil
}
func (blockingStatusNetwork) Remove(context.Context) error { return nil }

// A gateway that never responds must not extend the LAN selection window.
type stalledProbeRemote struct{ *fakeRemoteService }

func (s stalledProbeRemote) LocalProbeConfiguration(ctx context.Context, _ string) (model.LocalProbeConfiguration, error) {
	<-ctx.Done()
	return model.LocalProbeConfiguration{}, ctx.Err()
}

func TestManualHealthyCheckBoundsProbeAndSkipsConfigurationFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, _, _ := coordinationFixture(t)
		p.reachable.Store(false)
		if err := m.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		remote := &fakeRemoteService{configuration: managerClientConfiguration()}
		m.remote = func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return stalledProbeRemote{remote}, nil
		}
		applies, removes := n.applies.Load(), n.removes.Load()
		started := time.Now()
		if err := m.Retry(t.Context()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed > localSelectionTimeout {
			t.Fatalf("slow gateway extended healthy recheck to %v", elapsed)
		}
		if remote.configurationRequests != 0 || n.applies.Load() != applies || n.removes.Load() != removes {
			t.Fatal("healthy recheck fetched configuration or rebuilt the tunnel")
		}
		p.snapshot.Store(&NetworkSnapshot{InterfaceName: "en7", InterfaceIndex: 7,
			Prefixes: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}})
		if err := m.Retry(t.Context()); err != nil {
			t.Fatal(err)
		}
		if remote.configurationRequests != 1 || n.applies.Load() != applies+1 {
			t.Fatal("changed network incorrectly reused the healthy-network shortcut")
		}
	})
}
