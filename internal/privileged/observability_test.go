package privileged

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityMutationInfoLogsDoNotExposeCredentials(t *testing.T) {
	var output bytes.Buffer
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSecretStore(filepath.Join(directory, "credentials"),
		slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	input := SecretRequest{Service: WireGuardSecret, Account: "home-nas", Value: []byte("identity-value-canary")}
	for range 2 {
		if response := secretCall(t, store, 501, MethodPutSecret, input); !response.OK {
			t.Fatal(response.Error)
		}
	}
	beforeReads := output.Len()
	for range 5 {
		read := input
		read.Value = nil
		if response := secretCall(t, store, 501, MethodGetSecret, read); !response.OK {
			t.Fatal(response.Error)
		}
		cookie := SecretRequest{Service: CookieSecret, Account: "home-nas", Value: []byte("cookie-value-canary")}
		if response := secretCall(t, store, 501, MethodPutSecret, cookie); !response.OK {
			t.Fatal(response.Error)
		}
	}
	if output.Len() != beforeReads {
		t.Fatal("credential reads or cookie refreshes emitted INFO logs")
	}
	input.Value = nil
	for range 2 {
		if response := secretCall(t, store, 501, MethodDeleteSecret, input); !response.OK {
			t.Fatal(response.Error)
		}
	}
	decoder := json.NewDecoder(&output)
	for index, message := range []string{
		"client WireGuard identity stored", "client WireGuard identity stored", "client WireGuard identity removed",
	} {
		var event map[string]any
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event["level"] != "INFO" || event["msg"] != message || event["fn_id"] != "home-nas" || event["uid"] != float64(501) {
			t.Fatalf("identity mutation context missing: %+v", event)
		}
		if index < 2 && event["replaced"] != (index == 1) {
			t.Fatalf("identity creation/replacement indistinguishable: %+v", event)
		}
		encoded, err := json.Marshal(event)
		if err != nil || strings.Contains(string(encoded), "value-canary") {
			t.Fatal("credential content reached INFO logs")
		}
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected extra identity event: %+v (%v)", extra, err)
	}
}
