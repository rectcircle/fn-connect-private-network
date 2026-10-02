package packaging

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMacOSPreinstallSecuresHelperParent(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "fresh"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			parent := filepath.Join(directory, "helpers")
			if existing {
				if err := os.MkdirAll(filepath.Join(parent, "other-helper"), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(parent, 0o777); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(parent, "other-helper", "tool"), []byte("unrelated tool"), 0o640); err != nil {
					t.Fatal(err)
				}
			}

			output, calls, err := runMacOSPreinstall(t, directory, "")
			if err != nil {
				t.Fatalf("preinstall: %v\n%s", err, output)
			}
			info, err := os.Stat(parent)
			if err != nil || !info.IsDir() || info.Mode().Perm() != 0o755 {
				t.Fatalf("helper parent: info=%v error=%v, want directory mode 0755", info, err)
			}
			want := []string{
				"mkdir -p " + parent,
				"chown root:wheel " + parent,
				"chmod -N " + parent,
				"chmod 0755 " + parent,
			}
			for index, operation := range want {
				if len(calls) <= index || calls[index] != operation {
					t.Fatalf("permission setup must precede process operations: got %q, want prefix %q", calls, want)
				}
			}
			if existing {
				other := filepath.Join(parent, "other-helper", "tool")
				data, err := os.ReadFile(other)
				if err != nil || string(data) != "unrelated tool" {
					t.Fatalf("unrelated tool changed: %q %v", data, err)
				}
				info, err := os.Stat(other)
				if err != nil || info.Mode().Perm() != 0o640 {
					t.Fatalf("unrelated tool permissions changed: %v %v", info, err)
				}
			}
		})
	}
}

func TestMacOSPreinstallPermissionFailureStopsInstallation(t *testing.T) {
	for _, operation := range []string{"mkdir", "chown", "acl", "mode"} {
		t.Run(operation, func(t *testing.T) {
			output, calls, err := runMacOSPreinstall(t, t.TempDir(), operation)
			if err == nil {
				t.Fatalf("%s failure was ignored:\n%s\n%v", operation, output, calls)
			}
			if !strings.Contains(string(output), "injected "+operation+" failure") {
				t.Fatalf("missing failure diagnostic: %s", output)
			}
			for _, call := range calls {
				if strings.HasPrefix(call, "ps ") || strings.HasPrefix(call, "launchctl ") {
					t.Fatalf("process operations continued after permission failure: %v", calls)
				}
			}
		})
	}
}

func TestMacOSPreinstallRejectsSymlinkParent(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "unrelated")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "helpers")); err != nil {
		t.Fatal(err)
	}
	output, calls, err := runMacOSPreinstall(t, directory, "")
	if err == nil || !strings.Contains(string(output), "must not be a symbolic link") {
		t.Fatalf("symlink was not rejected: %v\n%s", err, output)
	}
	if len(calls) != 0 {
		t.Fatalf("symlink target was accessed: %v", calls)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("symlink target changed: %v %v", info, err)
	}
}

func runMacOSPreinstall(t *testing.T, directory, fail string) ([]byte, []string, error) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("macos", "scripts", "preinstall"))
	if err != nil {
		t.Fatal(err)
	}
	tools := filepath.Join(directory, "tools")
	if err := os.Mkdir(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	const stub = `#!/bin/sh
set -eu
name="${0##*/}"
printf '%s %s\n' "$name" "$*" >> "$TEST_CALLS"
operation="$name"
if [ "$name" = chmod ]; then
    if [ "$1" = -N ]; then operation=acl; else operation=mode; fi
fi
if [ "$operation" = "$TEST_FAIL" ]; then
    echo "injected $operation failure" >&2
    exit 23
fi
case "$name" in
    mkdir) exec /bin/mkdir "$@" ;;
    chmod)
        if [ "$1" = -N ] && [ "$(uname -s)" != Darwin ]; then exit 0; fi
        exec /bin/chmod "$@"
        ;;
    chown|ps|launchctl) exit 0 ;;
    *) echo "unexpected process operation: $name" >&2; exit 1 ;;
esac
`
	// Run the real script against a temporary filesystem with no privileged
	// ownership changes, daemon operations, or access to host installation state.
	replacements := []string{
		"/Library/PrivilegedHelperTools", filepath.Join(directory, "helpers"),
		"/var/db/fncpn/state.json", filepath.Join(directory, "state.json"),
	}
	for _, path := range []string{
		"/bin/mkdir", "/usr/sbin/chown", "/bin/chmod", "/bin/ps",
		"/bin/launchctl", "/bin/kill", "/bin/sleep",
	} {
		tool := filepath.Join(tools, filepath.Base(path))
		if err := os.WriteFile(tool, []byte(stub), 0o700); err != nil {
			t.Fatal(err)
		}
		replacements = append(replacements, path, tool)
	}
	command := exec.Command("sh", "-c", strings.NewReplacer(replacements...).Replace(string(script)))
	callsPath := filepath.Join(directory, "calls")
	command.Env = append(os.Environ(), "TEST_CALLS="+callsPath, "TEST_FAIL="+fail)
	output, commandErr := command.CombinedOutput()
	calls, err := os.ReadFile(callsPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var operations []string
	if len(calls) > 0 {
		operations = strings.Split(strings.TrimSuffix(string(calls), "\n"), "\n")
	}
	return output, operations, commandErr
}
