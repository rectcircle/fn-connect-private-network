//go:build !windows

package client

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func (s *ConfigStore) lockSharedLinks() (func(), error) {
	if s.sharedLinks == "" {
		return func() {}, nil
	}
	file, err := os.OpenFile(filepath.Join(s.sharedLinks, "links.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN); _ = file.Close() }, nil
}
