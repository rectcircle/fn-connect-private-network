//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Fixed at build time for immutable package layouts such as the Nix store.
var linuxEnableClientPath = "/usr/libexec/fncpn/enable-client"

func uiSocketPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(dir) {
		dir = filepath.Join("/run/user", strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(dir, "fncpn/client.sock")
}
func (a *App) startPlatformDaemon() error {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	socket := filepath.Join("/run/fncpn", strconv.Itoa(os.Getuid()), "privileged.sock")
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err == nil {
		conn.Close()
	} else {
		output, err := exec.CommandContext(ctx, "pkexec", linuxEnableClientPath, strconv.Itoa(os.Getuid())).CombinedOutput()
		if err != nil {
			return fmt.Errorf("启动特权服务需要管理员授权；也可运行 fncpn-linux-setup：%s: %w", output, err)
		}
	}
	output, err := exec.CommandContext(ctx, "systemctl", "--user", "start", "fncpn-client.service").CombinedOutput()
	if err != nil {
		return fmt.Errorf("启动用户服务：%s: %w", output, err)
	}
	return nil
}

// Linux Core is an independent systemd user service. Closing the UI does not
// change the user's persisted connection intent or stop headless operation.
func (a *App) stopPlatformDaemon() {}
func uiOpenLogs() error {
	directory := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(directory) {
		home, _ := os.UserHomeDir()
		directory = filepath.Join(home, ".local/state")
	}
	return exec.Command("xdg-open", filepath.Join(directory, "fncpn")).Start()
}
