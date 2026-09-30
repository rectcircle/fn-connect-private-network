package server

import (
	"errors"
	"fmt"
	"net/netip"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

const (
	DefaultOverlayCIDR = "10.253.203.0/24"
	DefaultListenPort  = 54789
)

var sharedAddressPrefix = netip.MustParsePrefix("100.64.0.0/10")

func ValidateSettings(settings model.ServerSettings) error {
	overlay, err := parseOverlay(settings.OverlayCIDR)
	if err != nil {
		return err
	}
	if settings.ListenPort == 0 {
		return errors.New("listen port is required")
	}
	if len(settings.LANCIDRs) > 32 {
		return errors.New("too many LAN CIDRs")
	}
	for _, value := range settings.LANCIDRs {
		network, err := wgconfig.ParsePrivateIPv4Prefix(value)
		if err != nil {
			return fmt.Errorf("invalid LAN CIDR %q: %w", value, err)
		}
		if prefixesOverlap(overlay, network) {
			return fmt.Errorf("overlay CIDR overlaps LAN CIDR %q", value)
		}
	}
	return nil
}

func ServerAddress(overlayCIDR string) (string, error) {
	overlay, err := parseOverlay(overlayCIDR)
	if err != nil {
		return "", err
	}
	return netip.PrefixFrom(overlay.Addr().Next(), overlay.Bits()).String(), nil
}

func AllocateAddresses(overlayCIDR string, count int) ([]string, error) {
	if count < 0 {
		return nil, errors.New("address count must not be negative")
	}
	overlay, err := parseOverlay(overlayCIDR)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, count)
	for address := overlay.Addr().Next().Next(); overlay.Contains(address); address = address.Next() {
		if !overlay.Contains(address.Next()) {
			break
		}
		result = append(result, netip.PrefixFrom(address, 32).String())
		if len(result) == count {
			return result, nil
		}
	}
	return nil, errors.New("overlay address pool is exhausted")
}

func ValidatePublicKey(value string) error {
	if err := wgconfig.ValidateKey(value); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return nil
}

func parseOverlay(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, errors.New("overlay CIDR must be IPv4")
	}
	if prefix != prefix.Masked() {
		return netip.Prefix{}, errors.New("overlay CIDR must use its network address")
	}
	if prefix.Bits() < 16 || prefix.Bits() > 29 {
		return netip.Prefix{}, errors.New("overlay prefix length must be between /16 and /29")
	}
	if !prefix.Addr().IsPrivate() && !sharedAddressPrefix.Contains(prefix.Addr()) {
		return netip.Prefix{}, errors.New("overlay CIDR must use RFC1918 or 100.64.0.0/10 space")
	}
	return prefix, nil
}

func prefixesOverlap(first, second netip.Prefix) bool {
	if first.Addr().BitLen() != second.Addr().BitLen() {
		return false
	}
	return first.Contains(second.Addr()) || second.Contains(first.Addr())
}
