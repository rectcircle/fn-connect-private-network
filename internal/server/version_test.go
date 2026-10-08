package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestVersionEndpointIsIndependentOfBusinessState(t *testing.T) {
	service, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, nil)
	request := httptest.NewRequest(http.MethodGet, "/version", nil)
	request.Header.Set("X-Trim-Userid", "user")
	request.Header.Set("X-FnCPN-Client-Version", "99.0.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var identity struct {
		ServerVersion string `json:"serverVersion"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &identity); err != nil || identity.ServerVersion != version.Current || response.Code != 200 {
		t.Fatalf("version: %d %s %v", response.Code, response.Body, err)
	}
	if response.Header().Get("X-FnCPN-Version") != version.Current {
		t.Fatal("missing response version")
	}
}

func TestIncompatibleClientCannotRegisterDevice(t *testing.T) {
	service, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/devices", nil)
	request.Header.Set("X-Trim-Userid", "user")
	request.Header.Set("X-FnCPN-Client-Version", "99.0.0")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 || len(service.Snapshot().Devices) != 0 {
		t.Fatalf("registration was admitted: %d", response.Code)
	}
	if response.Header().Get("X-FnCPN-Version") != version.Current {
		t.Fatal("missing version on rejection")
	}
}
