//go:build windows

package windowsinstall

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerCleanupRejectsUserJournalBeforeRecovery(t *testing.T) {
	if IsLocalSystem() {
		t.Skip("requires an ordinary account for the untrusted-owner fixture")
	}
	state := filepath.Join(t.TempDir(), "Service")
	if err := os.MkdirAll(filepath.Join(state, "Logs"), 0700); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(state, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, user.User.Sid, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(state, "network-state.json")
	contents := []byte(`{"interface":"must-not-be-read"}`)
	if err := os.WriteFile(journal, contents, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err = cleanupNetwork(context.Background(), state, func(context.Context) error { called = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "untrusted owner") || called {
		t.Fatalf("err=%v, recovery called=%v", err, called)
	}
	after, err := os.ReadFile(journal)
	if err != nil || string(after) != string(contents) {
		t.Fatalf("journal changed: %q, %v", after, err)
	}
}
