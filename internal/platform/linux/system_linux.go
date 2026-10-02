//go:build linux

package linux

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const (
	defaultServerStateDirectory = "/var/lib/fncpn"
	ipv4ForwardPath             = "/proc/sys/net/ipv4/ip_forward"
)

func NewSystemServerEngine(logger *slog.Logger, stateDirectory string) *ServerEngine {
	if stateDirectory == "" {
		stateDirectory = defaultServerStateDirectory
	}
	return NewServerEngine(
		defaultInterfaceName,
		fileKeyStore{path: filepath.Join(stateDirectory, "server.key")},
		systemDeviceManager{},
		fileForwardingManager{path: ipv4ForwardPath},
		systemFirewallManager{},
		fileServerStateStore{
			path: filepath.Join(stateDirectory, "network-state.json"),
		},
		logger,
	)
}

type systemDeviceManager struct{}

func (systemDeviceManager) Available() bool {
	return true
}

func (systemDeviceManager) Apply(
	ctx context.Context,
	interfaceName string,
	ownerToken string,
	privateKey wgtypes.Key,
	plan model.ServerPlan,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	link, err := netlink.LinkByName(interfaceName)
	created := false
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("find WireGuard interface: %w", err)
		}
		link = &netlink.GenericLink{
			LinkAttrs: netlink.LinkAttrs{Name: interfaceName},
			LinkType:  "wireguard",
		}
		if err := netlink.LinkAdd(link); err != nil {
			return fmt.Errorf("create WireGuard interface: %w", err)
		}
		created = true
	} else if link.Type() != "wireguard" {
		return model.NewError(
			model.ErrorConflict,
			fmt.Sprintf("interface %q exists and is not WireGuard", interfaceName),
			false,
		)
	}
	cleanup := func() {
		if created {
			_ = netlink.LinkDel(link)
		}
	}
	expectedAlias := "fncpn:" + ownerToken
	if !created && link.Attrs().Alias != expectedAlias {
		return model.NewError(
			model.ErrorConflict,
			fmt.Sprintf("interface %q is not owned by FnCPN", interfaceName),
			false,
		)
	}
	if err := netlink.LinkSetAlias(link, expectedAlias); err != nil {
		cleanup()
		return fmt.Errorf("mark WireGuard interface ownership: %w", err)
	}
	address, err := netlink.ParseAddr(plan.ServerAddress)
	if err != nil {
		cleanup()
		return fmt.Errorf("parse server address: %w", err)
	}
	addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		cleanup()
		return fmt.Errorf("list WireGuard addresses: %w", err)
	}
	for index := range addresses {
		if addresses[index].Equal(*address) {
			continue
		}
		if err := netlink.AddrDel(link, &addresses[index]); err != nil {
			cleanup()
			return fmt.Errorf(
				"remove stale WireGuard address %s: %w",
				addresses[index].String(),
				err,
			)
		}
	}
	if err := netlink.AddrReplace(link, address); err != nil {
		cleanup()
		return fmt.Errorf("assign server address: %w", err)
	}
	if err := netlink.LinkSetMTU(link, 1420); err != nil {
		cleanup()
		return fmt.Errorf("set WireGuard MTU: %w", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		cleanup()
		return fmt.Errorf("activate WireGuard interface: %w", err)
	}

	client, err := wgctrl.New()
	if err != nil {
		cleanup()
		return fmt.Errorf("open wgctrl client: %w", err)
	}
	defer client.Close()
	var device *wgtypes.Device
	if !created {
		device, err = client.Device(interfaceName)
		if err != nil {
			cleanup()
			return fmt.Errorf("read WireGuard device: %w", err)
		}
	}
	config, changed, err := serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		cleanup()
		return fmt.Errorf("compare WireGuard configuration: %w", err)
	}
	if !changed {
		return nil
	}
	if err := client.ConfigureDevice(interfaceName, config); err != nil {
		cleanup()
		return fmt.Errorf("configure WireGuard device: %w", err)
	}
	return nil
}

func (systemDeviceManager) Remove(
	ctx context.Context,
	interfaceName string,
	ownerToken string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		var notFound netlink.LinkNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("find WireGuard interface: %w", err)
	}
	if link.Type() != "wireguard" {
		return model.NewError(
			model.ErrorConflict,
			fmt.Sprintf("refusing to delete non-WireGuard interface %q", interfaceName),
			false,
		)
	}
	expectedAlias := "fncpn:" + ownerToken
	legacyOwned := ownerToken == "legacy-v1" && link.Attrs().Alias == ""
	if link.Attrs().Alias != expectedAlias && !legacyOwned {
		return model.NewError(
			model.ErrorConflict,
			fmt.Sprintf("refusing to delete unowned interface %q", interfaceName),
			false,
		)
	}
	if err := netlink.LinkDel(link); err != nil {
		return fmt.Errorf("delete WireGuard interface: %w", err)
	}
	return nil
}

func (systemDeviceManager) PeerStatus(
	ctx context.Context,
	interfaceName string,
) ([]model.PrivilegedPeerStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("open wgctrl client: %w", err)
	}
	defer client.Close()
	device, err := client.Device(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("read WireGuard device: %w", err)
	}
	result := make([]model.PrivilegedPeerStatus, 0, len(device.Peers))
	for _, peer := range device.Peers {
		status := model.PrivilegedPeerStatus{
			PublicKey:     peer.PublicKey.String(),
			ReceiveBytes:  uint64(max(peer.ReceiveBytes, 0)),
			TransmitBytes: uint64(max(peer.TransmitBytes, 0)),
		}
		if !peer.LastHandshakeTime.IsZero() {
			value := peer.LastHandshakeTime
			status.LastHandshake = &value
		}
		result = append(result, status)
	}
	return result, nil
}

type fileForwardingManager struct {
	path string
}

func (fileForwardingManager) Available() bool {
	return true
}

func (m fileForwardingManager) Enable(ctx context.Context) (bool, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return false, fmt.Errorf("read IPv4 forwarding state: %w", err)
	}
	previous := strings.TrimSpace(string(data)) == "1"
	if err := m.Set(ctx, true); err != nil {
		return false, err
	}
	return previous, nil
}

func (m fileForwardingManager) Set(ctx context.Context, enabled bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	value := []byte("0\n")
	if enabled {
		value = []byte("1\n")
	}
	if err := os.WriteFile(m.path, value, 0o644); err != nil {
		return fmt.Errorf("set IPv4 forwarding state: %w", err)
	}
	return nil
}

type systemFirewallManager struct{}

func (systemFirewallManager) Available() bool {
	return true
}

func (systemFirewallManager) Apply(
	ctx context.Context,
	tableName string,
	interfaceName string,
	plan model.ServerPlan,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	connection, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables connection: %w", err)
	}
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("list nftables tables: %w", err)
	}
	for _, existing := range tables {
		if existing.Name == tableName {
			connection.DelTable(existing)
		}
	}
	table := connection.AddTable(&nftables.Table{
		Name:   tableName,
		Family: nftables.TableFamilyINet,
	})
	input := connection.AddChain(&nftables.Chain{
		Name:     "input",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityRef(-10),
	})
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, plan.ListenPort)
	connection.AddRule(&nftables.Rule{
		Table: table,
		Chain: input,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     []byte{unix.IPPROTO_UDP},
			},
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseTransportHeader,
				Offset:       2,
				Len:          2,
			},
			&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: port},
			&expr.Verdict{Kind: expr.VerdictAccept},
		},
	})
	forward := connection.AddChain(&nftables.Chain{
		Name:     "forward",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookForward,
		Priority: nftables.ChainPriorityRef(-10),
	})
	if len(plan.LANCIDRs) > 0 {
		overlay, _ := netip.ParsePrefix(plan.OverlayCIDR)
		returnExpressions := interfaceMatch(expr.MetaKeyOIFNAME, interfaceName)
		returnExpressions = append(
			returnExpressions,
			ipv4PrefixMatch(16, overlay)...,
		)
		returnExpressions = append(
			returnExpressions,
			&expr.Verdict{Kind: expr.VerdictAccept},
		)
		connection.AddRule(&nftables.Rule{
			Table: table,
			Chain: forward,
			Exprs: returnExpressions,
		})
		postrouting := connection.AddChain(&nftables.Chain{
			Name:     "postrouting",
			Table:    table,
			Type:     nftables.ChainTypeNAT,
			Hooknum:  nftables.ChainHookPostrouting,
			Priority: nftables.ChainPriorityNATSource,
		})
		for _, value := range plan.LANCIDRs {
			lan, _ := netip.ParsePrefix(value)
			forwardExpressions := interfaceMatch(
				expr.MetaKeyIIFNAME,
				interfaceName,
			)
			forwardExpressions = append(
				forwardExpressions,
				ipv4PrefixMatch(16, lan)...,
			)
			forwardExpressions = append(
				forwardExpressions,
				&expr.Verdict{Kind: expr.VerdictAccept},
			)
			connection.AddRule(&nftables.Rule{
				Table: table,
				Chain: forward,
				Exprs: forwardExpressions,
			})

			natExpressions := ipv4PrefixMatch(12, overlay)
			natExpressions = append(
				natExpressions,
				ipv4PrefixMatch(16, lan)...,
			)
			natExpressions = append(natExpressions, &expr.Masq{})
			connection.AddRule(&nftables.Rule{
				Table: table,
				Chain: postrouting,
				Exprs: natExpressions,
			})
		}
	}
	if err := stageDockerForwarding(connection, tableName, interfaceName, plan); err != nil {
		return err
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("apply nftables rules: %w", err)
	}
	return nil
}

func (systemFirewallManager) Remove(ctx context.Context, tableName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	connection, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables connection: %w", err)
	}
	tables, err := connection.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return fmt.Errorf("list nftables tables: %w", err)
	}
	for _, table := range tables {
		if table.Name == tableName {
			connection.DelTable(table)
		}
	}
	if err := stageDockerForwarding(connection, tableName, "", model.ServerPlan{}); err != nil {
		return err
	}
	if err := connection.Flush(); err != nil {
		return fmt.Errorf("remove nftables table: %w", err)
	}
	return nil
}

func interfaceMatch(key expr.MetaKey, interfaceName string) []expr.Any {
	value := make([]byte, 16)
	copy(value, interfaceName+"\x00")
	return []expr.Any{
		&expr.Meta{Key: key, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: value},
	}
}

func ipv4PrefixMatch(offset uint32, prefix netip.Prefix) []expr.Any {
	return append([]expr.Any{
		&expr.Meta{Key: expr.MetaKeyNFPROTO, Register: 1},
		&expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     []byte{unix.NFPROTO_IPV4},
		},
	}, ipv4AddressMatch(offset, prefix)...)
}

func ipv4AddressMatch(offset uint32, prefix netip.Prefix) []expr.Any {
	address := prefix.Masked().Addr().As4()
	mask := net.CIDRMask(prefix.Bits(), 32)
	return []expr.Any{
		&expr.Payload{
			DestRegister: 1,
			Base:         expr.PayloadBaseNetworkHeader,
			Offset:       offset,
			Len:          4,
		},
		&expr.Bitwise{
			SourceRegister: 1,
			DestRegister:   1,
			Len:            4,
			Mask:           []byte(mask),
			Xor:            []byte{0, 0, 0, 0},
		},
		&expr.Cmp{
			Op:       expr.CmpOpEq,
			Register: 1,
			Data:     address[:],
		},
	}
}
