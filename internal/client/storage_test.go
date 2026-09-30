package client

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestConfigStorePersistsNonSecretConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "config.json")
	secrets := newMemorySecretStore()
	store := NewConfigStore(path, secrets)
	expected := LocalConfig{
		Version:   localConfigVersion,
		FNID:      "home-nas",
		DeviceID:  "device-1",
		PublicKey: managerKey(1),
		Configuration: model.ClientConfiguration{
			DeviceID:        "device-1",
			ServerPublicKey: managerKey(2),
			ServerAddress:   "10.203.0.1/24",
			ClientAddress:   "10.203.0.2/32",
			ListenPort:      51820,
			AllowedIPs:      []string{"10.203.0.1/32"},
		},
		AutoConnect: true,
	}
	if err := store.Save(expected); err != nil {
		t.Fatalf("save config: %v", err)
	}
	actual, err := store.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if actual == nil || !reflect.DeepEqual(*actual, expected) {
		t.Fatalf("config = %#v, want %#v", actual, expected)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("config mode = %o", mode)
	}
}

func TestConfigStoreKeepsSecretsOutsideConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secrets := newMemorySecretStore()
	store := NewConfigStore(path, secrets)
	privateKey, err := store.EnsureWireGuardKey("home-nas")
	if err != nil {
		t.Fatalf("ensure key: %v", err)
	}
	reloaded, err := store.EnsureWireGuardKey("home-nas")
	if err != nil {
		t.Fatalf("reload key: %v", err)
	}
	if privateKey != reloaded {
		t.Fatal("private key was not reused")
	}
	cookies := []Cookie{{
		Name:     "fnos-token",
		Value:    "secret-cookie",
		Domain:   "home-nas.fnos.net",
		Path:     "/",
		Secure:   true,
		HTTPOnly: true,
		Expires:  time.Now().UTC().Add(time.Hour).Truncate(time.Second),
	}}
	if err := store.SaveCookies("home-nas", cookies); err != nil {
		t.Fatalf("save cookies: %v", err)
	}
	actual, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatalf("load cookies: %v", err)
	}
	if !reflect.DeepEqual(actual, cookies) {
		t.Fatalf("cookies = %#v, want %#v", actual, cookies)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret operation unexpectedly created config file: %v", err)
	}
}

type memorySecretStore struct {
	values map[string][]byte
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{values: make(map[string][]byte)}
}

func (s *memorySecretStore) Get(
	service string,
	account string,
) ([]byte, bool, error) {
	value, ok := s.values[service+"\x00"+account]
	return append([]byte(nil), value...), ok, nil
}

func (s *memorySecretStore) Put(
	service string,
	account string,
	value []byte,
) error {
	s.values[service+"\x00"+account] = append([]byte(nil), value...)
	return nil
}

func (s *memorySecretStore) Delete(service string, account string) error {
	delete(s.values, service+"\x00"+account)
	return nil
}
