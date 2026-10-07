package privileged

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestSecretStorePersistsAndIsolatesCredentials(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "credentials")
	store := openTestSecretStore(t, directory)
	entries := []struct {
		uid     uint32
		service string
		account string
		value   string
	}{
		{501, CookieSecret, "home-nas", "user-one-cookie"},
		{502, CookieSecret, "home-nas", "user-two-cookie"},
		{501, CookieSecret, "other-nas", "other-account"},
		{501, NativeSessionSecret, "home-nas", "native-session"},
		{501, AdminWebSecret, "home-nas", "admin-web-session"},
		{501, WireGuardSecret, "home-nas", "private-key"},
	}
	for _, entry := range entries {
		response := secretCall(t, store, entry.uid, MethodPutSecret, SecretRequest{
			Service: entry.service, Account: entry.account, Value: []byte(entry.value),
		})
		if !response.OK {
			t.Fatalf("put: %+v", response.Error)
		}
	}
	store = openTestSecretStore(t, directory)
	for _, entry := range entries {
		response := secretCall(t, store, entry.uid, MethodGetSecret, SecretRequest{
			Service: entry.service, Account: entry.account,
		})
		result := decodeSecretResult(t, response)
		if !result.Found || string(result.Value) != entry.value {
			t.Fatalf("wrong credential for uid=%d service=%s account=%s", entry.uid, entry.service, entry.account)
		}
	}
	files := 0
	if err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := checkSecretPermissions(info, info.IsDir()); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		if !info.IsDir() {
			files++
			if filepath.Ext(path) != ".secret" {
				t.Errorf("temporary credential file was retained: %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != len(entries) {
		t.Fatalf("credential files = %d", files)
	}
	input := SecretRequest{Service: CookieSecret, Account: "home-nas", Value: []byte("updated")}
	if response := secretCall(t, store, 501, MethodPutSecret, input); !response.OK {
		t.Fatalf("update: %+v", response.Error)
	}
	input.Value = nil
	if result := decodeSecretResult(t, secretCall(t, store, 501, MethodGetSecret, input)); string(result.Value) != "updated" {
		t.Fatal("updated value was not persisted")
	}
	for range 2 {
		if response := secretCall(t, store, 501, MethodDeleteSecret, input); !response.OK {
			t.Fatalf("delete: %+v", response.Error)
		}
	}
	if result := decodeSecretResult(t, secretCall(t, store, 501, MethodGetSecret, input)); result.Found {
		t.Fatal("deleted credential is still present")
	}
	if result := decodeSecretResult(t, secretCall(t, store, 502, MethodGetSecret, input)); string(result.Value) != "user-two-cookie" {
		t.Fatal("delete affected another user")
	}
}

func TestSecretStoreRejectsForgedIdentityAndInvalidInputs(t *testing.T) {
	store := openTestSecretStore(t, filepath.Join(t.TempDir(), "credentials"))
	request, err := ipc.NewRequest("get", MethodGetSecret, SecretRequest{
		Service: CookieSecret, Account: "home-nas",
	})
	if err != nil {
		t.Fatal(err)
	}
	response := store.Handle(context.Background(), request)
	if response.OK || response.Error.Code != model.ErrorPermissionDenied {
		t.Fatalf("missing peer identity was accepted: %+v", response)
	}
	for _, input := range []any{
		map[string]any{"service": CookieSecret, "account": "home-nas", "uid": 502},
		SecretRequest{Service: "unrelated-service", Account: "home-nas", Value: []byte("value")},
		SecretRequest{Service: CookieSecret, Account: "", Value: []byte("value")},
		SecretRequest{Service: CookieSecret, Account: strings.Repeat("a", 64), Value: []byte("value")},
		SecretRequest{Service: CookieSecret, Account: "home-nas"},
		SecretRequest{Service: CookieSecret, Account: "home-nas", Value: make([]byte, maxSecretSize+1)},
	} {
		response := secretCall(t, store, 501, MethodPutSecret, input)
		if response.OK || response.Error.Code != model.ErrorInvalidArgument {
			t.Fatalf("invalid request was accepted: %+v", response)
		}
	}
	entries, err := os.ReadDir(store.directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid request changed storage: %v %v", entries, err)
	}
	server := NewServerService(&fakeServerEngine{})
	if response := server.Handle(context.Background(), request); response.OK {
		t.Fatal("server role exposed client credentials")
	}
}

func TestSecretStoreAccountCannotEscapeDirectory(t *testing.T) {
	directory := t.TempDir()
	store := openTestSecretStore(t, filepath.Join(directory, "credentials"))
	input := SecretRequest{Service: CookieSecret, Account: "../../outside", Value: []byte("value")}
	if response := secretCall(t, store, 501, MethodPutSecret, input); !response.OK {
		t.Fatalf("put opaque account: %+v", response.Error)
	}
	if _, err := os.Stat(filepath.Join(directory, "outside")); !os.IsNotExist(err) {
		t.Fatal("account escaped its directory")
	}
	input.Value = nil
	if result := decodeSecretResult(t, secretCall(t, store, 501, MethodGetSecret, input)); string(result.Value) != "value" {
		t.Fatal("opaque account did not round-trip")
	}
}

func TestSecretStoreRejectsUnsafeFilesAndDoesNotLogValues(t *testing.T) {
	for _, unsafe := range []string{"file-mode", "file-symlink", "directory-mode", "directory-symlink", "oversized"} {
		t.Run(unsafe, func(t *testing.T) {
			var logs bytes.Buffer
			directory := t.TempDir()
			if err := os.Chmod(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			store, err := OpenSecretStore(filepath.Join(directory, "credentials"), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			input := SecretRequest{Service: CookieSecret, Account: "home-nas", Value: []byte("must-not-appear-in-log")}
			if response := secretCall(t, store, 501, MethodPutSecret, input); !response.OK {
				t.Fatalf("put: %+v", response.Error)
			}
			userDirectory := filepath.Join(store.directory, "501")
			files, err := filepath.Glob(filepath.Join(userDirectory, "*.secret"))
			if err != nil || len(files) != 1 {
				t.Fatalf("credential files: %v %v", files, err)
			}
			path := files[0]
			switch unsafe {
			case "file-mode":
				err = os.Chmod(path, 0o644)
			case "file-symlink":
				target := filepath.Join(directory, "target")
				err = os.Rename(path, target)
				if err == nil {
					err = os.Symlink(target, path)
				}
			case "directory-mode":
				err = os.Chmod(userDirectory, 0o755)
			case "directory-symlink":
				target := filepath.Join(directory, "target")
				err = os.Rename(userDirectory, target)
				if err == nil {
					err = os.Symlink(target, userDirectory)
				}
			case "oversized":
				err = os.WriteFile(path, make([]byte, maxSecretSize+1), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			input.Value = nil
			response := secretCall(t, store, 501, MethodGetSecret, input)
			if response.OK || response.Error.Code != model.ErrorUnavailable {
				t.Fatalf("unsafe credential accepted: %+v", response)
			}
			if !strings.Contains(logs.String(), `"level":"ERROR"`) ||
				strings.Contains(logs.String(), "must-not-appear-in-log") ||
				strings.Contains(string(response.Result), "must-not-appear-in-log") {
				t.Fatalf("invalid failure logging: %s", logs.String())
			}
		})
	}
}

func TestSecretStoreRejectsWrongOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat := *info.Sys().(*syscall.Stat_t)
	stat.Uid++
	if err := checkSecretPermissions(secretFileInfo{FileInfo: info, stat: &stat}, false); err == nil {
		t.Fatal("wrong owner was accepted")
	}
}

type secretFileInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (i secretFileInfo) Sys() any { return i.stat }

func openTestSecretStore(t *testing.T, directory string) *SecretStore {
	t.Helper()
	if err := os.Chmod(filepath.Dir(directory), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSecretStore(directory, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func secretCall(t *testing.T, store *SecretStore, uid uint32, method string, input any) ipc.Response {
	t.Helper()
	request, err := ipc.NewRequest("secret", method, input)
	if err != nil {
		t.Fatal(err)
	}
	return store.Handle(ipc.WithPeerIdentity(context.Background(), ipc.PeerIdentity{
		UID: uid, PID: os.Getpid(),
	}), request)
}

func decodeSecretResult(t *testing.T, response ipc.Response) SecretResult {
	t.Helper()
	if !response.OK {
		t.Fatalf("credential operation: %+v", response.Error)
	}
	var result SecretResult
	if err := model.DecodeStrict(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
