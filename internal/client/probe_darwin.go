//go:build darwin

package client

import (
	"errors"

	"golang.org/x/sys/unix"
)

func bindSocketToInterface(fd uintptr, interfaceIndex int) error {
	if interfaceIndex <= 0 {
		return errors.New("physical interface is unavailable")
	}
	return unix.SetsockoptInt(
		int(fd),
		unix.IPPROTO_IP,
		unix.IP_BOUND_IF,
		interfaceIndex,
	)
}
