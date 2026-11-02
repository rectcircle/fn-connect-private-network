package client

import (
	"golang.org/x/sys/windows"
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
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		file.Close()
		return nil, err
	}
	return func() { _ = windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped); _ = file.Close() }, nil
}
