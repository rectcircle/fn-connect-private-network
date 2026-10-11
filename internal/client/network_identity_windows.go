//go:build windows

package client

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsAdapterInfo struct {
	index         uint32
	luid          uint64
	name          string
	ifType        uint32
	operStatus    uint32
	ipv4Metric    uint32
	tunnelType    uint32
	hardware      bool
	hardwareKnown bool
}

func defaultPhysicalInterface() (string, error) {
	adapters, err := windowsAdapterInfoByLUID()
	if err != nil {
		return "", err
	}
	var routes []windows.MibIpForwardRow2
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		if table != nil {
			windows.FreeMibTable(unsafe.Pointer(table))
			table = nil
		}
		if !isEmptyWindowsIPv4RouteTableError(err) {
			return "", fmt.Errorf("read Windows IPv4 default routes: %w", err)
		}
	}
	if table != nil {
		defer windows.FreeMibTable(unsafe.Pointer(table))
		routes = table.Rows()
	}
	name, ok := selectWindowsPhysicalInterface(adapters, routes)
	if !ok {
		return "", errors.New("no active physical IPv4 interface was found")
	}
	return name, nil
}

func selectWindowsPhysicalInterface(adapters map[uint64]windowsAdapterInfo, routes []windows.MibIpForwardRow2) (string, bool) {
	var candidates []physicalInterfaceCandidate
	for _, route := range routes {
		if route.DestinationPrefix.PrefixLength != 0 || route.DestinationPrefix.Prefix.Family != windows.AF_INET {
			continue
		}
		adapter, ok := adapters[route.InterfaceLuid]
		if !ok || !adapter.operational() || adapter.virtual() {
			continue
		}
		candidates = append(candidates, physicalInterfaceCandidate{
			name:   adapter.name,
			index:  adapter.index,
			metric: uint64(route.Metric) + uint64(adapter.ipv4Metric),
		})
	}
	if len(candidates) == 0 {
		for _, adapter := range adapters {
			if adapter.operational() && !adapter.virtual() {
				candidates = append(candidates, physicalInterfaceCandidate{
					name: adapter.name, index: adapter.index, metric: uint64(adapter.ipv4Metric),
				})
			}
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].metric != candidates[j].metric {
			return candidates[i].metric < candidates[j].metric
		}
		return candidates[i].index < candidates[j].index
	})
	return candidates[0].name, true
}

func isEmptyWindowsIPv4RouteTableError(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}

type physicalInterfaceCandidate struct {
	name   string
	index  uint32
	metric uint64
}

const windowsAdapterTableReadAttempts = 3

type windowsAdapterAddressReader func([]byte, *uint32) error

// readWindowsAdapterAddressBuffer retries the normal size-query/fill sequence
// when the adapter table grows between those calls, which is common during a
// Wi-Fi handoff or virtual adapter start/stop.
func readWindowsAdapterAddressBuffer(read windowsAdapterAddressReader) ([]byte, error) {
	for attempt := 0; attempt < windowsAdapterTableReadAttempts; attempt++ {
		size := uint32(0)
		err := read(nil, &size)
		if err == windows.ERROR_NO_DATA || size == 0 {
			return nil, nil
		}
		if err != nil && !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil, fmt.Errorf("read Windows adapter table size: %w", err)
		}
		buffer := make([]byte, size)
		if err := read(buffer, &size); errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("read Windows adapter table: %w", err)
		}
		return buffer, nil
	}
	return nil, fmt.Errorf("Windows adapter table kept changing during %d reads", windowsAdapterTableReadAttempts)
}

func (a windowsAdapterInfo) operational() bool { return a.operStatus == windows.IfOperStatusUp }

func (a windowsAdapterInfo) virtual() bool {
	if a.ifType == windows.IF_TYPE_TUNNEL || a.ifType == windows.IF_TYPE_PPP || a.ifType == windows.IF_TYPE_SOFTWARE_LOOPBACK || a.tunnelType != 0 {
		return true
	}
	if a.hardwareKnown && !a.hardware {
		return true
	}
	return windowsVirtualInterfaceName(a.name)
}

func windowsVirtualInterfaceName(name string) bool {
	name = strings.ToLower(name)
	for _, prefix := range []string{
		"vethernet", "wintun", "wireguard", "tap-windows", "openvpn", "hyper-v",
		"virtualbox", "vmware", "tailscale", "zerotier", "teredo", "isatap",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func windowsAdapterInfoByLUID() (map[uint64]windowsAdapterInfo, error) {
	buffer, err := readWindowsAdapterAddressBuffer(func(buffer []byte, size *uint32) error {
		var adapters *windows.IpAdapterAddresses
		if len(buffer) > 0 {
			adapters = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		}
		return windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, adapters, size)
	})
	if err != nil {
		return nil, err
	}
	if len(buffer) == 0 {
		return map[uint64]windowsAdapterInfo{}, nil
	}
	adapters := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
	result := make(map[uint64]windowsAdapterInfo)
	for adapter := adapters; adapter != nil; adapter = adapter.Next {
		if adapter.Luid == 0 {
			return nil, errors.New("Windows adapter has no stable interface LUID")
		}
		index := adapter.IfIndex
		if index == 0 {
			index = adapter.Ipv6IfIndex
		}
		value := windowsAdapterInfo{
			index:      index,
			luid:       adapter.Luid,
			name:       windows.UTF16PtrToString(adapter.FriendlyName),
			ifType:     adapter.IfType,
			operStatus: adapter.OperStatus,
			ipv4Metric: adapter.Ipv4Metric,
			tunnelType: adapter.TunnelType,
		}
		result[adapter.Luid] = value
	}
	hardware, err := windowsHardwareInterfaces()
	if err != nil {
		return nil, fmt.Errorf("classify Windows adapter hardware state: %w", err)
	}
	return applyWindowsHardwareMetadata(result, hardware)
}

func applyWindowsHardwareMetadata(adapters map[uint64]windowsAdapterInfo, hardware map[uint64]bool) (map[uint64]windowsAdapterInfo, error) {
	if hardware == nil {
		return nil, errors.New("Windows hardware interface table is unavailable")
	}
	for luid, value := range adapters {
		if luid == 0 || value.luid != luid {
			return nil, errors.New("Windows adapter has no stable interface LUID")
		}
		isHardware, ok := hardware[luid]
		if !ok {
			return nil, fmt.Errorf("Windows interface table has no hardware classification for adapter LUID %d", luid)
		}
		value.hardware = isHardware
		value.hardwareKnown = true
		adapters[luid] = value
	}
	return adapters, nil
}

func windowsHardwareInterfaces() (map[uint64]bool, error) {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormalWithoutStatistics, &table); err != nil {
		return nil, err
	}
	if table == nil {
		return nil, errors.New("Windows returned an empty interface table")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	result := make(map[uint64]bool, len(rows))
	const hardwareInterfaceFlag = 1
	for _, row := range rows {
		result[row.InterfaceLuid] = row.InterfaceAndOperStatusFlags&hardwareInterfaceFlag != 0
	}
	return result, nil
}

func enrichPhysicalNetwork(snapshot *NetworkSnapshot) error {
	adapters, err := windowsAdapterInfoByLUID()
	if err != nil {
		return fmt.Errorf("read Windows physical interface metadata: %w", err)
	}
	virtualNames := make(map[string]struct{})
	for _, adapter := range adapters {
		if adapter.virtual() {
			virtualNames[strings.ToLower(adapter.name)] = struct{}{}
		}
	}
	links := snapshot.Links[:0]
	for _, link := range snapshot.Links {
		if windowsVirtualInterfaceName(link.Name) {
			continue
		}
		if _, ok := virtualNames[strings.ToLower(link.Name)]; ok {
			continue
		}
		links = append(links, link)
	}
	snapshot.Links = links
	ipv6Networks := snapshot.IPv6Networks[:0]
	for _, network := range snapshot.IPv6Networks {
		name, _, found := strings.Cut(network, ":")
		if !found {
			ipv6Networks = append(ipv6Networks, network)
			continue
		}
		if windowsVirtualInterfaceName(name) {
			continue
		}
		if _, virtual := virtualNames[strings.ToLower(name)]; virtual {
			continue
		}
		ipv6Networks = append(ipv6Networks, network)
	}
	snapshot.IPv6Networks = ipv6Networks
	return nil
}
