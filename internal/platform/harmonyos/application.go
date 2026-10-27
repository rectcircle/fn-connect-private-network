package harmonyos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// applyApplicationLocked follows the iOS simulator model: the shared Manager,
// authorization, relay and WireGuard remain real, with no system VPN or routes.
func (e *Engine) applyApplicationLocked(plan model.ClientPlan) (model.ClientPrivilegedStatus, error) {
	old := e.plan
	if err := e.installApplicationLocked(plan); err != nil {
		e.closeLocked()
		var rollback error
		if old != nil {
			rollback = e.installApplicationLocked(*old)
		}
		if rollback != nil {
			e.closeLocked()
		}
		return model.ClientPrivilegedStatus{}, errors.Join(err, rollback)
	}
	return e.statusLocked(), nil
}
func (e *Engine) installApplicationLocked(plan model.ClientPlan) error {
	if e.plan != nil && (e.plan.MTU != plan.MTU || e.plan.ClientAddress != plan.ClientAddress) {
		e.closeLocked()
	}
	if e.device == nil {
		prefix, err := netip.ParsePrefix(plan.ClientAddress)
		if err != nil {
			return err
		}
		transport, stack, err := netstack.CreateNetTUN([]netip.Addr{prefix.Addr()}, nil, plan.MTU)
		if err != nil {
			return err
		}
		e.stack = stack
		logger := e.Logger
		if logger == nil {
			logger = slog.Default()
		}
		e.device = device.NewDevice(transport, &udpBind{}, &device.Logger{Verbosef: device.DiscardLogf, Errorf: func(format string, args ...any) {
			logger.Error("application WireGuard", "message", model.SafeText(fmt.Sprintf(format, args...)))
		}})
	}
	uapi, err := wgconfig.ClientUAPI(plan)
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
		return err
	}
	e.plan = &plan
	return nil
}

// DialPrivate enters only configured application tunnel routes. LOCAL mode uses
// the physical network, just as the iOS simulator implementation does.
func (e *Engine) DialPrivate(ctx context.Context, network, address string) (net.Conn, error) {
	e.mu.Lock()
	active := e.plan != nil && !e.stopping
	stack := e.stack
	var routes []string
	if e.plan != nil {
		routes = append(routes, e.plan.AllowedIPs...)
	}
	e.mu.Unlock()
	if !active {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || !ip.Is4() || n < 1 || n > 65535 || (!ip.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)) {
		return nil, errors.New("invalid application tunnel destination")
	}
	allowed := false
	for _, route := range routes {
		if p, err := netip.ParsePrefix(route); err == nil && p.Contains(ip) {
			allowed = true
			break
		}
	}
	if !allowed || stack == nil {
		return nil, errors.New("destination is outside application tunnel routes")
	}
	return stack.DialContext(ctx, network, address)
}

// DialApplication resolves private names through Manager's suffix-scoped DNS,
// rather than changing the emulator's system resolver.
func (e *Engine) DialApplication(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(host); err != nil {
		ips, err := e.LookupApplication(ctx, host)
		if err != nil {
			return nil, err
		}
		address = net.JoinHostPort(ips[0], port)
	}
	return e.DialPrivate(ctx, network, address)
}

func (e *Engine) LookupApplication(ctx context.Context, host string) ([]string, error) {
	e.mu.Lock()
	plan := e.dnsPlan
	e.mu.Unlock()
	name := strings.TrimSuffix(strings.ToLower(host), ".")
	suffix := strings.TrimSuffix(strings.ToLower(plan.Suffix), ".")
	if suffix == "" || (name != suffix && !strings.HasSuffix(name, "."+suffix)) {
		return nil, errors.New("name is outside configured private DNS suffix")
	}
	query := new(wire.Msg)
	query.SetQuestion(wire.Fqdn(name), wire.TypeA)
	answer, _, err := (&wire.Client{Timeout: 3 * time.Second}).ExchangeContext(ctx, query, net.JoinHostPort(plan.Address, strconv.Itoa(int(plan.Port))))
	if err != nil {
		return nil, err
	}
	ips := []string{}
	if answer.Rcode == wire.RcodeSuccess {
		for _, record := range answer.Answer {
			if a, ok := record.(*wire.A); ok {
				ips = append(ips, a.A.String())
			}
		}
	}
	if len(ips) == 0 {
		return nil, errors.New("private DNS returned no IPv4 address")
	}
	return ips, nil
}
