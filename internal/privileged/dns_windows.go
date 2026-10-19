//go:build windows

package privileged

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const nrptBase = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
const nrptID = `{BED5F570-4667-48EE-A05B-0AD62E12F895}`

var dnsapi = windows.NewLazySystemDLL("dnsapi.dll")

func flushDNS() { _, _, _ = dnsapi.NewProc("DnsFlushResolverCache").Call() }
func validatePlatformDNS(p model.DNSPlan) error {
	return dns.ValidateWindowsListener(p.Address, p.Port)
}

func installDNSRule(p model.DNSPlan) error {
	for _, base := range []string{nrptBase, `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`} {
		root, err := registry.OpenKey(registry.LOCAL_MACHINE, base, registry.READ)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		names, err := root.ReadSubKeyNames(-1)
		root.Close()
		if err != nil {
			return err
		}
		for _, name := range names {
			k, e := registry.OpenKey(registry.LOCAL_MACHINE, base+`\`+name, registry.READ)
			if e != nil {
				return e
			}
			domains, _, _ := k.GetStringsValue("Name")
			k.Close()
			for _, d := range domains {
				d = strings.TrimPrefix(strings.ToLower(d), ".")
				if d == "" || d == p.Suffix || strings.HasSuffix(d, "."+p.Suffix) || strings.HasSuffix(p.Suffix, "."+d) {
					return errors.New("existing NRPT namespace conflicts with FnCPN")
				}
			}
		}
	}
	path := nrptBase + `\` + nrptID
	k, exists, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	if exists {
		k.Close()
		return errors.New("FnCPN NRPT identifier is already in use")
	}
	failed := true
	defer func() {
		k.Close()
		if failed {
			_ = registry.DeleteKey(registry.LOCAL_MACHINE, path)
			flushDNS()
		}
	}()
	for _, entry := range []struct {
		name  string
		value uint32
	}{{"Version", 2}, {"ConfigOptions", 8}} {
		if err = k.SetDWordValue(entry.name, entry.value); err != nil {
			return err
		}
	}
	if err = k.SetStringsValue("Name", []string{"." + p.Suffix}); err != nil {
		return err
	}
	if err = k.SetStringValue("GenericDNSServers", p.Address); err != nil {
		return err
	}
	if err = k.SetStringValue("FnCPNGeneration", p.Generation); err != nil {
		return err
	}
	flushDNS()
	if err := verifyEffectiveDNSPolicy(p); err != nil {
		return err
	}
	failed = false
	return nil
}
func removeDNSRule(p model.DNSPlan) error {
	path := nrptBase + `\` + nrptID
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	owner, _, e := k.GetStringValue("FnCPNGeneration")
	domains, _, e2 := k.GetStringsValue("Name")
	server, _, e3 := k.GetStringValue("GenericDNSServers")
	k.Close()
	if e != nil || e2 != nil || e3 != nil || owner != p.Generation || len(domains) != 1 || domains[0] != "."+p.Suffix || server != p.Address {
		return errors.New("NRPT rule was modified by another program")
	}
	err = registry.DeleteKey(registry.LOCAL_MACHINE, path)
	flushDNS()
	return err
}

func verifyDNSRule(p model.DNSPlan) error {

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, nrptBase+`\`+nrptID, registry.READ)
	if err != nil {
		return err
	}
	defer k.Close()
	generation, _, err := k.GetStringValue("FnCPNGeneration")
	if err != nil {
		return err
	}
	domains, _, err := k.GetStringsValue("Name")
	if err != nil {
		return err
	}
	servers, _, err := k.GetStringValue("GenericDNSServers")
	if err != nil {
		return err
	}
	if generation != p.Generation || len(domains) != 1 || domains[0] != "."+p.Suffix || servers != p.Address {
		return errors.New("NRPT rule was modified by another program")
	}
	return verifyEffectiveDNSPolicy(p)
}

// A policy container alone does not prove that our namespace is overridden.
// Only machines with a policy container need the effective-policy query.
func verifyEffectiveDNSPolicy(p model.DNSPlan) error {
	policy, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`, registry.READ)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	policy.Close()
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// The suffix and address have already passed DNSPlan validation (ASCII labels
	// and IPv4 loopback), so neither can introduce PowerShell syntax.
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $r=@(Get-DnsClientNrptPolicy -Effective | Where-Object { $_.Namespace -eq '.%s' }); if ($r.Count -eq 1 -and @($r[0].NameServers).Count -eq 1 -and $r[0].NameServers -eq '%s') { exit 0 }; exit 1`, p.Suffix, p.Address)
	if err := exec.CommandContext(ctx, filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", script).Run(); err != nil {
		return fmt.Errorf("FnCPN DNS rule is not effective under Windows policy: %w", err)
	}
	return nil
}
