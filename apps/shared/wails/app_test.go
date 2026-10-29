package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func fakeDaemon(t *testing.T, respond func(ipc.Request) ipc.Response) ipc.Client {
	t.Helper()
	// macOS Unix socket addresses have a short fixed size.
	directory, err := os.MkdirTemp("/tmp", "fncpn-ui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	path := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		request, err := ipc.ReadRequest(connection)
		if err != nil {
			return
		}
		_ = ipc.WriteResponse(connection, respond(request))
	}()
	return ipc.Client{SocketPath: path}
}
func TestStructuredOTPChallengeSurvivesBridge(t *testing.T) {
	a := NewApp()
	a.ctx = context.Background()
	a.client = fakeDaemon(t, func(request ipc.Request) ipc.Response {
		if request.Method != "authorize-native" {
			t.Errorf("method=%s", request.Method)
		}
		failure := model.NewError(model.ErrorAuthRequired, "OTP required", false)
		failure.RemoteCode = "TWO_FACTOR_REQUIRED"
		return ipc.Failure(request.ID, failure)
	})
	reply := a.Call("authorize-native", map[string]any{"requestId": "pending", "fnId": "nas", "username": "user", "password": "secret"})
	if reply.Error == nil || reply.Error.RemoteCode != "TWO_FACTOR_REQUIRED" {
		t.Fatalf("lost challenge: %+v", reply)
	}
}
func TestBridgeRejectsMismatchedLocalProductVersion(t *testing.T) {
	a := NewApp()
	a.ctx = context.Background()
	a.client = fakeDaemon(t, func(request ipc.Request) ipc.Response {
		response := ipc.Success(request.ID, map[string]any{})
		response.ProductVersion = "99.0.0"
		return response
	})
	reply := a.Call("diagnose", nil)
	if reply.Error == nil || !strings.Contains(reply.Error.Message, "local components differ") {
		t.Fatalf("accepted mismatch: %+v", reply)
	}
}
func TestUIOperationsAreAllowlisted(t *testing.T) {
	a := NewApp()
	a.ctx = context.Background()
	reply := a.Call("shutdown-client-daemon", nil)
	if reply.Error == nil || reply.Error.Code != model.ErrorInvalidArgument {
		t.Fatalf("unexpected reply %+v", reply)
	}
}
func TestAdminURLMustBeLoopbackHTTP(t *testing.T) {
	for _, address := range []string{"https://example.com/", "http://localhost:1234/", "http://127.0.0.1/", "http://user@127.0.0.1:1234/", "http://127.0.0.1:65536/", "file:///etc/passwd"} {
		if validateAdminURL(address) == nil {
			t.Errorf("accepted %s", address)
		}
	}
	if err := validateAdminURL("http://127.0.0.1:1234/token/admin"); err != nil {
		t.Fatal(err)
	}
}

func TestSmokeDoesNotContactClientServices(t *testing.T) {
	app := NewApp()
	app.smokeResult = "/unused/smoke.json"
	// No context/socket/daemon is configured; calls must return locally.
	for _, method := range []string{"diagnose", "connect", "authorize-native"} {
		reply := app.Call(method, nil)
		if reply.Error == nil || reply.Error.Message != "UI smoke test runs without client services" {
			t.Fatalf("smoke %s = %#v", method, reply)
		}
	}
}

func TestQuitWaitsForServiceCleanupAndIgnoresDuplicateRequests(t *testing.T) {
	app := &App{}
	started, release, closed, done := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("quit cleanup has no deadline")
		}
		close(started)
		<-release
		return nil
	}
	go func() {
		defer close(done)
		app.quitWith(stop, func() { close(closed) }, func(err error) { t.Errorf("quit: %v", err) })
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	if reply := app.Call("connect", nil); reply.Error == nil {
		t.Error("new connection allowed while quitting")
	}
	app.quitWith(func(context.Context) error { t.Error("duplicate cleanup"); return nil }, func() { t.Error("duplicate UI close") }, func(error) {})
	select {
	case <-closed:
		t.Error("UI closed before cleanup")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("quit did not finish")
	}
	select {
	case <-closed:
	default:
		t.Fatal("UI did not close after cleanup")
	}
}

func TestQuitFailureKeepsUIAvailableForRetry(t *testing.T) {
	app := &App{}
	closed, reported := false, false
	app.quitWith(func(context.Context) error { return fmt.Errorf("cleanup failed") }, func() { closed = true }, func(error) { reported = true })
	if closed || !reported || app.quitting {
		t.Fatal("failed cleanup must keep UI open and allow retry")
	}
	app.quitWith(func(context.Context) error { return nil }, func() { closed = true }, func(err error) { t.Fatal(err) })
	if !closed {
		t.Fatal("successful retry did not close UI")
	}
}
