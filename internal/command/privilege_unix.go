//go:build !windows

package command

import "os"

func isPrivilegedProcess() bool { return os.Geteuid() == 0 }
