//go:build !darwin && !windows

package client

func bindSocketToInterface(uintptr, int) error {
	return nil
}
