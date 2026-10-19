//go:build windows

package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Invoked by scripts/windows/test-maintenance-compatibility.ps1 with binaries
// built from real historical source tags and a future-business-IPC mutation.
func TestMaintenanceHistoricalProcesses(t *testing.T) {
	manifest := os.Getenv("FNCPN_MAINTENANCE_FIXTURES")
	if manifest == "" {
		t.Skip("run scripts/windows/test-maintenance-compatibility.ps1 for historical source validation")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name string
		Path string
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("empty historical fixture list")
	}
	for _, fixture := range fixtures {
		for _, mode := range []string{"allow", "refuse", "deny"} {
			t.Run(fixture.Name+"/"+mode, func(t *testing.T) {
				socket := fmt.Sprintf(`\\.\pipe\FnCPN.Maintenance.History.%d.%d`, os.Getpid(), time.Now().UnixNano())
				processCtx, kill := context.WithTimeout(context.Background(), 10*time.Second)
				defer kill()
				command := exec.CommandContext(processCtx, fixture.Path, socket, mode)
				var stderr bytes.Buffer
				command.Stderr = &stderr
				stdout, err := command.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := command.Start(); err != nil {
					t.Fatal(err)
				}
				exited := make(chan error, 1)
				go func() { exited <- command.Wait() }()
				defer func() {
					kill()
					select {
					case <-exited:
					case <-time.After(3 * time.Second):
						t.Error("fixture failed to exit")
					}
				}()
				ready := make(chan bool, 1)
				go func() { scanner := bufio.NewScanner(stdout); ready <- scanner.Scan() && scanner.Text() == "ready" }()
				select {
				case ok := <-ready:
					if !ok {
						t.Fatal("fixture did not become ready")
					}
				case <-processCtx.Done():
					t.Fatal("fixture startup timed out")
				}
				if strings.Contains(fixture.Name, "future") && ProtocolVersion != 99 && mode != "deny" {
					// Actual future server rejects the current business client, while the
					// exact same maintenance client binary can still stop that process.
					err := (Client{SocketPath: socket, Timeout: time.Second}).Call(processCtx, "status", nil, nil)
					if err == nil || (model.AsError(err).Code != model.ErrorFailedPrecondition && model.AsError(err).Code != model.ErrorProtocol) {
						t.Fatalf("expected business version rejection, got %v", err)
					}
				}
				ctx, cancel := context.WithTimeout(processCtx, 3*time.Second)
				defer cancel()
				err = StopDaemonForMaintenance(ctx, socket)
				if mode == "allow" && err != nil {
					t.Fatal(err)
				}
				if mode != "allow" && err == nil {
					t.Fatal("refusal/permission denial hidden")
				}
				if mode == "allow" {
					select {
					case err := <-exited:
						exited <- err
						if err != nil {
							t.Fatalf("fixture exit: %v; %s", err, stderr.String())
						}
					case <-ctx.Done():
						t.Fatal("daemon did not exit")
					}
				}
			})
		}
	}
}
