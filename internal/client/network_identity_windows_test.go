//go:build windows

package client

import (
	"errors"
	"net"
	"testing"

	"golang.org/x/sys/windows"
)

func TestReadWindowsAdapterAddressBufferRetriesTableGrowth(t *testing.T) {
	calls := 0
	buffer, err := readWindowsAdapterAddressBuffer(func(buffer []byte, size *uint32) error {
		calls++
		if buffer == nil {
			if calls == 1 {
				*size = 4
			} else {
				*size = 8
			}
			return windows.ERROR_BUFFER_OVERFLOW
		}
		if calls == 2 {
			*size = 8
			return windows.ERROR_BUFFER_OVERFLOW
		}
		if len(buffer) != 8 {
			return errors.New("retry did not allocate the new adapter table size")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read adapter buffer: %v", err)
	}
	if len(buffer) != 8 || calls != 4 {
		t.Fatalf("read result = buffer length %d after %d calls, want 8 bytes after query/fill retry", len(buffer), calls)
	}
}

func TestReadWindowsAdapterAddressBufferBoundsTopologyRetries(t *testing.T) {
	calls := 0
	_, err := readWindowsAdapterAddressBuffer(func(_ []byte, size *uint32) error {
		calls++
		*size = 8
		return windows.ERROR_BUFFER_OVERFLOW
	})
	if err == nil || calls != windowsAdapterTableReadAttempts*2 {
		t.Fatalf("read result = err %v after %d calls, want bounded retry failure after %d", err, calls, windowsAdapterTableReadAttempts*2)
	}
}

func TestReadWindowsAdapterAddressBufferAcceptsNoAdapters(t *testing.T) {
	calls := 0
	buffer, err := readWindowsAdapterAddressBuffer(func([]byte, *uint32) error {
		calls++
		return windows.ERROR_NO_DATA
	})
	if err != nil || len(buffer) != 0 || calls != 1 {
		t.Fatalf("read empty adapter table = (%d bytes, %v) after %d calls", len(buffer), err, calls)
	}
}

func TestWindowsVirtualAdapterClassification(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "Ethernet", want: false},
		{name: "Wi-Fi", want: false},
		{name: "vEthernet (Default Switch)", want: true},
		{name: "Wintun Userspace Tunnel", want: true},
		{name: "WireGuard Tunnel", want: true},
		{name: "TAP-Windows Adapter V9", want: true},
	} {
		if got := windowsVirtualInterfaceName(test.name); got != test.want {
			t.Errorf("windowsVirtualInterfaceName(%q) = %v, want %v", test.name, got, test.want)
		}
	}
	if !(windowsAdapterInfo{ifType: windows.IF_TYPE_TUNNEL}).virtual() {
		t.Fatal("IP Helper tunnel interface type was treated as physical")
	}
	if (windowsAdapterInfo{ifType: windows.IF_TYPE_IEEE80211, operStatus: windows.IfOperStatusUp}).virtual() {
		t.Fatal("active Wi-Fi interface type was treated as virtual")
	}
	if !(windowsAdapterInfo{ifType: windows.IF_TYPE_ETHERNET_CSMACD, hardwareKnown: true}).virtual() {
		t.Fatal("software-created Ethernet interface was treated as physical")
	}
	if (windowsAdapterInfo{ifType: windows.IF_TYPE_ETHERNET_CSMACD, hardware: true, hardwareKnown: true}).virtual() {
		t.Fatal("hardware Ethernet interface was treated as virtual")
	}
}

func TestWindowsHardwareMetadataFailsClosedWhenInterfaceTableIsIncomplete(t *testing.T) {
	adapter := windowsAdapterInfo{index: 7, luid: 41, name: "Ethernet", ifType: windows.IF_TYPE_ETHERNET_CSMACD}
	adapters := map[uint64]windowsAdapterInfo{
		41: adapter,
	}
	if _, err := applyWindowsHardwareMetadata(adapters, nil); err == nil {
		t.Fatal("missing hardware interface table unexpectedly accepted")
	}
	if _, err := applyWindowsHardwareMetadata(adapters, map[uint64]bool{}); err == nil {
		t.Fatal("incomplete hardware interface table unexpectedly accepted")
	}
	classified, err := applyWindowsHardwareMetadata(adapters, map[uint64]bool{41: false})
	if err != nil {
		t.Fatal(err)
	}
	if adapter := classified[41]; !adapter.hardwareKnown || adapter.virtual() != true {
		t.Fatalf("software adapter classification at LUID 41 = %+v, want known virtual adapter", adapter)
	}
}

func TestWindowsEmptyIPv4RouteTableAllowsPhysicalInterfaceFallback(t *testing.T) {
	if !isEmptyWindowsIPv4RouteTableError(windows.ERROR_NOT_FOUND) {
		t.Fatal("ERROR_NOT_FOUND was not classified as an empty IPv4 route table")
	}
	if !isEmptyWindowsIPv4RouteTableError(windows.ERROR_NOT_SUPPORTED) {
		t.Fatal("ERROR_NOT_SUPPORTED was not classified as an unavailable IPv4 route table")
	}
	if isEmptyWindowsIPv4RouteTableError(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("an unrelated route table error was classified as empty")
	}

	adapters := map[uint64]windowsAdapterInfo{
		41: {
			index: 4, luid: 41, name: "Ethernet", ifType: windows.IF_TYPE_ETHERNET_CSMACD,
			operStatus: windows.IfOperStatusUp, ipv4Metric: 35, hardware: true, hardwareKnown: true,
		},
		72: {
			index: 7, luid: 72, name: "Wi-Fi", ifType: windows.IF_TYPE_IEEE80211,
			operStatus: windows.IfOperStatusUp, ipv4Metric: 10, hardware: true, hardwareKnown: true,
		},
		99: {
			index: 12, luid: 99, name: "Wintun Userspace Tunnel", ifType: windows.IF_TYPE_TUNNEL,
			operStatus: windows.IfOperStatusUp, ipv4Metric: 1, hardwareKnown: true,
		},
	}
	name, ok := selectWindowsPhysicalInterface(adapters, nil)
	if !ok || name != "Wi-Fi" {
		t.Fatalf("empty route table selected interface (%q, %v), want lowest-metric active physical interface Wi-Fi", name, ok)
	}
}

func TestWindowsPhysicalRouteSelectionUsesLUIDAcrossIndexFamilies(t *testing.T) {
	// The virtual adapter's IPv6 index overlaps the physical adapter's IPv4
	// index. A family-agnostic index map could misclassify this physical route.
	adapters := map[uint64]windowsAdapterInfo{
		41: {index: 7, luid: 41, name: "Ethernet", ifType: windows.IF_TYPE_ETHERNET_CSMACD, operStatus: windows.IfOperStatusUp, hardware: true, hardwareKnown: true},
		99: {index: 7, luid: 99, name: "Wintun Userspace Tunnel", ifType: windows.IF_TYPE_TUNNEL, operStatus: windows.IfOperStatusUp, hardwareKnown: true},
	}
	routes := []windows.MibIpForwardRow2{{
		InterfaceLuid:  41,
		InterfaceIndex: 7,
		DestinationPrefix: windows.IpAddressPrefix{
			Prefix: windows.RawSockaddrInet{Family: windows.AF_INET},
		},
	}}
	name, ok := selectWindowsPhysicalInterface(adapters, routes)
	if !ok || name != "Ethernet" {
		t.Fatalf("physical IPv4 default route selected (%q, %v), want Ethernet by LUID", name, ok)
	}
}

func TestWindowsProbeSocketBindsToPhysicalInterface(t *testing.T) {
	name, err := defaultPhysicalInterface()
	if err != nil {
		t.Skipf("no active physical IPv4 interface is available in this environment: %v", err)
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		t.Fatalf("resolve selected physical interface %q: %v", name, err)
	}
	socket, err := windows.Socket(windows.AF_INET, windows.SOCK_DGRAM, windows.IPPROTO_UDP)
	if err != nil {
		t.Fatalf("create IPv4 probe socket: %v", err)
	}
	defer windows.Closesocket(socket)
	if err := bindSocketToInterface(uintptr(socket), iface.Index); err != nil {
		t.Fatalf("set IP_UNICAST_IF for %q (index %d): %v", name, iface.Index, err)
	}
}

func TestWindowsProbeSocketRejectsMissingInterface(t *testing.T) {
	if err := bindSocketToInterface(1, 0); err == nil {
		t.Fatal("binding to a missing physical interface unexpectedly succeeded")
	}
}
