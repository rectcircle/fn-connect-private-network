//go:build darwin

package privileged

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func validatePlatformDNS(p model.DNSPlan) error {
	if p.Port < 1024 {
		return errors.New("macOS DNS must use an unprivileged port")
	}
	return nil
}
func dnsRulePath(p model.DNSPlan) string { return filepath.Join("/etc/resolver", p.Suffix) }
func dnsRuleContent(p model.DNSPlan) []byte {
	return []byte(fmt.Sprintf("# FnCPN %s\nnameserver %s\nport %d\ntimeout 3\n", p.Generation, p.Address, p.Port))
}
func installDNSRule(p model.DNSPlan) error {
	dir := "/etc/resolver"
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("resolver directory is not a real directory")
	}

	f, err := os.CreateTemp(dir, ".fncpn-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0644); err != nil {
		f.Close()
		return err
	}
	_, err = f.Write(dnsRuleContent(p))
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	// Link is atomic and refuses to replace an existing file or symlink.
	return os.Link(f.Name(), dnsRulePath(p))
}
func verifyDNSRule(p model.DNSPlan) error {
	info, err := os.Lstat(dnsRulePath(p))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("DNS resolver file was replaced")
	}
	content, err := os.ReadFile(dnsRulePath(p))
	if err != nil {
		return err
	}
	if !bytes.Equal(content, dnsRuleContent(p)) {
		return errors.New("DNS resolver file is owned or modified by another program")
	}
	return nil
}

func removeDNSRule(p model.DNSPlan) error {
	path := dnsRulePath(p)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("DNS resolver file was replaced")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, dnsRuleContent(p)) {
		return errors.New("DNS resolver file is owned or modified by another program")
	}
	return os.Remove(path)
}
