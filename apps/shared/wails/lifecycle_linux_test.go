//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxExplicitQuitStopsServiceAfterStartupIsCancelled(t *testing.T) {
	directory := t.TempDir()
	record := filepath.Join(directory, "arguments")
	t.Setenv("FNCPN_QUIT_TEST_RECORD", record)
	t.Setenv("PATH", directory)
	// Exercise the real command boundary without stopping the host's service.
	if err := os.WriteFile(filepath.Join(directory, "systemctl"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FNCPN_QUIT_TEST_RECORD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	cancelled := false
	app := &App{startupDone: done, startupCancel: func() { cancelled = true; close(done) }}
	if err := app.preparePlatformQuit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("pending startup was not cancelled")
	}
	arguments, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(arguments) != "--user\nstop\nfncpn-client.service\n" {
		t.Fatalf("unexpected stop command: %q", arguments)
	}
}
