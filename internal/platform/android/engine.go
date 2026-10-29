package androidvpn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
)

func fmtPort(p uint16) string { return strconv.Itoa(int(p)) }
func socketControl(system RPC) func(string, string, syscall.RawConn) error {
	return func(_, _ string, raw syscall.RawConn) error {
		var failure error
		err := raw.Control(func(fd uintptr) { failure = system.Call("network.protect", map[string]int{"fd": int(fd)}, nil) })
		return errors.Join(err, failure)
	}
}

type protectedBind struct {
	conn.Bind
	system RPC
}

func (b *protectedBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	fns, p, err := b.Bind.Open(port)
	if err != nil {
		return nil, 0, err
	}
	peek, ok := b.Bind.(conn.PeekLookAtSocketFd)
	if !ok {
		b.Close()
		return nil, 0, errors.New("WireGuard bind cannot expose Android sockets")
	}
	count := 0
	for _, get := range []func() (int, error){peek.PeekLookAtSocketFd4, peek.PeekLookAtSocketFd6} {
		fd, e := get()
		if e != nil {
			continue
		}
		if err := b.system.Call("network.protect", map[string]int{"fd": fd}, nil); err != nil {
			b.Close()
			return nil, 0, err
		}
		count++
	}
	if count == 0 {
		b.Close()
		return nil, 0, errors.New("no WireGuard sockets protected")
	}
	return fns, p, nil
}

type Engine struct {
	mu       sync.Mutex
	System   RPC
	Logger   *slog.Logger
	device   *device.Device
	tun      *packetTUN
	dns      *dnsStack
	plan     *model.ClientPlan
	dnsPlan  model.DNSPlan
	dnsIP    netip.Addr
	stopping bool
	// AdoptFD sets nonblocking mode before os.NewFile so Go can interrupt reads.
	AdoptFD func(int) (*os.File, error)
}

func (e *Engine) chooseDNSLocked(plan *model.ClientPlan) error {
	var snapshot struct {
		Prefixes []string `json:"prefixes"`
	}
	if err := e.System.Call("network.snapshot", nil, &snapshot); err != nil {
		return err
	}
	if e.dnsIP.IsValid() {
		for _, value := range snapshot.Prefixes {
			if prefix, err := netip.ParsePrefix(value); err != nil {
				return err
			} else if prefix.Contains(e.dnsIP) || prefix.Contains(e.dnsIP.Next()) {
				return errors.New("VPN DNS address conflicts with physical network; reconnect required")
			}
		}
		return nil
	}
	for _, candidate := range []string{"198.18.0.53", "198.19.0.53", "192.0.2.53", "192.0.2.54"} {
		ip := netip.MustParseAddr(candidate)
		conflict := false
		prefixes := append([]string(nil), snapshot.Prefixes...)
		if plan != nil {
			prefixes = append(prefixes, plan.AllowedIPs...)
			prefixes = append(prefixes, plan.ClientAddress)
		}
		for _, v := range prefixes {
			if p, err := netip.ParsePrefix(v); err == nil && (p.Contains(ip) || p.Contains(ip.Next())) {
				conflict = true
			}
		}
		if !conflict {
			e.dnsIP = ip
			return nil
		}
	}
	return errors.New("no non-overlapping VPN DNS address")
}
func (e *Engine) establishLocked(ctx context.Context, plan *model.ClientPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if e.AdoptFD == nil {
		return errors.New("Android TUN adapter is unavailable")
	}
	if err := e.chooseDNSLocked(plan); err != nil {
		return err
	}
	mtu := 1280
	address := e.dnsIP.Next().String() + "/32"
	routes := []string{}
	if plan != nil {
		mtu = plan.MTU
		address = plan.ClientAddress
		routes = append(routes, plan.AllowedIPs...)
	}
	if e.dnsPlan.Suffix != "" {
		routes = append(routes, e.dnsIP.String()+"/32")
	}
	request := map[string]any{"address": address, "routes": routes, "mtu": mtu, "dns": ""}
	if e.dnsPlan.Suffix != "" {
		request["dns"] = e.dnsIP.String()
	}
	var fd int
	if err := e.System.Call("vpn.establish", request, &fd); err != nil {
		return err
	}
	file, err := e.AdoptFD(fd)
	if err != nil {
		return err
	}
	if e.tun == nil {
		var stack *dnsStack
		e.tun = newPacketTUN(mtu, e.dnsIP, func(packet []byte) { stack.inject(packet) })
		stack, err = newDNSStack(e.dnsIP, mtu, e.System, e.tun.writePacket)
		if err != nil {
			file.Close()
			e.tun.Close()
			e.tun = nil
			return err
		}
		e.dns = stack
	}
	e.dns.setPlan(e.dnsPlan)
	e.tun.setMTU(mtu)
	return e.tun.replace(file)
}
func (e *Engine) Apply(ctx context.Context, plan model.ClientPlan) (model.ClientPrivilegedStatus, error) {
	normalized, err := wgconfig.NormalizeClientPlan(plan)
	if err != nil {
		return model.ClientPrivilegedStatus{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return model.ClientPrivilegedStatus{}, errors.New("Android VPN service is stopping")
	}
	uapi, err := wgconfig.ClientUAPI(normalized)
	if err != nil {
		return model.ClientPrivilegedStatus{}, err
	}
	old := e.plan
	if err := e.establishLocked(ctx, &normalized); err != nil {
		return model.ClientPrivilegedStatus{}, errors.Join(err, e.rollbackLocked(ctx, old))
	}
	if e.device == nil {
		logger := e.Logger
		if logger == nil {
			logger = slog.Default()
		}
		e.device = device.NewDevice(e.tun, &protectedBind{Bind: conn.NewDefaultBind(), system: e.System}, &device.Logger{Verbosef: device.DiscardLogf, Errorf: func(format string, args ...any) {
			logger.Error("wireguard device", "message", model.SafeText(fmt.Sprintf(format, args...)))
		}})
	}
	if err := e.device.IpcSet(uapi); err != nil {
		return model.ClientPrivilegedStatus{}, errors.Join(err, e.rollbackLocked(ctx, old))
	}
	if err := e.device.Up(); err != nil {
		return model.ClientPrivilegedStatus{}, errors.Join(err, e.rollbackLocked(ctx, old))
	}
	// Reopen and protect sockets on the selected physical Network, even when
	// WireGuard was already up during a Wi-Fi/cellular switch.
	if err := e.device.BindUpdate(); err != nil {
		return model.ClientPrivilegedStatus{}, errors.Join(err, e.rollbackLocked(ctx, old))
	}
	e.plan = &normalized
	return e.statusLocked(), nil
}
func (e *Engine) rollbackLocked(parent context.Context, old *model.ClientPlan) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer cancel()
	if old == nil {
		e.closeLocked()
		if e.dnsPlan.Suffix != "" {
			return e.establishLocked(ctx, nil)
		}
		return e.System.Call("vpn.remove", nil, nil)
	}
	if err := e.establishLocked(ctx, old); err != nil {
		e.closeLocked()
		return fmt.Errorf("rollback Android VPN: %w", err)
	}
	uapi, err := wgconfig.ClientUAPI(*old)
	if err == nil {
		err = e.device.IpcSet(uapi)
	}
	if err == nil {
		err = e.device.Up()
	}
	if err == nil {
		err = e.device.BindUpdate()
	}
	if err != nil {
		e.closeLocked()
		return fmt.Errorf("rollback WireGuard: %w", err)
	}
	e.plan = old
	return nil
}
func (e *Engine) Status(context.Context) (model.ClientPrivilegedStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(), nil
}
func (e *Engine) statusLocked() model.ClientPrivilegedStatus {
	s := model.ClientPrivilegedStatus{Active: e.device != nil && e.plan != nil, Interface: "android-vpn"}
	if !s.Active {
		return s
	}
	if !e.tun.healthy() {
		s.Active = false
		s.Degraded = true
		s.LastError = model.NewError(model.ErrorUnavailable, "Android VPN interface stopped", true)
		return s
	}
	s.MTU = e.plan.MTU
	uapi, err := e.device.IpcGet()
	if err != nil {
		s.Degraded = true
		s.LastError = model.PublicError(err)
		return s
	}
	var sec, nsec int64
	for _, line := range strings.Split(uapi, "\n") {
		key, value, _ := strings.Cut(line, "=")
		n, _ := strconv.ParseInt(value, 10, 64)
		switch key {
		case "listen_port":
			s.ListenPort = uint16(n)
		case "last_handshake_time_sec":
			sec = n
		case "last_handshake_time_nsec":
			nsec = n
		}
	}
	if sec > 0 {
		t := time.Unix(sec, nsec)
		s.LastHandshake = &t
	}
	return s
}
func (e *Engine) closeLocked() {
	if e.device != nil {
		e.device.Close()
		e.device = nil
	} else if e.tun != nil {
		e.tun.Close()
	}
	if e.dns != nil {
		e.dns.close()
		e.dns = nil
	}
	e.tun = nil
	e.plan = nil
	e.dnsIP = netip.Addr{}
}
func (e *Engine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
	if e.dnsPlan.Suffix != "" {
		return e.establishLocked(ctx, nil)
	}
	return e.System.Call("vpn.remove", nil, nil)
}
func (e *Engine) ApplyDNS(ctx context.Context, p model.DNSPlan) error {
	if p.Address != "127.0.0.1" || p.Port == 0 || p.Suffix == "" {
		return errors.New("invalid local DNS listener")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return errors.New("Android VPN service is stopping")
	}
	if e.dnsPlan == p {
		return nil
	}
	old := e.dnsPlan
	e.dnsPlan = p
	if err := e.establishLocked(ctx, e.plan); err != nil {
		e.dnsPlan = old
		return errors.Join(fmt.Errorf("apply VPN DNS: %w", err), e.rollbackLocked(ctx, e.plan))
	}
	return nil
}

// PrepareShutdown prevents shared Manager cleanup from preserving a DNS-only
// LOCAL tunnel while Android is destroying or revoking the system Service.
func (e *Engine) PrepareShutdown() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopping = true
	e.dnsPlan = model.DNSPlan{}
}
func (e *Engine) RenewDNS(ctx context.Context, p model.DNSPlan) error { return e.ApplyDNS(ctx, p) }
func (e *Engine) RemoveDNS(ctx context.Context, p model.DNSPlan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dnsPlan.Generation != p.Generation {
		return nil
	}
	old := e.dnsPlan
	e.dnsPlan = model.DNSPlan{}
	if e.plan == nil {
		e.closeLocked()
		return e.System.Call("vpn.remove", nil, nil)
	}
	if err := e.establishLocked(ctx, e.plan); err != nil {
		e.dnsPlan = old
		return errors.Join(err, e.rollbackLocked(ctx, e.plan))
	}
	return nil
}
