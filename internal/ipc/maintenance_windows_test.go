//go:build windows

package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestMaintenanceReplyValidation(t *testing.T) {
	for _, payload := range []string{
		`{}`, `null`, `{"maintenanceVersion":1,"id":"other","ok":true}`,
		`{"maintenanceVersion":2,"id":"id","ok":true}`,
		`{"maintenanceVersion":1,"id":"id","ok":true,"error":"refusal"}`,
		`{"maintenanceVersion":1,"id":"id","ok":false}`,
		`{"maintenanceVersion":1,"id":"id","ok":false,"error":"refusal"}`,
	} {
		if err := validateMaintenanceReply([]byte(payload), "id"); err == nil {
			t.Fatalf("accepted %s", payload)
		}
		if _, err := identifyMaintenanceReply([]byte(payload), "id"); err == nil {
			t.Fatalf("fallback on %s", payload)
		}
	}
	if err := validateMaintenanceReply([]byte(`{"maintenanceVersion":1,"id":"id","ok":true,"futureField":42}`), "id"); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []int{4, 5, 6, 99} {
		for _, code := range []string{"PROTOCOL_ERROR", "PERMISSION_DENIED"} {
			payload := []byte(fmt.Sprintf(`{"version":%d,"productVersion":"1.0.9","ok":false,"error":{"code":%q,"message":"rejected"}}`, protocol, code))
			got, err := identifyMaintenanceReply(payload, "id")
			allowed := (protocol == 4 || protocol == 5) && code == "PROTOCOL_ERROR"
			if (err == nil) != allowed || (allowed && got != protocol) {
				t.Fatalf("%s: %d %v", payload, got, err)
			}
		}
	}
	for _, payload := range []string{
		`{"version":4,"productVersion":"1.0.9","id":"wrong","ok":true}`,
		`{"version":4,"productVersion":"1.0.9","id":"id","ok":true,"error":{"code":"PERMISSION_DENIED"}}`,
		`{"version":4,"productVersion":"1.0.9","id":"id","ok":false}`,
		`{"version":5,"productVersion":"1.0.9","id":"id","ok":true}`,
		`{"version":4,"productVersion":"","id":"id","ok":true}`,
	} {
		if err := validateLegacyShutdownReply([]byte(payload), "id", 4); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
}

func TestMaintenanceUsesPeerAuthorization(t *testing.T) {
	socket := fmt.Sprintf(`\\.\pipe\FnCPN.Maintenance.Authorization.%d.%d`, os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	server := Server{SocketPath: socket, OnReady: func() { close(ready) },
		Authorize: func(context.Context, PeerIdentity) error {
			return model.NewError(model.ErrorPermissionDenied, "denied", false)
		},
		PrepareMaintenanceShutdown: func() (func(), error) { t.Error("unauthorized maintenance"); return cancel, nil },
		Handler:                    HandlerFunc(func(context.Context, Request) Response { t.Error("unauthorized business call"); return Response{} }),
	}
	go func() { done <- server.Serve(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timeout")
	}
	deadline, stop := context.WithTimeout(ctx, time.Second)
	defer stop()
	if err := StopDaemonForMaintenance(deadline, socket); err == nil {
		t.Fatal("permission denial treated as absence")
	}
	cancel()
	<-done
}

func TestMaintenanceDoesNotTreatLiveUnresponsiveDaemonAsStopped(t *testing.T) {
	socket := fmt.Sprintf(`\\.\pipe\FnCPN.Maintenance.Timeout.%d.%d`, os.Getpid(), time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	server := Server{SocketPath: socket, OnReady: func() { close(ready) },
		PrepareMaintenanceShutdown: func() (func(), error) { return func() {}, nil },
		Handler:                    HandlerFunc(func(context.Context, Request) Response { return Response{} }),
	}
	go func() { done <- server.Serve(ctx) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timeout")
	}
	deadline, stop := context.WithTimeout(ctx, 250*time.Millisecond)
	defer stop()
	err := StopDaemonForMaintenance(deadline, socket)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("live daemon reported stopped: %v", err)
	}
	cancel()
	<-done
}

// Cross-version fixtures include a future product/business version but retain
// maintenance v1. No current ProductVersion/ProtocolVersion decoder is involved.
func TestMaintenanceIgnoresFutureBusinessEnvelope(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"maintenanceVersion": 1, "id": "id", "ok": true, "productVersion": "9.0.0", "businessIPCVersion": 99})
	if protocol, err := identifyMaintenanceReply(payload, "id"); err != nil || protocol != 0 {
		t.Fatalf("%d %v", protocol, err)
	}
}
