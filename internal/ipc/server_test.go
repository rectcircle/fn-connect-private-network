//go:build darwin || linux

package ipc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestServerAuthorizesBeforeHandlingRequest(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/fncpn-ipc-%d.sock", os.Getpid())
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handled := make(chan struct{}, 1)
	identities := make(chan PeerIdentity, 1)
	server := Server{
		SocketPath: socketPath,
		Mode:       0o600,
		Authorize: func(_ context.Context, identity PeerIdentity) error {
			identities <- identity
			return RejectPeer("test rejection")
		},
		Handler: HandlerFunc(func(context.Context, Request) Response {
			handled <- struct{}{}
			return Success("", nil)
		}),
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(ctx)
	}()
	waitForSocket(t, socketPath)

	client := Client{SocketPath: socketPath, Timeout: time.Second}
	err := client.Call(context.Background(), "status", nil, nil)
	var typed *model.Error
	if !errors.As(err, &typed) || typed.Code != model.ErrorPermissionDenied {
		t.Fatalf("call error = %v", err)
	}
	select {
	case identity := <-identities:
		if identity.UID != uint32(os.Getuid()) {
			t.Fatalf("UID = %d, want %d", identity.UID, os.Getuid())
		}
	case <-time.After(time.Second):
		t.Fatal("authorizer was not called")
	}
	select {
	case <-handled:
		t.Fatal("handler called after rejected authorization")
	default:
	}
	cancel()
	if err := <-errs; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestClientCallHonorsContextAfterConnect(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/fncpn-ipc-timeout-%d.sock", os.Getpid())
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	serverContext, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	started := make(chan struct{})
	release := make(chan struct{})
	server := Server{
		SocketPath: socketPath,
		Mode:       0o600,
		Handler: HandlerFunc(func(context.Context, Request) Response {
			close(started)
			<-release
			return Success("", nil)
		}),
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(serverContext)
	}()
	waitForSocket(t, socketPath)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		callDone <- (Client{SocketPath: socketPath, Timeout: 5 * time.Second}).
			Call(ctx, "status", nil, nil)
	}()
	<-started
	select {
	case err := <-callDone:
		if model.AsError(err).Code != model.ErrorTimeout {
			t.Fatalf("call error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client call ignored context deadline")
	}
	close(release)
	stopServer()
	if err := <-errs; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func TestServerWritesResponseAfterHandlerTimeout(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/fncpn-ipc-handler-timeout-%d.sock", os.Getpid())
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	serverContext, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	server := Server{
		SocketPath: socketPath,
		Mode:       0o600,
		Timeout:    20 * time.Millisecond,
		Handler: HandlerFunc(func(ctx context.Context, request Request) Response {
			<-ctx.Done()
			return Success(request.ID, map[string]bool{"timedOut": true})
		}),
	}
	errs := make(chan error, 1)
	go func() {
		errs <- server.Serve(serverContext)
	}()
	waitForSocket(t, socketPath)

	var result struct {
		TimedOut bool `json:"timedOut"`
	}
	err := (Client{SocketPath: socketPath, Timeout: time.Second}).
		Call(context.Background(), "watch", nil, &result)
	if err != nil {
		t.Fatalf("call after handler timeout: %v", err)
	}
	if !result.TimedOut {
		t.Fatalf("response = %+v", result)
	}

	stopServer()
	if err := <-errs; err != nil {
		t.Fatalf("serve: %v", err)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("socket %q was not created", path)
}
