package clienthost

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type testRuntime struct {
	client.Runtime
	runStopped     chan struct{}
	connectStarted chan struct{}
	closed         atomic.Bool
}

func (r *testRuntime) Start() error                { return nil }
func (r *testRuntime) Run(ctx context.Context)     { <-ctx.Done(); close(r.runStopped) }
func (r *testRuntime) Close(context.Context) error { r.closed.Store(true); return nil }
func (r *testRuntime) Status() model.ClientStatus {
	return model.ClientStatus{State: model.ClientPaused}
}
func (r *testRuntime) Connect(ctx context.Context) error {
	close(r.connectStarted)
	<-ctx.Done()
	return ctx.Err()
}

func newTestHost(t *testing.T) (*Host, *testRuntime) {
	t.Helper()
	r := &testRuntime{runStopped: make(chan struct{}), connectStarted: make(chan struct{})}
	h, err := New(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := h.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return h, r
}

func TestCancellingUIRequestKeepsServiceRunning(t *testing.T) {
	h, r := newTestHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Call(ctx, client.MethodConnect, nil, nil) }()
	select {
	case <-r.connectStarted:
	case <-time.After(time.Second):
		t.Fatal("connect did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("call did not cancel")
	}
	var status model.ClientStatus
	if err := h.Call(context.Background(), client.MethodStatus, nil, &status); err != nil {
		t.Fatal(err)
	}
	if status.State != model.ClientPaused {
		t.Fatalf("unexpected status: %+v", status)
	}
	select {
	case <-r.runStopped:
		t.Fatal("UI cancelled service")
	default:
	}
	if r.closed.Load() {
		t.Fatal("UI closed runtime")
	}
}

func TestSystemStopCancelsCallsAndRejectsNewWork(t *testing.T) {
	h, r := newTestHost(t)
	done := make(chan error, 1)
	go func() { done <- h.Call(context.Background(), client.MethodConnect, nil, nil) }()
	select {
	case <-r.connectStarted:
	case <-time.After(time.Second):
		t.Fatal("connect did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.closed.Load() {
		t.Fatal("runtime not cleaned up")
	}
	if err := <-done; err == nil {
		t.Fatal("active call was not cancelled")
	}
	if err := h.Call(ctx, client.MethodStatus, nil, nil); err == nil {
		t.Fatal("stopped host admitted a call")
	}
	if err := h.Start(ctx); err == nil {
		t.Fatal("stopped host restarted old manager")
	}
}

var _ Runtime = (*client.Manager)(nil)
