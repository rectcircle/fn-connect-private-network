//go:build windows

package command

import "github.com/rectcircle/fn-connect-private-network/internal/windowsinstall"

func preparePrivilegedCleanupDirectories(role, stateDirectory, logPath string) error {
	if role != "client" {
		return nil
	}
	return preparePrivilegedDataDirectories(stateDirectory, logPath)
}

func preparePrivilegedDataDirectories(stateDirectory, logPath string) error {
	return windowsinstall.PrepareDataDirectories(stateDirectory, logPath)
}

func securePrivilegedDirectory(path string) error {
	return windowsinstall.SecureDirectory(path)
}
