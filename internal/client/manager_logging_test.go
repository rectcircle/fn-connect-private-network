package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
)

func TestManagerReportsGatewayAuthenticationRequired(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "invalid token")
	}))
	defer gateway.Close()
	remote, err := NewRemoteClient(gateway.URL, nil, gateway.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = remote.Configuration(context.Background(), "test-device")
	if err == nil {
		t.Fatal("invalid session was accepted")
	}
	var output bytes.Buffer
	manager := &Manager{
		logger:        slog.New(slog.NewJSONHandler(&output, nil)),
		statusChanges: notify.New(),
	}
	manager.fail(err)
	status := manager.Status()
	if status.State != model.ClientAuthRequired || status.LastError == nil ||
		status.LastError.Code != model.ErrorAuthRequired || status.LastError.HTTPStatus != 200 ||
		status.LastError.Retryable || status.LastError.RequestID != "" ||
		!strings.Contains(status.LastError.Detail, "invalid token") ||
		!strings.Contains(status.LastError.Operation, "/api/v1/devices/test-device/config") {
		t.Fatalf("gateway authentication status = %+v, error = %+v", status, status.LastError)
	}
	var entry struct {
		Code       model.ErrorCode `json:"code"`
		Detail     string          `json:"detail"`
		HTTPStatus int             `json:"http_status"`
	}
	if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Code != model.ErrorAuthRequired || entry.HTTPStatus != 200 ||
		!strings.Contains(entry.Detail, "invalid token") {
		t.Fatalf("gateway authentication log = %+v", entry)
	}
}

func TestManagerLogsRemoteCauseWithoutChangingPublicError(t *testing.T) {
	const failureURL = "https://account:password@gateway.example/bootstrap?entry-token=secret#fragment-secret"
	for _, phase := range []string{"authorize", "watch_configuration"} {
		t.Run(phase, func(t *testing.T) {
			transportErr := &url.Error{
				Op:  "Get",
				URL: failureURL,
				Err: io.EOF,
			}
			remote, err := NewRemoteClient(
				"https://nas.example/app/fncpn",
				nil,
				&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, transportErr
				})},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = remote.Bootstrap(context.Background())
			if !errors.Is(err, io.EOF) {
				t.Fatalf("transport error was lost: %v", err)
			}

			var output bytes.Buffer
			manager := &Manager{
				logger:        slog.New(slog.NewJSONHandler(&output, nil)),
				statusChanges: notify.New(),
				status:        model.ClientStatus{State: model.ClientRelay},
			}
			if phase == "authorize" {
				if returned := manager.fail(err); !errors.Is(returned, err) || model.AsError(returned).Code != model.AsError(err).Code || returned.Error() != err.Error() {
					t.Fatal("logging changed the public error or lost its cause")
				}
			} else {
				manager.recordMaintenanceError(phase, err)
				if manager.Status().State != model.ClientRelay {
					t.Fatal("configuration error interrupted the existing connection")
				}
			}
			var entry struct {
				Level string `json:"level"`
				Code  string `json:"code"`
				Error string `json:"error"`
				Cause string `json:"cause"`
				Phase string `json:"phase"`
			}
			if decodeErr := json.Unmarshal(output.Bytes(), &entry); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if entry.Level != "ERROR" ||
				entry.Code != string(model.ErrorUnavailable) ||
				entry.Error != "server configuration is unavailable" {
				t.Fatalf("unexpected error log: %+v", entry)
			}
			for _, part := range []string{"Get", "nas.example/app/fncpn/api/v1/bootstrap", "EOF"} {
				if !strings.Contains(entry.Cause, part) {
					t.Fatalf("cause %q omits %q", entry.Cause, part)
				}
			}
			if phase == "watch_configuration" && entry.Phase != phase {
				t.Fatalf("phase = %q", entry.Phase)
			}
			for _, secret := range []string{"account", "password", "entry-token", "secret"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("log contains URL secret %q", secret)
				}
			}
			statusJSON, marshalErr := json.Marshal(manager.Status())
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			for _, internal := range []string{"account:password", "entry-token", "secret", `"cause"`} {
				if bytes.Contains(statusJSON, []byte(internal)) {
					t.Fatalf("public status exposes internal diagnostic %q", internal)
				}
			}
			for _, detail := range []string{"EOF", "gateway.example", `"operation"`, `"detail"`} {
				if !bytes.Contains(statusJSON, []byte(detail)) {
					t.Fatalf("diagnostic omits %q: %s", detail, statusJSON)
				}
			}
			if !errors.Is(err, io.EOF) || transportErr.URL != failureURL {
				t.Fatal("diagnostic formatting mutated the error chain")
			}
		})
	}
}

func TestManagerLogsTimeoutCauseWithoutChangingClassification(t *testing.T) {
	var output bytes.Buffer
	manager := &Manager{
		logger:        slog.New(slog.NewJSONHandler(&output, nil)),
		statusChanges: notify.New(),
	}
	err := model.NormalizeError(
		&url.Error{Op: "Get", URL: "https://nas.example/app/fncpn/api/v1/bootstrap", Err: context.DeadlineExceeded},
		model.ErrorUnavailable,
		"server configuration is unavailable",
		true,
	)
	manager.fail(err)
	status := manager.Status()
	if status.State != model.ClientReconnecting ||
		status.LastError.Code != model.ErrorTimeout ||
		!errors.Is(err, context.DeadlineExceeded) ||
		!strings.Contains(output.String(), "context deadline exceeded") {
		t.Fatalf("timeout classification or diagnostic changed: %s", output.String())
	}
}
