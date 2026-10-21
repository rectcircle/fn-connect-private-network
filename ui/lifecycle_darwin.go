//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func uiSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library/Application Support/FnCPN/run/client.sock")
}
func (a *App) serviceTarget() string {
	return "gui/" + strconv.Itoa(os.Getuid()) + "/cn.rectcircle.fncpn.client"
}
func (a *App) startPlatformDaemon() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	template := filepath.Join(filepath.Dir(executable), "../Resources/client-agent.plist")
	data, err := os.ReadFile(template)
	if err != nil {
		return fmt.Errorf("请使用 scripts/build-wails-macos.sh 构建完整应用；客户端服务尚未运行：%w", err)
	}
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, "Library/LaunchAgents/cn.rectcircle.fncpn.client.plist")
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".wails.tmp"
	if err = os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err = os.Rename(temporary, path); err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	if exec.Command("/bin/launchctl", "print", a.serviceTarget()).Run() != nil {
		if output, e := exec.Command("/bin/launchctl", "bootstrap", domain, path).CombinedOutput(); e != nil {
			return fmt.Errorf("启动客户端服务：%s: %w", output, e)
		}
	}
	if output, e := exec.Command("/bin/launchctl", "kickstart", a.serviceTarget()).CombinedOutput(); e != nil {
		return fmt.Errorf("启动客户端进程：%s: %w", output, e)
	}
	a.mu.Lock()
	a.started = true
	a.mu.Unlock()
	return nil
}
func (a *App) stopPlatformDaemon() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.client.Call(ctx, "shutdown-client-daemon", nil, nil)
	_ = exec.Command("/bin/launchctl", "bootout", a.serviceTarget()).Run()
}
func uiOpenLogs() error {
	home, _ := os.UserHomeDir()
	return exec.Command("/usr/bin/open", filepath.Join(home, "Library/Logs/FnCPN")).Run()
}
