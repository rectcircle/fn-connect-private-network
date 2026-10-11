//go:build windows

package platform

import (
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestNetworkFingerprintIgnoresTemporaryIPv6AndVirtualAdapters(t *testing.T) {
	temporary := netip.MustParseAddr("2001:db8:1::abcd")
	addresses := map[netip.Addr]struct{}{temporary: {}}
	tests := []struct {
		name   string
		prefix string
		want   bool
	}{
		{name: "stable IPv6", prefix: "2001:db8:1::1/64", want: true},
		{name: "temporary IPv6", prefix: "2001:db8:1::abcd/64"},
		{name: "IPv4", prefix: "192.168.1.20/24", want: true},
		{name: "loopback", prefix: "127.0.0.1/8"},
		{name: "link local", prefix: "fe80::1/64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := includeNetworkFingerprintAddress(netip.MustParsePrefix(test.prefix), addresses); got != test.want {
				t.Fatalf("includeNetworkFingerprintAddress(%s) = %v, want %v", test.prefix, got, test.want)
			}
		})
	}

	for _, name := range []string{"FnCPN", "Wintun Userspace Tunnel", "Ethernet", "Wi-Fi"} {
		got := isFnCPNVirtualInterface(name)
		want := name == "FnCPN" || name == "Wintun Userspace Tunnel"
		if got != want {
			t.Errorf("isFnCPNVirtualInterface(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestVirtualInterfaceIndexesRemainScopedToAddressFamily(t *testing.T) {
	indexes := newWindowsVirtualInterfaceIndexes()
	indexes.add(windows.AF_INET, 11)
	indexes.add(windows.AF_INET6, 7)
	if !indexes.contains(windows.AF_INET, 11) || !indexes.contains(windows.AF_INET6, 7) {
		t.Fatal("virtual interface index was not recorded for its address family")
	}
	if indexes.contains(windows.AF_INET, 7) || indexes.contains(windows.AF_INET6, 11) {
		t.Fatal("IPv4 and IPv6 interface indexes aliased across address families")
	}
	indexes.add(windows.AF_INET6, 0)
	if indexes.contains(windows.AF_INET6, 0) {
		t.Fatal("zero interface index was treated as a virtual adapter")
	}
}

func TestWindowsNetworkFingerprintReadsIPHelperTables(t *testing.T) {
	fingerprint, err := windowsNetworkFingerprint()
	if err != nil {
		t.Fatalf("read Windows network fingerprint: %v", err)
	}
	digest, err := hex.DecodeString(fingerprint)
	if err != nil || len(digest) != 32 {
		t.Fatalf("Windows network fingerprint = %q, want a SHA-256 hex digest (err=%v)", fingerprint, err)
	}
}

func TestReadWindowsAdapterAddressTableRetriesConcurrentGrowth(t *testing.T) {
	calls := 0
	buffer, err := readWindowsAdapterAddressTable(func(adapters *windows.IpAdapterAddresses, size *uint32) error {
		calls++
		if adapters == nil {
			if calls == 1 {
				*size = 8
				return windows.ERROR_BUFFER_OVERFLOW
			}
			if calls == 3 {
				*size = 16
				return windows.ERROR_BUFFER_OVERFLOW
			}
			t.Fatalf("unexpected size query on call %d", calls)
		}
		if calls == 2 {
			return windows.ERROR_BUFFER_OVERFLOW
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read adapter table after growth: %v", err)
	}
	if calls != 4 || len(buffer) != 16 {
		t.Fatalf("reader calls=%d buffer bytes=%d, want 4 calls and 16 bytes", calls, len(buffer))
	}
}

func TestReadWindowsAdapterAddressTableFailsAfterBoundedGrowth(t *testing.T) {
	calls := 0
	_, err := readWindowsAdapterAddressTable(func(adapters *windows.IpAdapterAddresses, size *uint32) error {
		calls++
		if adapters == nil {
			*size = uint32(calls * 8)
			return windows.ERROR_BUFFER_OVERFLOW
		}
		return windows.ERROR_BUFFER_OVERFLOW
	})
	if err == nil || !strings.Contains(err.Error(), "kept changing") {
		t.Fatalf("readWindowsAdapterAddressTable() error = %v, want bounded-growth error", err)
	}
	if want := 2 * windowsAdapterMetadataReadAttempts; calls != want {
		t.Fatalf("reader calls=%d, want bounded %d", calls, want)
	}
}

func TestEmptyWindowsRouteTableErrorsAreNotSnapshotFailures(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "no route entries", err: windows.ERROR_NOT_FOUND},
		{name: "address family unsupported", err: windows.ERROR_NOT_SUPPORTED},
	} {
		t.Run(test.name, func(t *testing.T) {
			if !isEmptyWindowsRouteTableError(test.err) {
				t.Fatalf("isEmptyWindowsRouteTableError(%v) = false", test.err)
			}
		})
	}
	if isEmptyWindowsRouteTableError(windows.ERROR_NOT_ENOUGH_MEMORY) {
		t.Fatal("resource failure was treated as an empty route table")
	}
}

func TestWindowsNetworkChangeSubscriptionsRegisterAndCancel(t *testing.T) {
	signals := make(chan struct{}, 1)
	handles, err := subscribeWindowsNetworkChanges(signals)
	if err != nil {
		_ = cancelWindowsNetworkChanges(handles)
		t.Fatalf("subscribe to IP Helper notifications: %v", err)
	}
	if len(handles) != 3 {
		_ = cancelWindowsNetworkChanges(handles)
		t.Fatalf("registered %d IP Helper notifications, want interface, address, and route subscriptions", len(handles))
	}
	if err := cancelWindowsNetworkChanges(handles); err != nil {
		t.Fatalf("cancel IP Helper notifications: %v", err)
	}
	for _, handle := range handles {
		if handle == 0 {
			t.Fatal("IP Helper returned a zero notification handle")
		}
	}
}
