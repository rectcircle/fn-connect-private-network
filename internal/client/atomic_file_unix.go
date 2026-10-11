//go:build !windows

package client

import (
	"os"
)

func replaceClientFile(source, destination, directory string) error {
	if err := os.Rename(source, destination); err != nil {
		return err
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}
