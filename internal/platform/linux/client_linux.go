//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const clientJournalVersion = 1

// Only the owned link and its connected routes are persistent kernel resources.
// Clients do not forward packets or change the host firewall/forwarding policy.
type clientJournal struct {
	Version   int    `json:"version"`
	Interface string `json:"interface"`
	Token     string `json:"token"`
	UID       uint32 `json:"uid"`
}
type ClientEngine struct {
	mu       sync.Mutex
	path     string
	state    *clientJournal
	degraded bool
	logger   *slog.Logger
	lease    *os.File
}

func NewSystemClientEngine(logger *slog.Logger, directory string) *ClientEngine {
	if logger == nil {
		logger = slog.Default()
	}
	return &ClientEngine{path: filepath.Join(directory, "client-network.json"), logger: logger}
}
func (e *ClientEngine) Available() bool { return true }
func clientLinkName(uid uint32) string  { return "fnc" + strconv.FormatUint(uint64(uid), 36) }
func validateClientJournal(j clientJournal) error {
	if j.Version != clientJournalVersion || j.Interface != clientLinkName(j.UID) || len(j.Token) != 32 {
		return errors.New("unsupported or invalid Linux client network journal")
	}
	for _, c := range j.Token {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return errors.New("invalid network owner token")
		}
	}
	return nil
}

// Hold the process lease across disconnects: another helper must never recover
// this process's live resources. The kernel releases the lease after a crash.
func (e *ClientEngine) acquireLocked() error {
	if e.lease != nil {
		return nil
	}
	directory := filepath.Dir(e.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ok || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("client state directory must be private and owned by the helper")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	fd, err := unix.Open(filepath.Join(directory, "client-network.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "client-network.lock")
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return fmt.Errorf("another client helper owns this state directory: %w", err)
	}
	e.lease = file
	return nil
}
func (e *ClientEngine) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.removeLocked(ctx); err != nil {
		return err
	}
	if e.lease == nil {
		return nil
	}
	err := e.lease.Close()
	e.lease = nil
	return err
}
func (e *ClientEngine) save(j clientJournal) error {
	if err := validateClientJournal(j); err != nil {
		return err
	}
	if err := ensurePrivateDirectory(filepath.Dir(e.path)); err != nil {
		return err
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	return writePrivateFileAtomic(e.path, data)
}
func (e *ClientEngine) Recover(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.acquireLocked(); err != nil {
		return err
	}
	info, err := os.Lstat(e.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return errors.New("invalid client journal file permissions or size")
	}
	data, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	var j clientJournal
	if err = model.DecodeStrict(data, &j); err != nil {
		return err
	}
	if err = validateClientJournal(j); err != nil {
		return err
	}
	e.state = &j
	return e.removeLocked(ctx)
}
func (e *ClientEngine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.removeLocked(ctx)
}
func (e *ClientEngine) removeLocked(ctx context.Context) error {
	if e.state == nil {
		return nil
	}
	if err := (systemDeviceManager{}).Remove(ctx, e.state.Interface, e.state.Token); err != nil {
		e.degraded = true
		return err
	}
	if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) {
		e.degraded = true
		return err
	}
	e.state = nil
	e.degraded = false
	return nil
}
func (e *ClientEngine) Apply(ctx context.Context, input model.ClientPlan) error {
	plan, err := wgconfig.NormalizeClientPlan(input)
	if err != nil {
		return model.WrapError(model.ErrorInvalidArgument, "invalid client network plan", false, err)
	}
	identity, ok := peer.IdentityFromContext(ctx)
	if !ok {
		return model.NewError(model.ErrorPermissionDenied, "network owner identity is required", false)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.acquireLocked(); err != nil {
		return err
	}
	if e.state != nil && e.state.UID != identity.UID {
		return model.NewError(model.ErrorPermissionDenied, "network belongs to another user", false)
	}
	if err = e.removeLocked(ctx); err != nil {
		return err
	}
	name := clientLinkName(identity.UID)
	if link, findErr := netlink.LinkByName(name); findErr == nil {
		return fmt.Errorf("refusing to replace existing interface %s (%s)", name, link.Type())
	} else {
		var absent netlink.LinkNotFoundError
		if !errors.As(findErr, &absent) {
			return findErr
		}
	}
	token, err := newOwnerToken()
	if err != nil {
		return err
	}
	j := clientJournal{Version: 1, Interface: name, Token: token, UID: identity.UID}
	if err = e.save(j); err != nil {
		return err
	}
	e.state = &j
	if err = e.applyLink(ctx, j, plan); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return errors.Join(err, e.removeLocked(cleanup))
	}
	return nil
}
func (e *ClientEngine) applyLink(ctx context.Context, j clientJournal, p model.ClientPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	link := &netlink.GenericLink{LinkAttrs: netlink.LinkAttrs{Name: j.Interface, Alias: "fncpn:" + j.Token}, LinkType: "wireguard"}
	if err := netlink.LinkAdd(link); err != nil {
		return fmt.Errorf("create kernel WireGuard interface: %w", err)
	}
	deviceLink, err := netlink.LinkByName(j.Interface)
	if err != nil {
		return err
	}
	if err = netlink.LinkSetAlias(deviceLink, "fncpn:"+j.Token); err != nil {
		_ = netlink.LinkDel(deviceLink)
		return err
	}
	address, err := netlink.ParseAddr(p.ClientAddress)
	if err != nil {
		return err
	}
	if err = netlink.AddrAdd(deviceLink, address); err != nil {
		return err
	}
	if err = netlink.LinkSetMTU(deviceLink, p.MTU); err != nil {
		return err
	}
	privateKey, _ := wgtypes.ParseKey(p.PrivateKey)
	publicKey, _ := wgtypes.ParseKey(p.ServerPublicKey)
	endpoint, err := net.ResolveUDPAddr("udp", p.Endpoint)
	if err != nil {
		return err
	}
	allowed := make([]net.IPNet, 0, len(p.AllowedIPs))
	for _, value := range p.AllowedIPs {
		_, prefix, _ := net.ParseCIDR(value)
		allowed = append(allowed, *prefix)
	}
	keepalive := 25 * time.Second
	control, err := wgctrl.New()
	if err != nil {
		return err
	}
	defer control.Close()
	if err = control.ConfigureDevice(j.Interface, wgtypes.Config{PrivateKey: &privateKey, ReplacePeers: true, Peers: []wgtypes.PeerConfig{{PublicKey: publicKey, Endpoint: endpoint, AllowedIPs: allowed, ReplaceAllowedIPs: true, PersistentKeepaliveInterval: &keepalive}}}); err != nil {
		return err
	}
	if err = netlink.LinkSetUp(deviceLink); err != nil {
		return err
	}
	for _, prefix := range allowed {
		// Add never Replace: a route owned by another application must not be overwritten.
		route := &netlink.Route{LinkIndex: deviceLink.Attrs().Index, Dst: &prefix, Scope: netlink.SCOPE_LINK, Protocol: unix.RTPROT_STATIC, Table: unix.RT_TABLE_MAIN}
		if err = netlink.RouteAdd(route); err != nil {
			return fmt.Errorf("add client route %s: %w", prefix.String(), err)
		}
	}
	return ctx.Err()
}
func (e *ClientEngine) Status() model.ClientPrivilegedStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	status := model.ClientPrivilegedStatus{Degraded: e.degraded}
	if e.state == nil {
		return status
	}
	status.Interface = e.state.Interface
	link, ownershipErr := netlink.LinkByName(e.state.Interface)
	if ownershipErr != nil || link.Type() != "wireguard" || link.Attrs().Alias != "fncpn:"+e.state.Token {
		status.Degraded = true
		status.LastError = model.PublicError(model.NewError(model.ErrorUnavailable, "client interface missing or ownership changed", true))
		return status
	}
	control, err := wgctrl.New()
	if err == nil {
		defer control.Close()
		var device *wgtypes.Device
		device, err = control.Device(e.state.Interface)
		if err == nil {
			status.Active = true
			status.ListenPort = uint16(device.ListenPort)
			if link, e := netlink.LinkByName(e.state.Interface); e == nil {
				status.MTU = link.Attrs().MTU
			}
			for _, p := range device.Peers {
				if !p.LastHandshakeTime.IsZero() && (status.LastHandshake == nil || p.LastHandshakeTime.After(*status.LastHandshake)) {
					value := p.LastHandshakeTime
					status.LastHandshake = &value
				}
			}
		}
	}
	if err != nil {
		status.Degraded = true
		status.LastError = model.PublicError(model.WrapError(model.ErrorUnavailable, "read kernel WireGuard status", true, err))
	}
	return status
}
