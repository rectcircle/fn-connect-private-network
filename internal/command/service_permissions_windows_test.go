//go:build windows

package command

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPreparePrivilegedDataDirectoriesRejectsUserOwnedState(t *testing.T) {
	directory := t.TempDir()
	serviceDirectory := filepath.Join(directory, "service")
	if err := os.Mkdir(serviceDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	logDirectory := filepath.Join(serviceDirectory, "logs")
	if err := os.Mkdir(logDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	original, err := windows.GetNamedSecurityInfo(serviceDirectory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	originalSDDL := original.String()

	logPath := filepath.Join(serviceDirectory, "logs", "service.log")
	err = preparePrivilegedDataDirectories(serviceDirectory, logPath)
	if err == nil || !strings.Contains(err.Error(), "untrusted owner") {
		t.Fatalf("preparePrivilegedDataDirectories() error = %v, want untrusted-owner refusal", err)
	}
	// The function must fail before altering an attacker-owned directory's ACL.
	info, err := windows.GetNamedSecurityInfo(serviceDirectory, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if info.String() != originalSDDL {
		t.Fatalf("untrusted directory DACL changed after refusal: before %q, after %q", originalSDDL, info.String())
	}
}

func TestPrivilegedCleanupRejectsUserOwnedJournalBeforeRecovery(t *testing.T) {
	directory := t.TempDir()
	stateDirectory := filepath.Join(directory, "service")
	logDirectory := filepath.Join(stateDirectory, "logs")
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(stateDirectory, "network-state.json")
	contents := []byte(`{"interfaceName":"attacker-controlled"}`)
	if err := os.WriteFile(journal, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	cleanupCalled := false
	err := runCleanupNetwork(
		context.Background(), "client", stateDirectory, filepath.Join(logDirectory, "service.log"),
		func(context.Context, string) error {
			cleanupCalled = true
			return nil
		},
	)
	if err == nil || !strings.Contains(err.Error(), "untrusted owner") {
		t.Fatalf("runCleanupNetwork() error = %v, want untrusted-owner refusal", err)
	}
	if cleanupCalled {
		t.Fatal("network Recover/Remove callback ran with a user-owned journal")
	}
	after, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(contents) {
		t.Fatalf("cleanup preflight modified user-owned journal: got %q, want %q", after, contents)
	}
}

func TestSecurePrivilegedDirectoryRejectsReparsePoint(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		quotePowerShell := func(value string) string { return strings.ReplaceAll(value, "'", "''") }
		command := "$null = New-Item -ItemType Junction -Path '" + quotePowerShell(link) +
			"' -Target '" + quotePowerShell(target) + "'"
		output, junctionErr := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
		if junctionErr != nil {
			t.Skipf("could not create directory symlink (%v) or junction (%v): %s", err, junctionErr, strings.TrimSpace(string(output)))
		}
	}
	defer os.Remove(link)
	path16, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	attributes, err := windows.GetFileAttributes(path16)
	if err != nil {
		t.Fatal(err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		t.Fatal("test link is not a Windows reparse point")
	}
	redirectedChild := filepath.Join(target, "service")
	servicePath := filepath.Join(link, "service")
	if err := securePrivilegedDirectory(servicePath); err == nil || !strings.Contains(err.Error(), "reparse point") {
		t.Fatalf("securePrivilegedDirectory(%q) error = %v, want reparse point refusal", servicePath, err)
	}
	if _, err := os.Lstat(redirectedChild); !os.IsNotExist(err) {
		t.Fatalf("security setup followed the junction and created %q: %v", redirectedChild, err)
	}
}
