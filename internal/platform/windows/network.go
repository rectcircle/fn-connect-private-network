//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sys/windows"
)

const networkStateVersion = 1

var iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")

type networkState struct {
	Version        int      `json:"version"`
	Interface      string   `json:"interface"`
	InterfaceIndex uint32   `json:"interfaceIndex"`
	InterfaceGUID  string   `json:"interfaceGuid"`
	Address        string   `json:"address"`
	MTU            uint32   `json:"mtu"`
	Routes         []string `json:"routes"`
}

type NetworkConfigurator struct {
	mu      sync.Mutex
	path    string
	cleanup networkCleanupOps
	apply   networkApplyOps
}

var errNetworkInterfaceNotFound = errors.New("network interface was removed")

type networkCleanupOps interface {
	InterfaceIndexByGUID(string) (uint32, error)
	DeleteRoute(uint32, netip.Prefix) error
	DeleteAddress(uint32, netip.Prefix) error
	SetMTU(uint32, uint16, uint32) error
}

type networkApplyOps interface {
	GetMTU(uint32, uint16) (uint32, error)
	SetMTU(uint32, uint16, uint32) error
	CreateAddress(uint32, netip.Prefix) error
	DeleteAddress(uint32, netip.Prefix) error
	CreateRoute(uint32, netip.Prefix) error
	DeleteRoute(uint32, netip.Prefix) error
}

type ipHelperNetworkOps struct{}

func (ipHelperNetworkOps) GetMTU(index uint32, family uint16) (uint32, error) {
	return getInterfaceMTU(int(index), family)
}

func (ipHelperNetworkOps) CreateAddress(index uint32, prefix netip.Prefix) error {
	return createAddress(index, prefix)
}

func (ipHelperNetworkOps) CreateRoute(index uint32, prefix netip.Prefix) error {
	return createRoute(index, prefix)
}

func (ipHelperNetworkOps) InterfaceIndexByGUID(guid string) (uint32, error) {
	rows, err := readWindowsInterfaceRows()
	if err != nil {
		return 0, fmt.Errorf("enumerate network adapters: %w", err)
	}
	return interfaceIndexByGUID(rows, guid)
}

func readWindowsInterfaceRows() ([]windows.MibIfRow2, error) {
	var table *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormalWithoutStatistics, &table); err != nil {
		return nil, err
	}
	if table == nil {
		return nil, errors.New("Windows returned an empty interface table")
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	if table.NumEntries == 0 {
		return []windows.MibIfRow2{}, nil
	}
	return append([]windows.MibIfRow2(nil), unsafe.Slice(&table.Table[0], table.NumEntries)...), nil
}

func interfaceIndexByGUID(rows []windows.MibIfRow2, guid string) (uint32, error) {
	target, err := windows.GUIDFromString(guid)
	if err != nil || target == (windows.GUID{}) {
		return 0, errors.New("network journal contains an invalid interface GUID")
	}
	for _, row := range rows {
		if row.InterfaceGuid == target {
			if row.InterfaceIndex == 0 {
				return 0, errors.New("network adapter has no interface index")
			}
			return row.InterfaceIndex, nil
		}
	}
	return 0, errNetworkInterfaceNotFound
}

func (ipHelperNetworkOps) DeleteRoute(index uint32, prefix netip.Prefix) error {
	return deleteRoute(index, prefix)
}

func (ipHelperNetworkOps) DeleteAddress(index uint32, prefix netip.Prefix) error {
	return deleteAddress(index, prefix)
}

func (ipHelperNetworkOps) SetMTU(index uint32, family uint16, mtu uint32) error {
	return setInterfaceMTU(index, family, mtu)
}

func NewNetworkConfigurator(stateDirectory string) *NetworkConfigurator {
	ops := ipHelperNetworkOps{}
	return &NetworkConfigurator{
		path:    filepath.Join(stateDirectory, "network-state.json"),
		cleanup: ops,
		apply:   ops,
	}
}

func (n *NetworkConfigurator) Available() bool {
	return n != nil && n.path != "" && iphlpapi.Load() == nil
}

func (n *NetworkConfigurator) Recover(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	state, err := n.load()
	if err != nil || state == nil {
		return err
	}
	return n.removeStateLocked(ctx, *state)
}

func (n *NetworkConfigurator) Apply(ctx context.Context, interfaceName string, plan model.ClientPlan) error {
	if !n.Available() {
		return model.NewError(model.ErrorUnavailable, "Windows IP Helper network API is unavailable", false)
	}
	address, err := netip.ParsePrefix(plan.ClientAddress)
	if err != nil || !address.Addr().Is4() || address.Bits() != 32 {
		return errors.New("client address must be an IPv4 host prefix")
	}
	if len(plan.AllowedIPs) == 0 {
		return errors.New("client plan must include the NAS overlay route")
	}
	serverOverlay, err := netip.ParsePrefix(plan.AllowedIPs[0])
	if err != nil || !serverOverlay.Addr().Is4() || serverOverlay.Bits() != 32 {
		return errors.New("first allowed route must be the NAS overlay IPv4 host route")
	}
	iface, err := findInterface(interfaceName)
	if err != nil {
		return err
	}
	adapterGUID, err := interfaceGUID(uint32(iface.Index))
	if err != nil {
		return fmt.Errorf("identify Wintun adapter: %w", err)
	}
	routes, err := safeRoutes(iface.Index, serverOverlay, plan.AllowedIPs)
	if err != nil {
		return err
	}
	return n.applyPrepared(ctx, interfaceName, uint32(iface.Index), adapterGUID, address, uint32(plan.MTU), routes)
}

func (n *NetworkConfigurator) applyPrepared(
	ctx context.Context,
	interfaceName string,
	interfaceIndex uint32,
	interfaceGUID string,
	address netip.Prefix,
	mtu uint32,
	routes []netip.Prefix,
) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	parsedGUID, err := windows.GUIDFromString(interfaceGUID)
	if err != nil || parsedGUID == (windows.GUID{}) {
		return errors.New("Wintun adapter has an invalid interface GUID")
	}
	if existing, err := n.load(); err != nil {
		return err
	} else if existing != nil {
		return model.NewError(model.ErrorConflict, "previous Windows network state must be recovered before apply", false)
	}
	ops := n.apply
	if ops == nil {
		ops = ipHelperNetworkOps{}
	}
	oldMTU, err := ops.GetMTU(interfaceIndex, windows.AF_INET)
	if err != nil {
		return fmt.Errorf("read Wintun MTU: %w", err)
	}
	state := networkState{Version: networkStateVersion, Interface: interfaceName, InterfaceIndex: interfaceIndex, InterfaceGUID: interfaceGUID, Address: address.String(), MTU: oldMTU}
	for _, route := range routes {
		state.Routes = append(state.Routes, route.String())
	}
	if err := n.save(state); err != nil {
		return fmt.Errorf("persist Windows cleanup intent: %w", err)
	}

	var appliedRoutes []netip.Prefix
	mtuApplied := false
	addressApplied := false
	rollback := func(cause error) error {
		var rollbackErrors []error
		var remaining []string
		for i := len(appliedRoutes) - 1; i >= 0; i-- {
			if err := ops.DeleteRoute(state.InterfaceIndex, appliedRoutes[i]); err != nil {
				rollbackErrors = append(rollbackErrors, err)
				remaining = append(remaining, appliedRoutes[i].String())
			}
		}
		addressCleanupComplete := !addressApplied
		if addressApplied {
			addressErr := ops.DeleteAddress(state.InterfaceIndex, address)
			if addressErr != nil {
				rollbackErrors = append(rollbackErrors, addressErr)
			} else {
				addressCleanupComplete = true
			}
		}
		if mtuApplied {
			if err := ops.SetMTU(state.InterfaceIndex, windows.AF_INET, oldMTU); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		if len(rollbackErrors) == 0 {
			if err := n.clear(); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		if len(rollbackErrors) > 0 {
			state.Routes = remaining
			if addressCleanupComplete {
				state.Address = "0.0.0.0/32"
			}
			rollbackErrors = append(rollbackErrors, n.save(state))
		}
		return errors.Join(append([]error{cause}, rollbackErrors...)...)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := ops.SetMTU(state.InterfaceIndex, windows.AF_INET, mtu); err != nil {
		return rollback(fmt.Errorf("set Wintun MTU: %w", err))
	}
	mtuApplied = true
	if err := ops.CreateAddress(state.InterfaceIndex, address); err != nil {
		return rollback(fmt.Errorf("add Wintun address: %w", err))
	}
	addressApplied = true
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := ops.CreateRoute(state.InterfaceIndex, route); err != nil {
			return rollback(fmt.Errorf("add Wintun route %s: %w", route, err))
		}
		appliedRoutes = append(appliedRoutes, route)
	}
	return nil
}

func (n *NetworkConfigurator) Remove(ctx context.Context, interfaceName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	state, err := n.load()
	if err != nil || state == nil {
		return err
	}
	if state.Interface != interfaceName {
		return errors.New("refusing to remove network state owned by another interface")
	}
	return n.removeStateLocked(ctx, *state)
}

func (n *NetworkConfigurator) removeStateLocked(ctx context.Context, state networkState) error {
	ops := n.cleanup
	if ops == nil {
		ops = ipHelperNetworkOps{}
	}
	currentIndex, err := ops.InterfaceIndexByGUID(state.InterfaceGUID)
	if errors.Is(err, errNetworkInterfaceNotFound) {
		// Windows removes adapter-bound addresses and routes when the adapter
		// itself is removed. Clear stale intent only after a successful adapter
		// table read proves that the owned adapter no longer exists.
		return n.clear()
	}
	if err != nil {
		return fmt.Errorf("verify owned Wintun adapter before cleanup; keeping recovery journal: %w", err)
	}
	if currentIndex == 0 {
		return errors.New("verify owned Wintun adapter before cleanup: resolved interface index is zero; keeping recovery journal")
	}
	// Interface indexes may change across reboot or adapter re-enumeration.
	// Resolve the persisted stable GUID first so cleanup never targets a new
	// adapter that happened to inherit the old index.
	state.InterfaceIndex = currentIndex
	var failures []error
	for i := len(state.Routes) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		prefix, parseErr := netip.ParsePrefix(state.Routes[i])
		if parseErr != nil {
			failures = append(failures, parseErr)
			continue
		}
		if err := ops.DeleteRoute(state.InterfaceIndex, prefix); err != nil {
			failures = append(failures, fmt.Errorf("remove owned route %s: %w", prefix, err))
		}
	}
	if address, err := netip.ParsePrefix(state.Address); err != nil {
		failures = append(failures, err)
	} else if err := ops.DeleteAddress(state.InterfaceIndex, address); err != nil {
		failures = append(failures, fmt.Errorf("remove owned interface address: %w", err))
	}
	if err := ops.SetMTU(state.InterfaceIndex, windows.AF_INET, state.MTU); err != nil {
		failures = append(failures, fmt.Errorf("restore Wintun MTU: %w", err))
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	return n.clear()
}

func (n *NetworkConfigurator) load() (*networkState, error) {
	data, err := os.ReadFile(n.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, errors.New("Windows network journal exceeds size limit")
	}
	var state networkState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decode Windows network journal: %w", err)
	}
	guid, guidErr := windows.GUIDFromString(state.InterfaceGUID)
	if state.Version != networkStateVersion || state.Interface == "" || state.InterfaceIndex == 0 || guidErr != nil || guid == (windows.GUID{}) {
		return nil, errors.New("Windows network journal is invalid")
	}
	if _, err := netip.ParsePrefix(state.Address); err != nil {
		return nil, errors.New("Windows network journal address is invalid")
	}
	for _, value := range state.Routes {
		if _, err := netip.ParsePrefix(value); err != nil {
			return nil, errors.New("Windows network journal route is invalid")
		}
	}
	return &state, nil
}

func (n *NetworkConfigurator) save(state networkState) error {
	if err := os.MkdirAll(filepath.Dir(n.path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(n.path), ".network-state-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(n.path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func (n *NetworkConfigurator) clear() error {
	if err := os.Remove(n.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func findInterface(name string) (*net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for i := range interfaces {
		if strings.EqualFold(interfaces[i].Name, name) || strings.EqualFold(interfaces[i].Name, "FnCPN") {
			return &interfaces[i], nil
		}
	}
	return nil, fmt.Errorf("Wintun interface %q is not visible to IP Helper", name)
}

func interfaceGUID(index uint32) (string, error) {
	rows, err := readWindowsInterfaceRows()
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.InterfaceIndex == index {
			if row.InterfaceGuid == (windows.GUID{}) {
				return "", errors.New("Wintun adapter has no stable interface GUID")
			}
			return row.InterfaceGuid.String(), nil
		}
	}
	return "", errors.New("Wintun adapter was not found in the IP Helper adapter table")
}

func safeRoutes(interfaceIndex int, serverOverlay netip.Prefix, values []string) ([]netip.Prefix, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	return safeRoutesWithReaders(interfaceIndex, serverOverlay, values, interfaces, func(iface net.Interface) ([]net.Addr, error) {
		return iface.Addrs()
	}, readWindowsIPv4RouteRows)
}

func safeRoutesWithReaders(
	interfaceIndex int,
	serverOverlay netip.Prefix,
	values []string,
	interfaces []net.Interface,
	addresses func(net.Interface) ([]net.Addr, error),
	readRoutes func() ([]windows.MibIpForwardRow2, error),
) ([]netip.Prefix, error) {
	physical, err := physicalIPv4Prefixes(interfaceIndex, interfaces, func(iface net.Interface) ([]net.Addr, error) {
		return addresses(iface)
	})
	if err != nil {
		return nil, err
	}
	routes, err := readRoutes()
	if err != nil {
		if !isEmptyWindowsIPv4RouteTableError(err) {
			return nil, fmt.Errorf("inspect current IPv4 routes: %w", err)
		}
		routes = nil
	}
	for _, route := range routes {
		if int(route.InterfaceIndex) == interfaceIndex || route.DestinationPrefix.PrefixLength == 0 || route.DestinationPrefix.Prefix.Family != windows.AF_INET {
			continue
		}
		address := (*windows.RawSockaddrInet4)(unsafe.Pointer(&route.DestinationPrefix.Prefix)).Addr
		prefix := netip.PrefixFrom(netip.AddrFrom4(address), int(route.DestinationPrefix.PrefixLength))
		physical = append(physical, prefix.Masked())
	}
	return filterConflictingRoutes(serverOverlay, values, physical)
}

func readWindowsIPv4RouteRows() ([]windows.MibIpForwardRow2, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_INET, &table); err != nil {
		if table != nil {
			windows.FreeMibTable(unsafe.Pointer(table))
		}
		return nil, err
	}
	if table == nil {
		return nil, nil
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	return append([]windows.MibIpForwardRow2(nil), table.Rows()...), nil
}

func isEmptyWindowsIPv4RouteTableError(err error) bool {
	return errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_NOT_SUPPORTED)
}

func physicalIPv4Prefixes(interfaceIndex int, interfaces []net.Interface, addresses func(net.Interface) ([]net.Addr, error)) ([]netip.Prefix, error) {
	var physical []netip.Prefix
	for _, iface := range interfaces {
		if iface.Index == interfaceIndex || strings.Contains(strings.ToLower(iface.Name), "wintun") || strings.EqualFold(iface.Name, "FnCPN") || iface.Flags&net.FlagUp == 0 {
			continue
		}
		values, err := addresses(iface)
		if err != nil {
			return nil, fmt.Errorf("inspect addresses on physical interface %q: %w", iface.Name, err)
		}
		for _, address := range values {
			if prefix, err := netip.ParsePrefix(address.String()); err == nil && prefix.Addr().Is4() {
				physical = append(physical, prefix.Masked())
			}
		}
	}
	return physical, nil
}

func filterConflictingRoutes(serverOverlay netip.Prefix, values []string, physical []netip.Prefix) ([]netip.Prefix, error) {
	if !serverOverlay.Addr().Is4() || serverOverlay.Bits() != 32 {
		return nil, errors.New("NAS overlay route must be an IPv4 host prefix")
	}
	out := make([]netip.Prefix, 0, len(values))
	seen := make(map[netip.Prefix]bool)
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed route %q", value)
		}
		prefix = prefix.Masked()
		if seen[prefix] {
			continue
		}
		seen[prefix] = true
		// Keep the exact NAS overlay host route when it overlaps a physical
		// LAN. Filter conflicting LAN routes, even when they contain the local
		// client's distinct overlay address.
		if prefix == serverOverlay {
			out = append(out, prefix)
			continue
		}
		conflict := false
		for _, local := range physical {
			if prefix.Addr().Is4() && local.Overlaps(prefix) {
				conflict = true
				break
			}
		}
		if !conflict {
			out = append(out, prefix)
		}
	}
	if !seen[serverOverlay] {
		return nil, errors.New("NAS overlay host route is missing from allowed routes")
	}
	return out, nil
}

func getInterfaceMTU(index int, family uint16) (uint32, error) {
	row := windows.MibIpInterfaceRow{Family: family, InterfaceIndex: uint32(index)}
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return 0, err
	}
	return row.NlMtu, nil
}

func setInterfaceMTU(index uint32, family uint16, mtu uint32) error {
	return setInterfaceMTUWithOps(index, family, mtu, windows.GetIpInterfaceEntry, func(row *windows.MibIpInterfaceRow) error {
		result, _, _ := iphlpapi.NewProc("SetIpInterfaceEntry").Call(uintptr(unsafe.Pointer(row)))
		if result != 0 {
			return syscall.Errno(result)
		}
		return nil
	})
}

func setInterfaceMTUWithOps(index uint32, family uint16, mtu uint32, get, set func(*windows.MibIpInterfaceRow) error) error {
	row := windows.MibIpInterfaceRow{Family: family, InterfaceIndex: index}
	if err := get(&row); err != nil {
		return err
	}
	row.NlMtu = mtu
	// GetIpInterfaceEntry can return a nonzero IPv4 SitePrefixLength,
	// but SetIpInterfaceEntry requires zero for IPv4 (netioapi contract).
	if family == windows.AF_INET {
		row.SitePrefixLength = 0
	}
	return set(&row)
}

func addressRow(index uint32, prefix netip.Prefix) windows.MibUnicastIpAddressRow {
	row := windows.MibUnicastIpAddressRow{InterfaceIndex: index, OnLinkPrefixLength: uint8(prefix.Bits()), PrefixOrigin: windows.IpPrefixOriginManual, SuffixOrigin: windows.NlsoManual, ValidLifetime: 0xffffffff, PreferredLifetime: 0xffffffff, SkipAsSource: 0}
	// Address is a SOCKADDR_INET union represented as IPv6 by x/sys.
	// IPv4 bytes belong at offset 4, not the IPv6 Addr field at offset 8.
	*(*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address)) = windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: prefix.Addr().As4()}
	return row
}

func createAddress(index uint32, prefix netip.Prefix) error {
	if !prefix.Addr().Is4() {
		return errors.New("only IPv4 overlay addresses are supported")
	}
	row := addressRow(index, prefix)
	result, _, _ := iphlpapi.NewProc("CreateUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(&row)))
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func deleteAddress(index uint32, prefix netip.Prefix) error {
	if !prefix.Addr().Is4() {
		return nil
	}
	row := addressRow(index, prefix)
	result, _, _ := iphlpapi.NewProc("DeleteUnicastIpAddressEntry").Call(uintptr(unsafe.Pointer(&row)))
	if result != 0 && result != 1168 {
		return syscall.Errno(result)
	}
	return nil
}

func routeRow(index uint32, prefix netip.Prefix) (windows.MibIpForwardRow2, error) {
	if !prefix.Addr().Is4() {
		return windows.MibIpForwardRow2{}, errors.New("only IPv4 routes are currently supported")
	}
	row := windows.MibIpForwardRow2{InterfaceIndex: index, Metric: 1, Protocol: windows.MIB_IPPROTO_NETMGMT, ValidLifetime: 0xffffffff, PreferredLifetime: 0xffffffff, Immortal: 1, Origin: windows.NlroManual}
	address := prefix.Addr().As4()
	sockaddr := windows.RawSockaddrInet4{Family: windows.AF_INET, Addr: address}
	row.DestinationPrefix.Prefix = *(*windows.RawSockaddrInet)(unsafe.Pointer(&sockaddr))
	row.DestinationPrefix.PrefixLength = uint8(prefix.Bits())
	row.NextHop.Family = windows.AF_INET
	return row, nil
}

func createRoute(index uint32, prefix netip.Prefix) error {
	row, err := routeRow(index, prefix)
	if err != nil {
		return err
	}
	result, _, _ := iphlpapi.NewProc("CreateIpForwardEntry2").Call(uintptr(unsafe.Pointer(&row)))
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func deleteRoute(index uint32, prefix netip.Prefix) error {
	row, err := routeRow(index, prefix)
	if err != nil {
		return err
	}
	result, _, _ := iphlpapi.NewProc("DeleteIpForwardEntry2").Call(uintptr(unsafe.Pointer(&row)))
	if result != 0 && result != 1168 {
		return syscall.Errno(result)
	}
	return nil
}
