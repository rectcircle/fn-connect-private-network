//go:build !windows

package command

import "context"

// runWindowsService is referenced by the shared runtime dispatch. On Unix the
// privileged process remains a foreground daemon, as before the Windows SCM
// integration.
func runWindowsService(
	parent context.Context,
	role string,
	arguments []string,
	environment Environment,
) error {
	return runPrivilegedDaemon(parent, role, arguments, environment)
}
