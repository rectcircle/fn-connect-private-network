//go:build linux

package linux

import (
	"fmt"
	"net/netip"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Stage changes in the same transaction as the private table. An empty plan
// removes our rules; the journal's table name is also their ownership marker.
func stageDockerForwarding(
	connection *nftables.Conn,
	owner string,
	interfaceName string,
	plan model.ServerPlan,
) error {
	chains, err := connection.ListChainsOfTableFamily(nftables.TableFamilyIPv4)
	if err != nil {
		return fmt.Errorf("list Docker forwarding chains: %w", err)
	}
	var chain *nftables.Chain
	for _, candidate := range chains {
		if candidate.Table.Name == "filter" && candidate.Name == "DOCKER-USER" {
			chain = candidate
			break
		}
	}
	if chain == nil {
		return nil
	}
	rules, err := connection.GetRules(chain.Table, chain)
	if err != nil {
		return fmt.Errorf("read Docker user rules: %w", err)
	}
	marker := owner + ":forward"
	var beforeReturn uint64
	for _, rule := range rules {
		comment, _ := userdata.GetString(rule.UserData, userdata.TypeComment)
		if comment == marker {
			if err := connection.DelRule(rule); err != nil {
				return fmt.Errorf("remove owned Docker forwarding rule: %w", err)
			}
		} else if beforeReturn == 0 && unconditionalReturn(rule) {
			beforeReturn = rule.Handle
		}
	}
	if len(plan.LANCIDRs) == 0 {
		return nil
	}
	overlay, err := netip.ParsePrefix(plan.OverlayCIDR)
	if err != nil {
		return fmt.Errorf("parse Docker forwarding overlay: %w", err)
	}
	for _, value := range plan.LANCIDRs {
		lan, err := netip.ParsePrefix(value)
		if err != nil {
			return fmt.Errorf("parse Docker forwarding LAN: %w", err)
		}
		for _, direction := range []struct {
			key                 expr.MetaKey
			source, destination netip.Prefix
		}{
			{expr.MetaKeyIIFNAME, overlay, lan},
			{expr.MetaKeyOIFNAME, lan, overlay},
		} {
			expressions := interfaceMatch(direction.key, interfaceName)
			// The ip table already selects IPv4. Keep these expressions in the
			// form supported by iptables-nft, including its per-rule counter.
			expressions = append(expressions, ipv4AddressMatch(12, direction.source)...)
			expressions = append(expressions, ipv4AddressMatch(16, direction.destination)...)
			expressions = append(expressions, &expr.Counter{}, &expr.Verdict{Kind: expr.VerdictAccept})
			rule := &nftables.Rule{
				Table:    chain.Table,
				Chain:    chain,
				Exprs:    expressions,
				UserData: userdata.AppendString(nil, userdata.TypeComment, marker),
			}
			// Preserve administrator rules ahead of Docker's terminal RETURN.
			// Appending after that RETURN would make our rules unreachable.
			if beforeReturn != 0 {
				rule.Position = beforeReturn
				connection.InsertRule(rule)
			} else {
				connection.AddRule(rule)
			}
		}
	}
	return nil
}

func unconditionalReturn(rule *nftables.Rule) bool {
	found := false
	for _, expression := range rule.Exprs {
		switch value := expression.(type) {
		case *expr.Counter:
		case *expr.Verdict:
			if value.Kind != expr.VerdictReturn {
				return false
			}
			found = true
		default:
			return false
		}
	}
	return found
}
