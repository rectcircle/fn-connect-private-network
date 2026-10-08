//go:build !darwin || !cgo

package client

import (
	"errors"
	"net"
	"net/netip"
)

func enrichPhysicalNetwork(snapshot *NetworkSnapshot) {}

func defaultPhysicalInterface() (string, error) {
	connection, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return "", nil
	}
	defer connection.Close()
	local := connection.LocalAddr().(*net.UDPAddr).IP
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, networkInterface := range interfaces {
		addresses, _ := networkInterface.Addrs()
		for _, value := range addresses {
			prefix, parseErr := netip.ParsePrefix(value.String())
			if parseErr == nil && prefix.Addr().String() == local.String() {
				return networkInterface.Name, nil
			}
		}
	}
	return "", errors.New("default physical interface was not found")
}
