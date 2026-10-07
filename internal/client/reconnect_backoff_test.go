package client

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func retryableErrorFixture() error {
	return model.WithOperation(model.NewError(
		model.ErrorUnavailable, "relay session disconnected", true,
	), "relay.session")
}

// TestReconnectBackoffGrowsAndResetsOnSuccess verifies the full-reconnect
// cooldown that stops a discovery storm when the NAS is unreachable (the
// spontaneous HTTP 429 seen with the NAS powered off): the backoff doubles on
// each retryable failure, is bounded by maxReconnectBackoff, and resets to zero
// once a reconnect succeeds.
func TestReconnectBackoffGrowsAndResetsOnSuccess(t *testing.T) {
	m := &Manager{}

	// Consecutive retryable failures drive the next-attempt delay upward.
	for i, want := range []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
	} {
		m.noteReconnectOutcome(retryableErrorFixture())
		if m.reconnectBackoff != min(want*2, maxReconnectBackoff) {
			t.Fatalf("iteration %d: reconnectBackoff = %v, want next %v",
				i, m.reconnectBackoff, want*2)
		}
		if m.reconnectRetryAfter.IsZero() {
			t.Fatalf("iteration %d: retryAfter not armed", i)
		}
		if in := time.Until(m.reconnectRetryAfter); in < want-100*time.Millisecond || in > want+time.Second {
			t.Fatalf("iteration %d: retryAfter ~= %v, want ~%v", i, in, want)
		}
	}

	// The backoff is capped so it never exceeds maxReconnectBackoff.
	for i := 0; i < 10; i++ {
		m.noteReconnectOutcome(retryableErrorFixture())
	}
	if m.reconnectBackoff != maxReconnectBackoff {
		t.Fatalf("reconnectBackoff = %v, want capped %v", m.reconnectBackoff, maxReconnectBackoff)
	}

	// A successful reconnect clears the cooldown entirely.
	m.noteReconnectOutcome(nil)
	if m.reconnectBackoff != 0 || !m.reconnectRetryAfter.IsZero() {
		t.Fatalf("cooldown not reset after success: backoff=%v retry=%v",
			m.reconnectBackoff, m.reconnectRetryAfter)
	}
}

// TestReconnectBackoffIgnoresNonRetryable verifies a terminal (non-retryable)
// failure does not arm the reconnect cooldown; that class of error is handled by
// the state machine, not by the backoff.
func TestReconnectBackoffIgnoresNonRetryable(t *testing.T) {
	m := &Manager{}
	m.noteReconnectOutcome(model.WithOperation(model.NewError(
		model.ErrorPermissionDenied, "administrator access is required", false,
	), "admin_proxy.probe"))
	if m.reconnectBackoff != 0 || !m.reconnectRetryAfter.IsZero() {
		t.Fatalf("non-retryable failure armed cooldown: backoff=%v retry=%v",
			m.reconnectBackoff, m.reconnectRetryAfter)
	}
}

// TestResetReconnectCooldown verifies the recovery path can clear a pending
// cooldown so a network-ready event reconnects immediately.
func TestResetReconnectCooldown(t *testing.T) {
	m := &Manager{}
	m.noteReconnectOutcome(retryableErrorFixture())
	if m.reconnectBackoff == 0 {
		t.Fatal("cooldown not armed before reset")
	}
	m.resetReconnectCooldown()
	if m.reconnectBackoff != 0 || !m.reconnectRetryAfter.IsZero() {
		t.Fatalf("reset failed: backoff=%v retry=%v",
			m.reconnectBackoff, m.reconnectRetryAfter)
	}
}

// flakyLocalProbe mimics a LAN probe that flaps: it reports unreachable until
// successCall (1-based) is reached, then stays reachable.
type flakyLocalProbe struct {
	calls       atomic.Int32
	successCall int32
	err         error
}

func (f *flakyLocalProbe) Reachable(
	context.Context, model.LocalProbeConfiguration, string, NetworkSnapshot,
) (bool, error) {
	call := f.calls.Add(1)
	reachable := f.successCall > 0 && call >= f.successCall
	return reachable, f.err
}

func (f *flakyLocalProbe) Snapshot() (NetworkSnapshot, error) {
	return NetworkSnapshot{InterfaceName: "en0", InterfaceIndex: 1}, nil
}

// TestLocalPathStillHealthyRetriesTransientFailure verifies the LOCAL health
// check does not drop the path on a single transient probe timeout: when the NAS
// answers on the second probe (the first flaps), the path is still healthy.
func TestLocalPathStillHealthyRetriesTransientFailure(t *testing.T) {
	probe := &flakyLocalProbe{successCall: 2}
	m := &Manager{
		probe:  probe,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		localProbeConfig: &model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.71.2:54790"},
		},
	}
	if !m.localPathStillHealthy(t.Context(), &LocalConfig{DeviceID: "device-1"}) {
		t.Fatal("transient probe failure dropped a healthy LOCAL path")
	}
	if got := probe.calls.Load(); got != 2 {
		t.Fatalf("expected the probe to be retried once, got %d calls", got)
	}
}

// TestLocalPathStillHealthyDeclaresLostAfterConsecutiveFailures verifies the
// LOCAL path is only declared lost after consecutive failures, never on the
// first one.
func TestLocalPathStillHealthyDeclaresLostAfterConsecutiveFailures(t *testing.T) {
	probe := &flakyLocalProbe{successCall: 0}
	m := &Manager{
		probe:  probe,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		localProbeConfig: &model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.71.2:54790"},
		},
	}
	if m.localPathStillHealthy(t.Context(), &LocalConfig{DeviceID: "device-1"}) {
		t.Fatal("LOCAL path unexpectedly considered healthy after consecutive failures")
	}
	if got := probe.calls.Load(); got != localProbeHealthChecks {
		t.Fatalf("consecutive failures did not exhaust the retry budget: %d calls", got)
	}
}

// TestManagerUsesPersistedLocalProbeCacheWithoutGateway verifies the LOCal path
// can be established from a persisted LAN probe cache without contacting the
// public gateway — the core fix for the "LOCAL requires WAN" chicken-and-egg.
func TestManagerUsesPersistedLocalProbeCacheWithoutGateway(t *testing.T) {
	store := configuredManagerStore(t)
	if err := store.SaveLocalProbeConfig("home-nas", model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-key",
	}); err != nil {
		t.Fatalf("seed local probe cache: %v", err)
	}
	remote := &fakeRemoteService{configuration: managerClientConfiguration()}
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if manager.Status().State != model.ClientLocal {
		t.Fatalf("state = %s, want LOCAL", manager.Status().State)
	}
	if remote.probeRequests != 0 {
		t.Fatalf("LOCAL established via cached config, but the gateway was still consulted %d times", remote.probeRequests)
	}
}

// TestManagerPersistsLocalProbeCacheAfterConnect verifies a successfully
// established LOCAL path writes its probe configuration back to disk, so a later
// reconnect or a daemon restart can reuse it without hitting the gateway.
func TestManagerPersistsLocalProbeCacheAfterConnect(t *testing.T) {
	store := configuredManagerStore(t)
	remote := &fakeRemoteService{
		configuration: managerClientConfiguration(),
		probeConfiguration: model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.71.2:54790"},
			Key:       "probe-key",
		},
	}
	manager, err := NewManager(ManagerOptions{
		Store:      store,
		Discoverer: fakeDiscoverer{},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return remote, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{},
		Probe:      fakeLocalProbe{reachable: true},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if manager.Status().State != model.ClientLocal {
		t.Fatalf("state = %s, want LOCAL", manager.Status().State)
	}
	cached, found, _, err := store.LoadLocalProbeConfig("home-nas")
	if err != nil || !found {
		t.Fatalf("LOCAL config not persisted: found=%v err=%v", found, err)
	}
	if !slices.Equal(cached.Endpoints, []string{"192.168.71.2:54790"}) || cached.Key != "probe-key" {
		t.Fatalf("persisted local probe = %+v", cached)
	}
}