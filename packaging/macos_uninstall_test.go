package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSUninstallPreservesOtherUserCredentials(t *testing.T) {
	for _, purge := range []bool{false, true} {
		name := "retain"
		if purge {
			name = "purge-one-user"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			state := filepath.Join(directory, "state")
			for _, uid := range []string{"501", "502"} {
				path := filepath.Join(state, "credentials", uid)
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "fixture.secret"), []byte("private"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"state.json", "owner.lock"} {
				if err := os.WriteFile(filepath.Join(state, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runMacOSUninstallFixture(t, directory, purge)
			if err != nil {
				t.Fatalf("uninstall: %v\n%s", err, output)
			}
			_, ownErr := os.Stat(filepath.Join(state, "credentials", "501", "fixture.secret"))
			if purge && !os.IsNotExist(ownErr) || !purge && ownErr != nil {
				t.Fatalf("own credential retention: purge=%v err=%v", purge, ownErr)
			}
			if _, err := os.Stat(filepath.Join(state, "credentials", "502", "fixture.secret")); err != nil {
				t.Fatalf("other user's credential was deleted: %v", err)
			}
			for _, name := range []string{"state.json", "owner.lock"} {
				if _, err := os.Stat(filepath.Join(state, name)); !os.IsNotExist(err) {
					t.Fatalf("network state retained: %s %v", name, err)
				}
			}
		})
	}
}

func runMacOSUninstallFixture(t *testing.T, directory string, purge bool) ([]byte, error) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("macos", "uninstall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const stub = `#!/bin/sh
set -eu
case "${0##*/}" in
  id) printf '0\n' ;;
  ps|launchctl) exit 0 ;;
  dscl)
    case "$2" in
      -search) printf 'fixture 501\n' ;;
      -read) printf 'NFSHomeDirectory: %s/home\n' "$TEST_ROOT" ;;
      *) exit 1 ;;
    esac ;;
  fncpn)
    [ "$1" = internal-cleanup ]
    /bin/rm -f "$TEST_ROOT/state/state.json" ;;
  rm|rmdir)
    for path in "$@"; do
      case "$path" in
        /*) case "$path" in "$TEST_ROOT"/*) ;; *) echo "unsafe test path: $path" >&2; exit 1 ;; esac ;;
      esac
    done
    exec "/bin/${0##*/}" "$@" ;;
  *) echo "unexpected command: $0" >&2; exit 1 ;;
esac
`
	tools := filepath.Join(directory, "tools")
	helpers := filepath.Join(directory, "helpers")
	for _, path := range []string{tools, helpers} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(helpers, "fncpn"), []byte(stub), 0o700); err != nil {
		t.Fatal(err)
	}
	replacements := []string{
		"/Library/PrivilegedHelperTools/cn.rectcircle.fncpn", helpers,
		"/var/db/fncpn", filepath.Join(directory, "state"),
		"/var/run/fncpn-client-privileged.sock", filepath.Join(directory, "priv.sock"),
		"/usr/local/bin/fncpn", filepath.Join(directory, "cli"),
		"/Users/*/Library/LaunchAgents/cn.rectcircle.fncpn.client.plist", filepath.Join(directory, "agent.plist"),
		"/Library/LaunchAgents/cn.rectcircle.fncpn.client.plist", filepath.Join(directory, "agent.plist"),
		"/Library/LaunchDaemons/cn.rectcircle.fncpn.privileged.plist", filepath.Join(directory, "daemon.plist"),
		"/Applications/FnCPN.app", filepath.Join(directory, "app"),
		"/var/log/fncpn", filepath.Join(directory, "logs"),
	}
	for _, path := range []string{"/usr/bin/id", "/bin/ps", "/bin/launchctl", "/usr/bin/dscl", "/bin/rm", "/bin/rmdir"} {
		tool := filepath.Join(tools, filepath.Base(path))
		if err := os.WriteFile(tool, []byte(stub), 0o700); err != nil {
			t.Fatal(err)
		}
		replacements = append(replacements, path, tool)
	}
	arguments := []string{"-c", strings.NewReplacer(replacements...).Replace(string(script)), "uninstall"}
	if purge {
		arguments = append(arguments, "--purge-user-data", "501")
	}
	command := exec.Command("sh", arguments...)
	command.Env = append(os.Environ(), "TEST_ROOT="+directory)
	return command.CombinedOutput()
}
