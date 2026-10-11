//go:build windows

package command

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePurgeUserPathsAllowsOnlyCurrentProfile(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("LOCALAPPDATA", profile)
	data := filepath.Join(profile, "FnCPN")
	config := filepath.Join(data, "config.json")
	logs := filepath.Join(data, "Logs")

	if err := validatePurgeUserPaths(data, config, logs); err != nil {
		t.Fatalf("standard profile paths rejected: %v", err)
	}
	if err := validatePurgeUserPaths(filepath.Join(profile, "..", "outside"), config, logs); err == nil {
		t.Fatal("outside data directory was accepted")
	}
	if err := validatePurgeUserPaths(data, filepath.Join(profile, "other.json"), logs); err == nil {
		t.Fatal("outside config path was accepted")
	}
	if err := validatePurgeUserPaths(data, config, filepath.Join(profile, "other-logs")); err == nil {
		t.Fatal("outside log directory was accepted")
	}
	if err := validatePurgeUserPaths(data, config, os.TempDir()); err == nil {
		t.Fatal("system temporary directory was accepted as the log directory")
	}
}
