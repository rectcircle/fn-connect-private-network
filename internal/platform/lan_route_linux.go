//go:build linux

package platform

import "github.com/vishvananda/netlink"

func defaultIPv4Interface(routes []netlink.Route) int {
	bestIndex := 0
	bestPriority := int(^uint(0) >> 1)
	for _, route := range routes {
		if route.LinkIndex <= 0 {
			continue
		}
		// netlink may represent the default destination as nil or 0.0.0.0/0.
		if route.Dst != nil {
			ones, bits := route.Dst.Mask.Size()
			if ones != 0 || bits != 32 ||
				route.Dst.IP.To4() == nil || !route.Dst.IP.IsUnspecified() {
				continue
			}
		}
		if route.Priority < bestPriority {
			bestPriority = route.Priority
			bestIndex = route.LinkIndex
		}
	}
	return bestIndex
}
