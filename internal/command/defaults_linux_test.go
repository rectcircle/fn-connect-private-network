//go:build linux

package command

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestLinuxDefaultsHonorAbsoluteXDGAndKeepServerPaths(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "run"))
	if got := defaultClientConfig(); got != filepath.Join(root, "config/fncpn/config.json") {
		t.Fatal(got)
	}
	if got := defaultClientLog(); got != filepath.Join(root, "state/fncpn/client.log") {
		t.Fatal(got)
	}
	if got := defaultClientSocket(); got != filepath.Join(root, "run/fncpn/client.sock") {
		t.Fatal(got)
	}
	if got := defaultPrivilegedSocket("client"); got != filepath.Join("/run/fncpn", strconv.Itoa(os.Getuid()), "privileged.sock") {
		t.Fatal(got)
	}
	if defaultPrivilegedSocket("server") != "/var/run/fncpn-server-privileged.sock" || defaultPrivilegedStateDirectory("server") != "/var/lib/fncpn" {
		t.Fatal("server defaults changed")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative")
	t.Setenv("XDG_STATE_HOME", "relative")
	t.Setenv("XDG_RUNTIME_DIR", "relative")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if defaultClientConfig() != filepath.Join(home, ".config/fncpn/config.json") {
		t.Fatal("relative XDG config accepted")
	}
	if defaultClientDataDirectory() != filepath.Join(home, ".local/state/fncpn") {
		t.Fatal("relative XDG state accepted")
	}
	if defaultClientSocket() != filepath.Join("/run/user", strconv.Itoa(os.Getuid()), "fncpn/client.sock") {
		t.Fatal("relative XDG runtime accepted")
	}
}
