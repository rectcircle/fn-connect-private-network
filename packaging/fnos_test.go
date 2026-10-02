package packaging

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFnOSInitializesFreshInstallation(t *testing.T) {
	directory := t.TempDir()
	output, err := runFnOSFunctions(t, directory, "initialize_app")
	if err != nil {
		t.Fatalf("initialize: %v\n%s", err, output)
	}
	for _, expected := range []struct {
		path string
		mode os.FileMode
	}{
		{"app", 0o755}, {"app/server", 0o755}, {"app/server/fncpn", 0o755},
		{"etc", 0o750}, {"var", 0o750}, {"var/logs", 0o750},
		{"etc/control", 0o700}, {"etc/privileged", 0o700},
		{"var/logs/server", 0o700}, {"var/logs/privileged", 0o700},
		{"var/run", 0o750}, {"app/run", 0o750},
	} {
		info, err := os.Stat(filepath.Join(directory, expected.path))
		if err != nil || info.Mode().Perm() != expected.mode {
			t.Fatalf("%s: info=%v error=%v, want mode %#o", expected.path, info, err, expected.mode)
		}
	}
	logPath := filepath.Join(directory, "var", "logs", "server", "server.log")
	assertFnOSFile(t, logPath, "", 0o600)
	assertFnOSFile(t, filepath.Join(directory, "var/logs/privileged/privileged.log"), "", 0o600)
	for _, path := range []string{"etc/control", "etc/privileged"} {
		entries, err := os.ReadDir(filepath.Join(directory, path))
		if err != nil || len(entries) != 0 {
			t.Fatalf("%s must be left for its owning daemon to initialize: %v %v", path, entries, err)
		}
	}
	calls, err := os.ReadFile(filepath.Join(directory, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"chown root:fncpn " + filepath.Join(directory, "etc") + " " + filepath.Join(directory, "var"),
		"chown fncpn:fncpn " + filepath.Join(directory, "etc/control") + " " + filepath.Join(directory, "var/logs/server"),
		"chown root:root " + filepath.Join(directory, "etc/privileged") + " " + filepath.Join(directory, "var/logs/privileged"),
		"chown root:fncpn " + filepath.Join(directory, "var/run"),
		"chown fncpn:fncpn " + filepath.Join(directory, "app/run"),
		"su fncpn umask 077; : >> '" + logPath + "'",
	} {
		if !strings.Contains(string(calls), expected) {
			t.Fatalf("missing ownership operation %q:\n%s", expected, calls)
		}
	}
}

func TestFnOSInitializationPreservesExistingData(t *testing.T) {
	directory := t.TempDir()
	if output, err := runFnOSFunctions(t, directory, "initialize_app"); err != nil {
		t.Fatalf("initialize: %v\n%s", err, output)
	}
	files := []string{
		"etc/control/state.json", "etc/control/probe.key", "etc/privileged/server.key",
		"var/logs/server/server.log", "var/logs/privileged/privileged.log",
	}
	for _, path := range files {
		if err := os.WriteFile(filepath.Join(directory, path), []byte("retained data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := runFnOSFunctions(t, directory, "initialize_app\nprepare_runtime"); err != nil {
		t.Fatalf("repeat initialization: %v\n%s", err, output)
	}
	for _, path := range files {
		assertFnOSFile(t, filepath.Join(directory, path), "retained data", 0o600)
	}
}

func TestFnOSRuntimePreparationDoesNotInitializePersistentState(t *testing.T) {
	directory := t.TempDir()
	if output, err := runFnOSFunctions(t, directory, "prepare_runtime"); err != nil {
		t.Fatalf("prepare runtime: %v\n%s", err, output)
	}
	for _, path := range []string{"etc", "var/logs"} {
		if _, err := os.Stat(filepath.Join(directory, path)); !os.IsNotExist(err) {
			t.Fatalf("runtime preparation touched persistent path %s: %v", path, err)
		}
	}
}

func TestFnOSScriptErrorDoesNotCreateBusinessLog(t *testing.T) {
	directory := t.TempDir()
	message := `server daemon failed its "healthcheck"`
	output, err := runFnOSFunctions(t, directory, `log_error 'server daemon failed its "healthcheck"'`)
	if err != nil {
		t.Fatalf("log error: %v\n%s", err, output)
	}
	assertFnOSFile(t, filepath.Join(directory, "installer-error"), message+"\n", 0o600)
	var entry struct {
		Level   string `json:"level"`
		Message string `json:"msg"`
	}
	if err := json.Unmarshal(output, &entry); err != nil {
		t.Fatalf("invalid stderr log entry: %v\n%s", err, output)
	}
	if entry.Level != "ERROR" || entry.Message != message {
		t.Fatalf("unexpected log entry: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(directory, "var/logs")); !os.IsNotExist(err) {
		t.Fatalf("root script created business log storage: %v", err)
	}
}

func TestFnOSInitializationFailureIsReported(t *testing.T) {
	directory := t.TempDir()
	output, err := runFnOSFunctions(t, directory, `
if ! initialize_app; then
    log_error 'FnCPN installation initialization failed'
    exit 1
fi
`, "TEST_FAIL_CHOWN=1")
	if err == nil {
		t.Fatalf("ownership failure was ignored: %s", output)
	}
	assertFnOSFile(t, filepath.Join(directory, "installer-error"), "FnCPN installation initialization failed\n", 0o600)
	logPath := filepath.Join(directory, "var", "logs", "server", "server.log")
	if _, err := os.Lstat(logPath); !os.IsNotExist(err) {
		t.Fatalf("failed preparation still created the log: %v", err)
	}
}

func TestFnOSLifecycleHooksUseSiblingMain(t *testing.T) {
	for _, hook := range []struct {
		name   string
		action string
	}{
		{name: "install_callback", action: "initialize"},
		{name: "upgrade_init", action: "stop"},
		{name: "upgrade_callback", action: "start"},
		{name: "uninstall_init", action: "stop"},
	} {
		for _, status := range []string{"0", "23"} {
			t.Run(hook.name+"/exit_"+status, func(t *testing.T) {
				directory := t.TempDir()
				cmdDirectory := filepath.Join(directory, "app metadata", "cmd")
				targetDirectory := filepath.Join(directory, "app target")
				for _, path := range []string{cmdDirectory, targetDirectory} {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				script, err := os.ReadFile(filepath.Join("fnos", "cmd", hook.name))
				if err != nil {
					t.Fatal(err)
				}
				hookPath := filepath.Join(cmdDirectory, hook.name)
				if err := os.WriteFile(hookPath, script, 0o755); err != nil {
					t.Fatal(err)
				}
				const main = `#!/bin/bash
set -eu
printf '%s\n' "$*" > "$TEST_MAIN_ACTION"
exit "$TEST_MAIN_STATUS"
`
				if err := os.WriteFile(filepath.Join(cmdDirectory, "main"), []byte(main), 0o755); err != nil {
					t.Fatal(err)
				}
				actionPath := filepath.Join(directory, "action")
				command := exec.Command("bash", hookPath)
				command.Dir = directory
				command.Env = append(os.Environ(),
					"TRIM_APPDEST="+targetDirectory,
					"TEST_MAIN_ACTION="+actionPath,
					"TEST_MAIN_STATUS="+status,
				)
				output, err := command.CombinedOutput()
				if (status == "0" && err != nil) || (status != "0" && err == nil) {
					t.Fatalf("main exit %s: hook error=%v output=%s", status, err, output)
				}
				action, err := os.ReadFile(actionPath)
				if err != nil {
					t.Fatalf("sibling main was not invoked: %v\n%s", err, output)
				}
				if string(action) != hook.action+"\n" {
					t.Fatalf("main action=%q, want %q", action, hook.action)
				}
			})
		}
	}
}

func assertFnOSFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != contents || info.Mode().Perm() != mode {
		t.Fatalf("%s: contents=%q mode=%#o, want %q %#o", path, data, info.Mode().Perm(), contents, mode)
	}
}

func runFnOSFunctions(t *testing.T, directory, operation string, environment ...string) ([]byte, error) {
	t.Helper()
	serverDirectory := filepath.Join(directory, "app", "server")
	if err := os.MkdirAll(serverDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(serverDirectory, "fncpn"), []byte("test payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join("fnos", "cmd", "main"))
	if err != nil {
		t.Fatal(err)
	}
	// Load the actual functions without dispatching a command or starting daemons.
	definitions, _, found := strings.Cut(string(script), "\ncase \"${1:-}\" in\n")
	if !found {
		t.Fatal("fnOS command dispatch was not found")
	}
	// Stub privilege changes; all real file operations stay in the test directory.
	const privileges = `
chown() {
    printf 'chown %s\n' "$*" >> "$TEST_CALLS"
    [ "${TEST_FAIL_CHOWN:-0}" = 0 ]
}
su() {
    [ "$#" -eq 5 ] && [ "$1" = -s ] && [ "$2" = /bin/sh ] &&
        [ "$3" = fncpn ] && [ "$4" = -c ] || return 1
    printf 'su %s %s\n' "$3" "$5" >> "$TEST_CALLS"
    /bin/sh -c "$5"
}
umask 077
`
	command := exec.Command("bash", "-c", definitions+privileges+"\n"+operation)
	command.Env = append(os.Environ(),
		"TRIM_APPDEST="+filepath.Join(directory, "app"),
		"TRIM_PKGVAR="+filepath.Join(directory, "var"),
		"TRIM_PKGETC="+filepath.Join(directory, "etc"),
		"TRIM_TEMP_LOGFILE="+filepath.Join(directory, "installer-error"),
		"TEST_CALLS="+filepath.Join(directory, "calls"),
	)
	command.Env = append(command.Env, environment...)
	return command.CombinedOutput()
}
