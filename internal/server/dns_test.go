//go:build !windows

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestGatewayDoesNotExposeDeviceDNS(t *testing.T) {
	service, e := OpenService(t.TempDir(), testNetwork{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	registered, _, e := service.RegisterDevice(t.Context(), "historical client", testPublicKey(1))
	if e != nil {
		t.Fatal(e)
	}
	handler := NewHTTPHandler(service, nil)
	request := httptest.NewRequest(http.MethodPost, model.DNSQueryPath, bytes.NewBufferString(`{"name":"abc","type":"A"}`))
	request.Header.Set("X-Trim-Userid", "admin")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("DNS remains on gateway: %d", response.Code)
	}
	// The unchanged historical configuration route remains available to 1.0 clients.
	request = httptest.NewRequest(http.MethodGet, "/api/v1/devices/"+registered.Device.ID+"/config", nil)
	request.Header.Set("X-Trim-Userid", "user")
	request.Header.Set("X-FnCPN-Client-Version", "1.0.10")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	if e = version.Check("1.1.0", "1.0.10"); e == nil {
		t.Fatal("higher minor admitted")
	}
}
func TestSchemaOneUpgradePreservesIdentityAndBackup(t *testing.T) {
	dir := t.TempDir()
	service, e := OpenService(dir, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = createTestDevice(service, "historical client", testPublicKey(1)); e != nil {
		t.Fatal(e)
	}
	before := service.Snapshot()
	historical, e := json.Marshal(stateDocument{Version: 1, State: before})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, stateFileName), historical, 0600); e != nil {
		t.Fatal(e)
	}
	upgraded, e := OpenService(dir, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(before, upgraded.Snapshot()) {
		t.Fatal("upgrade changed devices or network")
	}
	backup, e := os.ReadFile(filepath.Join(dir, "state.schema-1.backup.json"))
	if e != nil {
		t.Fatal(e)
	}
	var old stateDocument
	if e = json.Unmarshal(backup, &old); e != nil || old.Version != 1 || !reflect.DeepEqual(old.State, before) {
		t.Fatal("invalid rollback backup")
	}
	var current stateDocument
	data, _ := os.ReadFile(filepath.Join(dir, stateFileName))
	_ = json.Unmarshal(data, &current)
	if current.Version != stateVersion {
		t.Fatal("schema did not advance")
	}
	suffix := "custom.fncpn"
	if _, e = upgraded.UpdateNetworks(t.Context(), NetworkUpdate{DNSSuffix: &suffix}); e != nil {
		t.Fatal(e)
	}
	again, e := OpenService(dir, nil, nil)
	if e != nil || again.Snapshot().Settings.DNSSuffix != suffix {
		t.Fatalf("suffix not persisted: %v", e)
	}
}

func TestDNSSettingsDoNotReapplyNetwork(t *testing.T) {
	calls := 0
	service, e := OpenService(t.TempDir(), testNetwork{apply: func(model.ServerState) error { calls++; return nil }}, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = service.Reconcile(t.Context()); e != nil {
		t.Fatal(e)
	}
	before := calls
	suffix := "custom.fncpn"
	if _, e = service.UpdateNetworks(t.Context(), NetworkUpdate{DNSSuffix: &suffix}); e != nil {
		t.Fatal(e)
	}
	if calls != before {
		t.Fatal("DNS settings interrupted network configuration")
	}
}
