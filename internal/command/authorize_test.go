package command

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestAuthorizeCommandUsesTerminalCredentialsAndWaitsForDaemon(t *testing.T) {
	socket := authorizationTestSocket(t)
	handler := &authorizationCommandHandler{changed: make(chan struct{})}
	serverCtx, stopServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- (ipc.Server{SocketPath: socket, Handler: handler}).Serve(serverCtx)
	}()
	waitForAuthorizationTestServer(t, socket)
	t.Cleanup(func() {
		stopServer()
		select {
		case err := <-serverDone:
			if err != nil {
				t.Errorf("authorization test IPC server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("authorization test IPC server did not stop")
		}
	})

	var stdout, stderr bytes.Buffer
	err := runClientCommand(context.Background(), []string{
		"authorize", "--socket", socket, "home-nas",
	}, Environment{
		Stdin:        strings.NewReader("alice\n"),
		Stdout:       &stdout,
		Stderr:       &stderr,
		ReadPassword: func() ([]byte, error) { return []byte("secret password"), nil },
	})
	if err != nil || stdout.String() != "authorized\n" {
		t.Fatalf("authorize result = (%v, %q); stderr=%q", err, stdout.String(), stderr.String())
	}
	if got := handler.currentState(); got != "succeeded" {
		t.Fatalf("authorization state = %q, want succeeded", got)
	}
	username, password := handler.credentials()
	if username != "alice" || password != "secret password" {
		t.Fatalf("daemon credentials = (%q, %q), want terminal values", username, password)
	}
}

func authorizationTestSocket(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\.\pipe\FnCPN.CLI.Authorize.` + strconv.Itoa(os.Getpid()) + "." + strings.ReplaceAll(t.Name(), "/", ".")
	}
	// macOS limits Unix socket paths; t.TempDir includes the long test name.
	directory, err := os.MkdirTemp("", "fncpn-auth-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "client.sock")
}

func waitForAuthorizationTestServer(t *testing.T, socket string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		var status model.ClientStatus
		err := (ipc.Client{SocketPath: socket, Timeout: 100 * time.Millisecond}).Call(ctx, client.MethodStatus, nil, &status)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("authorization test IPC server did not start at %q", socket)
}

type authorizationCommandHandler struct {
	mu        sync.Mutex
	requestID string
	state     string
	username  string
	password  string
	changed   chan struct{}
}

func (h *authorizationCommandHandler) Handle(ctx context.Context, request ipc.Request) ipc.Response {
	switch request.Method {
	case client.MethodStatus:
		return ipc.Success(request.ID, model.ClientStatus{State: model.ClientUnconfigured})
	case client.MethodAuthorizationBegin:
		var input client.AuthorizationRequest
		if decoded, err := ipc.DecodeParams[client.AuthorizationRequest](request); err != nil {
			return ipc.Failure(request.ID, err)
		} else {
			input = decoded
		}
		if input.FNID != "home-nas" {
			return ipc.Failure(request.ID, model.NewError(model.ErrorInvalidArgument, "unexpected FN ID", false))
		}
		h.mu.Lock()
		h.requestID = "authorization-test-request"
		h.state = "pending"
		h.changed = make(chan struct{})
		requestID := h.requestID
		h.mu.Unlock()
		return ipc.Success(request.ID, client.AuthorizationResult{RequestID: requestID, State: "pending"})
	case client.MethodAuthorizationCancel:
		var input client.AuthorizationCancelRequest
		if decoded, err := ipc.DecodeParams[client.AuthorizationCancelRequest](request); err != nil {
			return ipc.Failure(request.ID, err)
		} else {
			input = decoded
		}
		h.mu.Lock()
		if input.RequestID != h.requestID {
			h.mu.Unlock()
			return ipc.Failure(request.ID, model.NewError(model.ErrorNotFound, "authorization request not found", false))
		}
		if h.state == "pending" {
			h.state = "canceled"
			close(h.changed)
		}
		result := h.resultLocked()
		h.mu.Unlock()
		return ipc.Success(request.ID, result)
	case client.MethodAuthorizeNative:
		input, err := ipc.DecodeParams[client.NativeAuthorization](request)
		if err != nil {
			return ipc.Failure(request.ID, err)
		}
		h.mu.Lock()
		if input.RequestID != h.requestID || input.FNID != "home-nas" {
			h.mu.Unlock()
			return ipc.Failure(request.ID, model.NewError(model.ErrorInvalidArgument, "unexpected authorization input", false))
		}
		h.username, h.password = input.Username, input.Password
		h.mu.Unlock()
		h.complete("succeeded")
		return ipc.Success(request.ID, model.ClientStatus{State: model.ClientUnconfigured})
	case client.MethodAuthorizationWait:
		var input client.AuthorizationRequest
		if decoded, err := ipc.DecodeParams[client.AuthorizationRequest](request); err != nil {
			return ipc.Failure(request.ID, err)
		} else {
			input = decoded
		}
		h.mu.Lock()
		if input.RequestID != h.requestID {
			h.mu.Unlock()
			return ipc.Failure(request.ID, model.NewError(model.ErrorNotFound, "authorization request not found", false))
		}
		changed := h.changed
		state := h.state
		h.mu.Unlock()
		if state == "pending" {
			select {
			case <-ctx.Done():
				return ipc.Failure(request.ID, ctx.Err())
			case <-changed:
			}
		}
		h.mu.Lock()
		result := h.resultLocked()
		h.mu.Unlock()
		return ipc.Success(request.ID, result)
	default:
		return ipc.Failure(request.ID, model.NewError(model.ErrorInvalidArgument, "unexpected method", false))
	}
}

func (h *authorizationCommandHandler) complete(state string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != "pending" {
		return
	}
	h.state = state
	close(h.changed)
}

func (h *authorizationCommandHandler) currentState() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *authorizationCommandHandler) credentials() (string, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.username, h.password
}

func (h *authorizationCommandHandler) resultLocked() client.AuthorizationResult {
	result := client.AuthorizationResult{RequestID: h.requestID, State: h.state}
	if h.state == "canceled" {
		result.Error = model.NewError(model.ErrorCanceled, "authorization canceled", false)
	}
	return result
}
