//go:build darwin

package darwin

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestClientEngineApplyAndRemove(t *testing.T) {
	runtime := &fakeRuntime{}
	network := &fakeNetwork{available: true}
	engine := NewClientEngine(runtime, network)
	plan := testClientPlan()

	if err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	status := engine.Status()
	if !status.Active || status.Interface != "utun-test" {
		t.Fatalf("status = %+v", status)
	}
	if network.applied != 1 {
		t.Fatalf("network apply count = %d", network.applied)
	}

	if err := engine.Remove(context.Background()); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if runtime.stopped != 1 || network.removed != 1 {
		t.Fatalf("cleanup runtime=%d network=%d", runtime.stopped, network.removed)
	}
}

func TestClientEngineRollsBackWireGuardOnNetworkFailure(t *testing.T) {
	runtime := &fakeRuntime{}
	network := &fakeNetwork{available: true, applyErr: errors.New("route failed")}
	engine := NewClientEngine(runtime, network)

	if err := engine.Apply(context.Background(), testClientPlan()); err == nil {
		t.Fatal("network failure unexpectedly ignored")
	}
	if runtime.stopped != 1 || runtime.status.Active {
		t.Fatalf("runtime was not rolled back: %+v", runtime)
	}
}

func TestClientEngineReconcilesActivePlanBeforeApply(t *testing.T) {
	runtime := &fakeRuntime{}
	network := &fakeNetwork{available: true}
	engine := NewClientEngine(runtime, network)
	plan := testClientPlan()
	if err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("reconcile same plan: %v", err)
	}
	changed := plan
	changed.Endpoint = "127.0.0.1:51999"
	if err := engine.Apply(context.Background(), changed); err != nil {
		t.Fatalf("reconcile changed plan: %v", err)
	}
	if runtime.stopped != 2 ||
		network.removed != 2 ||
		network.applied != 3 ||
		!runtime.status.Active {
		t.Fatalf(
			"reconcile runtime=%+v network=%+v",
			runtime,
			network,
		)
	}
}

func TestClientEngineCleanupFailureBecomesDegraded(t *testing.T) {
	runtime := &fakeRuntime{}
	applyError := errors.New("network apply failed")
	cleanupError := errors.New("network cleanup failed")
	network := &fakeNetwork{
		available: true,
		applyErr:  applyError,
		removeErr: cleanupError,
	}
	engine := NewClientEngine(runtime, network)
	plan := testClientPlan()
	err := engine.Apply(context.Background(), plan)
	if !errors.Is(err, applyError) || !errors.Is(err, cleanupError) {
		t.Fatalf("apply error = %v", err)
	}
	if !engine.degraded || runtime.stopped != 0 {
		t.Fatalf(
			"incomplete cleanup state: degraded=%t stopped=%d",
			engine.degraded,
			runtime.stopped,
		)
	}
	if err := engine.Apply(context.Background(), plan); !errors.Is(err, cleanupError) {
		t.Fatalf("reconcile while degraded error = %v", err)
	}
	network.removeErr = nil
	network.applyErr = nil
	if err := engine.Remove(context.Background()); err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	if engine.degraded || runtime.status.Active {
		t.Fatalf("retry cleanup did not reach empty")
	}
}

func TestClientEngineRecoversNetworkStateBeforeApply(t *testing.T) {
	runtime := &fakeRuntime{}
	network := &fakeNetwork{available: true}
	engine := NewClientEngine(runtime, network)

	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if network.recovered != 1 {
		t.Fatalf("network recover count = %d", network.recovered)
	}
	if runtime.status.Active {
		t.Fatalf("recovery unexpectedly started WireGuard: %+v", runtime.status)
	}
}

func TestClientEngineKeepsRuntimeWhenNetworkCleanupFails(t *testing.T) {
	runtime := &fakeRuntime{}
	cleanupError := errors.New("cleanup failed")
	network := &fakeNetwork{available: true, removeErr: cleanupError}
	engine := NewClientEngine(runtime, network)

	if err := engine.Apply(context.Background(), testClientPlan()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := engine.Remove(context.Background()); !errors.Is(err, cleanupError) {
		t.Fatalf("remove error = %v", err)
	}
	if runtime.stopped != 0 || !runtime.status.Active {
		t.Fatalf("runtime stopped before network cleanup succeeded: %+v", runtime)
	}

	network.removeErr = nil
	if err := engine.Remove(context.Background()); err != nil {
		t.Fatalf("retry remove: %v", err)
	}
	if runtime.stopped != 1 || runtime.status.Active {
		t.Fatalf("runtime was not stopped after cleanup: %+v", runtime)
	}
}

func TestClientEngineKeepsRuntimeWhenApplyRollbackFails(t *testing.T) {
	runtime := &fakeRuntime{}
	applyError := errors.New("route failed")
	cleanupError := errors.New("cleanup failed")
	network := &fakeNetwork{
		available: true,
		applyErr:  applyError,
		removeErr: cleanupError,
	}
	engine := NewClientEngine(runtime, network)

	err := engine.Apply(context.Background(), testClientPlan())
	if !errors.Is(err, applyError) || !errors.Is(err, cleanupError) {
		t.Fatalf("apply error = %v", err)
	}
	if runtime.stopped != 0 || !runtime.status.Active {
		t.Fatalf("runtime stopped before rollback succeeded: %+v", runtime)
	}

	network.removeErr = nil
	if err := engine.Remove(context.Background()); err != nil {
		t.Fatalf("retry remove: %v", err)
	}
	if runtime.stopped != 1 || runtime.status.Active {
		t.Fatalf("runtime was not stopped after cleanup: %+v", runtime)
	}
}

func TestParseLastHandshake(t *testing.T) {
	value := parseLastHandshake(
		"public_key=abc\nlast_handshake_time_sec=1700000000\n" +
			"last_handshake_time_nsec=123\n",
	)
	if value == nil || value.Unix() != 1700000000 || value.Nanosecond() != 123 {
		t.Fatalf("handshake = %v", value)
	}
}

func TestParseListenPort(t *testing.T) {
	if port := parseListenPort("listen_port=49152\nerrno=0\n"); port != 49152 {
		t.Fatalf("listen port = %d", port)
	}
	if port := parseListenPort("listen_port=invalid\n"); port != 0 {
		t.Fatalf("invalid listen port = %d", port)
	}
}

type fakeRuntime struct {
	status  model.ClientPrivilegedStatus
	stopped int
}

func (r *fakeRuntime) Start(
	_ context.Context,
	_ model.ClientPlan,
) (string, error) {
	r.status = model.ClientPrivilegedStatus{
		Active:    true,
		Interface: "utun-test",
	}
	return r.status.Interface, nil
}

func (r *fakeRuntime) Stop() {
	r.stopped++
	r.status = model.ClientPrivilegedStatus{}
}

func (r *fakeRuntime) Status() model.ClientPrivilegedStatus {
	return r.status
}

type fakeNetwork struct {
	available bool
	applied   int
	removed   int
	recovered int
	applyErr  error
	removeErr error
}

func (n *fakeNetwork) Available() bool {
	return n.available
}

func (n *fakeNetwork) Recover(context.Context) error {
	n.recovered++
	return nil
}

func (n *fakeNetwork) Apply(
	context.Context,
	string,
	model.ClientPlan,
) error {
	n.applied++
	return n.applyErr
}

func (n *fakeNetwork) Remove(
	context.Context,
	string,
) error {
	n.removed++
	return n.removeErr
}

func testClientPlan() model.ClientPlan {
	return model.ClientPlan{
		Mode:            "relay",
		PrivateKey:      testPlanKey(1),
		ClientAddress:   "10.203.0.2/32",
		ServerPublicKey: testPlanKey(2),
		Endpoint:        "127.0.0.1:51821",
		AllowedIPs:      []string{"10.203.0.1/32"},
		MTU:             1280,
	}
}

func testPlanKey(value byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}
