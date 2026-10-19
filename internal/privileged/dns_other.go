//go:build !darwin && !windows

package privileged

import (
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func validatePlatformDNS(model.DNSPlan) error {
	return errors.New("split DNS unsupported on this platform")
}
func installDNSRule(model.DNSPlan) error { return errors.New("split DNS unsupported on this platform") }
func removeDNSRule(model.DNSPlan) error  { return nil }

func verifyDNSRule(model.DNSPlan) error { return nil }
