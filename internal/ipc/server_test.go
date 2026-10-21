//go:build !windows

package ipc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
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
	defer close(release)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		callDone <- (Client{SocketPath: socketPath, Timeout: 5 * time.Second}).
			Call(ctx, "status", nil, nil)
	}()
	// Do not start a short deadline before the handler is reached: on a busy
	// runner the request can time out before being written, leaving started open.
	select {
	case <-started:
	case err := <-callDone:
		t.Fatalf("client returned before handler started: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not receive the request")
	}
	cancel()
	select {
	case err := <-callDone:
		if model.AsError(err).Code != model.ErrorCanceled {
			t.Fatalf("call error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client call ignored context cancellation")
	}
	stopServer()
	select {
	case err := <-errs:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
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

func TestServerCancelsHandlerWhenClientDisconnects(t *testing.T) {
	socketPath := fmt.Sprintf("/tmp/fncpn-ipc-cancel-%d.sock", os.Getpid())
	_ = os.Remove(socketPath)
	t.Cleanup(func() { _ = os.Remove(socketPath) })
	serverContext, stopServer := context.WithCancel(t.Context())
	defer stopServer()
	canceled := make(chan struct{})
	started := make(chan struct{})
	server := Server{SocketPath: socketPath, Mode: 0o600, Handler: HandlerFunc(func(ctx context.Context, r Request) Response {
		close(started)
		<-ctx.Done()
		close(canceled)
		return Failure(r.ID, ctx.Err())
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(serverContext) }()
	waitForSocket(t, socketPath)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		callDone <- (Client{SocketPath: socketPath, Timeout: 5 * time.Second}).Call(ctx, "apply", nil, nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("client request did not reach the handler")
	}
	cancel()
	select {
	case err := <-callDone:
		if err == nil {
			t.Fatal("canceled call unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled client call did not return")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("client cancellation did not reach privileged handler")
	}
	stopServer()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRejectedPeerCanFinishUnparsedFrameAndReadFailure(t *testing.T) {
	connection, done := rejectedTestConnection(t)
	// Send only the header, wait for the rejection, then finish an invalid JSON
	// payload. Authorization must precede decoding, and closing must not race
	// the still-writing caller or invoke the handler.
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize)
	if _, err := connection.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	response, err := ReadResponse(connection)
	if err != nil {
		t.Fatal(err)
	}
	if response.OK || response.Error == nil || response.Error.Code != model.ErrorPermissionDenied {
		t.Fatalf("rejection = %+v", response)
	}
	if _, err := io.CopyN(connection, strings.NewReader(strings.Repeat("x", MaxFrameSize)), MaxFrameSize); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("rejected connection was not closed")
	}
}

func TestRejectedSilentPeerDoesNotHoldServerConnection(t *testing.T) {
	connection, done := rejectedTestConnection(t)
	if _, err := ReadResponse(connection); err != nil {
		t.Fatal(err)
	}
	// A peer that sends nothing and keeps the socket open cannot hold the
	// rejection goroutine for the ordinary five-minute request timeout.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("silent rejected peer held the connection")
	}
}

func rejectedTestConnection(t *testing.T) (*net.UnixConn, <-chan struct{}) {
	t.Helper()
	path := fmt.Sprintf("/tmp/fncpn-ipc-rejected-%d.sock", os.Getpid())
	_ = os.Remove(path)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.Remove(path) })
	done := make(chan struct{})
	server := Server{
		Authorize: func(context.Context, PeerIdentity) error { return RejectPeer("test rejection") },
		Handler: HandlerFunc(func(context.Context, Request) Response {
			t.Error("rejected request reached handler")
			return Success("", nil)
		}),
	}
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		server.handleConnection(t.Context(), connection, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	return connection, done
}
