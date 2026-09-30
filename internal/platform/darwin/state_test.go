//go:build darwin

package darwin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileNetworkStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root", "state.json")
	store := fileNetworkStateStore{path: path}
	expected := persistedNetworkState{
		Version:          networkStateVersion,
		PID:              os.Getpid(),
		ProcessStartedAt: 1,
		Executable:       "fncpn",
		OwnerToken:       "00112233445566778899aabbccddeeff",
		Interface:        "utun8",
		Address:          "10.203.0.2/32",
		MTU:              1280,
		Routes:           []string{"10.203.0.1/32", "192.168.71.0/24"},
	}

	if err := store.Save(expected); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("state mode = %o", mode)
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat state directory: %v", err)
	}
	if mode := directoryInfo.Mode().Perm(); mode != 0o700 {
		t.Fatalf("state directory mode = %o", mode)
	}

	actual, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if actual == nil || !reflect.DeepEqual(*actual, expected) {
		t.Fatalf("state = %#v, want %#v", actual, expected)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("state still exists: %v", err)
	}
}

func TestFileNetworkStateStoreRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{
		"version": 1,
		"pid": 42,
		"interface": "utun8",
		"address": "10.203.0.2/32",
		"mtu": 1280,
		"routes": [],
		"privateKey": "must-not-be-accepted"
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	store := fileNetworkStateStore{path: path}
	if _, err := store.Load(); err == nil {
		t.Fatal("state with unknown field was accepted")
	}
}

func TestFileNetworkStateStoreRejectsLoosePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	data := []byte(`{
		"version": 1,
		"pid": 42,
		"interface": "utun8",
		"address": "10.203.0.2/32",
		"mtu": 1280,
		"routes": []
	}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write state: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("set state mode: %v", err)
	}
	store := fileNetworkStateStore{path: path}
	if _, err := store.Load(); err == nil {
		t.Fatal("state with loose permissions was accepted")
	}
}
