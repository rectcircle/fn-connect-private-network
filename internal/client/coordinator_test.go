package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type coordinatedProbe struct {
	snapshot  atomic.Pointer[NetworkSnapshot]
	calls     atomic.Int32
	reachable atomic.Bool
	reach     func(context.Context, int32) bool
}

func (p *coordinatedProbe) Snapshot() (NetworkSnapshot, error) { return *p.snapshot.Load(), nil }
func (p *coordinatedProbe) Reachable(ctx context.Context, _ model.LocalProbeConfiguration, _ string, _ NetworkSnapshot) (bool, error) {
	n := p.calls.Add(1)
	if p.reach != nil {
		return p.reach(ctx, n), ctx.Err()
	}
	return p.reachable.Load(), nil
}

type coordinatedNetwork struct {
	mu      sync.Mutex
	applies atomic.Int32
	removes atomic.Int32
	status  model.ClientPrivilegedStatus
	apply   func(context.Context, model.ClientPlan) (model.ClientPrivilegedStatus, error)
}

func (n *coordinatedNetwork) Apply(ctx context.Context, p model.ClientPlan) (model.ClientPrivilegedStatus, error) {
	n.applies.Add(1)
	if n.apply != nil {
		return n.apply(ctx, p)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	n.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", ListenPort: 49152, MTU: p.MTU, LastHandshake: &now}
	return n.status, nil
}
func (n *coordinatedNetwork) Status(context.Context) (model.ClientPrivilegedStatus, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status, nil
}
func (n *coordinatedNetwork) Remove(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	n.removes.Add(1)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status = model.ClientPrivilegedStatus{}
	return nil
}

type coordinatedBridge struct {
	started   atomic.Int32
	connected atomic.Bool
	events    chan error
}

func (b *coordinatedBridge) Start(context.Context, string, CookieProvider, ...func([]Cookie, []Cookie) error) (string, error) {
	b.started.Add(1)
	b.connected.Store(true)
	return "127.0.0.1:51821", nil
}
func (b *coordinatedBridge) Stop()                 { b.connected.Store(false) }
func (b *coordinatedBridge) Connected() bool       { return b.connected.Load() }
func (b *coordinatedBridge) Events() <-chan error  { return b.events }
func (b *coordinatedBridge) BindPeer(uint16) error { return nil }

type coordinatedMonitor struct{ events chan model.NetworkChange }

func (m coordinatedMonitor) Events(context.Context) <-chan model.NetworkChange { return m.events }

type coordinatedDiscoverer struct {
	calls    atomic.Int32
	discover func(context.Context) (Discovery, error)
}

func (d *coordinatedDiscoverer) Discover(ctx context.Context, _ string) (Discovery, error) {
	d.calls.Add(1)
	if d.discover != nil {
		return d.discover(ctx)
	}
	return Discovery{}, nil
}

func coordinationFixture(t *testing.T) (*Manager, *coordinatedProbe, *coordinatedNetwork, *coordinatedDiscoverer, chan model.NetworkChange) {
	t.Helper()
	probe := &coordinatedProbe{}
	probe.snapshot.Store(&NetworkSnapshot{InterfaceName: "en0", InterfaceIndex: 1, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.71.0/24")}})
	probe.reachable.Store(true)
	network := &coordinatedNetwork{}
	discoverer := &coordinatedDiscoverer{}
	events := make(chan model.NetworkChange, 32)
	store := configuredManagerStore(t)
	if err := store.SaveLocalProbeConfig("home-nas", model.LocalProbeConfiguration{Endpoints: []string{"192.168.71.2:54790"}, Key: "probe-key"}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(ManagerOptions{
		Store: store, Probe: probe, Privileged: network, Discoverer: discoverer, Monitor: coordinatedMonitor{events}, Bridge: &coordinatedBridge{events: make(chan error, 1)},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{configuration: managerClientConfiguration()}, nil
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, probe, network, discoverer, events
}

func runCoordinated(t *testing.T, m *Manager) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	synctest.Wait()
	return func() { cancel(); <-done }
}
func advanceCoordinator(d time.Duration) { time.Sleep(d); synctest.Wait() }

func TestCoordinatorCoalescesManualChecks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, _, _ := coordinationFixture(t)
		release := make(chan struct{})
		p.reach = func(ctx context.Context, _ int32) bool {
			select {
			case <-release:
				return true
			case <-ctx.Done():
				return false
			}
		}
		stop := runCoordinated(t, m)
		defer stop()
		first, second := make(chan error, 1), make(chan error, 1)
		go func() { first <- m.Connect(t.Context()) }()
		synctest.Wait()
		go func() { second <- m.Retry(t.Context()) }()
		synctest.Wait()
		close(release)
		synctest.Wait()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		if p.calls.Load() != 1 || n.removes.Load() != 1 {
			t.Fatalf("duplicate check: probes=%d removes=%d", p.calls.Load(), n.removes.Load())
		}
	})
}

func TestCoordinatorIgnoresTunnelEventsAndAcceptsImmediatePhysicalChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, _, events := coordinationFixture(t)
		m.setStatus(model.ClientPaused, "", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		if m.Status().State != model.ClientLocal {
			t.Fatal(m.Status())
		}
		initial := p.calls.Load()
		for i := 0; i < 20; i++ {
			events <- model.NetworkChange{PrimaryNetworkChanged: true}
		}
		advanceCoordinator(300 * time.Millisecond)
		if p.calls.Load() != initial {
			t.Fatal("self-generated notifications triggered another check")
		}
		for i := 0; i < 2; i++ {
			snapshot, _ := p.Snapshot()
			snapshot.Links = []PhysicalLink{{Name: "en0", Index: 1, Identity: time.Duration(i + 1).String()}}
			p.snapshot.Store(&snapshot)
			events <- model.NetworkChange{PrimaryNetworkChanged: true}
			advanceCoordinator(300 * time.Millisecond)
		}
		if p.calls.Load() != initial+2 {
			t.Fatalf("real switch lost: probes=%d", p.calls.Load())
		}
		if n.removes.Load() != 1 {
			t.Fatal("healthy LOCAL was unnecessarily torn down")
		}
	})
}

func TestCoordinatorDisconnectCancelsActiveAndQueuedManualIntent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, _, _, events := coordinationFixture(t)
		p.reach = func(ctx context.Context, _ int32) bool { <-ctx.Done(); return false }
		stop := runCoordinated(t, m)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- m.Connect(t.Context()) }()
		synctest.Wait()
		if err := m.Disconnect(t.Context()); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("manual result=%v", err)
		}
		events <- model.NetworkChange{PrimaryNetworkChanged: true}
		advanceCoordinator(20 * time.Second)
		config, _ := m.store.Load()
		if config.AutoConnect || m.Status().State != model.ClientPaused {
			t.Fatal("canceled work resumed a paused client")
		}
	})
}

func TestCoordinatorSupersedesOldNetworkResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, _, _, events := coordinationFixture(t)
		var canceled atomic.Bool
		p.reach = func(ctx context.Context, n int32) bool {
			if n == 1 {
				<-ctx.Done()
				canceled.Store(true)
				return true
			} // late success must be ignored
			return true
		}
		stop := runCoordinated(t, m)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- m.Connect(t.Context()) }()
		synctest.Wait()
		snapshot, _ := p.Snapshot()
		snapshot.Addresses = []netip.Addr{netip.MustParseAddr("192.168.71.99")}
		p.snapshot.Store(&snapshot)
		events <- model.NetworkChange{PrimaryNetworkChanged: true}
		advanceCoordinator(300 * time.Millisecond)
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if !canceled.Load() || m.Status().State != model.ClientLocal || m.lastNetworkFingerprint != snapshot.Fingerprint() {
			t.Fatal("obsolete network result committed")
		}
	})
}

func TestCoordinatorRetriesWithoutNewNetworkNotification(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, d, _ := coordinationFixture(t)
		// No LOCAL endpoints in this scenario; relay fails once and then recovers.
		m.localProbeConfig = &model.LocalProbeConfiguration{}
		p.reachable.Store(false)
		var applies atomic.Int32
		n.apply = func(ctx context.Context, plan model.ClientPlan) (model.ClientPrivilegedStatus, error) {
			if applies.Add(1) == 1 {
				return model.ClientPrivilegedStatus{}, model.NewError(model.ErrorUnavailable, "temporary apply failure", true)
			}
			now := time.Now()
			return model.ClientPrivilegedStatus{Active: true, Interface: "utun9", ListenPort: 49152, LastHandshake: &now}, nil
		}
		m.setStatus(model.ClientPaused, "", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		advanceCoordinator(250 * time.Millisecond)
		if m.Status().State != model.ClientReconnecting {
			t.Fatal(m.Status())
		}
		advanceCoordinator(1500 * time.Millisecond)
		if m.Status().State != model.ClientRelay || d.calls.Load() != 2 {
			t.Fatalf("retry state=%s discovery=%d", m.Status().State, d.calls.Load())
		}
	})
}

func TestCoordinatorPollRepairsMissingNetworkEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, _, _, _ := coordinationFixture(t)
		m.setStatus(model.ClientPaused, "", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		before := p.calls.Load()
		snapshot, _ := p.Snapshot()
		snapshot.Addresses = []netip.Addr{netip.MustParseAddr("192.168.71.88")}
		p.snapshot.Store(&snapshot)
		advanceCoordinator(2100 * time.Millisecond)
		if p.calls.Load() != before+1 {
			t.Fatal("lost OS event was not repaired by polling")
		}
	})
}

func TestCoordinatorManualWaiterReceivesAutomaticFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, n, _, _ := coordinationFixture(t)
		release := make(chan struct{})
		p.reach = func(ctx context.Context, _ int32) bool {
			select {
			case <-release:
				return false
			case <-ctx.Done():
				return false
			}
		}
		n.apply = func(context.Context, model.ClientPlan) (model.ClientPrivilegedStatus, error) {
			return model.ClientPrivilegedStatus{}, model.NewError(model.ErrorUnavailable, "NAS is offline", true)
		}
		m.setStatus(model.ClientPaused, "", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		result := make(chan error, 1)
		go func() { result <- m.Retry(t.Context()) }()
		synctest.Wait()
		close(release)
		if err := <-result; err == nil {
			t.Fatal("joined automatic failure was reported as successful manual check")
		}
		if n.applies.Load() != 1 {
			t.Fatal("manual request repeated the automatic connection")
		}
	})
}

func TestCoordinatorAuthenticationRecoveryStopsAfterRepeatedRejection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		session := NativeSession{Version: nativeSessionVersion, FNID: "home-nas", Username: "admin", DeviceID: "stable-device", Token: "expired", LongToken: "long", Secret: "c2VjcmV0", UpdatedAt: time.Now()}
		if err := m.store.SaveNativeSession(session); err != nil {
			t.Fatal(err)
		}
		session.Token = "recovered"
		auth := &fakeNativeAuthenticator{recovered: session}
		m.authenticator = auth
		m.remote = func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeWatchingRemoteService{fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()}, watch: func(context.Context, string, string) (ConfigurationWatchResult, error) {
				return ConfigurationWatchResult{}, model.NewError(model.ErrorAuthRequired, "still rejected after recovery", false)
			}}, nil
		}
		m.setStatus(model.ClientLocal, "local", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		advanceCoordinator(1100 * time.Millisecond)
		if m.Status().State != model.ClientAuthRequired || auth.recoverCalls.Load() != 1 {
			t.Fatalf("authentication recovery loop: state=%s recoveries=%d", m.Status().State, auth.recoverCalls.Load())
		}
	})
}

func TestPermissionGrantRechecksWithoutResumingPausedClient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, p, _, _, _ := coordinationFixture(t)
		stop := runCoordinated(t, m)
		defer stop()
		before := p.calls.Load()
		m.RecheckNetwork()
		synctest.Wait()
		if p.calls.Load() <= before {
			t.Fatal("permission grant did not immediately recheck LOCAL")
		}
		if err := m.Disconnect(t.Context()); err != nil {
			t.Fatal(err)
		}
		before = p.calls.Load()
		m.RecheckNetwork()
		synctest.Wait()
		config, err := m.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if config.AutoConnect || m.Status().State != model.ClientPaused || p.calls.Load() != before {
			t.Fatal("late permission grant resumed a paused client")
		}
	})
}
