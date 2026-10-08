package packaging

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func TestVersionTemplatesAreCurrent(t *testing.T) {
	if output, err := exec.Command("python3", "../scripts/sync-version.py", "--check").CombinedOutput(); err != nil {
		t.Fatalf("version templates: %v\n%s", err, output)
	}
	data, err := os.ReadFile("../platform/macos/FnCPNApp.swift")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("private let ipcProtocolVersion = %d\n", ipc.ProtocolVersion)) {
		t.Fatal("Swift and Go local IPC versions differ")
	}
}
