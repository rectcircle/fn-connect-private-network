//go:build windows

package main

import (
	"bytes"
	"context"
	"github.com/rectcircle/fn-connect-private-network/internal/windowsinstall"
	"testing"
)

func TestInstallerRejectsOtherCommandsAndArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"client"}, {"server"}, {"stop-daemons", "--anything"}, {"cleanup-network", "--state-dir", "C:\\other"}} {
		var output bytes.Buffer
		if code := run(context.Background(), args, &output); code != 2 {
			t.Fatalf("%v: code %d, output %s", args, code, output.String())
		}
	}
}

func TestInstallerOperationsRequireLocalSystem(t *testing.T) {
	if windowsinstall.IsLocalSystem() {
		t.Skip("do not execute maintenance operations on a SYSTEM test host")
	}
	for _, operation := range []string{"stop-daemons", "cleanup-network"} {
		var output bytes.Buffer
		if code := run(context.Background(), []string{operation}, &output); code != 1 || !bytes.Contains(output.Bytes(), []byte("LocalSystem")) {
			t.Fatalf("%s: code %d, output %s", operation, code, output.String())
		}
	}
}
