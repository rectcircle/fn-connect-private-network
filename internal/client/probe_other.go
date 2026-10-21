//go:build !darwin && !windows && !linux

package client

func bindSocketToInterface(uintptr, int) error {
	return nil
}
