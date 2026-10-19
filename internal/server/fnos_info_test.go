//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fnOSInfoFixture(t *testing.T, status int, body, settings string) *fnOSInfoReader {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "fncpn-info-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "fn.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/trimfn" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected metadata request %s %s", r.Method, r.URL)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	server.Listener = ln
	server.Start()
	t.Cleanup(server.Close)
	path := filepath.Join(dir, "gateway.json")
	if err = os.WriteFile(path, []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	reader := newFNOSInfoReader(socket, path)
	t.Cleanup(reader.client.CloseIdleConnections)
	return reader
}

const gatewayFixture = `{"schema":{"http":{"port":5666},"https":{"port":5667}},"force_https":false,"future":true}`

func TestFNOSInfoReadLocalMetadata(t *testing.T) {
	for _, https := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[https], func(t *testing.T) {
			config := gatewayFixture
			if https {
				config = strings.Replace(config, `"force_https":false`, `"force_https":true`, 1)
			}
			reader := fnOSInfoFixture(t, 200, "Home-NAS\n", config)
			info := reader.read(t.Context())
			port, scheme := uint16(5666), "http"
			if https {
				port, scheme = 5667, "https"
			}
			if info.FNID != "home-nas" || info.DefaultDNSSuffix != "home-nas.fncpn" || info.FNConnectState != "ready" || info.ManagementState != "ready" || info.ManagementPort != port || info.ManagementScheme != scheme {
				t.Fatalf("%+v", info)
			}
		})
	}
}
func TestFNOSInfoFailureIsolation(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		status                                 int
		body, config, fnState, managementState string
	}{
		{"unconfigured", 200, " \n", gatewayFixture, "not-configured", "ready"},
		{"bad ID", 200, "<html>error</html>", gatewayFixture, "unavailable", "ready"},
		{"oversized ID", 200, strings.Repeat("a", 1025), gatewayFixture, "unavailable", "ready"},
		{"HTTP error", 503, "home-nas", gatewayFixture, "unavailable", "ready"},
		{"bad JSON", 200, "home-nas", "{", "ready", "unavailable"},
		{"oversized JSON", 200, "home-nas", strings.Repeat(" ", 4097), "ready", "unavailable"},
		{"invalid port", 200, "home-nas", `{"schema":{"http":{"port":65536}},"force_https":false}`, "ready", "unavailable"},
		{"unknown protocol", 200, "home-nas", `{"schema":{"http":{"port":5666}}}`, "ready", "unavailable"},
		{"forced HTTPS unavailable", 200, "home-nas", `{"schema":{"http":{"port":5666}},"force_https":true}`, "ready", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := fnOSInfoFixture(t, tc.status, tc.body, tc.config).read(t.Context())
			if info.FNConnectState != tc.fnState || info.ManagementState != tc.managementState {
				t.Fatalf("%+v", info)
			}
		})
	}
}
func TestFNOSBootstrapMetadataDoesNotOverwriteSettings(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	suffix := "custom.fncpn"
	if _, err = store.UpdateNetworks(t.Context(), NetworkUpdate{DNSSuffix: &suffix}); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(store, nil)
	reader := fnOSInfoFixture(t, 200, "home-nas", gatewayFixture)
	handler.nasInfo = reader.read
	req := httptest.NewRequest("GET", "/api/v1/bootstrap?fnId=forged&nasPort=9999", nil)
	req.Header.Set("X-Trim-Userid", "1000")
	req.Header.Set("X-FnCPN-Client-Version", "1.0.10")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	var body struct {
		NASInfo  fnOSInfo `json:"nasInfo"`
		Settings struct {
			DNSSuffix string `json:"dnsSuffix"`
		} `json:"settings"`
	}
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.NASInfo.FNID != "home-nas" || body.Settings.DNSSuffix != suffix {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
	// Local metadata absence never blocks bootstrap or clears persisted settings.
	handler.nasInfo = newFNOSInfoReader("/nonexistent/fn.sock", "/nonexistent/gateway.json").read
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != 200 || store.Snapshot().Settings.DNSSuffix != suffix {
		t.Fatal("metadata outage damaged bootstrap/configuration")
	}
}
func TestFNOSInfoHonorsCancellation(t *testing.T) {
	reader := fnOSInfoFixture(t, 200, "home-nas", gatewayFixture)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	info := reader.read(ctx)
	if info.FNConnectState != "unavailable" || info.ManagementState != "ready" {
		t.Fatalf("%+v", info)
	}
	// The HTTP client has a hard upper bound even without a caller deadline.
	if reader.client.Timeout > time.Second || reader.client.Timeout <= 0 {
		t.Fatal("missing metadata deadline")
	}
}

// Explicit opt-in for a local NAS socket or an SSH-forwarded Unix socket.
func TestFNOSInfoNativeSmoke(t *testing.T) {
	socket, settings := os.Getenv("FNCPN_TEST_FNOS_SOCKET"), os.Getenv("FNCPN_TEST_FNOS_SETTINGS")
	if socket == "" || settings == "" {
		t.Skip("requires explicit NAS metadata paths")
	}
	reader := newFNOSInfoReader(socket, settings)
	defer reader.client.CloseIdleConnections()
	info := reader.read(t.Context())
	if info.FNConnectState != "ready" || info.ManagementState != "ready" || info.DefaultDNSSuffix == "" {
		t.Fatalf("metadata read failed: %+v", info)
	}
	t.Logf("metadata ready; management scheme=%s port=%d", info.ManagementScheme, info.ManagementPort)
}
