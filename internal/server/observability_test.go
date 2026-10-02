package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestRegistrationIdentityAndInfoLogs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	directory := t.TempDir()
	service, err := OpenService(directory, testNetwork{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(service, nil)
	var secondKey [32]byte
	secondKey[0] = 2
	keys := []string{apiTestPublicKey(), base64.StdEncoding.EncodeToString(secondKey[:])}
	register := func(key string, wantStatus int) model.DeviceRegistration {
		t.Helper()
		body, err := json.Marshal(map[string]string{"name": "macos.shared", "publicKey": key})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/devices", bytes.NewReader(body))
		request.Header.Set("X-Trim-Userid", "admin")
		request.Header.Set("X-Trim-Isadmin", "true")
		request.Header.Set("Cookie", "session=server-cookie-canary")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("register status = %d: %s", response.Code, response.Body.String())
		}
		var result model.DeviceRegistration
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := register(keys[0], http.StatusCreated)
	reused := register(keys[0], http.StatusOK)
	if first.Device.ID != reused.Device.ID || first.Device.OverlayAddress != reused.Device.OverlayAddress ||
		len(service.Snapshot().Devices) != 1 {
		t.Fatal("same public key created a duplicate")
	}
	second := register(keys[1], http.StatusCreated)
	if first.Device.ID == second.Device.ID || first.Device.OverlayAddress == second.Device.OverlayAddress ||
		len(service.Snapshot().Devices) != 2 {
		t.Fatal("different public keys with the same hostname were merged")
	}
	service, err = OpenService(directory, testNetwork{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	handler = NewHTTPHandler(service, nil)
	reused = register(keys[0], http.StatusOK)
	if reused.Device.ID != first.Device.ID || len(service.Snapshot().Devices) != 2 {
		t.Fatal("server restart lost registration idempotency")
	}
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/v1/bootstrap", nil)
	bootstrap.Header.Set("X-Trim-Userid", "admin")
	bootstrap.Header.Set("X-Trim-Isadmin", "true")
	handler.ServeHTTP(httptest.NewRecorder(), bootstrap)

	beforePoll := output.Len()
	for range 5 {
		if err := service.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/snapshot", nil)
		request.Header = bootstrap.Header.Clone()
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	if output.Len() != beforePoll {
		t.Fatal("unchanged reconcile/admin polling emitted INFO logs")
	}
	for _, secret := range append(keys, "server-cookie-canary") {
		if strings.Contains(output.String(), secret) {
			t.Fatal("registration logs exposed public key or cookie material")
		}
	}
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	var created []bool
	bootstrapLogged := false
	for {
		var event map[string]any
		if err := decoder.Decode(&event); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event["msg"] == "device registration completed" {
			if event["level"] != "INFO" || event["http_request_id"] == nil || event["http_request_id"] == "" ||
				event["device_id"] == nil || event["address"] == nil {
				t.Fatalf("registration context missing: %+v", event)
			}
			value, ok := event["created"].(bool)
			if !ok {
				t.Fatal("registration did not distinguish creation from reuse")
			}
			created = append(created, value)
		}
		if event["msg"] == "bootstrap authorization checked" {
			bootstrapLogged = event["level"] == "INFO" && event["administrator"] == true && event["http_request_id"] != nil
		}
	}
	if len(created) != 4 || !created[0] || created[1] || !created[2] || created[3] || !bootstrapLogged {
		t.Fatalf("unexpected registration/bootstrap milestones: created=%v bootstrap=%v\n%s",
			created, bootstrapLogged, output.String())
	}
}
