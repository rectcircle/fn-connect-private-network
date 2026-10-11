//go:build windows

package client

import (
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const ipUnicastIf = 31

// bindSocketToInterface pins the local IPv4 HTTP probe to the selected
// physical interface. Windows expects IP_UNICAST_IF in network byte order.
func bindSocketToInterface(fd uintptr, interfaceIndex int) error {
	if fd == 0 {
		return errors.New("local probe socket is unavailable")
	}
	if interfaceIndex <= 0 {
		return errors.New("physical interface is unavailable")
	}
	var networkOrder [4]byte
	binary.BigEndian.PutUint32(networkOrder[:], uint32(interfaceIndex))
	optionValue := binary.LittleEndian.Uint32(networkOrder[:])
	if err := windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, ipUnicastIf, int(optionValue)); err != nil {
		return fmt.Errorf("bind local probe to IPv4 interface %d: %w", interfaceIndex, err)
	}
	return nil
}
