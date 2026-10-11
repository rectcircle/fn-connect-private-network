package command

import (
	"bytes"
	"context"
	"errors"
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

func TestAuthorizeCommandWaitsForUIResultAndCancelsIncompleteLaunch(t *testing.T) {
	cases := []struct {
		name          string
		openResult    string
		wantState     string
		wantCode      model.ErrorCode
		wantOutput    string
		wantOpenError bool
		cancelCaller  bool
	}{
		{name: "success", openResult: "succeeded", wantOutput: "authorized\n"},
		{name: "user canceled", openResult: "canceled", wantState: "canceled", wantCode: model.ErrorCanceled},
		{name: "caller canceled", wantState: "canceled", wantCode: model.ErrorCanceled, cancelCaller: true},
		{name: "UI launch failed", wantState: "canceled", wantOpenError: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			socket := authorizationTestSocket(t)
			handler := &authorizationCommandHandler{changed: make(chan struct{})}
			serverCtx, stopServer := context.WithCancel(context.Background())
			serverDone := make(chan error, 1)
			go func() {
				serverDone <- (ipc.Server{
					SocketPath: socket,
					Handler:    handler,
				}).Serve(serverCtx)
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

			openCalls := 0
			commandContext, cancelCommand := context.WithCancel(context.Background())
			t.Cleanup(cancelCommand)
			openAuthorization := func(ctx context.Context, fnID, requestID string) error {
				openCalls++
				if fnID != "home-nas" || requestID != handler.requestID {
					t.Errorf("authorization launch got fnID=%q requestID=%q", fnID, requestID)
				}
				if testCase.wantOpenError {
					return errors.New("shell association failed")
				}
				if testCase.cancelCaller {
					cancelCommand()
					return nil
				}
				handler.complete(testCase.openResult)
				return nil
			}

			var stdout, stderr bytes.Buffer
			err := runClientCommandWithAuthorizationOpener(commandContext, []string{
				"authorize", "--socket", socket, "home-nas",
			}, Environment{Stdout: &stdout, Stderr: &stderr}, openAuthorization)
			if openCalls != 1 {
				t.Fatalf("authorization UI launches = %d, want 1; err=%v stderr=%q", openCalls, err, stderr.String())
			}
			if testCase.wantOutput != "" {
				if err != nil || stdout.String() != testCase.wantOutput {
					t.Fatalf("authorize result = (%v, %q), want (nil, %q); stderr=%q", err, stdout.String(), testCase.wantOutput, stderr.String())
				}
				if got := handler.currentState(); got != testCase.openResult {
					t.Fatalf("authorization state = %q, want %q", got, testCase.openResult)
				}
				return
			}
			if testCase.wantOpenError {
				if err == nil || !strings.Contains(err.Error(), "shell association failed") {
					t.Fatalf("authorize error = %v, want shell association failure", err)
				}
				if got := handler.currentState(); got != testCase.wantState {
					t.Fatalf("authorization state after UI launch failure = %q, want %q", got, testCase.wantState)
				}
				return
			}
			if err == nil || model.AsError(err).Code != testCase.wantCode {
				t.Fatalf("authorize error = %v, want code %q; stderr=%q", err, testCase.wantCode, stderr.String())
			}
			if got := handler.currentState(); got != testCase.wantState {
				t.Fatalf("authorization state = %q, want %q", got, testCase.wantState)
			}
		})
	}
}

func authorizationTestSocket(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return `\\.\pipe\FnCPN.CLI.Authorize.` + strconv.Itoa(os.Getpid()) + "." + strings.ReplaceAll(t.Name(), "/", ".")
	}
	return filepath.Join(t.TempDir(), "client.sock")
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

func (h *authorizationCommandHandler) resultLocked() client.AuthorizationResult {
	result := client.AuthorizationResult{RequestID: h.requestID, State: h.state}
	if h.state == "canceled" {
		result.Error = model.NewError(model.ErrorCanceled, "authorization canceled", false)
	}
	return result
}
