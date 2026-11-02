package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsUIIsLocalAuthenticatedAndIndependentOfNAS(t *testing.T) {
	m := &Manager{store: NewConfigStore(filepath.Join(t.TempDir(), "client.json"), nil)}
	address, err := m.SettingsURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer m.closeSettingsUI()
	u, _ := url.Parse(address)
	token := u.Query().Get("fncpn_proxy")
	if u.Hostname() != "127.0.0.1" || len(token) != 64 {
		t.Fatal("unsafe bootstrap", u.Hostname())
	}
	client := &http.Client{}
	response, err := client.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(string(data), "网络链路测试") || len(response.Cookies()) != 1 || !response.Cookies()[0].HttpOnly {
		t.Fatal("bootstrap failed")
	}
	for _, test := range []struct {
		name, path, auth, origin, host string
		code                           int
	}{
		{name: "no token", path: "/api/state", code: 403},
		{name: "bad token", path: "/api/state", auth: "wrong", code: 403},
		{name: "foreign origin", path: "/api/state", auth: token, origin: "https://example.com", code: 403},
		{name: "DNS rebinding", path: "/api/state", auth: token, host: "evil.example", code: 403},
		{name: "unknown method", path: "/api/shutdown", auth: token, code: 404},
		{name: "state without NAS", path: "/api/state", auth: token, code: 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := http.NewRequest("GET", "http://"+u.Host+settingsPath+test.path, nil)
			request.Header.Set("Authorization", "Bearer "+test.auth)
			request.Header.Set("Origin", test.origin)
			if test.host != "" {
				request.Host = test.host
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.code {
				t.Fatalf("got %d, want %d", response.StatusCode, test.code)
			}
		})
	}
	again, err := m.SettingsURL(context.Background())
	if err != nil || again != address {
		t.Fatal("listener was not reused")
	}
	m.closeSettingsUI()
	if _, err := m.SettingsURL(context.Background()); err == nil {
		t.Fatal("reopened after close")
	}
}
func TestSettingsUIRejectsInvalidChangesAndStreamsTerminalFailure(t *testing.T) {
	m := &Manager{store: NewConfigStore(filepath.Join(t.TempDir(), "client.json"), nil)}
	ui := &settingsUI{address: "127.0.0.1:1234", token: strings.Repeat("a", 64)}
	for _, test := range []struct {
		path, body string
		code       int
	}{
		{"/api/preferences", `{"fnId":"other","links":[{"link":"local","enabled":false}]}`, 400},
		{"/api/preferences", `{} {}`, 400},
		{"/api/preferences", `{"extra":true}`, 400},
		{"/api/test", `{"link":"all"}`, 200},
	} {
		request := httptest.NewRequest("POST", "http://"+ui.address+settingsPath+test.path, strings.NewReader(test.body))
		request.Header.Set("Authorization", "Bearer "+ui.token)
		recorder := httptest.NewRecorder()
		ui.serve(m, recorder, request)
		if recorder.Code != test.code {
			t.Fatalf("%s: %d: %s", test.path, recorder.Code, recorder.Body.String())
		}
		if test.path == "/api/test" {
			var final SpeedTestStatus
			if err := json.Unmarshal(recorder.Body.Bytes(), &final); err != nil {
				t.Fatal(err)
			}
			if final.Active || final.Error == "" {
				t.Fatal("missing terminal error")
			}
		}
	}
}

type settingsStreamFixture struct {
	settingsInfoBackend
	canceled chan struct{}
}

func (f settingsStreamFixture) RunSpeedTest(ctx context.Context, _ SpeedTestRequest, emit func(SpeedTestStatus) error) error {
	if err := emit(SpeedTestStatus{Active: true, Stage: "latency"}); err != nil {
		return err
	}
	<-ctx.Done()
	close(f.canceled)
	return ctx.Err()
}
func TestSettingsStreamCancellationEndsTheRequest(t *testing.T) {
	fixture := settingsStreamFixture{canceled: make(chan struct{})}
	ui, err := startSettingsUI(fixture)
	if err != nil {
		t.Fatal(err)
	}
	defer ui.server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", "http://"+ui.address+settingsPath+"/api/test", strings.NewReader(`{"link":"all"}`))
	request.Header.Set("Authorization", "Bearer "+ui.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var status SpeedTestStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || !status.Active {
		t.Fatal("progress did not flush", err)
	}
	cancel()
	response.Body.Close()
	select {
	case <-fixture.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("closing the view did not cancel the measurement")
	}
}
func TestSettingsInfoPageDoesNotRequireVPN(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address, err := NewSettingsInfoPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(address)
	request, _ := http.NewRequest("GET", "http://"+u.Host+settingsPath+"/api/state", nil)
	request.Header.Set("Authorization", "Bearer "+u.Query().Get("fncpn_proxy"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state struct {
		InfoOnly bool         `json:"infoOnly"`
		Settings LinkSettings `json:"settings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	if !state.InfoOnly || state.Settings.FNID != "" {
		t.Fatal("offline page exposed a connection runtime")
	}
}
