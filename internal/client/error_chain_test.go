package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
	"github.com/rectcircle/fn-connect-private-network/internal/server"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestPrivilegedFailureCrossesHTTPAndClientIPC(t *testing.T) {
	directory, err := os.MkdirTemp("", "fncpn-errors-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	var rootLog, serverLog, clientLog diagnosticBuffer
	rootFailure := model.WrapError(model.ErrorUnavailable, "network apply failed", true,
		errors.Join(errors.New("nftables: operation not permitted"), errors.New("rollback: device busy")))
	rootFailure.Operation = "network.apply"
	serve := func(name string, handler ipc.Handler, output *diagnosticBuffer) ipc.Client {
		t.Helper()
		socket := filepath.Join(directory, name)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- (ipc.Server{SocketPath: socket, Handler: handler, Logger: slog.New(slog.NewJSONHandler(output, nil))}).Serve(ctx)
		}()
		t.Cleanup(func() {
			cancel()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
		deadline := time.Now().Add(time.Second)
		for {
			if _, err := os.Stat(socket); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("IPC listener did not start")
			}
			time.Sleep(time.Millisecond)
		}
		return ipc.Client{SocketPath: socket}
	}
	root := serve("root.sock", ipc.HandlerFunc(func(_ context.Context, request ipc.Request) ipc.Response {
		return ipc.Failure(request.ID, rootFailure)
	}), &rootLog)
	service, err := server.OpenService(filepath.Join(directory, "server"), server.IPCNetwork{Client: root},
		slog.New(slog.NewJSONHandler(&serverLog, nil)))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.NewHTTPHandler(service, nil)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Set("X-Trim-Userid", "test-user")
		r.Header.Set("X-Trim-Isadmin", "true")
		handler.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	remote, err := NewRemoteClient(gateway.URL, nil, gateway.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, remoteErr := remote.RegisterDevice(context.Background(), "test", key.PublicKey().String())
	if remoteErr == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	manager := &Manager{logger: slog.New(slog.NewJSONHandler(&clientLog, nil)), statusChanges: notify.New()}
	manager.fail(remoteErr)
	app := serve("app.sock", ipc.HandlerFunc(func(_ context.Context, request ipc.Request) ipc.Response {
		return ipc.Failure(request.ID, remoteErr)
	}), &clientLog)
	err = app.Call(context.Background(), "authorize-complete", nil, nil)
	failure := model.PublicError(err)
	if failure == nil || failure.Code != model.ErrorUnavailable || failure.HTTPStatus != 503 ||
		failure.RequestID == "" || failure.Operation != "network.apply" {
		t.Fatalf("final IPC failure = %+v", failure)
	}
	for _, detail := range []string{"nftables: operation not permitted", "rollback: device busy"} {
		if !strings.Contains(failure.Detail, detail) || !strings.Contains(model.FormatError(err), detail) {
			t.Fatalf("root cause %q missing in %+v", detail, failure)
		}
	}
	status, _ := json.Marshal(manager.Status())
	if !bytes.Contains(status, []byte("nftables")) || bytes.Contains(status, []byte(`"cause"`)) {
		t.Fatalf("status does not carry safe details: %s", status)
	}
	for name, log := range map[string]*diagnosticBuffer{"root": &rootLog, "server": &serverLog, "client": &clientLog} {
		if !strings.Contains(log.String(), "nftables") || !strings.Contains(log.String(), failure.RequestID) {
			t.Fatalf("%s log omitted root cause or correlation ID: %s", name, log.String())
		}
	}
}

type diagnosticBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *diagnosticBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *diagnosticBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
