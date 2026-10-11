//go:build windows

package client

import "golang.org/x/sys/windows"

func replaceClientFile(source, destination, _ string) error {
	sourceName, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationName, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourceName, destinationName, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
