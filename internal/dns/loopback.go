package dns

import (
	"fmt"
	"net"
	"net/netip"
)

// The pool is bounded so a wildcard listener does not trigger an exhaustive scan
// of 127/8. Try the previously selected address first to avoid needless NRPT changes.
func loopbackCandidates(preferred string) []string {
	candidates := make([]string, 0, 65)
	if a, err := netip.ParseAddr(preferred); err == nil && a.Is4() && a.IsLoopback() {
		candidates = append(candidates, a.String())
	}
	for last := 153; last < 217; last++ {
		address := fmt.Sprintf("127.0.0.%d", last)
		if address != preferred {
			candidates = append(candidates, address)
		}
	}
	return candidates
}

// ListenLoopback reserves UDP and TCP on the same address before returning it.
// Listen releases a partial binding if the other transport cannot be bound.
func ListenLoopback(preferred, suffix string, lookup Lookup, allowed func(string) bool) (*Forwarder, string, uint16, error) {
	if err := ValidateSuffix(suffix); err != nil {
		return nil, "", 0, err
	}
	return listenLoopback(preferred, func(address string) (*Forwarder, uint16, error) { return Listen(address, suffix, lookup, allowed) })
}
func listenLoopback(preferred string, bind func(string) (*Forwarder, uint16, error)) (*Forwarder, string, uint16, error) {
	candidates := loopbackCandidates(preferred)
	var lastErr error
	for _, address := range candidates {
		forwarder, port, err := bind(net.JoinHostPort(address, "53"))
		if err == nil {
			return forwarder, address, port, nil
		}
		lastErr = err
	}
	return nil, "", 0, fmt.Errorf("no available loopback DNS listener on UDP/TCP 53 after trying %d addresses: %w", len(candidates), lastErr)
}

// ValidateWindowsListener is also used by the privileged side, independently of
// address selection in the unprivileged daemon.
func ValidateWindowsListener(address string, port uint16) error {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsLoopback() || port != 53 {
		return fmt.Errorf("Windows DNS must use an IPv4 loopback address on port 53")
	}
	return nil
}
