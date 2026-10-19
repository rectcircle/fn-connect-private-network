//go:build windows

package windowsinstall

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Microsoft/go-winio"
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
			server := ipc.Server{SocketPath: socket, OnReady: func() { close(ready) },
				PrepareMaintenanceShutdown: func() (func(), error) {
					if refuse {
						return nil, model.NewError(model.ErrorPermissionDenied, "test refusal", false)
					}
					return cancel, nil
				},
				Handler: ipc.HandlerFunc(func(_ context.Context, request ipc.Request) ipc.Response {
					t.Error("maintenance must not enter the business handler")
					return ipc.Failure(request.ID, model.NewError(model.ErrorPermissionDenied, "business call forbidden", false))
				}),
			}
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

// Matches the v1.0.9 framing, IPC 4 admission and response product version.
func TestStopReleasedDaemonProtocol(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("refuse=%v", refuse), func(t *testing.T) {
			socket := fmt.Sprintf(`\\.\pipe\FnCPN.Installer.Historical.%d.%d`, os.Getpid(), time.Now().UnixNano())
			listener, err := winio.ListenPipe(socket, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					conn.SetDeadline(time.Now().Add(3 * time.Second))
					payload, err := ipc.ReadFrame(conn)
					if err != nil {
						conn.Close()
						return
					}
					var req ipc.Request
					if json.Unmarshal(payload, &req) != nil {
						conn.Close()
						return
					}
					response := ipc.Success(req.ID, nil)
					stop := false
					if req.Version != 4 {
						response = ipc.Failure("", model.NewError(model.ErrorProtocol, "unsupported local IPC version", false))
					} else if req.Method == "shutdown-client-daemon" {
						if refuse {
							response = ipc.Failure(req.ID, model.NewError(model.ErrorPermissionDenied, "historical refusal", false))
						} else {
							stop = true
						}
					}
					response.Version, response.ProductVersion = 4, "1.0.9"
					ipc.WriteResponse(conn, response)
					conn.Close()
					if stop {
						listener.Close()
						return
					}
				}
			}()
			err = stopClientDaemonAt(socket)
			if refuse {
				if err == nil || !strings.Contains(err.Error(), "historical refusal") {
					t.Fatalf("refusal hidden: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			listener.Close()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("historical server did not stop")
			}
		})
	}
}
