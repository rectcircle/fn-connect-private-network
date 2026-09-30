//go:build !darwin

package client

func bindSocketToInterface(uintptr, int) error {
	return nil
}
