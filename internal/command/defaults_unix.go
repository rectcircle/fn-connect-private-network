//go:build !windows && !linux

package command

import (
	"os"
	"path/filepath"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func defaultClientSocket() string {
	return filepath.Join(defaultClientDataDirectory(), "run", "client.sock")
}

func defaultPrivilegedSocket(role string) string {
	return filepath.Join("/var/run", "fncpn-"+role+"-privileged.sock")
}

func defaultPrivilegedPipeSecurityDescriptor(string) string { return "" }

func defaultClientPipeAuthorizer() ipc.Authorizer { return nil }

func defaultPrivilegedStateDirectory(role string) string {
	if role == "server" {
		return "/var/lib/fncpn"
	}
	return "/var/db/fncpn"
}

func defaultClientConfig() string { return filepath.Join(defaultClientDataDirectory(), "config.json") }

func defaultClientDataDirectory() string {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "FnCPN")
	}
	return filepath.Join(configDirectory, "FnCPN")
}

func defaultClientLog() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "fncpn-client.log")
	}
	return filepath.Join(home, "Library", "Logs", "FnCPN", "client.log")
}

func defaultPrivilegedLog(role string) string {
	if role == "client" {
		return "/var/log/fncpn/client-privileged.log"
	}
	return "/var/log/fncpn/server-privileged.log"
}
