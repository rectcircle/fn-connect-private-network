//go:build !windows

package command

func validatePurgeUserPaths(_, _, _ string) error { return nil }
