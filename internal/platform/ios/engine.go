package iosvpn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type System interface {
	Call(string, any, any) error
	WritePacket([]byte) error
}

// Engine implements client.PrivilegedNetwork in-process; there is no privileged daemon.
type Engine struct {
	Logger  *slog.Logger
	mu      sync.Mutex
	system  System
	device  *device.Device
	tun     *PacketTUN
	plan    *model.ClientPlan
	dns     *dnsStack
	dnsPlan model.DNSPlan
	dnsIP   netip.Addr
	// Last successfully installed OS settings; identical refreshes must not
	// reconfigure NetworkExtension and disturb existing TCP connections.
	networkSettings string
	stopping        bool
	userspace       bool
	stack           *netstack.Net
}

func NewEngine(s System) *Engine { return &Engine{system: s, Logger: slog.Default()} }

// NewUserspaceEngine owns an application-only stack; it never installs OS routes.
func NewUserspaceEngine() *Engine { return &Engine{userspace: true, Logger: slog.Default()} }
func (e *Engine) Apply(ctx context.Context, p model.ClientPlan) (model.ClientPrivilegedStatus, error) {
	p, err := wgconfig.NormalizeClientPlan(p)
	if err != nil {
		return model.ClientPrivilegedStatus{}, err
	}
	if err = ctx.Err(); err != nil {
		return model.ClientPrivilegedStatus{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return model.ClientPrivilegedStatus{}, errors.New("iOS tunnel stopping")
	}
	old := e.plan
	if err = e.installLocked(p); err != nil {
		e.closeLocked()
		var rollback error
		if old != nil {
			rollback = e.installLocked(*old)
		} else {
			rollback = e.configureLocked(nil)
		}
		if rollback != nil {
			e.closeLocked()
			if !e.userspace {
				rollback = errors.Join(rollback, e.system.Call("network.remove", nil, nil))
			}
		}
		return model.ClientPrivilegedStatus{}, errors.Join(err, rollback)
	}
	return e.statusLocked(), nil
}

func (e *Engine) installLocked(p model.ClientPlan) error {
	if err := e.configureLocked(&p); err != nil {
		return err
	}
	if e.plan != nil && (e.plan.MTU != p.MTU || (e.userspace && e.plan.ClientAddress != p.ClientAddress)) {
		e.closeLocked()
	}
	if e.device == nil {
		var transport tun.Device
		if e.userspace {
			prefix, err := netip.ParsePrefix(p.ClientAddress)
			if err != nil {
				return err
			}
			transport, e.stack, err = netstack.CreateNetTUN([]netip.Addr{prefix.Addr()}, nil, p.MTU)
			if err != nil {
				return err
			}
		} else {
			e.tun = NewPacketTUN(p.MTU, e.system.WritePacket)
			transport = e.tun
		}
		e.device = device.NewDevice(transport, conn.NewDefaultBind(), &device.Logger{Verbosef: device.DiscardLogf, Errorf: func(format string, args ...any) {
			e.Logger.Error("iOS WireGuard device error", "message", model.SafeText(fmt.Sprintf(format, args...)))
		}})
	}
	uapi, err := wgconfig.ClientUAPI(p)
	if err != nil {
		return err
	}
	if err = e.device.IpcSet(uapi); err != nil {
		return err
	}
	if err = e.device.Up(); err != nil {
		return err
	}
	if err = e.device.BindUpdate(); err != nil {
		return err
	}
	e.plan = &p
	return nil
}

func (e *Engine) NetworkChanged() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.device != nil && !e.stopping {
		if err := e.device.BindUpdate(); err != nil {
			e.Logger.Warn("WireGuard bind refresh failed")
		}
	}
}

func (e *Engine) Inject(p []byte) bool {
	e.mu.Lock()
	t := e.tun
	d, ip := e.dns, e.dnsIP
	e.mu.Unlock()
	if d != nil && len(p) >= 20 && p[0]>>4 == 4 && netip.AddrFrom4([4]byte{p[16], p[17], p[18], p[19]}) == ip {
		d.inject(p)
		return true
	}
	return t != nil && t.Inject(p)
}
func (e *Engine) Status(context.Context) (model.ClientPrivilegedStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.statusLocked(), nil
}
func (e *Engine) statusLocked() model.ClientPrivilegedStatus {
	s := model.ClientPrivilegedStatus{Active: e.device != nil && e.plan != nil, Interface: "ios-packet-tunnel"}
	if e.userspace {
		s.Interface = "simulator-app-netstack"
	}
	if !s.Active {
		return s
	}
	s.MTU = e.plan.MTU
	u, err := e.device.IpcGet()
	if err != nil {
		s.Degraded = true
		s.LastError = model.PublicError(err)
		return s
	}
	var sec, nsec int64
	for _, line := range strings.Split(u, "\n") {
		k, v, _ := strings.Cut(line, "=")
		n, _ := strconv.ParseInt(v, 10, 64)
		switch k {
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
	}
	e.device = nil
	e.tun = nil
	e.stack = nil
	e.plan = nil
}
func (e *Engine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
	if e.stopping && e.dns != nil {
		e.dns.close()
		e.dns = nil
	}
	return e.configureLocked(nil)
}
