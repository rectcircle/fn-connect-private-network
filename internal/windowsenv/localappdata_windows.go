//go:build windows

package windowsenv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// LocalAppDataDirectory resolves the interactive user's local application data
// directory. LOCALAPPDATA remains first so isolated profiles and redirected
// environments are honored; Known Folder lookup avoids falling back to the
// current working directory when the environment variable is missing.
func LocalAppDataDirectory() (string, error) {
	return resolveLocalAppData(
		os.Getenv("LOCALAPPDATA"),
		func() (string, error) {
			return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
		},
		os.UserHomeDir,
	)
}

func resolveLocalAppData(
	environment string,
	knownFolder func() (string, error),
	userHome func() (string, error),
) (string, error) {
	if strings.TrimSpace(environment) != "" {
		if !filepath.IsAbs(environment) {
			return "", errors.New("LOCALAPPDATA must be an absolute Windows path")
		}
		return filepath.Clean(environment), nil
	}

	knownPath, knownErr := knownFolder()
	if knownErr == nil && filepath.IsAbs(knownPath) {
		return filepath.Clean(knownPath), nil
	}

	home, homeErr := userHome()
	if homeErr == nil && filepath.IsAbs(home) {
		return filepath.Join(filepath.Clean(home), "AppData", "Local"), nil
	}

	if knownErr == nil {
		knownErr = errors.New("Windows LocalAppData known folder is not an absolute path")
	}
	if homeErr == nil {
		homeErr = errors.New("user profile directory is not an absolute path")
	}
	return "", errors.Join(
		errors.New("could not resolve the current user's LocalAppData directory"),
		knownErr,
		homeErr,
	)
}
