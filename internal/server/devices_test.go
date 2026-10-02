package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func activityView(at time.Time, received uint64) serverSnapshot {
	return serverSnapshot{
		state: model.ServerState{Devices: []model.Device{{PublicKey: testPublicKey(1)}}},
		network: model.ServerNetworkSnapshot{
			Fresh: true, RefreshedAt: at,
			Network: model.ServerPrivilegedStatus{
				Active: true, Interface: "fncpn0", PublicKey: testPublicKey(9),
				Peers: []model.PrivilegedPeerStatus{{PublicKey: testPublicKey(1), ReceiveBytes: received}},
			},
		},
	}
}

func observed(previous, current serverSnapshot) serverSnapshot {
	current.activity = observeDeviceActivity(previous, current)
	return current
}

func TestDeviceKeepaliveActivity(t *testing.T) {
	now := time.Now().UTC()
	view := observed(serverSnapshot{}, activityView(now, 1000))
	check := func(want deviceConnectionState) {
		t.Helper()
		if got := deviceConnection(testPublicKey(1), view, view.network.RefreshedAt); got != want {
			t.Fatalf("connection=%s want=%s at=%s", got, want, view.network.RefreshedAt.Sub(now))
		}
	}
	check(deviceUnknown)
	view = observed(view, activityView(now.Add(5*time.Second), 1032))
	check(deviceConnected)
	for seconds := 10; seconds <= 95; seconds += 5 {
		view = observed(view, activityView(now.Add(time.Duration(seconds)*time.Second), 1032))
		check(deviceConnected)
	}
	beforeExpiry := view
	view = observed(view, activityView(now.Add(100*time.Second), 1032))
	check(deviceDisconnected)
	if semanticSnapshotCursor(beforeExpiry) == semanticSnapshotCursor(view) {
		t.Fatal("keepalive expiry must wake the admin watch even with unchanged counters")
	}
	if configurationSnapshotCursor(beforeExpiry) != configurationSnapshotCursor(view) {
		t.Fatal("activity must not change the client configuration cursor")
	}
	view = observed(view, activityView(now.Add(105*time.Second), 1064))
	check(deviceConnected)
	if deviceConnection(testPublicKey(1), view, now.Add(121*time.Second)) != deviceUnknown {
		t.Fatal("stale status must not authorize deletion")
	}
}

func TestDeviceActivityDoesNotUseTXOrHandshakeAsHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	var view serverSnapshot
	for seconds := 0; seconds <= 95; seconds += 5 {
		current := activityView(now.Add(time.Duration(seconds)*time.Second), 999999)
		current.network.Network.Peers[0].TransmitBytes = uint64(seconds * 100)
		handshake := current.network.RefreshedAt
		current.network.Network.Peers[0].LastHandshake = &handshake
		view = observed(view, current)
		want := deviceUnknown
		if seconds > 90 {
			want = deviceDisconnected
		}
		if got := deviceConnection(testPublicKey(1), view, current.network.RefreshedAt); got != want {
			t.Fatalf("t=%d got=%s want=%s", seconds, got, want)
		}
	}
}

func TestDeviceActivityResetsOnUncertainObservation(t *testing.T) {
	for _, reason := range []string{"counter_reset", "gap", "clock_backwards", "interface", "server_key", "missing_peer", "status_error", "inactive", "degraded"} {
		t.Run(reason, func(t *testing.T) {
			now := time.Now().UTC()
			previous := observed(serverSnapshot{}, activityView(now, 100))
			previous = observed(previous, activityView(now.Add(5*time.Second), 132))
			current := activityView(now.Add(10*time.Second), 132)
			switch reason {
			case "counter_reset":
				current.network.Network.Peers[0].ReceiveBytes = 0
			case "gap":
				current.network.RefreshedAt = now.Add(30 * time.Second)
			case "clock_backwards":
				current.network.RefreshedAt = now
			case "interface":
				current.network.Network.Interface = "fncpn1"
			case "server_key":
				current.network.Network.PublicKey = testPublicKey(8)
			case "missing_peer":
				current.network.Network.Peers = nil
			case "status_error":
				current.network.LastError = model.NewError(model.ErrorUnavailable, "status failed", true)
			case "inactive":
				current.network.Network.Active = false
			case "degraded":
				current.network.Network.Degraded = true
			}
			current = observed(previous, current)
			if got := deviceConnection(testPublicKey(1), current, current.network.RefreshedAt); got != deviceUnknown {
				t.Fatalf("unsafe connection state=%s", got)
			}
			recovered := observed(current, activityView(current.network.RefreshedAt.Add(5*time.Second), 132))
			if got := deviceConnection(testPublicKey(1), recovered, recovered.network.RefreshedAt); got != deviceUnknown &&
				reason != "counter_reset" {
				t.Fatalf("recovery must establish a new observation window: %s", got)
			}
		})
	}
}

type deletionNetwork struct {
	status    model.ServerPrivilegedStatus
	statusErr error
	applyErr  error
	applies   []model.ServerState
}

func (n *deletionNetwork) Status(context.Context) (model.ServerPrivilegedStatus, error) {
	return cloneServerNetworkSnapshot(model.ServerNetworkSnapshot{Network: n.status}).Network, n.statusErr
}

func (n *deletionNetwork) Apply(ctx context.Context, state model.ServerState) (model.ServerPrivilegedStatus, error) {
	n.applies = append(n.applies, cloneState(state))
	if n.applyErr != nil {
		err := n.applyErr
		n.applyErr = nil
		return model.ServerPrivilegedStatus{}, err
	}
	n.status.Peers = slices.DeleteFunc(n.status.Peers, func(peer model.PrivilegedPeerStatus) bool {
		return !slices.ContainsFunc(state.Devices, func(device model.Device) bool { return device.PublicKey == peer.PublicKey })
	})
	// Rollback must restore a removed peer as well as its persisted record.
	for _, device := range state.Devices {
		if !slices.ContainsFunc(n.status.Peers, func(peer model.PrivilegedPeerStatus) bool { return peer.PublicKey == device.PublicKey }) {
			n.status.Peers = append(n.status.Peers, model.PrivilegedPeerStatus{PublicKey: device.PublicKey})
		}
	}
	return n.Status(ctx)
}

func deviceDeletionFixture(t *testing.T) (*Service, *deletionNetwork, model.Device, model.Device) {
	t.Helper()
	service, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := createTestDevice(service, "macos.shared", testPublicKey(1))
	if err != nil {
		t.Fatal(err)
	}
	active, _, err := createTestDevice(service, "macos.shared", testPublicKey(2))
	if err != nil {
		t.Fatal(err)
	}
	network := &deletionNetwork{status: model.ServerPrivilegedStatus{
		Active: true, Interface: "fncpn0", PublicKey: testPublicKey(9), ListenPort: DefaultListenPort,
		Peers: []model.PrivilegedPeerStatus{{PublicKey: old.PublicKey}, {PublicKey: active.PublicKey}},
	}}
	service.network = network
	start := time.Now().UTC().Add(-100 * time.Second)
	for seconds := 0; seconds <= 100; seconds += 5 {
		network.status.Peers[1].ReceiveBytes += 32
		status, _ := network.Status(context.Background())
		service.publish(serverSnapshot{state: service.Snapshot(), network: model.ServerNetworkSnapshot{
			Fresh: true, RefreshedAt: start.Add(time.Duration(seconds) * time.Second), Network: status,
		}})
	}
	return service, network, old, active
}

func deleteRequest(service *Service, id, role string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodDelete, "/app/fncpn/api/v1/admin/devices/"+id, nil)
	if role != "" {
		request.Header.Set("X-Trim-Userid", "test-user")
		request.Header.Set("X-Trim-Isadmin", role)
	}
	response := httptest.NewRecorder()
	NewHTTPHandler(service, nil).ServeHTTP(response, request)
	return response
}

func TestDeleteOfflineDeviceByID(t *testing.T) {
	service, network, old, active := deviceDeletionFixture(t)
	view := service.snapshot()
	rows := deviceViews(view, time.Now())
	if rows[0].ConnectionState != deviceDisconnected || rows[1].ConnectionState != deviceConnected {
		t.Fatalf("connection states=%+v", rows)
	}
	adminGeneration, configGeneration := service.adminChanges.Current(), service.configChanges.Current()
	response := deleteRequest(service, old.ID, "true")
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if got := service.Snapshot().Devices; len(got) != 1 || !reflect.DeepEqual(got[0], active) {
		t.Fatalf("same-name active device was modified: %+v", got)
	}
	plan, err := ServerPlan(network.applies[0])
	if err != nil || len(plan.Peers) != 1 || plan.Peers[0].PublicKey != active.PublicKey {
		t.Fatalf("peer removal plan=%+v err=%v", plan, err)
	}
	reopened, err := OpenService(service.store.dir, nil, nil)
	if err != nil || !reflect.DeepEqual(reopened.Snapshot(), service.Snapshot()) {
		t.Fatalf("deletion not persisted: %v", err)
	}
	if service.adminChanges.Current() == adminGeneration || service.configChanges.Current() == configGeneration {
		t.Fatal("deletion did not notify both watches")
	}
	if _, err := service.clientConfiguration(old.ID); err == nil || model.AsError(err).Code != model.ErrorDeviceRevoked {
		t.Fatalf("removed device can still get configuration: %v", err)
	}
	if response := deleteRequest(service, old.ID, "true"); response.Code != http.StatusNotFound {
		t.Fatalf("repeated deletion status=%d", response.Code)
	}
	if len(network.applies) != 1 {
		t.Fatal("repeated deletion applied the network again")
	}
	if len(service.snapshot().activity) != 1 {
		t.Fatal("removed device retained activity state")
	}
}

func TestDeleteDeviceRefusals(t *testing.T) {
	for _, reason := range []string{"unauthenticated", "non_admin", "connected", "new_rx", "startup", "status_error", "degraded", "no_network"} {
		t.Run(reason, func(t *testing.T) {
			service, network, old, active := deviceDeletionFixture(t)
			role, id, want := "true", old.ID, http.StatusServiceUnavailable
			switch reason {
			case "unauthenticated":
				role, want = "", http.StatusUnauthorized
			case "non_admin":
				role, want = "false", http.StatusForbidden
			case "connected":
				id, want = active.ID, http.StatusPreconditionFailed
			case "new_rx":
				network.status.Peers[0].ReceiveBytes += 32
				want = http.StatusPreconditionFailed
			case "startup":
				view := service.snapshot()
				view.activity = nil
				service.view.Store(&view)
			case "status_error":
				network.statusErr = model.NewError(model.ErrorUnavailable, "status read failed", true)
			case "degraded":
				network.status.Degraded = true
			case "no_network":
				service.network = nil
			}
			response := deleteRequest(service, id, role)
			if response.Code != want || len(network.applies) != 0 || len(service.Snapshot().Devices) != 2 {
				t.Fatalf("status=%d want=%d applies=%d body=%s", response.Code, want, len(network.applies), response.Body)
			}
			var body struct{ Error *model.Error }
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error == nil || body.Error.RequestID == "" {
				t.Fatalf("missing structured error: %s", response.Body)
			}
		})
	}
}

func TestDeleteDeviceRollbackAndCommitPoint(t *testing.T) {
	for _, failure := range []string{"apply", "rename", "sync"} {
		t.Run(failure, func(t *testing.T) {
			service, network, old, _ := deviceDeletionFixture(t)
			want := errors.New("injected deletion failure")
			switch failure {
			case "apply":
				network.applyErr = want
			case "rename":
				service.store.renameFile = func(string, string) error { return want }
			case "sync":
				service.store.syncDir = func(string) error { return want }
			}
			if err := service.DeleteDevice(context.Background(), old.ID); !errors.Is(err, want) {
				t.Fatalf("failure was lost: %v", err)
			}
			count, applies := 2, 2
			if failure == "sync" {
				count, applies = 1, 1
			}
			reopened, err := OpenService(service.store.dir, nil, nil)
			if err != nil || len(service.Snapshot().Devices) != count ||
				len(reopened.Snapshot().Devices) != count || len(network.status.Peers) != count ||
				len(network.applies) != applies {
				t.Fatalf("commit boundary mismatch: err=%v state=%+v peers=%+v applies=%d", err, service.Snapshot(), network.status.Peers, len(network.applies))
			}
		})
	}
}

func TestConcurrentDeleteDevice(t *testing.T) {
	service, network, old, _ := deviceDeletionFixture(t)
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() { results <- service.DeleteDevice(context.Background(), old.ID) })
	}
	workers.Wait()
	close(results)
	var successes, missing int
	for err := range results {
		if err == nil {
			successes++
		} else if model.AsError(err).Code == model.ErrorNotFound {
			missing++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || missing != 1 || len(network.applies) != 1 {
		t.Fatalf("success=%d missing=%d applies=%d", successes, missing, len(network.applies))
	}
}

func TestDeviceActivityLogsOnlyTransitions(t *testing.T) {
	service, network, old, _ := deviceDeletionFixture(t)
	var output bytes.Buffer
	service.logger = slog.New(slog.NewTextHandler(&output, nil))
	service.refreshNetwork(context.Background())
	if output.Len() != 0 {
		t.Fatalf("unchanged sampling logged: %s", &output)
	}
	for range 3 {
		network.status.Peers[0].ReceiveBytes += 32
		service.refreshNetwork(context.Background())
	}
	if strings.Count(output.String(), "device connection state changed") != 1 ||
		!strings.Contains(output.String(), "device_id="+old.ID) ||
		!strings.Contains(output.String(), "state=connected") {
		t.Fatalf("missing transition or logged each keepalive: %s", &output)
	}
	network.status.Peers[0].ReceiveBytes = 0
	service.refreshNetwork(context.Background())
	service.refreshNetwork(context.Background())
	if strings.Count(output.String(), "device connection state changed") != 2 ||
		!strings.Contains(output.String(), "state=unknown") {
		t.Fatalf("counter reset transition not logged once: %s", &output)
	}
}
