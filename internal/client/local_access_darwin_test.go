//go:build darwin && cgo

package client

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNativeLocalAccessReadyAndCancel(t *testing.T) {
	// Loopback exercises the native callbacks and teardown without triggering or
	// modifying the user's system privacy permission.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		events, stop := (SystemLocalProbe{}).WatchLocalAccess(ctx, listener.Addr().String())
		select {
		case state := <-events:
			if state != localAccessAllowed {
				t.Fatalf("unexpected native result %v", state)
			}
		case <-ctx.Done():
			t.Fatal("native TCP readiness timed out")
		}
		stop()
		cancel()
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, stop := (SystemLocalProbe{}).WatchLocalAccess(ctx, listener.Addr().String())
	cancel()
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("native connection cancellation blocked")
	}
}
