//go:build windows

package windowsinstall

import (
	"context"
	"fmt"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStopClientDaemonAtIsolatedPipe(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("refuse=%v", refuse), func(t *testing.T) {
			// Never address an installed daemon or enumerate the user's sessions.
			socket := fmt.Sprintf(`\\.\pipe\FnCPN.Installer.Test.%d.%d`, os.Getpid(), time.Now().UnixNano())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			done := make(chan error, 1)
			server := ipc.Server{SocketPath: socket, OnReady: func() { close(ready) }, Handler: ipc.HandlerFunc(func(_ context.Context, request ipc.Request) ipc.Response {
				if request.Method == "shutdown-client-daemon" {
					if refuse {
						return ipc.Failure(request.ID, model.NewError(model.ErrorPermissionDenied, "test refusal", false))
					}
					go func() { time.Sleep(50 * time.Millisecond); cancel() }()
				}
				return ipc.Success(request.ID, model.ClientStatus{State: model.ClientPaused})
			})}
			go func() { done <- server.Serve(ctx) }()
			select {
			case <-ready:
			case err := <-done:
				t.Fatalf("server failed: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("server startup timed out")
			}
			err := stopClientDaemonAt(socket)
			if refuse && (err == nil || !strings.Contains(err.Error(), "test refusal")) {
				t.Fatalf("shutdown refusal was hidden: %v", err)
			}
			if !refuse && err != nil {
				t.Fatalf("shutdown failed: %v", err)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("server did not stop")
			}
			if err := stopClientDaemonAt(socket); err != nil {
				t.Fatalf("absent daemon should be harmless: %v", err)
			}
		})
	}
}
