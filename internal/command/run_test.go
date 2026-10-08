package command

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerStateOpenFailureIsWrittenToProcessLog(t *testing.T) {
	directory := t.TempDir()
	stateDirectory := filepath.Join(directory, "state")
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		t.Fatalf("create state directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(stateDirectory, "settings.json"),
		[]byte("{broken"),
		0o600,
	); err != nil {
		t.Fatalf("write invalid settings: %v", err)
	}
	logPath := filepath.Join(directory, "logs", "server.log")
	err := runServerDaemon(
		context.Background(),
		[]string{
			"--socket", filepath.Join(directory, "app.sock"),
			"--state-dir", stateDirectory,
			"--privileged-socket", filepath.Join(directory, "privileged.sock"),
			"--log-file", logPath,
		},
		Environment{Stderr: io.Discard},
	)
	if err == nil {
		t.Fatal("invalid state unexpectedly opened")
	}
	data, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read process log: %v", readErr)
	}
	logLine := string(data)
	if !strings.Contains(logLine, `"msg":"open server state"`) ||
		!strings.Contains(logLine, `"phase":"open_state"`) ||
		!strings.Contains(logLine, "unsupported development data settings.json") {
		t.Fatalf("process log does not contain startup cause: %s", logLine)
	}
}

func TestPurgeUserRemovesDataAndLogDirectories(t *testing.T) {
	directory := t.TempDir()
	dataDirectory := filepath.Join(directory, "data")
	logDirectory := filepath.Join(directory, "logs")
	if err := os.MkdirAll(filepath.Join(dataDirectory, "run"), 0o700); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dataDirectory, "run", "client.sock"),
		nil,
		0o600,
	); err != nil {
		t.Fatalf("create stale socket: %v", err)
	}
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatalf("create log directory: %v", err)
	}

	err := runPurgeUser([]string{
		"--data-dir", dataDirectory,
		"--config", filepath.Join(dataDirectory, "config.json"),
		"--log-dir", logDirectory,
	}, Environment{Stderr: io.Discard})
	if err != nil {
		t.Fatalf("purge user: %v", err)
	}
	if _, err := os.Stat(dataDirectory); !os.IsNotExist(err) {
		t.Fatalf("data directory still exists: %v", err)
	}
	if _, err := os.Stat(logDirectory); !os.IsNotExist(err) {
		t.Fatalf("log directory still exists: %v", err)
	}
}
