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
	ctx, cancel := context.WithTimeout(a.startupContext, 90*time.Second)
	defer cancel()
	socket := filepath.Join("/run/fncpn", strconv.Itoa(os.Getuid()), "privileged.sock")
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err == nil {
		conn.Close()
	} else {
		command := exec.CommandContext(ctx, "pkexec", linuxEnableClientPath, strconv.Itoa(os.Getuid()))
		command.WaitDelay = time.Second
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("启动特权服务需要管理员授权；也可运行 fncpn-linux-setup：%s: %w", output, err)
		}
	}
	command := exec.CommandContext(ctx, "systemctl", "--user", "start", "fncpn-client.service")
	command.WaitDelay = time.Second
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("启动用户服务：%s: %w", output, err)
	}
	return nil
}

// Window-close keeps the UI and headless service alive. Explicit Quit stops
// the user service, whose normal shutdown cleans networking and preserves intent.
func (a *App) stopPlatformDaemon() {}
func (a *App) preparePlatformQuit(ctx context.Context) error {
	if a.smokeResult != "" {
		return nil
	}
	// Join a cancelled startup before stopping: a late pkexec/systemctl start
	// must not bring the service back after the user has chosen Quit.
	if a.startupCancel != nil {
		a.startupCancel()
	}
	if a.startupDone != nil {
		select {
		case <-a.startupDone:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	command := exec.CommandContext(ctx, "systemctl", "--user", "stop", "fncpn-client.service")
	command.WaitDelay = time.Second
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("退出前停止客户端服务失败：%s: %w", output, err)
	}
	return nil
}

func uiOpenLogs() error {
	directory := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(directory) {
		home, _ := os.UserHomeDir()
		directory = filepath.Join(home, ".local/state")
	}
	return exec.Command("xdg-open", filepath.Join(directory, "fncpn")).Start()
}
