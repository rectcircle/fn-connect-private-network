package logging

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingWriterLimitsFilesAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs", "fncpn.log")
	writer, err := Open(path, 8, 2)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for _, value := range []string{"first\n", "second\n", "third\n"} {
		if _, err := writer.Write([]byte(value)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o", candidate, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}

func TestRedactSensitiveErrors(t *testing.T) {
	if got := Redact("read privateKey failed"); got != "read privateKey failed" {
		t.Fatalf("harmless description was redacted: %q", got)
	}
	if got := Redact("privateKey=canary-secret"); strings.Contains(got, "canary-secret") {
		t.Fatalf("secret was retained: %q", got)
	}
	if got := Redact("permission denied"); got != "permission denied" {
		t.Fatalf("ordinary value changed: %q", got)
	}
}

func TestRedactErrorPreservesRequestFailureWithoutURLSecrets(t *testing.T) {
	const address = "https://account:password@nas.example/app/fncpn/api/v1/bootstrap?entry-token=secret#fragment-secret"
	cause := &url.Error{Op: "Get", URL: address, Err: io.EOF}
	got := RedactError(fmt.Errorf("request failed: %w", cause))
	if got != `request failed: Get "https://nas.example/app/fncpn/api/v1/bootstrap": EOF` {
		t.Fatalf("diagnostic = %q", got)
	}
	if cause.URL != address || !errors.Is(cause, io.EOF) {
		t.Fatal("redaction mutated the original error")
	}
}

func TestRedactErrorFiltersNestedAndSensitiveCauses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", want: ""},
		{name: "plain", err: io.EOF, want: "EOF"},
		{
			name: "sensitive body",
			err:  errors.New("Cookie: session=private-value"),
			want: "[redacted sensitive error]",
		},
		{
			name: "malformed URL",
			err:  &url.Error{Op: "Get", URL: "https://user:secret%ZZ@nas.example/", Err: io.EOF},
			want: `Get "[redacted URL]": EOF`,
		},
		{
			name: "nested redirect",
			err: &url.Error{
				Op: "Get", URL: "https://nas.example/app/fncpn?token=old",
				Err: &url.Error{
					Op: "Get", URL: "https://user:secret@gateway.example/login?token=new",
					Err: errors.New("stopped after 10 redirects"),
				},
			},
			want: `Get "https://nas.example/app/fncpn": Get "https://gateway.example/login": stopped after 10 redirects`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := RedactError(test.err); got != test.want {
				t.Fatalf("diagnostic = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRotatingWriterKeepsWritingAfterRotationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fncpn.log")
	writer, err := Open(path, 4, 2)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	defer writer.Close()
	if _, err := writer.Write([]byte("one\n")); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	blockedBackup := path + ".2"
	if err := os.Mkdir(blockedBackup, 0o700); err != nil {
		t.Fatalf("create blocked backup: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(blockedBackup, "child"),
		[]byte("x"),
		0o600,
	); err != nil {
		t.Fatalf("populate blocked backup: %v", err)
	}
	count, err := writer.Write([]byte("two\n"))
	if err == nil || count != len("two\n") {
		t.Fatalf("rotation failure write count=%d err=%v", count, err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read active log: %v", readErr)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("active log = %q", data)
	}
}

func TestMultiWriterWritesFallbackBeforeFileFailure(t *testing.T) {
	var fallback bytes.Buffer
	writer := MultiWriter(failingWriter{}, &fallback)
	if _, err := writer.Write([]byte("message")); err == nil {
		t.Fatal("file failure was not returned")
	}
	if fallback.String() != "message" {
		t.Fatalf("fallback = %q", fallback.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

var _ io.Writer = failingWriter{}
