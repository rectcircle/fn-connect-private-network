//go:build darwin

package darwin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

const (
	defaultNetworkStatePath = "/var/db/fncpn/state.json"
	networkStateVersion     = 2
	maxNetworkStateSize     = 64 * 1024
)

type persistedNetworkState struct {
	Version          int      `json:"version"`
	PID              int      `json:"pid"`
	ProcessStartedAt int64    `json:"processStartedAt"`
	Executable       string   `json:"executable"`
	OwnerToken       string   `json:"ownerToken"`
	OwnerUID         uint32   `json:"ownerUid"`
	Interface        string   `json:"interface"`
	Address          string   `json:"address"`
	MTU              int      `json:"mtu"`
	Routes           []string `json:"routes"`
}

type legacyNetworkState struct {
	Version   int      `json:"version"`
	PID       int      `json:"pid"`
	Interface string   `json:"interface"`
	Address   string   `json:"address"`
	MTU       int      `json:"mtu"`
	Routes    []string `json:"routes"`
}

type networkStateStore interface {
	Load() (*persistedNetworkState, error)
	Save(persistedNetworkState) error
	Clear() error
}

type fileNetworkStateStore struct {
	path string
}

func (s fileNetworkStateStore) Load() (*persistedNetworkState, error) {
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open network state: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat network state: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("network state is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("network state permissions must not allow group or other access")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNetworkStateSize+1))
	if err != nil {
		return nil, fmt.Errorf("read network state: %w", err)
	}
	if len(data) > maxNetworkStateSize {
		return nil, errors.New("network state exceeds size limit")
	}

	var version struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return nil, fmt.Errorf("decode network state version: %w", err)
	}
	var state persistedNetworkState
	if version.Version == 1 {
		var legacy legacyNetworkState
		if err := model.DecodeStrict(data, &legacy); err != nil {
			return nil, fmt.Errorf("decode legacy network state: %w", err)
		}
		ownerToken, err := newNetworkOwnerToken()
		if err != nil {
			return nil, fmt.Errorf("migrate network owner token: %w", err)
		}
		state = persistedNetworkState{
			Version:          networkStateVersion,
			PID:              legacy.PID,
			ProcessStartedAt: 1,
			Executable:       "legacy-v1",
			OwnerToken:       ownerToken,
			Interface:        legacy.Interface,
			Address:          legacy.Address,
			MTU:              legacy.MTU,
			Routes:           legacy.Routes,
		}
	} else if err := model.DecodeStrict(data, &state); err != nil {
		return nil, fmt.Errorf("decode network state: %w", err)
	}
	if err := validatePersistedNetworkState(state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s fileNetworkStateStore) Save(state persistedNetworkState) error {
	if err := validatePersistedNetworkState(state); err != nil {
		return err
	}
	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create network state directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("set network state directory mode: %w", err)
	}
	data, err := marshalNetworkState(state)
	if err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary network state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set temporary network state mode: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary network state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary network state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary network state: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace network state: %w", err)
	}
	return syncDirectory(directory)
}

func (s fileNetworkStateStore) Clear() error {
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove network state: %w", err)
	}
	return syncDirectory(filepath.Dir(s.path))
}

func marshalNetworkState(state persistedNetworkState) ([]byte, error) {
	data, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("encode network state: %w", err)
	}
	return append(data, '\n'), nil
}

func validatePersistedNetworkState(state persistedNetworkState) error {
	if state.Version != networkStateVersion {
		return fmt.Errorf("unsupported network state version %d", state.Version)
	}
	if state.PID <= 1 {
		return errors.New("network state PID is invalid")
	}
	if state.ProcessStartedAt <= 0 ||
		state.Executable == "" ||
		len(state.OwnerToken) != 32 {
		return errors.New("network state owner identity is invalid")
	}
	if !validUTUNName(state.Interface) {
		return errors.New("network state interface is invalid")
	}
	address, err := netip.ParsePrefix(state.Address)
	if err != nil || !address.Addr().Is4() || address.Bits() != 32 {
		return errors.New("network state address must be an IPv4 host prefix")
	}
	if state.MTU < 576 || state.MTU > 1420 {
		return errors.New("network state MTU must be between 576 and 1420")
	}
	if len(state.Routes) > 64 {
		return errors.New("network state contains too many routes")
	}
	for _, value := range state.Routes {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix != prefix.Masked() {
			return fmt.Errorf("network state route %q is invalid", value)
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open network state directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync network state directory: %w", err)
	}
	return nil
}

type recoveryInspector interface {
	ProcessMatches(int, int64, string) bool
	InterfaceHasAddress(string, netip.Prefix) bool
	RouteUsesInterface(context.Context, netip.Prefix, string) (bool, error)
}

type systemRecoveryInspector struct{}

func (systemRecoveryInspector) ProcessMatches(
	pid int,
	startedAt int64,
	executable string,
) bool {
	err := unix.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	currentStartedAt, currentExecutable, err := processIdentity(pid)
	return err == nil &&
		currentStartedAt == startedAt &&
		currentExecutable == executable
}

func processIdentity(pid int) (int64, string, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, "", err
	}
	arguments, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return 0, "", err
	}
	if len(arguments) < 5 {
		return 0, "", errors.New("process arguments are incomplete")
	}
	pathBytes := arguments[4:]
	if index := bytes.IndexByte(pathBytes, 0); index >= 0 {
		pathBytes = pathBytes[:index]
	}
	if len(pathBytes) == 0 {
		return 0, "", errors.New("process executable is unavailable")
	}
	startedAt := process.Proc.P_starttime.Sec*1_000_000 +
		int64(process.Proc.P_starttime.Usec)
	return startedAt, string(pathBytes), nil
}

func newNetworkOwnerToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (systemRecoveryInspector) InterfaceHasAddress(
	interfaceName string,
	address netip.Prefix,
) bool {
	present, _ := interfaceHasAddress(interfaceName, address)
	return present
}

func (systemRecoveryInspector) RouteUsesInterface(
	ctx context.Context,
	prefix netip.Prefix,
	interfaceName string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	index := 0
	for _, iface := range interfaces {
		if iface.Name == interfaceName {
			index = iface.Index
			break
		}
	}
	if index == 0 {
		return false, nil
	}
	data, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return false, fmt.Errorf("read routes: %w", err)
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, data)
	if err != nil {
		return false, fmt.Errorf("parse routes: %w", err)
	}
	for _, message := range messages {
		entry, ok := message.(*route.RouteMessage)
		if ok && routeMatches(entry, prefix, index) {
			return true, nil
		}
	}
	return false, nil
}

func routeMatches(entry *route.RouteMessage, prefix netip.Prefix, index int) bool {
	if entry.Index != index || len(entry.Addrs) <= syscall.RTAX_DST {
		return false
	}
	var address netip.Addr
	switch value := entry.Addrs[syscall.RTAX_DST].(type) {
	case *route.Inet4Addr:
		address = netip.AddrFrom4(value.IP)
	case *route.Inet6Addr:
		address = netip.AddrFrom16(value.IP)
	default:
		return false
	}
	bits := 0
	if entry.Flags & syscall.RTF_HOST != 0 {
		bits = address.BitLen()
	} else if len(entry.Addrs) > syscall.RTAX_NETMASK {
		var mask net.IPMask
		switch value := entry.Addrs[syscall.RTAX_NETMASK].(type) {
		case *route.Inet4Addr:
			mask = net.IPMask(value.IP[:])
		case *route.Inet6Addr:
			mask = net.IPMask(value.IP[:])
		}
		if mask != nil {
			var total int
			bits, total = mask.Size()
			if total != address.BitLen() {
				return false
			}
		}
	}
	return netip.PrefixFrom(address, bits).Masked() == prefix.Masked()
}

type discardNetworkStateStore struct{}

func (discardNetworkStateStore) Load() (*persistedNetworkState, error) {
	return nil, nil
}

func (discardNetworkStateStore) Save(persistedNetworkState) error {
	return nil
}

func (discardNetworkStateStore) Clear() error {
	return nil
}
