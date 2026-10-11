//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSetInterfaceMTUPreservesSettingsAndNormalizesIPv4SitePrefix(t *testing.T) {
	for _, family := range []uint16{windows.AF_INET, windows.AF_INET6} {
		err := setInterfaceMTUWithOps(17, family, 1380, func(row *windows.MibIpInterfaceRow) error {
			row.SitePrefixLength = 64
			row.Metric = 42
			return nil
		}, func(row *windows.MibIpInterfaceRow) error {
			if family == windows.AF_INET && row.SitePrefixLength != 0 {
				return windows.ERROR_INVALID_PARAMETER
			}
			if family == windows.AF_INET6 && row.SitePrefixLength != 64 {
				t.Fatal("IPv6 prefix changed")
			}
			if row.NlMtu != 1380 || row.InterfaceIndex != 17 || row.Metric != 42 {
				t.Fatal("interface settings lost")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	want := windows.ERROR_ACCESS_DENIED
	if err := setInterfaceMTUWithOps(17, windows.AF_INET, 1380, func(*windows.MibIpInterfaceRow) error { return want }, func(*windows.MibIpInterfaceRow) error { t.Fatal("write after failed read"); return nil }); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestInterfaceIndexByGUIDUsesCompleteInterfaceRows(t *testing.T) {
	guid, err := windows.GUIDFromString("{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}")
	if err != nil {
		t.Fatal(err)
	}
	rows := []windows.MibIfRow2{{InterfaceGuid: guid, InterfaceIndex: 19}}
	index, err := interfaceIndexByGUID(rows, strings.ToLower(guid.String()))
	if err != nil || index != 19 {
		t.Fatalf("interfaceIndexByGUID = %d, %v; want 19, nil", index, err)
	}
	if _, err := interfaceIndexByGUID(nil, guid.String()); !errors.Is(err, errNetworkInterfaceNotFound) {
		t.Fatalf("missing GUID error = %v, want confirmed-not-found sentinel", err)
	}
	rows[0].InterfaceIndex = 0
	if _, err := interfaceIndexByGUID(rows, guid.String()); err == nil || errors.Is(err, errNetworkInterfaceNotFound) {
		t.Fatalf("zero-index error = %v, want invalid matching row", err)
	}
	if _, err := interfaceIndexByGUID(nil, "{00000000-0000-0000-0000-000000000000}"); err == nil || errors.Is(err, errNetworkInterfaceNotFound) {
		t.Fatalf("zero GUID error = %v, want invalid identity rather than absent adapter", err)
	}
}

func TestReadWindowsInterfaceRowsReturnsStableInterfaceIdentity(t *testing.T) {
	rows, err := readWindowsInterfaceRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("GetIfTable2Ex returned no local interfaces")
	}
	for _, row := range rows {
		if row.InterfaceIndex == 0 || row.InterfaceGuid == (windows.GUID{}) {
			continue
		}
		index, err := interfaceIndexByGUID(rows, row.InterfaceGuid.String())
		if err != nil || index != row.InterfaceIndex {
			t.Fatalf("interface identity round trip = %d, %v; want %d", index, err, row.InterfaceIndex)
		}
		guid, err := interfaceGUID(row.InterfaceIndex)
		if err != nil || guid != row.InterfaceGuid.String() {
			t.Fatalf("interfaceGUID(%d) = %q, %v; want %q", row.InterfaceIndex, guid, err, row.InterfaceGuid.String())
		}
		return
	}
	t.Fatal("GetIfTable2Ex returned no interface with a stable index and GUID")
}

func TestPhysicalIPv4PrefixesFailsClosedWhenAddressEnumerationFails(t *testing.T) {
	enumerationErr := errors.New("address table unavailable")
	interfaces := []net.Interface{
		{Index: 1, Name: "Wi-Fi", Flags: net.FlagUp},
		{Index: 2, Name: "FnCPN", Flags: net.FlagUp},
	}
	_, err := physicalIPv4Prefixes(2, interfaces, func(iface net.Interface) ([]net.Addr, error) {
		if iface.Index == 1 {
			return nil, enumerationErr
		}
		return nil, nil
	})
	if !errors.Is(err, enumerationErr) {
		t.Fatalf("physicalIPv4Prefixes error = %v, want wrapped address-table error", err)
	}
}

func TestSafeRoutesTreatsEmptyIPv4RouteTableAsNoAdditionalConflicts(t *testing.T) {
	serverOverlay := netip.MustParsePrefix("10.253.203.1/32")
	allowed := []string{serverOverlay.String(), "192.168.50.0/24", "172.20.0.0/16"}
	interfaces := []net.Interface{
		{Index: 4, Name: "FnCPN", Flags: net.FlagUp},
		{Index: 7, Name: "Ethernet", Flags: net.FlagUp},
	}
	addresses := func(iface net.Interface) ([]net.Addr, error) {
		if iface.Index != 7 {
			return nil, nil
		}
		_, prefix, err := net.ParseCIDR("192.168.50.0/24")
		if err != nil {
			return nil, err
		}
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.168.50.4"), Mask: prefix.Mask}}, nil
	}
	got, err := safeRoutesWithReaders(4, serverOverlay, allowed, interfaces, addresses, func() ([]windows.MibIpForwardRow2, error) {
		return nil, windows.ERROR_NOT_FOUND
	})
	if err != nil {
		t.Fatalf("safeRoutesWithReaders on an empty IPv4 route table: %v", err)
	}
	want := []netip.Prefix{serverOverlay, netip.MustParsePrefix("172.20.0.0/16")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("safeRoutesWithReaders = %v, want %v", got, want)
	}
}

func TestSafeRoutesStillFailsClosedOnUnexpectedRouteTableError(t *testing.T) {
	routeErr := windows.ERROR_ACCESS_DENIED
	_, err := safeRoutesWithReaders(4, netip.MustParsePrefix("10.253.203.1/32"), []string{"10.253.203.1/32"}, nil,
		func(net.Interface) ([]net.Addr, error) { return nil, nil },
		func() ([]windows.MibIpForwardRow2, error) { return nil, routeErr })
	if !errors.Is(err, routeErr) {
		t.Fatalf("safeRoutesWithReaders error = %v, want wrapped route table error", err)
	}
}

type fakeNetworkApplyOps struct {
	resolvedIndex  uint32
	oldMTU         uint32
	failures       map[string]error
	events         []string
	deletedRoutes  []netip.Prefix
	deletedAddress netip.Prefix
	setMTUs        []uint32
}

func (f *fakeNetworkApplyOps) record(event string) error {
	f.events = append(f.events, event)
	return f.failures[event]
}

func (f *fakeNetworkApplyOps) InterfaceIndexByGUID(string) (uint32, error) {
	return f.resolvedIndex, nil
}

func (f *fakeNetworkApplyOps) GetMTU(uint32, uint16) (uint32, error) {
	if err := f.record("get-mtu"); err != nil {
		return 0, err
	}
	return f.oldMTU, nil
}

func (f *fakeNetworkApplyOps) SetMTU(_ uint32, _ uint16, mtu uint32) error {
	f.setMTUs = append(f.setMTUs, mtu)
	return f.record(fmt.Sprintf("set-mtu:%d", mtu))
}

func (f *fakeNetworkApplyOps) CreateAddress(_ uint32, prefix netip.Prefix) error {
	return f.record("create-address:" + prefix.String())
}

func (f *fakeNetworkApplyOps) DeleteAddress(_ uint32, prefix netip.Prefix) error {
	f.deletedAddress = prefix
	return f.record("delete-address:" + prefix.String())
}

func (f *fakeNetworkApplyOps) CreateRoute(_ uint32, prefix netip.Prefix) error {
	return f.record("create-route:" + prefix.String())
}

func (f *fakeNetworkApplyOps) DeleteRoute(_ uint32, prefix netip.Prefix) error {
	f.deletedRoutes = append(f.deletedRoutes, prefix)
	return f.record("delete-route:" + prefix.String())
}

type fakeNetworkCleanupOps struct {
	index          uint32
	resolveErr     error
	deletedRoutes  []netip.Prefix
	deletedAddress netip.Prefix
	mtuIndex       uint32
}

func (f *fakeNetworkCleanupOps) InterfaceIndexByGUID(string) (uint32, error) {
	return f.index, f.resolveErr
}

func (f *fakeNetworkCleanupOps) DeleteRoute(index uint32, prefix netip.Prefix) error {
	if index != f.index {
		return errors.New("cleanup used the wrong interface index")
	}
	f.deletedRoutes = append(f.deletedRoutes, prefix)
	return nil
}

func (f *fakeNetworkCleanupOps) DeleteAddress(index uint32, prefix netip.Prefix) error {
	if index != f.index {
		return errors.New("cleanup used the wrong interface index")
	}
	f.deletedAddress = prefix
	return nil
}

func (f *fakeNetworkCleanupOps) SetMTU(index uint32, _ uint16, _ uint32) error {
	if index != f.index {
		return errors.New("cleanup used the wrong interface index")
	}
	f.mtuIndex = index
	return nil
}

func writeTestNetworkState(t *testing.T, configurator *NetworkConfigurator) {
	t.Helper()
	err := configurator.save(networkState{
		Version:        networkStateVersion,
		Interface:      "FnCPN",
		InterfaceIndex: 4,
		InterfaceGUID:  "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}",
		Address:        "10.253.203.2/32",
		MTU:            1380,
		Routes:         []string{"10.253.203.1/32", "192.168.50.0/24"},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFilterConflictingRoutesPreservesOnlyNASOverlayHostRoute(t *testing.T) {
	serverOverlay := netip.MustParsePrefix("10.253.203.1/32")
	allowed := []string{
		serverOverlay.String(),
		"10.253.203.0/24", // contains this client's .2 overlay address, but is a conflicting LAN route
		"192.168.1.0/24",
		"172.20.0.0/16",
	}
	physical := []netip.Prefix{
		netip.MustParsePrefix("10.253.203.0/24"),
		netip.MustParsePrefix("192.168.1.0/24"),
	}

	got, err := filterConflictingRoutes(serverOverlay, allowed, physical)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		serverOverlay,
		netip.MustParsePrefix("172.20.0.0/16"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered routes = %v, want %v", got, want)
	}
}

func TestFilterConflictingRoutesRequiresNASOverlayRoute(t *testing.T) {
	serverOverlay := netip.MustParsePrefix("10.253.203.1/32")
	if _, err := filterConflictingRoutes(serverOverlay, []string{"172.20.0.0/16"}, nil); err == nil {
		t.Fatal("route list without the NAS overlay host route was accepted")
	}
	if _, err := filterConflictingRoutes(netip.MustParsePrefix("10.253.203.0/24"), []string{"10.253.203.1/32"}, nil); err == nil {
		t.Fatal("non-host NAS overlay route was accepted")
	}
}

func TestApplyPreparedRollsBackAndRetainsOnlyFailedCleanupIntent(t *testing.T) {
	applyFailure := errors.New("second route failed")
	cleanupFailure := errors.New("first route cleanup failed")
	address := netip.MustParsePrefix("10.253.203.2/32")
	overlay := netip.MustParsePrefix("10.253.203.1/32")
	lan := netip.MustParsePrefix("192.168.50.0/24")
	ops := &fakeNetworkApplyOps{
		resolvedIndex: 12,
		oldMTU:        1500,
		failures: map[string]error{
			"create-route:" + lan.String():     applyFailure,
			"delete-route:" + overlay.String(): cleanupFailure,
		},
	}
	configurator := &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json"), cleanup: ops, apply: ops}

	err := configurator.applyPrepared(context.Background(), "FnCPN", 4, "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}", address, 1380, []netip.Prefix{overlay, lan})
	if !errors.Is(err, applyFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("Apply error = %v, want apply and rollback failures", err)
	}
	if !reflect.DeepEqual(ops.setMTUs, []uint32{1380, 1500}) {
		t.Fatalf("MTU writes = %v, want target then original", ops.setMTUs)
	}
	if ops.deletedAddress != address || !reflect.DeepEqual(ops.deletedRoutes, []netip.Prefix{overlay}) {
		t.Fatalf("rollback mutations = routes %v, address %v", ops.deletedRoutes, ops.deletedAddress)
	}
	state, err := configurator.load()
	if err != nil {
		t.Fatalf("load residual cleanup journal: %v", err)
	}
	if state == nil || !reflect.DeepEqual(state.Routes, []string{overlay.String()}) || state.Address != "0.0.0.0/32" || state.MTU != 1500 {
		t.Fatalf("residual cleanup journal = %+v", state)
	}

	ops.failures = nil
	ops.deletedRoutes = nil
	ops.deletedAddress = netip.Prefix{}
	ops.setMTUs = nil
	if err := configurator.Recover(context.Background()); err != nil {
		t.Fatalf("recover residual state: %v", err)
	}
	if !reflect.DeepEqual(ops.deletedRoutes, []netip.Prefix{overlay}) || !ops.deletedAddress.IsValid() || ops.deletedAddress.String() != "0.0.0.0/32" {
		t.Fatalf("recovery did not target only residual owned resources: routes %v, address %v", ops.deletedRoutes, ops.deletedAddress)
	}
	if !reflect.DeepEqual(ops.setMTUs, []uint32{1500}) {
		t.Fatalf("recovery MTU writes = %v, want original MTU", ops.setMTUs)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("recovery journal still exists: %v", err)
	}
}

func TestApplyPreparedClearsJournalAfterSuccessfulRollback(t *testing.T) {
	applyFailure := errors.New("route creation failed")
	address := netip.MustParsePrefix("10.253.203.2/32")
	overlay := netip.MustParsePrefix("10.253.203.1/32")
	lan := netip.MustParsePrefix("192.168.50.0/24")
	ops := &fakeNetworkApplyOps{
		resolvedIndex: 12,
		oldMTU:        1500,
		failures: map[string]error{
			"create-route:" + lan.String(): applyFailure,
		},
	}
	configurator := &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json"), cleanup: ops, apply: ops}

	err := configurator.applyPrepared(context.Background(), "FnCPN", 4, "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}", address, 1380, []netip.Prefix{overlay, lan})
	if !errors.Is(err, applyFailure) {
		t.Fatalf("Apply error = %v, want route creation failure", err)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("journal remains after successful rollback: %v", err)
	}
	if !reflect.DeepEqual(ops.deletedRoutes, []netip.Prefix{overlay}) || ops.deletedAddress != address || !reflect.DeepEqual(ops.setMTUs, []uint32{1380, 1500}) {
		t.Fatalf("rollback cleanup = routes %v, address %v, MTU writes %v", ops.deletedRoutes, ops.deletedAddress, ops.setMTUs)
	}
}

func TestApplyPreparedDoesNotDeleteAddressWhenCreationFails(t *testing.T) {
	applyFailure := errors.New("address already exists")
	address := netip.MustParsePrefix("10.253.203.2/32")
	ops := &fakeNetworkApplyOps{
		resolvedIndex: 12,
		oldMTU:        1500,
		failures: map[string]error{
			"create-address:" + address.String(): applyFailure,
		},
	}
	configurator := &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json"), cleanup: ops, apply: ops}

	err := configurator.applyPrepared(context.Background(), "FnCPN", 4, "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}", address, 1380, []netip.Prefix{netip.MustParsePrefix("10.253.203.1/32")})
	if !errors.Is(err, applyFailure) {
		t.Fatalf("Apply error = %v, want address creation failure", err)
	}
	for _, event := range ops.events {
		if event == "delete-address:"+address.String() {
			t.Fatalf("rollback deleted an address not created by this Apply: %v", ops.events)
		}
	}
	if !reflect.DeepEqual(ops.setMTUs, []uint32{1380, 1500}) {
		t.Fatalf("MTU writes = %v, want changed MTU and its restoration", ops.setMTUs)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("journal remains after successful rollback: %v", err)
	}
}

func TestApplyPreparedCancellationBeforeMutationDoesNotDeleteAddress(t *testing.T) {
	address := netip.MustParsePrefix("10.253.203.2/32")
	ops := &fakeNetworkApplyOps{resolvedIndex: 12, oldMTU: 1500}
	configurator := &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json"), cleanup: ops, apply: ops}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := configurator.applyPrepared(ctx, "FnCPN", 4, "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}", address, 1380, []netip.Prefix{netip.MustParsePrefix("10.253.203.1/32")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply error = %v, want cancellation", err)
	}
	if !reflect.DeepEqual(ops.events, []string{"get-mtu"}) {
		t.Fatalf("operations ran despite pre-mutation cancellation: %v", ops.events)
	}
	if len(ops.deletedRoutes) != 0 || ops.deletedAddress.IsValid() || len(ops.setMTUs) != 0 {
		t.Fatalf("rollback touched unowned resources: %+v", ops)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("journal remains after empty rollback: %v", err)
	}
}

func TestApplyPreparedPersistsStateForSuccessfulRemove(t *testing.T) {
	address := netip.MustParsePrefix("10.253.203.2/32")
	overlay := netip.MustParsePrefix("10.253.203.1/32")
	lan := netip.MustParsePrefix("192.168.50.0/24")
	ops := &fakeNetworkApplyOps{resolvedIndex: 12, oldMTU: 1500}
	configurator := &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json"), cleanup: ops, apply: ops}
	if err := configurator.applyPrepared(context.Background(), "FnCPN", 4, "{95D3FC3A-8333-4A55-9E30-9483E1A54D7D}", address, 1380, []netip.Prefix{overlay, lan}); err != nil {
		t.Fatalf("apply prepared state: %v", err)
	}
	state, err := configurator.load()
	if err != nil || state == nil || !reflect.DeepEqual(state.Routes, []string{overlay.String(), lan.String()}) {
		t.Fatalf("applied cleanup journal = %+v, err=%v", state, err)
	}

	ops.events = nil
	if err := configurator.Remove(context.Background(), "FnCPN"); err != nil {
		t.Fatalf("remove applied state: %v", err)
	}
	if !reflect.DeepEqual(ops.deletedRoutes, []netip.Prefix{lan, overlay}) {
		t.Fatalf("removed routes = %v, want reverse apply order", ops.deletedRoutes)
	}
	if ops.deletedAddress != address || !reflect.DeepEqual(ops.setMTUs, []uint32{1380, 1500}) {
		t.Fatalf("remove cleanup address/MTU = %v/%v", ops.deletedAddress, ops.setMTUs)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("remove journal still exists: %v", err)
	}
}

func TestRecoverUsesCurrentInterfaceIndexResolvedByStableGUID(t *testing.T) {
	ops := &fakeNetworkCleanupOps{index: 12}
	configurator := &NetworkConfigurator{path: t.TempDir() + "\\network-state.json", cleanup: ops}
	writeTestNetworkState(t, configurator)

	if err := configurator.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ops.deletedRoutes, []netip.Prefix{
		netip.MustParsePrefix("192.168.50.0/24"),
		netip.MustParsePrefix("10.253.203.1/32"),
	}) {
		t.Fatalf("deleted routes = %v", ops.deletedRoutes)
	}
	if ops.deletedAddress != netip.MustParsePrefix("10.253.203.2/32") || ops.mtuIndex != 12 {
		t.Fatalf("cleanup address/index = %v/%d, want 10.253.203.2/32/12", ops.deletedAddress, ops.mtuIndex)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("recovered journal still exists: %v", err)
	}
}

func TestRecoverPreservesJournalWhenAdapterEnumerationFails(t *testing.T) {
	enumerationErr := errors.New("temporary IP Helper failure")
	ops := &fakeNetworkCleanupOps{resolveErr: enumerationErr}
	configurator := &NetworkConfigurator{path: t.TempDir() + "\\network-state.json", cleanup: ops}
	writeTestNetworkState(t, configurator)

	err := configurator.Recover(context.Background())
	if !errors.Is(err, enumerationErr) {
		t.Fatalf("Recover error = %v, want wrapped enumeration error", err)
	}
	if ops.deletedRoutes != nil || ops.deletedAddress.IsValid() || ops.mtuIndex != 0 {
		t.Fatalf("cleanup ran after adapter enumeration failed: %+v", ops)
	}
	if _, err := os.Stat(configurator.path); err != nil {
		t.Fatalf("recovery journal was discarded: %v", err)
	}
}

func TestRecoverPreservesJournalWhenResolvedInterfaceIndexIsZero(t *testing.T) {
	ops := &fakeNetworkCleanupOps{}
	configurator := &NetworkConfigurator{path: t.TempDir() + "\\network-state.json", cleanup: ops}
	writeTestNetworkState(t, configurator)

	if err := configurator.Recover(context.Background()); err == nil {
		t.Fatal("Recover succeeded with an invalid zero interface index")
	}
	if ops.deletedRoutes != nil || ops.deletedAddress.IsValid() || ops.mtuIndex != 0 {
		t.Fatalf("cleanup ran with a zero interface index: %+v", ops)
	}
	if _, err := os.Stat(configurator.path); err != nil {
		t.Fatalf("recovery journal was discarded: %v", err)
	}
}

func TestRecoverPreservesJournalWithInvalidInterfaceGUID(t *testing.T) {
	configurator := &NetworkConfigurator{path: t.TempDir() + "\\network-state.json", cleanup: &fakeNetworkCleanupOps{}}
	writeTestNetworkState(t, configurator)
	state, err := configurator.load()
	if err != nil {
		t.Fatal(err)
	}
	state.InterfaceGUID = "{00000000-0000-0000-0000-000000000000}"
	if err := configurator.save(*state); err != nil {
		t.Fatal(err)
	}
	if err := configurator.Recover(context.Background()); err == nil {
		t.Fatal("Recover succeeded with an invalid zero GUID")
	}
	if _, err := os.Stat(configurator.path); err != nil {
		t.Fatalf("invalid journal was discarded: %v", err)
	}
}

func TestRecoverClearsJournalOnlyWhenAdapterIsConfirmedRemoved(t *testing.T) {
	ops := &fakeNetworkCleanupOps{resolveErr: errNetworkInterfaceNotFound}
	configurator := &NetworkConfigurator{path: t.TempDir() + "\\network-state.json", cleanup: ops}
	writeTestNetworkState(t, configurator)

	if err := configurator.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ops.deletedRoutes != nil || ops.deletedAddress.IsValid() || ops.mtuIndex != 0 {
		t.Fatalf("removed adapter cleanup touched a possibly reused index: %+v", ops)
	}
	if _, err := os.Stat(configurator.path); !os.IsNotExist(err) {
		t.Fatalf("stale journal still exists after adapter removal: %v", err)
	}
}

func TestAddressRowUsesIPv4SockaddrLayoutAndAddressOrigins(t *testing.T) {
	row := addressRow(17, netip.MustParsePrefix("10.253.203.2/32"))
	raw := *(*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
	if raw.Family != windows.AF_INET || raw.Port != 0 || raw.Addr != [4]byte{10, 253, 203, 2} {
		t.Fatalf("invalid IPv4 sockaddr: %+v", raw)
	}
	if row.InterfaceIndex != 17 || row.PrefixOrigin != windows.IpPrefixOriginManual || row.SuffixOrigin != windows.NlsoManual || row.OnLinkPrefixLength != 32 || row.SkipAsSource != 0 || row.ValidLifetime < row.PreferredLifetime {
		t.Fatalf("invalid address settings: %+v", row)
	}
}
