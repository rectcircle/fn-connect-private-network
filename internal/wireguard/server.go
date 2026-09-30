package wireguard

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const maxServerPeers = 4096

func NormalizeServerPlan(plan model.ServerPlan) (model.ServerPlan, error) {
	overlay, err := netip.ParsePrefix(plan.OverlayCIDR)
	if err != nil ||
		!overlay.Addr().Is4() ||
		overlay != overlay.Masked() ||
		overlay.Bits() < 16 ||
		overlay.Bits() > 29 {
		return model.ServerPlan{}, errors.New(
			"overlay CIDR must be a canonical IPv4 /16-/29 prefix",
		)
	}
	if !overlay.Addr().IsPrivate() && !sharedIPv4Prefix.Contains(overlay.Addr()) {
		return model.ServerPlan{}, errors.New(
			"overlay CIDR must use RFC1918 or 100.64.0.0/10 space",
		)
	}
	serverAddress, err := netip.ParsePrefix(plan.ServerAddress)
	if err != nil ||
		!serverAddress.Addr().Is4() ||
		serverAddress.Bits() != overlay.Bits() ||
		serverAddress.Addr() != overlay.Addr().Next() {
		return model.ServerPlan{}, errors.New(
			"server address must be the first usable overlay address",
		)
	}
	if plan.ListenPort == 0 {
		return model.ServerPlan{}, errors.New("listen port is required")
	}
	if len(plan.LANCIDRs) > 32 {
		return model.ServerPlan{}, errors.New("too many LAN CIDRs")
	}
	for index, value := range plan.LANCIDRs {
		prefix, err := ParsePrivateIPv4Prefix(value)
		if err != nil {
			return model.ServerPlan{}, fmt.Errorf(
				"invalid LAN CIDR %q: %w",
				value,
				err,
			)
		}
		if prefixesOverlap(overlay, prefix) {
			return model.ServerPlan{}, fmt.Errorf(
				"LAN CIDR %q overlaps the overlay",
				value,
			)
		}
		plan.LANCIDRs[index] = prefix.String()
	}
	if len(plan.Peers) > maxServerPeers {
		return model.ServerPlan{}, errors.New("too many WireGuard peers")
	}

	keys := make(map[string]struct{}, len(plan.Peers))
	addresses := make(map[netip.Addr]struct{}, len(plan.Peers))
	for index := range plan.Peers {
		peer := &plan.Peers[index]
		peer.PublicKey = strings.TrimSpace(peer.PublicKey)
		if _, err := decodeKey(peer.PublicKey); err != nil {
			return model.ServerPlan{}, fmt.Errorf(
				"invalid peer %d public key: %w",
				index,
				err,
			)
		}
		if _, exists := keys[peer.PublicKey]; exists {
			return model.ServerPlan{}, errors.New("duplicate peer public key")
		}
		keys[peer.PublicKey] = struct{}{}

		address, err := netip.ParsePrefix(peer.Address)
		if err != nil ||
			!address.Addr().Is4() ||
			address.Bits() != 32 ||
			!overlay.Contains(address.Addr()) ||
			address.Addr() == overlay.Addr() ||
			address.Addr() == serverAddress.Addr() ||
			!overlay.Contains(address.Addr().Next()) {
			return model.ServerPlan{}, fmt.Errorf(
				"invalid overlay address for peer %d",
				index,
			)
		}
		if _, exists := addresses[address.Addr()]; exists {
			return model.ServerPlan{}, errors.New("duplicate peer overlay address")
		}
		addresses[address.Addr()] = struct{}{}
		peer.Address = address.String()
	}
	plan.OverlayCIDR = overlay.String()
	plan.ServerAddress = serverAddress.String()
	return plan, nil
}

func prefixesOverlap(first, second netip.Prefix) bool {
	return first.Contains(second.Addr()) || second.Contains(first.Addr())
}
