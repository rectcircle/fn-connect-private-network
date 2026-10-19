package dns

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"
)

func NormalizeName(value string) (string, error) {
	value = strings.ToLower(strings.TrimSuffix(value, "."))
	if value == "" || len(value) > 253 {
		return "", errors.New("invalid DNS name length")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("invalid DNS label")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", errors.New("DNS name must use ASCII letters, digits and hyphens")
			}
		}
	}
	if _, err := netip.ParseAddr(value); err == nil {
		return "", errors.New("DNS name cannot be an IP address")
	}
	return value, nil
}
func ValidateSuffix(value string) error {
	n, err := NormalizeName(value)
	if err != nil {
		return err
	}
	if n != value || !strings.Contains(n, ".") {
		return errors.New("suffix must be a canonical multi-label domain")
	}
	for _, suffix := range []string{"local", "localhost", "invalid", "test", "example"} {
		if n == suffix || strings.HasSuffix(n, "."+suffix) {
			return errors.New("suffix uses a special-use namespace")
		}
	}
	if strings.HasSuffix(n, ".arpa") && n != "home.arpa" && !strings.HasSuffix(n, ".home.arpa") {
		return errors.New("suffix uses a reserved reverse namespace")
	}
	return nil
}
func DefaultSuffix(id string) string {
	label := strings.ToLower(id)
	if n, err := NormalizeName(label); err != nil || strings.Contains(n, ".") {
		sum := sha256.Sum256([]byte(id))
		label = "id-" + hex.EncodeToString(sum[:10])
	}
	return label + ".fncpn"
}
func Unwrap(name, suffix string) (string, error) {
	n, err := NormalizeName(name)
	if err != nil {
		return "", err
	}
	if n == suffix {
		return "", nil
	}
	if !strings.HasSuffix(n, "."+suffix) {
		return "", errors.New("name is outside the FnCPN suffix")
	}
	return strings.TrimSuffix(n, "."+suffix), nil
}
func AllowedAddress(value string, prefixes []string) bool {
	a, err := netip.ParseAddr(value)
	if err != nil {
		return false
	}
	if !a.Is4() || a.IsLoopback() || a.IsUnspecified() || a.IsMulticast() || a.IsLinkLocalUnicast() {
		return false
	}
	for _, v := range prefixes {
		p, e := netip.ParsePrefix(v)
		if e == nil && p.Contains(a) {
			return true
		}
	}
	return false
}
