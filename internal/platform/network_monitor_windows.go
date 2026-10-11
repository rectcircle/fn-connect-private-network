//go:build windows

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sys/windows"
)

type windowsNetworkMonitor struct{}

type windowsVirtualInterfaceIndexes struct {
	ipv4 map[uint32]bool
	ipv6 map[uint32]bool
}

func newWindowsVirtualInterfaceIndexes() windowsVirtualInterfaceIndexes {
	return windowsVirtualInterfaceIndexes{ipv4: make(map[uint32]bool), ipv6: make(map[uint32]bool)}
}

func (indexes windowsVirtualInterfaceIndexes) add(family uint16, index uint32) {
	if index == 0 {
		return
	}
	switch family {
	case windows.AF_INET:
		indexes.ipv4[index] = true
	case windows.AF_INET6:
		indexes.ipv6[index] = true
	}
}

func (indexes windowsVirtualInterfaceIndexes) contains(family uint16, index uint32) bool {
	switch family {
	case windows.AF_INET:
		return indexes.ipv4[index]
	case windows.AF_INET6:
		return indexes.ipv6[index]
	default:
		return false
	}
}

func NewNetworkMonitor() windowsNetworkMonitor { return windowsNetworkMonitor{} }

func (windowsNetworkMonitor) Events(ctx context.Context) <-chan model.NetworkChange {
	events := make(chan model.NetworkChange, 1)
	go func() {
		defer close(events)
		previous, initialized := "", false
		lastFingerprintError := ""
		if fingerprint, err := windowsNetworkFingerprint(); err != nil {
			slog.Warn("read initial Windows network fingerprint; polling will retry", "error", err)
			lastFingerprintError = err.Error()
		} else {
			previous, initialized = fingerprint, true
		}
		notifications := make(chan struct{}, 1)
		handles, subscribeErr := subscribeWindowsNetworkChanges(notifications)
		if subscribeErr != nil {
			slog.Warn("some Windows IP Helper change notifications are unavailable; fingerprint polling remains active", "error", subscribeErr)
		}
		defer func() {
			if err := cancelWindowsNetworkChanges(handles); err != nil {
				slog.Warn("cancel Windows IP Helper change notifications", "error", err)
			}
		}()
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		refresh := func() {
			var err error
			previous, initialized, err = publishNetworkChangeIfChanged(events, previous, initialized)
			if err != nil {
				if err.Error() != lastFingerprintError {
					slog.Warn("read Windows network fingerprint; retaining previous state", "error", err)
					lastFingerprintError = err.Error()
				}
				return
			}
			lastFingerprintError = ""
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-notifications:
				refresh()
			case <-ticker.C:
				refresh()
			}
		}
	}()
	return events
}

func publishNetworkChangeIfChanged(events chan<- model.NetworkChange, previous string, initialized bool) (string, bool, error) {
	current, err := windowsNetworkFingerprint()
	if err != nil {
		return previous, initialized, err
	}
	if initialized && current == previous {
		return previous, true, nil
	}
	select {
	case events <- model.NetworkChange{PrimaryNetworkChanged: true}:
	default:
	}
	return current, true, nil
}

func subscribeWindowsNetworkChanges(signals chan<- struct{}) ([]windows.Handle, error) {
	callback := windows.NewCallback(func(_ uintptr, _ uintptr, _ uint32) uintptr {
		select {
		case signals <- struct{}{}:
		default:
		}
		return 0
	})
	registrations := []func(*windows.Handle) error{
		func(handle *windows.Handle) error {
			return windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, callback, nil, false, handle)
		},
		func(handle *windows.Handle) error {
			return windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, callback, nil, false, handle)
		},
		func(handle *windows.Handle) error {
			return windows.NotifyRouteChange2(windows.AF_UNSPEC, callback, nil, false, handle)
		},
	}
	handles := make([]windows.Handle, 0, len(registrations))
	var failures []error
	for _, register := range registrations {
		var handle windows.Handle
		if err := register(&handle); err != nil {
			failures = append(failures, err)
			continue
		}
		handles = append(handles, handle)
	}
	return handles, errors.Join(failures...)
}

func cancelWindowsNetworkChanges(handles []windows.Handle) error {
	var failures []error
	for _, handle := range handles {
		if err := windows.CancelMibChangeNotify2(handle); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func windowsNetworkFingerprint() (string, error) {
	var values []string
	temporaryIPv6, virtualInterfaces, err := windowsAdapterNetworkMetadata()
	if err != nil {
		return "", err
	}
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || isFnCPNVirtualInterface(iface.Name) {
				continue
			}
			addresses, err := iface.Addrs()
			if err != nil {
				return "", fmt.Errorf("read addresses for Windows interface %q: %w", iface.Name, err)
			}
			for _, address := range addresses {
				prefix, err := netip.ParsePrefix(address.String())
				if err == nil && includeNetworkFingerprintAddress(prefix, temporaryIPv6) {
					values = append(values, iface.Name+"/"+prefix.String())
				}
			}
		}
	} else {
		return "", fmt.Errorf("enumerate Windows network interfaces: %w", err)
	}
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		var table *windows.MibIpForwardTable2
		if err := windows.GetIpForwardTable2(family, &table); err != nil {
			if table != nil {
				windows.FreeMibTable(unsafe.Pointer(table))
			}
			if isEmptyWindowsRouteTableError(err) {
				continue
			}
			return "", fmt.Errorf("read Windows IP family %d route table: %w", family, err)
		}
		if table == nil {
			continue
		}
		for _, row := range table.Rows() {
			if row.DestinationPrefix.PrefixLength != 0 || virtualInterfaces.contains(family, row.InterfaceIndex) {
				continue
			}
			values = append(values, fmt.Sprintf("default=%d:%d:%v", row.InterfaceIndex, row.NextHop.Family, row.NextHop.Data))
		}
		windows.FreeMibTable(unsafe.Pointer(table))
	}
	sort.Strings(values)
	digest := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return hex.EncodeToString(digest[:]), nil
}

func isEmptyWindowsRouteTableError(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}

const windowsAdapterMetadataReadAttempts = 3

type windowsAdapterAddressReader func(*windows.IpAdapterAddresses, *uint32) error

func readWindowsAdapterAddressTable(read windowsAdapterAddressReader) ([]byte, error) {
	for attempt := 0; attempt < windowsAdapterMetadataReadAttempts; attempt++ {
		size := uint32(0)
		err := read(nil, &size)
		if errors.Is(err, windows.ERROR_NO_DATA) || size == 0 {
			return nil, nil
		}
		if err != nil && !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			return nil, fmt.Errorf("query Windows adapter table size: %w", err)
		}
		buffer := make([]byte, size)
		adapters := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		if err := read(adapters, &size); errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		} else if err != nil {
			return nil, fmt.Errorf("read Windows adapter table: %w", err)
		}
		return buffer, nil
	}
	return nil, fmt.Errorf("Windows adapter table kept changing during %d reads", windowsAdapterMetadataReadAttempts)
}

func windowsAdapterNetworkMetadata() (map[netip.Addr]struct{}, windowsVirtualInterfaceIndexes, error) {
	temporaryIPv6 := make(map[netip.Addr]struct{})
	virtualInterfaces := newWindowsVirtualInterfaceIndexes()
	buffer, err := readWindowsAdapterAddressTable(func(adapters *windows.IpAdapterAddresses, size *uint32) error {
		return windows.GetAdaptersAddresses(windows.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, adapters, size)
	})
	if err != nil {
		return nil, windowsVirtualInterfaceIndexes{}, err
	}
	if len(buffer) == 0 {
		return temporaryIPv6, virtualInterfaces, nil
	}
	adapters := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
	for adapter := adapters; adapter != nil; adapter = adapter.Next {
		if isFnCPNVirtualInterface(windows.UTF16PtrToString(adapter.FriendlyName)) {
			virtualInterfaces.add(windows.AF_INET, adapter.IfIndex)
			virtualInterfaces.add(windows.AF_INET6, adapter.Ipv6IfIndex)
		}
		for address := adapter.FirstUnicastAddress; address != nil; address = address.Next {
			if address.SuffixOrigin != windows.IpSuffixOriginRandom {
				continue
			}
			ip := address.Address.IP()
			parsed, ok := netip.AddrFromSlice(ip)
			if ok && parsed.Is6() {
				temporaryIPv6[parsed] = struct{}{}
			}
		}
	}
	return temporaryIPv6, virtualInterfaces, nil
}

func includeNetworkFingerprintAddress(prefix netip.Prefix, temporaryIPv6 map[netip.Addr]struct{}) bool {
	if !prefix.IsValid() || prefix.Addr().IsLoopback() || prefix.Addr().IsLinkLocalUnicast() {
		return false
	}
	if prefix.Addr().Is6() {
		_, temporary := temporaryIPv6[prefix.Addr()]
		return !temporary
	}
	return true
}

func isFnCPNVirtualInterface(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "fncpn") || strings.Contains(name, "wintun")
}
