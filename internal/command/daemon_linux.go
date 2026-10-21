//go:build linux

package command

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"time"
)

func ensureClientDaemon(ctx context.Context, socket string) error {
	connection, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
	if err == nil {
		connection.Close()
		return nil
	}
	// Never start the installed service for an explicit custom socket used by tests/administrators.
	if socket != defaultClientSocket() {
		return nil
	}
	start, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(start, "systemctl", "--user", "start", "fncpn-client.service").CombinedOutput(); err != nil {
		return fmt.Errorf("start client service (run fncpn-linux-setup first): %s: %w", output, err)
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		connection, err = net.DialTimeout("unix", socket, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			return nil
		}
		select {
		case <-start.Done():
			return fmt.Errorf("client service socket did not become ready: %w", start.Err())
		case <-tick.C:
		}
	}
}
func runStopAllClientDaemons() error {
	return fmt.Errorf("stop Linux client services with systemctl --user stop fncpn-client")
}
