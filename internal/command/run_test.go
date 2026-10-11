package command

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestClientCLIConfirmationAndJSONContract(t *testing.T) {
	t.Run("forget requires confirmation before sending destructive IPC", func(t *testing.T) {
		socket := authorizationTestSocket(t)
		handler := &cliContractHandler{}
		startCLIContractServer(t, socket, handler)

		var stdout, stderr strings.Builder
		err := runClientCommandWithAuthorizationOpener(context.Background(), []string{
			"forget", "--socket", socket,
		}, Environment{Stdout: &stdout, Stderr: &stderr}, func(context.Context, string, string) error {
			t.Fatal("forget unexpectedly attempted to open authorization UI")
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "forget requires --yes") {
			t.Fatalf("unconfirmed forget error = %v, want explicit confirmation requirement", err)
		}
		if got := handler.methods(); strings.Contains(strings.Join(got, ","), client.MethodForget) {
			t.Fatalf("unconfirmed forget reached daemon: %v", got)
		}
	})

	t.Run("forget confirmation and status JSON reach daemon", func(t *testing.T) {
		socket := authorizationTestSocket(t)
		handler := &cliContractHandler{}
		startCLIContractServer(t, socket, handler)

		var stdout, stderr strings.Builder
		err := runClientCommandWithAuthorizationOpener(context.Background(), []string{
			"forget", "--yes", "--socket", socket,
		}, Environment{Stdout: &stdout, Stderr: &stderr}, func(context.Context, string, string) error {
			t.Fatal("forget unexpectedly attempted to open authorization UI")
			return nil
		})
		if err != nil || stdout.String() != "PAUSED\n" {
			t.Fatalf("confirmed forget = (%v, %q), want (nil, PAUSED); stderr=%q", err, stdout.String(), stderr.String())
		}
		if got := handler.methods(); !containsString(got, client.MethodForget) {
			t.Fatalf("confirmed forget did not reach daemon: %v", got)
		}

		stdout.Reset()
		stderr.Reset()
		err = runClientCommandWithAuthorizationOpener(context.Background(), []string{
			"status", "--json", "--socket", socket,
		}, Environment{Stdout: &stdout, Stderr: &stderr}, func(context.Context, string, string) error {
			t.Fatal("status unexpectedly attempted to open authorization UI")
			return nil
		})
		var status model.ClientStatus
		if err != nil || json.Unmarshal([]byte(stdout.String()), &status) != nil || status.State != model.ClientPaused {
			t.Fatalf("status --json = (%v, %q), want valid JSON with PAUSED state; stderr=%q", err, stdout.String(), stderr.String())
		}
	})
}

func startCLIContractServer(t *testing.T, socket string, handler *cliContractHandler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- (ipc.Server{SocketPath: socket, Handler: handler}).Serve(ctx) }()
	waitForAuthorizationTestServer(t, socket)
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("CLI contract IPC server: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("CLI contract IPC server did not stop")
		}
	})
}

type cliContractHandler struct {
	mu          sync.Mutex
	methodsSeen []string
}

func (h *cliContractHandler) Handle(_ context.Context, request ipc.Request) ipc.Response {
	h.mu.Lock()
	h.methodsSeen = append(h.methodsSeen, request.Method)
	h.mu.Unlock()
	if request.Method == client.MethodForget {
		return ipc.Success(request.ID, model.ClientStatus{State: model.ClientPaused})
	}
	if request.Method == client.MethodStatus {
		return ipc.Success(request.ID, model.ClientStatus{State: model.ClientPaused})
	}
	return ipc.Failure(request.ID, model.NewError(model.ErrorNotFound, "unexpected command", false))
}

func (h *cliContractHandler) methods() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.methodsSeen...)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestServerStateOpenFailureIsWrittenToProcessLog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the runtime crash-output file handle is process-global and cannot be released on Windows")
	}
	directory := t.TempDir()
	stateDirectory := filepath.Join(directory, "state")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(stateDirectory, "settings.json"),
		[]byte("{broken"),
		0o600,
	); err != nil {
		t.Fatalf("write invalid settings: %v", err)
	}
	logPath := filepath.Join(directory, "logs", "server.log")
	err := runServerDaemon(
		context.Background(),
		[]string{
			"--socket", filepath.Join(directory, "app.sock"),
			"--state-dir", stateDirectory,
			"--privileged-socket", filepath.Join(directory, "privileged.sock"),
			"--log-file", logPath,
		},
		Environment{Stderr: io.Discard},
	)
	if err == nil {
		t.Fatal("invalid state unexpectedly opened")
	}
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read process log: %v", readErr)
	}
	logLine := string(data)
	if !strings.Contains(logLine, `"msg":"open server state"`) ||
		!strings.Contains(logLine, `"phase":"open_state"`) ||
		!strings.Contains(logLine, "unsupported development data settings.json") {
		t.Fatalf("process log does not contain startup cause: %s", logLine)
	}
}

func TestPurgeUserRemovesDataAndLogDirectories(t *testing.T) {
	directory := t.TempDir()
	dataDirectory := filepath.Join(directory, "data")
	logDirectory := filepath.Join(directory, "logs")
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", directory)
		dataDirectory = filepath.Join(directory, "FnCPN")
		logDirectory = filepath.Join(dataDirectory, "Logs")
	}
	if err := os.MkdirAll(filepath.Join(dataDirectory, "run"), 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dataDirectory, "run", "client.sock"),
		nil,
		0o600,
	); err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatalf("create log directory: %v", err)
	}

	err := runPurgeUser([]string{
		"--data-dir", dataDirectory,
		"--config", filepath.Join(dataDirectory, "config.json"),
		"--log-dir", logDirectory,
	}, Environment{Stderr: io.Discard})
	if err != nil {
		t.Fatalf("purge user: %v", err)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("data directory still exists: %v", err)
	}
	if _, err := os.Stat(logDirectory); !os.IsNotExist(err) {
		t.Fatalf("log directory still exists: %v", err)
	}
}
