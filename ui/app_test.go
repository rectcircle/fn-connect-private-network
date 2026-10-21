package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
