//go:build linux

package command

import (
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"os"
	"path/filepath"
	"strconv"
)

func linuxHome(kind, fallback string) string {
	if value := os.Getenv(kind); filepath.IsAbs(value) {
		return filepath.Join(value, "fncpn")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback, "fncpn")
}
func defaultClientDataDirectory() string { return linuxHome("XDG_STATE_HOME", ".local/state") }
func defaultClientConfig() string {
	return filepath.Join(linuxHome("XDG_CONFIG_HOME", ".config"), "config.json")
}
func defaultClientSocket() string {
	if value := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(value) {
		return filepath.Join(value, "fncpn/client.sock")
	}
	return filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "fncpn/client.sock")
}
func defaultClientLog() string { return filepath.Join(defaultClientDataDirectory(), "client.log") }
func defaultPrivilegedSocket(role string) string {
	if role == "server" {
		return "/var/run/fncpn-server-privileged.sock"
	}
	return filepath.Join("/run/fncpn", strconv.Itoa(os.Getuid()), "privileged.sock")
}
func defaultPrivilegedStateDirectory(role string) string {
	if role == "server" {
		return "/var/lib/fncpn"
	}
	return filepath.Join("/var/lib/fncpn/client", strconv.Itoa(os.Getuid()))
}
func defaultPrivilegedLog(role string) string {
	if role == "server" {
		return "/var/log/fncpn/server-privileged.log"
	}
	return "/var/log/fncpn/client-privileged-" + strconv.Itoa(os.Getuid()) + ".log"
}
func defaultPrivilegedPipeSecurityDescriptor(string) string { return "" }
func defaultClientPipeAuthorizer() ipc.Authorizer           { return nil }
