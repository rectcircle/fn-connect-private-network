//go:build linux

package platform

import (
	"context"
	"errors"
	"net/netip"
	"strings"

	"github.com/vishvananda/netlink"
)

func DetectLANCIDRs() ([]string, error) {
	addresses, err := defaultLANAddresses()
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if address.Addr().IsPrivate() {
			return []string{address.Masked().String()}, nil
		}
	}
	return nil, errors.New("default route has no private IPv4 address")
}

func DetectLANAddresses() ([]string, error) {
	addresses, err := defaultLANAddresses()
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if address.Addr().IsPrivate() {
			result = append(result, address.Addr().String())
		}
	}
	if len(result) == 0 {
		return nil, errors.New("default route has no private IPv4 address")
	}
	return result, nil
}

func LANAddressEvents(ctx context.Context) (<-chan struct{}, error) {
	addressUpdates := make(chan netlink.AddrUpdate, 1)
	addressDone := make(chan struct{})
	if err := netlink.AddrSubscribe(addressUpdates, addressDone); err != nil {
		return nil, err
	}
	routeUpdates := make(chan netlink.RouteUpdate, 1)
	routeDone := make(chan struct{})
	if err := netlink.RouteSubscribe(routeUpdates, routeDone); err != nil {
		close(addressDone)
		return nil, err
	}
	events := make(chan struct{}, 1)
	go func() {
		defer close(addressDone)
		defer close(routeDone)
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-addressUpdates:
				if !ok {
					return
				}
			case _, ok := <-routeUpdates:
				if !ok {
					return
				}
			}
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	return events, nil
}

func defaultLANAddresses() ([]netip.Prefix, error) {
	routes, err := netlink.RouteList(nil, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	bestIndex := defaultIPv4Interface(routes)
	if bestIndex == 0 {
		return nil, errors.New("default IPv4 route was not found")
	}
	link, err := netlink.LinkByIndex(bestIndex)
	if err != nil {
		return nil, err
	}
	name := link.Attrs().Name
	for _, prefix := range []string{"lo", "docker", "br-", "veth", "tun", "wg", "fncpn"} {
		if strings.HasPrefix(name, prefix) {
			return nil, errors.New("default route uses a virtual interface")
		}
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	result := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		prefix, err := netip.ParsePrefix(address.IPNet.String())
		if err == nil {
			result = append(result, prefix)
		}
	}
	return result, nil
}
