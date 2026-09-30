//go:build darwin

package privileged

import (
	"os"
	"syscall"
)

func defaultClientUID() int {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return -1
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid < 500 {
		return -1
	}
	return int(stat.Uid)
}
