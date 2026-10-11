//go:build !windows

package command

func preparePrivilegedDataDirectories(_, _ string) error { return nil }

func preparePrivilegedCleanupDirectories(_, _, _ string) error { return nil }
