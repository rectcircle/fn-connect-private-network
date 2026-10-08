package client

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestGatewayApplicationFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		code      model.ErrorCode
		retryable bool
	}{
		{"plain_404", 404, "Not Found", model.ErrorUnavailable, true},
		{"empty_404", 404, "", model.ErrorUnavailable, true},
		{"html_404", 404, "<html>Cookie: gateway-secret</html>", model.ErrorUnavailable, true},
		{"unknown_json_404", 404, `{"message":"Not Found","token":"gateway-secret"}`, model.ErrorUnavailable, true},
		{"application_404", 404, `{"error":{"code":"NOT_FOUND","message":"device missing","retryable":false}}`, model.ErrorNotFound, false},
		{"application_revoked", 404, `{"error":{"code":"DEVICE_REVOKED","message":"device deleted","retryable":false}}`, model.ErrorDeviceRevoked, false},
		{"unauthorized", 401, "Unauthorized", model.ErrorAuthRequired, false},
		{"forbidden", 403, "Forbidden", model.ErrorPermissionDenied, false},
		{"gone", 410, "Gone", model.ErrorDeviceRevoked, false},
		{"bad_gateway", 502, "Bad Gateway", model.ErrorUnavailable, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-FnCPN-Version", version.Current)
				w.Header().Set("X-Request-ID", "gateway-request")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer gateway.Close()
			remote, err := NewRemoteClient(gateway.URL+gatewayApplicationPath, nil, gateway.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := url.Parse("ws" + strings.TrimPrefix(gateway.URL, "http") +
				gatewayApplicationPath + "/relay/v1/wireguard")
			for name, call := range map[string]func() error{
				"bootstrap": func() error {
					_, err := remote.Bootstrap(context.Background())
					return err
				},
				"register": func() error {
					_, err := remote.RegisterDevice(context.Background(), "test-device", managerKey(1))
					return err
				},
				"configuration": func() error {
					_, err := remote.Configuration(context.Background(), "device-1")
					return err
				},
				"watch_configuration": func() error {
					_, err := remote.WatchConfiguration(context.Background(), "device-1", "cursor")
					return err
				},
				"local_probe": func() error {
					_, err := remote.LocalProbeConfiguration(context.Background(), "device-1")
					return err
				},
				"websocket": func() error {
					_, err := dialRelay(context.Background(), target, func() ([]Cookie, error) { return nil, nil })
					return err
				},
			} {
				t.Run(name, func(t *testing.T) {
					err := call()
					if err == nil {
						t.Fatal("failure was accepted")
					}
					failure := model.PublicError(err)
					expectedCode, expectedRetry := test.code, test.retryable
					if name == "bootstrap" && test.status == 404 && (test.code == model.ErrorUnavailable || test.code == model.ErrorNotFound) {
						expectedCode, expectedRetry = model.ErrorServerUnavailable, false
					}
					if failure.Code != expectedCode || failure.Retryable != expectedRetry ||
						failure.HTTPStatus != test.status || failure.RequestID != "gateway-request" ||
						failure.Operation == "" {
						t.Fatalf("failure = %#v", failure)
					}
					encoded, _ := json.Marshal(failure)
					if strings.Contains(string(encoded), "gateway-secret") {
						t.Fatalf("gateway body leaked: %s", encoded)
					}
				})
			}
		})
	}
}

func TestGatewayNotFoundRequiresApplicationPath(t *testing.T) {
	for _, target := range []string{
		"", "no-url", "/api/v1/devices/device-1/config", "/app/fncpn-other/relay/v1/wireguard",
		"/other/app/fncpn/relay/v1/wireguard",
	} {
		t.Run(target, func(t *testing.T) {
			response := &http.Response{
				StatusCode: http.StatusNotFound,
				Body:       io.NopCloser(strings.NewReader("Not Found")),
				Header:     make(http.Header),
			}
			switch target {
			case "":
			case "no-url":
				response.Request = &http.Request{}
			default:
				response.Request = httptest.NewRequest(http.MethodGet, target, nil)
			}
			failure := HTTPResponseError(response, "http.response", nil)
			if failure.Code != model.ErrorNotFound || failure.Retryable {
				t.Fatalf("unrelated 404 was treated as gateway downtime: %#v", failure)
			}
		})
	}
}

func TestManagerRecoversRelayAfterGatewayDowntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var attempts atomic.Int32
	stopInitial := make(chan struct{})
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-FnCPN-Version", version.Current)
		attempt := attempts.Add(1)
		switch attempt {
		case 2:
			http.Error(w, "Not Found", http.StatusNotFound)
			return
		case 3:
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			return
		}
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		if attempt == 1 {
			select {
			case <-stopInitial:
			case <-ctx.Done():
			}
			return
		}
		for {
			kind, payload, err := connection.Read(ctx)
			if err != nil {
				return
			}
			if err := connection.Write(ctx, kind, payload); err != nil {
				return
			}
		}
	}))
	defer gateway.Close()
	// Stop handlers before httptest waits for open WebSocket sessions.
	defer cancel()
	var output diagnosticBuffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	bridge := &RelayBridge{Logger: logger}
	defer bridge.Stop()
	endpoint, err := bridge.Start(ctx,
		"ws"+strings.TrimPrefix(gateway.URL, "http")+gatewayApplicationPath+"/relay/v1/wireguard",
		func() ([]Cookie, error) { return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	peer, err := net.Dial("udp", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err := bridge.BindPeer(uint16(peer.LocalAddr().(*net.UDPAddr).Port)); err != nil {
		t.Fatal(err)
	}
	store := configuredManagerStore(t)
	saved, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	privileged := &fakePrivilegedNetwork{
		status: model.ClientPrivilegedStatus{Active: true, Interface: "utun-test"},
	}
	manager, err := NewManager(ManagerOptions{
		Store: store, Discoverer: fakeDiscoverer{}, Privileged: privileged,
		Bridge: bridge, Probe: fakeLocalProbe{}, Logger: logger,
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.setStatus(model.ClientRelay, "fn-connect", "utun-test", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	close(stopInitial)

	for {
		status := manager.Status()
		if status.State != model.ClientRelay ||
			status.LastError != nil && !status.LastError.Retryable {
			t.Fatalf("gateway downtime terminated the connection: %+v, error=%#v", status, status.LastError)
		}
		if attempts.Load() == 4 && bridge.Connected() && status.LastError == nil &&
			strings.Contains(output.String(), `"msg":"relay WebSocket reconnected"`) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("relay did not recover automatically: %s", output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := peer.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	const payload = "datagram after automatic recovery"
	if _, err := peer.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 128)
	n, err := peer.Read(buffer)
	if err != nil || string(buffer[:n]) != payload {
		t.Fatalf("recovered relay cannot forward datagrams: %q, %v", buffer[:n], err)
	}
	cancel()
	<-done
	if privileged.removed != 0 || len(privileged.plans) != 0 || !privileged.status.Active {
		t.Fatalf("temporary downtime changed the privileged network: %+v", privileged)
	}
	current, err := store.Load()
	if err != nil || current == nil || current.DeviceID != saved.DeviceID ||
		current.PublicKey != saved.PublicKey || !current.AutoConnect {
		t.Fatalf("recovery changed the saved identity: %+v, %v", current, err)
	}
	for _, entry := range []string{`"http_status":404`, `"http_status":502`, `"msg":"client maintenance recovered"`} {
		if !strings.Contains(output.String(), entry) {
			t.Fatalf("missing recovery diagnostic %s: %s", entry, output.String())
		}
	}
}

func TestManagerRetriesConfigurationAfterGatewayDowntime(t *testing.T) {
	var attempts atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-FnCPN-Version", version.Current)
		if cookie, err := r.Cookie("fnos-token"); err != nil || cookie.Value != "recovery-cookie" {
			t.Error("configuration retry did not reuse its cookie")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == gatewayApplicationPath+"/version" {
			_ = json.NewEncoder(w).Encode(map[string]string{"serverVersion": version.Current})
			return
		}
		if r.URL.Path != gatewayApplicationPath+"/api/v1/devices/device-1/config" ||
			r.URL.Query().Get("watch") != "1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		switch attempts.Add(1) {
		case 1:
			http.Error(w, "Not Found", http.StatusNotFound)
		case 2:
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
		case 3:
			_ = json.NewEncoder(w).Encode(ConfigurationWatchResult{Cursor: "restored"})
		default:
			<-r.Context().Done()
		}
	}))
	defer gateway.Close()
	origin, _ := url.Parse(gateway.URL)
	store := configuredManagerStore(t)
	if err := store.SaveCookies("home-nas", []Cookie{{
		Name: "fnos-token", Value: "recovery-cookie", Domain: origin.Hostname(), Path: "/",
	}}); err != nil {
		t.Fatal(err)
	}
	var output diagnosticBuffer
	privileged := &fakePrivilegedNetwork{
		status: model.ClientPrivilegedStatus{Active: true, Interface: "utun-test"},
	}
	manager, err := NewManager(ManagerOptions{
		Store: store, Discoverer: fakeDiscoverer{}, Privileged: privileged,
		Bridge: &fakeBridge{}, Probe: fakeLocalProbe{},
		Logger: slog.New(slog.NewJSONHandler(&output, nil)),
		Remote: func(_ string, cookies []Cookie, save func([]Cookie) error) (RemoteService, error) {
			return NewRemoteClient(gateway.URL+gatewayApplicationPath, cookies, gateway.Client(), save)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.setStatus(model.ClientRelay, "fn-connect", "utun-test", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.watchConfiguration(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	for {
		status := manager.Status()
		if status.State != model.ClientRelay ||
			status.LastError != nil && !status.LastError.Retryable {
			t.Fatalf("configuration downtime became permanent: %+v, error=%#v; logs=%s", status, status.LastError, output.String())
		}
		if attempts.Load() >= 3 && status.LastError == nil &&
			strings.Contains(output.String(), `"msg":"client maintenance recovered"`) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("configuration watch did not recover: %s", output.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if privileged.removed != 0 || len(privileged.plans) != 0 || !privileged.status.Active {
		t.Fatalf("configuration downtime changed the privileged network: %+v", privileged)
	}
	for _, entry := range []string{`"http_status":404`, `"http_status":502`, `"phase":"watch_configuration"`} {
		if !strings.Contains(output.String(), entry) {
			t.Fatalf("missing configuration diagnostic %s: %s", entry, output.String())
		}
	}
}
