package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestRemoteClientRegistersAndRefreshesCookies(t *testing.T) {
	var saved []Cookie
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != gatewayApplicationPath+"/api/v1/devices" {
			t.Errorf("path = %q", request.URL.Path)
		}
		cookie, err := request.Cookie("fnos-token")
		if err != nil || cookie.Value != "old-token" {
			t.Errorf("cookie = %#v, err=%v", cookie, err)
		}
		http.SetCookie(writer, &http.Cookie{
			Name:     "fnos-token",
			Value:    "new-token",
			Path:     "/",
			HttpOnly: true,
		})
		_ = json.NewEncoder(writer).Encode(model.DeviceRegistration{
			Device: model.Device{ID: "device-1", Enabled: true},
			Configuration: model.ClientConfiguration{
				DeviceID:      "device-1",
				ClientAddress: "10.203.0.2/32",
			},
		})
	}))
	defer server.Close()

	client, err := NewRemoteClient(
		server.URL+gatewayApplicationPath,
		[]Cookie{{
			Name:   "fnos-token",
			Value:  "old-token",
			Domain: strings.Split(strings.TrimPrefix(server.URL, "http://"), ":")[0],
			Path:   "/",
		}},
		server.Client(),
		func(cookies []Cookie) error {
			saved = cookies
			return nil
		},
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	registration, err := client.RegisterDevice(
		context.Background(),
		"MacBook",
		"public-key",
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registration.Device.ID != "device-1" {
		t.Fatalf("registration = %+v", registration)
	}
	if len(saved) != 1 || saved[0].Value != "new-token" {
		t.Fatalf("saved cookies = %+v", saved)
	}
}

func TestRemoteClientMapsAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, "invalid token", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, err := NewRemoteClient(server.URL, nil, server.Client(), nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	_, err = client.Bootstrap(context.Background())
	if err == nil || model.AsError(err).Code != model.ErrorAuthRequired {
		t.Fatalf("bootstrap error = %v", err)
	}
}

func TestRemoteClientLoadsLocalProbeConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path !=
			gatewayApplicationPath+"/api/v1/devices/device-1/local-probe" {
			t.Errorf("path = %q", request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(model.LocalProbeConfiguration{
			Endpoints: []string{"192.168.1.10:54790"},
			Key:       "probe-key",
		})
	}))
	defer server.Close()
	client, err := NewRemoteClient(
		server.URL+gatewayApplicationPath,
		nil,
		server.Client(),
		nil,
	)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	configuration, err := client.LocalProbeConfiguration(
		context.Background(),
		"device-1",
	)
	if err != nil {
		t.Fatalf("local probe configuration: %v", err)
	}
	if len(configuration.Endpoints) != 1 ||
		configuration.Endpoints[0] != "192.168.1.10:54790" ||
		configuration.Key != "probe-key" {
		t.Fatalf("local probe configuration = %+v", configuration)
	}
}

func TestRemoteClientWatchesConfigurationWithCursor(t *testing.T) {
	const cursor = "configuration-1"
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Query().Get("watch") != "1" {
			t.Errorf("watch = %q", request.URL.Query().Get("watch"))
		}
		if request.URL.Query().Get("after") == cursor {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"changed": false,
				"cursor":  cursor,
			})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"changed": true,
			"cursor":  cursor,
			"configuration": model.ClientConfiguration{
				DeviceID:      "device-1",
				ClientAddress: "10.203.0.2/32",
			},
		})
	}))
	defer server.Close()
	client, err := NewRemoteClient(server.URL, nil, server.Client(), nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	first, err := client.WatchConfiguration(
		context.Background(),
		"device-1",
		"",
	)
	if err != nil {
		t.Fatalf("initial watch: %v", err)
	}
	if !first.Changed ||
		first.Cursor != cursor ||
		first.Configuration.DeviceID != "device-1" {
		t.Fatalf("initial watch = %+v", first)
	}
	second, err := client.WatchConfiguration(
		context.Background(),
		"device-1",
		first.Cursor,
	)
	if err != nil {
		t.Fatalf("unchanged watch: %v", err)
	}
	if second.Changed || second.Cursor != cursor {
		t.Fatalf("unchanged watch = %+v", second)
	}
}
