package wireguard

import (
	"encoding/base64"
	"errors"
	"net/netip"
	"strings"
)

var privateIPv4Prefixes = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
}

var sharedIPv4Prefix = netip.MustParsePrefix("100.64.0.0/10")

func ValidateKey(value string) error {
	_, err := decodeKey(value)
	return err
}

func decodeKey(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("key must be a base64-encoded 32-byte value")
	}
	var nonzero byte
	for _, value := range decoded {
		nonzero |= value
	}
	if nonzero == 0 {
		return nil, errors.New("key must not be all zero")
	}
	return decoded, nil
}

func ParsePrivateIPv4Prefix(value string) (netip.Prefix, error) {
	prefix, err := parseCanonicalIPv4Prefix(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	for _, private := range privateIPv4Prefixes {
		if prefixWithin(prefix, private) {
			return prefix, nil
		}
	}
	return netip.Prefix{}, errors.New("must use RFC1918 private address space")
}

func ParsePrivateOrSharedIPv4Prefix(value string) (netip.Prefix, error) {
	prefix, err := parseCanonicalIPv4Prefix(value)
	if err != nil {
		return netip.Prefix{}, err
	}
	for _, private := range privateIPv4Prefixes {
		if prefixWithin(prefix, private) {
			return prefix, nil
		}
	}
	if prefixWithin(prefix, sharedIPv4Prefix) {
		return prefix, nil
	}
	return netip.Prefix{}, errors.New(
		"must use RFC1918 or 100.64.0.0/10 address space",
	)
}

func parseCanonicalIPv4Prefix(value string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() {
		return netip.Prefix{}, errors.New("must be a canonical IPv4 prefix")
	}
	return prefix, nil
}

func prefixWithin(prefix, parent netip.Prefix) bool {
	return parent.Bits() <= prefix.Bits() && parent.Contains(prefix.Addr())
}
