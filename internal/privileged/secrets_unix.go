//go:build !windows

package privileged

import (
	"errors"
	"os"
	"syscall"
)

func checkSecretMetadata(info os.FileInfo, directory bool, mode os.FileMode) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) ||
		info.Mode().Perm() != mode ||
		(directory && !info.IsDir()) ||
		(!directory && !info.Mode().IsRegular()) {
		return errors.New("credential storage permissions or owner are invalid")
	}
	return nil
}
