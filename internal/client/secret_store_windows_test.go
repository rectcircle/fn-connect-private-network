//go:build windows

package client

import (
	"bytes"
	"os"
	"testing"
)

func TestDPAPISecretStoreRoundTripAndIsolation(t *testing.T) {
	store := dpapiSecretStore{directory: t.TempDir()}
	const service, account = "wireguard-private-key", "home-nas"
	value := []byte("private-key-test-canary")
	if err := store.Put(service, account, value); err != nil {
		t.Fatal(err)
	}
	if sealed, err := os.ReadFile(store.path(service, account)); err != nil {
		t.Fatal(err)
	} else if bytes.Contains(sealed, value) {
		t.Fatal("DPAPI file contains plaintext credential")
	}
	actual, found, err := store.Get(service, account)
	if err != nil || !found || !bytes.Equal(actual, value) {
		t.Fatalf("Get = %q, %v, %v", actual, found, err)
	}
	sealed, err := os.ReadFile(store.path(service, account))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(service, "another-nas"), sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Get(service, "another-nas"); err == nil || found {
		t.Fatalf("credential crossed account boundary: found=%v err=%v", found, err)
	}
	if err := store.Delete(service, account); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(service, account); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, found, err := store.Get(service, account); err != nil || found {
		t.Fatalf("deleted credential = found %v err %v", found, err)
	}
}
