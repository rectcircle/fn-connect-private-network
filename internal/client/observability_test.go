package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestAuthorizationAndConnectionInfoMilestones(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured_%v", configured), func(t *testing.T) {
			store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), newMemorySecretStore())
			if configured {
				store = configuredManagerStore(t)
			}
			privateKey, err := store.EnsureWireGuardKey("home-nas")
			if err != nil {
				t.Fatal(err)
			}
			configuration := managerClientConfiguration()
			remote := &fakeRemoteService{
				bootstrap: Bootstrap{Administrator: true},
				registration: model.DeviceRegistration{
					Device: model.Device{
						ID: configuration.DeviceID, OverlayAddress: configuration.ClientAddress, Enabled: true,
					},
					Configuration: configuration,
				},
				configuration: configuration,
			}
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil))
			for attempt := range 2 {
				manager, err := NewManager(ManagerOptions{
					Store: store, Logger: logger, DeviceName: "macos.shared",
					Discoverer: fakeDiscoverer{result: Discovery{FN: []string{"home-nas.fnos.net:443"}}},
					Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
						return remote, nil
					},
					Privileged: &fakePrivilegedNetwork{},
					Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
					Probe:      fakeLocalProbe{},
				})
				if err != nil {
					t.Fatal(err)
				}
				service := NewWithRuntime(manager)
				service.Logger = logger
				begin, _ := ipc.NewRequest("begin", MethodAuthorizationBegin, AuthorizationRequest{FNID: "home-nas"})
				response := service.Handle(context.Background(), begin)
				var pending AuthorizationResult
				if !response.OK {
					t.Fatal(response.Error)
				}
				if err := json.Unmarshal(response.Result, &pending); err != nil {
					t.Fatal(err)
				}
				request, _ := ipc.NewRequest("complete", MethodAuthorize, Authorization{
					RequestID: pending.RequestID, FNID: "home-nas",
					Cookies: []Cookie{{Name: "session", Value: "cookie-value-canary"}},
				})
				ctx := logging.WithLogger(context.Background(), logger.With("ipc_request_id", "ipc-auth"))
				if result := service.Handle(ctx, request); !result.OK {
					t.Fatal(result.Error)
				}
				if manager.Status().State != model.ClientRelay {
					t.Fatal("authorization did not connect")
				}
				saved, err := store.Load()
				if err != nil || saved == nil || saved.DeviceID != configuration.DeviceID ||
					saved.PublicKey != privateKey.PublicKey().String() {
					t.Fatalf("device identity changed on attempt %d: %v", attempt, err)
				}
				key, err := store.EnsureWireGuardKey("home-nas")
				if err != nil || key != privateKey {
					t.Fatal("reauthorization or manager restart rotated the private key")
				}
				beforePoll := output.Len()
				for range 5 {
					service.Handle(ctx, ipc.Request{ID: "status", Method: MethodStatus})
					service.Handle(ctx, ipc.Request{ID: "diagnose", Method: MethodDiagnose})
					manager.maintainHealth(ctx)
					manager.maintainPrivileged(ctx)
					manager.operation.Lock()
					err := manager.applyWatchedConfiguration(ctx, configuration)
					manager.operation.Unlock()
					if err != nil {
						t.Fatal(err)
					}
				}
				if output.Len() != beforePoll {
					t.Fatal("unchanged status/diagnostic/maintenance polling emitted INFO logs")
				}
				if err := manager.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			wantRegistrations := 1
			if configured {
				wantRegistrations = 0
			}
			if remote.registrations != wantRegistrations {
				t.Fatalf("registrations = %d, want %d", remote.registrations, wantRegistrations)
			}
			events := clientInfoEvents(t, output.String())
			for _, milestone := range []string{
				"authorization requested", "authorization credentials received", "authorization validation started",
				"gateway session validated", "authorization saved", "client connection started",
				"device configuration ready", "local probe completed", "FN Connect discovery completed",
				"relay path selected", "client network applied", "WireGuard handshake confirmed",
				"client connected", "authorization completed",
			} {
				count := 0
				for _, event := range events {
					if event["msg"] == milestone {
						count++
						if milestone == "client connected" &&
							(event["authorization_id"] == "" || event["authorization_id"] == nil ||
								event["ipc_request_id"] != "ipc-auth" || event["device_id"] != configuration.DeviceID) {
							t.Fatalf("connection lost request/device context: %+v", event)
						}
					}
				}
				if count != 2 {
					t.Fatalf("milestone %q count = %d, want 2\n%s", milestone, count, output.String())
				}
			}
			for _, secret := range []string{"cookie-value-canary", privateKey.String(), configuration.ServerPublicKey} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("logs exposed key or cookie material")
				}
			}
		})
	}
}

func TestClearingConfigurationDoesNotRotateIdentityButForgetDoes(t *testing.T) {
	store := configuredManagerStore(t)
	original, err := store.EnsureWireGuardKey("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	retained, err := store.EnsureWireGuardKey("home-nas")
	if err != nil || retained != original {
		t.Fatal("clearing non-secret configuration changed identity")
	}
	if err := store.Forget("home-nas"); err != nil {
		t.Fatal(err)
	}
	recreated, err := store.EnsureWireGuardKey("home-nas")
	if err != nil || recreated == original {
		t.Fatal("clearing credentials did not create a new identity")
	}
}

func clientInfoEvents(t *testing.T, output string) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(output))
	var events []map[string]any
	for {
		var event map[string]any
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			return events
		} else if err != nil {
			t.Fatal(err)
		}
		if event["level"] == "INFO" {
			events = append(events, event)
		}
	}
}
