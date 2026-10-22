//go:build linux && !android

package client

import (
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// Bind LAN probes to the physical device. Linux >= 5.7 permits an
// unprivileged process to bind a fresh socket; all supported kernels qualify.
// Otherwise a NAS probe can succeed through our tunnel and oscillate LOCAL/DIRECT.
func bindSocketToInterface(fd uintptr, interfaceIndex int) error {
	if interfaceIndex <= 0 {
		return errors.New("physical interface is unavailable")
	}
	iface, err := net.InterfaceByIndex(interfaceIndex)
	if err != nil {
		return fmt.Errorf("resolve physical interface %d: %w", interfaceIndex, err)
	}
	if err := unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, iface.Name); err != nil {
		return fmt.Errorf("bind local probe to IPv4 interface %d: %w", interfaceIndex, err)
	}
	return nil
}
