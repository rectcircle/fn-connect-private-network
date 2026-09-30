//go:build darwin

package darwin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func currentConsoleUID() (uint32, error) {
	info, err := os.Stat("/dev/console")
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid < 500 {
		return 0, errors.New("no interactive console user")
	}
	return stat.Uid, nil
}

func acquireOwnerLease(stateDirectory, token string) (*os.File, error) {
	if err := os.MkdirAll(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create owner lease directory: %w", err)
	}
	if err := os.Chmod(stateDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("set owner lease directory permissions: %w", err)
	}
	path := filepath.Join(stateDirectory, "owner.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open owner lease: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire owner lease: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		file.Close()
		return nil, fmt.Errorf("truncate owner lease: %w", err)
	}
	if _, err := file.WriteAt([]byte(token+"\n"), 0); err != nil {
		file.Close()
		return nil, fmt.Errorf("write owner lease: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("sync owner lease: %w", err)
	}
	return file, nil
}
