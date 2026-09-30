package linux

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileKeyStoreCreatesAndReusesPrivateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "server.key")
	store := fileKeyStore{path: path}
	first, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	second, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if first != second {
		t.Fatal("server private key changed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("key mode = %o", mode)
	}
}

func TestFileServerStateStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "network-state.json")
	store := fileServerStateStore{path: path}
	expected := persistedServerState{
		Version:       serverStateVersion,
		OwnerToken:    "owner",
		InterfaceName: defaultInterfaceName,
		FirewallTable: "fncpn_owner",
	}
	if err := store.Save(expected); err != nil {
		t.Fatalf("save: %v", err)
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
