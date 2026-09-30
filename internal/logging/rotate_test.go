package logging

import (
	"bytes"
	"errors"
	"io"
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
	if got := Redact("read privateKey failed"); !strings.Contains(got, "redacted") {
		t.Fatalf("redacted value = %q", got)
	}
	if got := Redact("permission denied"); got != "permission denied" {
		t.Fatalf("ordinary value changed: %q", got)
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
