//go:build linux

package platform

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestDefaultIPv4Interface(t *testing.T) {
	prefix := func(value string) *net.IPNet {
		t.Helper()
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			t.Fatal(err)
		}
		return network
	}
	// netlink v1.3.1 fills an absent IPv4 RTA_DST with IPv4zero and a /0 mask.
	defaultDestination := &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)}
	tests := []struct {
		name   string
		routes []netlink.Route
		want   int
	}{
		{
			name: "legacy nil destination",
			routes: []netlink.Route{
				{LinkIndex: 2, Priority: 600},
			},
			want: 2,
		},
		{
			name: "fnOS default alongside overlay LAN and Docker routes",
			routes: []netlink.Route{
				{Dst: defaultDestination, LinkIndex: 2, Priority: 600},
				{Dst: prefix("10.253.203.0/24"), LinkIndex: 3},
				{Dst: prefix("172.17.0.0/16"), LinkIndex: 4},
				{Dst: prefix("172.22.0.0/16"), LinkIndex: 5},
				{Dst: prefix("192.168.71.0/24"), LinkIndex: 2, Priority: 600},
			},
			want: 2,
		},
		{
			name: "lower metric explicit default wins over nil destination",
			routes: []netlink.Route{
				{LinkIndex: 2, Priority: 600},
				{Dst: defaultDestination, LinkIndex: 3, Priority: 100},
			},
			want: 3,
		},
		{
			name: "lower metric nil destination wins over explicit default",
			routes: []netlink.Route{
				{LinkIndex: 2, Priority: 100},
				{Dst: defaultDestination, LinkIndex: 3, Priority: 600},
			},
			want: 2,
		},
		{
			name: "equal metrics preserve first route",
			routes: []netlink.Route{
				{Dst: defaultDestination, LinkIndex: 2, Priority: 100},
				{Dst: defaultDestination, LinkIndex: 3, Priority: 100},
			},
			want: 2,
		},
		{
			name: "ignore missing interface and IPv6 default",
			routes: []netlink.Route{
				{Dst: defaultDestination, LinkIndex: 0},
				{LinkIndex: -1},
				{Dst: prefix("::/0"), LinkIndex: 3},
				{Dst: defaultDestination, LinkIndex: 2, Priority: 600},
			},
			want: 2,
		},
		{
			name: "nondefault zero address and malformed mask",
			routes: []netlink.Route{
				{Dst: prefix("0.0.0.0/8"), LinkIndex: 2},
				{Dst: &net.IPNet{IP: net.IPv4zero}, LinkIndex: 3},
			},
		},
		{
			name: "no default route",
			routes: []netlink.Route{
				{Dst: prefix("192.168.71.0/24"), LinkIndex: 2},
			},
		},
		{name: "empty route list"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := defaultIPv4Interface(test.routes); got != test.want {
				t.Fatalf("defaultIPv4Interface() = %d, want %d", got, test.want)
			}
		})
	}
}
