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
	session := NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "stable-device", Token: "short", LongToken: "long",
		Secret: "c2VjcmV0", UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.SaveNativeSession(session); err != nil {
		t.Fatalf("save native session: %v", err)
	}
	reloadedSession, found, err := store.LoadNativeSession("home-nas")
	if err != nil || !found || !reflect.DeepEqual(reloadedSession, session) {
		t.Fatalf("native session = %#v, found=%v, err=%v", reloadedSession, found, err)
	}
	adminSession := AdminWebSession{
		Version: adminWebSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "admin-web-device", Token: "web-short",
		UpdatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := store.SaveAdminWebSession(adminSession); err != nil {
		t.Fatalf("save admin Web session: %v", err)
	}
	reloadedAdminSession, found, err := store.LoadAdminWebSession("home-nas")
	if err != nil || !found || !reflect.DeepEqual(reloadedAdminSession, adminSession) {
		t.Fatalf("admin Web session = %#v, found=%v, err=%v",
			reloadedAdminSession, found, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("secret operation unexpectedly created config file: %v", err)
	}
}

func TestConfigStorePersistsLocalProbeConfiguration(t *testing.T) {
	store := NewConfigStore(
		filepath.Join(t.TempDir(), "config.json"),
		newMemorySecretStore(),
	)
	cfg := model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
		Key:       "probe-hmac-key",
	}
	if err := store.SaveLocalProbeConfig("home-nas", cfg); err != nil {
		t.Fatalf("save local probe: %v", err)
	}
	reloaded, found, checkedAt, err := store.LoadLocalProbeConfig("home-nas")
	if err != nil || !found {
		t.Fatalf("load local probe: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(reloaded, cfg) {
		t.Fatalf("local probe = %#v, want %#v", reloaded, cfg)
	}
	if checkedAt.IsZero() {
		t.Fatal("local probe cache did not record a check time")
	}
	if err := store.ClearLocalProbeConfig("home-nas"); err != nil {
		t.Fatalf("clear local probe: %v", err)
	}
	if _, found, _, err := store.LoadLocalProbeConfig("home-nas"); err != nil || found {
		t.Fatalf("local probe cache not cleared: found=%v err=%v", found, err)
	}
}

func TestConfigStoreRejectsInvalidLocalProbeConfiguration(t *testing.T) {
	store := NewConfigStore(
		filepath.Join(t.TempDir(), "config.json"),
		newMemorySecretStore(),
	)
	// Missing key is invalid and must not be persisted.
	if err := store.SaveLocalProbeConfig("home-nas", model.LocalProbeConfiguration{
		Endpoints: []string{"192.168.71.2:54790"},
	}); err == nil {
		t.Fatal("expected invalid local probe configuration to be rejected")
	}
	// Missing/invalid endpoint is invalid.
	if err := store.SaveLocalProbeConfig("home-nas", model.LocalProbeConfiguration{
		Endpoints: []string{"not-an-endpoint"},
		Key:       "probe-hmac-key",
	}); err == nil {
		t.Fatal("expected invalid endpoint to be rejected")
	}
	if _, found, _, err := store.LoadLocalProbeConfig("home-nas"); err != nil || found {
		t.Fatalf("invalid config was stored: found=%v err=%v", found, err)
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
