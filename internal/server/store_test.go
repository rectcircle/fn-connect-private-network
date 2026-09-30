package server

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func createTestDevice(service *Service, name, key string) (model.Device, bool, error) {
	service.operation.Lock()
	defer service.operation.Unlock()
	return service.createDevice(context.Background(), name, key)
}

type testNetwork struct {
	apply func(model.ServerState) error
}

func (n testNetwork) Apply(_ context.Context, state model.ServerState) (model.ServerPrivilegedStatus, error) {
	if n.apply != nil {
		if err := n.apply(state); err != nil {
			return model.ServerPrivilegedStatus{}, err
		}
	}
	return model.ServerPrivilegedStatus{
		Active: true, PublicKey: testPublicKey(9), Interface: "fncpn0",
		ListenPort: state.Settings.ListenPort,
	}, nil
}

func (n testNetwork) Status(context.Context) (model.ServerPrivilegedStatus, error) {
	return model.ServerPrivilegedStatus{Active: true, PublicKey: testPublicKey(9), ListenPort: DefaultListenPort}, nil
}

func TestStoreDeviceLifecycleAndOverlayChange(t *testing.T) {
	service, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, created, err := createTestDevice(service, "MacBook", testPublicKey(1))
	if err != nil || !created || first.OverlayAddress != "10.253.203.2/32" {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	second, _, err := createTestDevice(service, "Desktop", testPublicKey(2))
	if err != nil || second.OverlayAddress != "10.253.203.3/32" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	reused, created, err := createTestDevice(service, "Renamed", testPublicKey(1))
	if err != nil || created || reused != first {
		t.Fatalf("reused=%+v created=%v err=%v", reused, created, err)
	}
	overlay := "172.20.0.0/24"
	updated, err := service.UpdateNetworks(context.Background(), NetworkUpdate{OverlayCIDR: &overlay})
	if err != nil || updated.Devices[0].OverlayAddress != "172.20.0.2/32" ||
		updated.Devices[1].OverlayAddress != "172.20.0.3/32" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
}

func TestStoreSingleFileAndLegacyMigration(t *testing.T) {
	for _, transaction := range []bool{false, true} {
		t.Run(map[bool]string{false: "files", true: "transaction"}[transaction], func(t *testing.T) {
			dir := t.TempDir()
			state := model.ServerState{
				Settings: model.ServerSettings{OverlayCIDR: "192.168.240.0/24", ListenPort: DefaultListenPort},
			}
			if transaction {
				if err := writeJSONAtomic(filepath.Join(dir, "state.transaction.json"), stateDocument{Version: 2, State: state}); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := writeJSONAtomic(filepath.Join(dir, "settings.json"), map[string]any{
					"version": 2, "settings": state.Settings,
				}); err != nil {
					t.Fatal(err)
				}
				if err := writeJSONAtomic(filepath.Join(dir, "devices.json"), map[string]any{
					"version": 1, "devices": []model.Device{},
				}); err != nil {
					t.Fatal(err)
				}
			}
			service, err := OpenService(dir, nil, nil)
			if err != nil || service.Snapshot().Settings.OverlayCIDR != state.Settings.OverlayCIDR {
				t.Fatalf("migration err=%v", err)
			}
			if _, _, err := createTestDevice(service, "Mac", testPublicKey(1)); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenService(dir, nil, nil)
			if err != nil || len(reopened.Snapshot().Devices) != 1 {
				t.Fatalf("new file was not authoritative: %v", err)
			}
		})
	}
}

func TestStoreLANOnlyUpdatePreservesDeviceAddressGaps(t *testing.T) {
	service, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = createTestDevice(service, "Mac", testPublicKey(1))
	_, _, _ = createTestDevice(service, "Desktop", testPublicKey(2))
	state := service.Snapshot()
	state.Devices[1].OverlayAddress = "10.253.203.4/32"
	service.publish(serverSnapshot{state: state})
	lans := []string{"192.168.71.0/24"}
	updated, err := service.UpdateNetworks(context.Background(), NetworkUpdate{LANCIDRs: &lans})
	if err != nil || updated.Devices[1].OverlayAddress != "10.253.203.4/32" {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
}

func TestStoreRollsBackFailedApplyOrPersistence(t *testing.T) {
	for _, failure := range []string{"apply", "rename", "rollback"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			want := errors.New("injected failure")
			var counts []int
			service, err := OpenService(dir, testNetwork{apply: func(state model.ServerState) error {
				counts = append(counts, len(state.Devices))
				if failure == "apply" && len(counts) == 1 || failure == "rollback" {
					return want
				}
				return nil
			}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "rename" {
				service.store.renameFile = func(string, string) error { return want }
			}
			_, _, err = createTestDevice(service, "Mac", testPublicKey(1))
			if !errors.Is(err, want) || len(service.Snapshot().Devices) != 0 || !slices.Equal(counts, []int{1, 0}) {
				t.Fatalf("error=%v state=%+v applies=%v", err, service.Snapshot(), counts)
			}
			reopened, err := OpenService(dir, nil, nil)
			if err != nil || len(reopened.Snapshot().Devices) != 0 {
				t.Fatalf("failed state was persisted: %v", err)
			}
		})
	}
}

func TestStoreSnapshotDoesNotWaitForNetworkApply(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	service, err := OpenService(t.TempDir(), testNetwork{apply: func(model.ServerState) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := createTestDevice(service, "Mac", testPublicKey(1))
		done <- err
	}()
	<-started
	read := make(chan model.ServerState, 1)
	go func() { read <- service.Snapshot() }()
	select {
	case state := <-read:
		if len(state.Devices) != 0 {
			t.Fatal("uncommitted state was published")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot blocked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStoreUsesNewMemoryStateWhenCommitSyncFails(t *testing.T) {
	dir := t.TempDir()
	service, err := OpenService(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("sync directory failed")
	service.store.syncDir = func(string) error { return want }
	if _, _, err := createTestDevice(service, "Mac", testPublicKey(1)); !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	reopened, err := OpenService(dir, nil, nil)
	if err != nil || len(service.Snapshot().Devices) != 1 || len(reopened.Snapshot().Devices) != 1 {
		t.Fatalf("commit point was not respected: %v", err)
	}
}

func TestStoreRejectsCorruptOrNewerState(t *testing.T) {
	for _, data := range []string{
		`{`, `{"version":99,"state":{}}`,
		`{"version":1,"state":{"settings":{"overlayCIDR":"10.203.0.0/24","listenPort":51820,"lanCIDRs":["10.0.0.0/8"]},"devices":[]}}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenService(dir, nil, nil); err == nil {
			t.Fatalf("invalid state accepted: %s", data)
		}
	}
}

func testPublicKey(value byte) string {
	key := make([]byte, 32)
	for index := range key {
		key[index] = value
	}
	return base64.StdEncoding.EncodeToString(key)
}
