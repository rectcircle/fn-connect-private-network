package iosvpn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// The system sends only matched suffixes to this virtual /32. The maintained
// netstack supplies UDP/TCP; the shared daemon's DNS forwarder validates answers.
type dnsStack struct {
	tun      tun.Device
	udp, tcp *wire.Server
	mu       sync.RWMutex
	plan     model.DNSPlan
	slots    chan struct{}
	closed   bool
}

// Bound accepted TCP sessions as well as in-flight queries.
type boundedListener struct {
	net.Listener
	slots chan struct{}
}
type boundedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *boundedConnection) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *boundedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &boundedConnection{Conn: c, release: func() { <-l.slots }}, nil
		default:
			c.Close()
		}
	}
}

func newDNS(ip netip.Addr, output func([]byte) error) (*dnsStack, error) {
	t, n, err := netstack.CreateNetTUN([]netip.Addr{ip}, nil, 1280)
	if err != nil {
		return nil, err
	}
	udp, err := n.ListenUDPAddrPort(netip.AddrPortFrom(ip, 53))
	if err != nil {
		t.Close()
		return nil, err
	}
	tcp, err := n.ListenTCPAddrPort(netip.AddrPortFrom(ip, 53))
	if err != nil {
		udp.Close()
		t.Close()
		return nil, err
	}
	d := &dnsStack{tun: t, slots: make(chan struct{}, 32)}
	d.udp = &wire.Server{PacketConn: udp, Handler: wire.HandlerFunc(d.serve), UDPSize: 1232, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second}
	d.tcp = &wire.Server{Listener: &boundedListener{Listener: tcp, slots: make(chan struct{}, 32)}, Handler: wire.HandlerFunc(d.serve), ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxTCPQueries: 16}
	go d.udp.ActivateAndServe()
	go d.tcp.ActivateAndServe()
	go func() {
		buf := make([]byte, 65535)
		sizes := make([]int, 1)
		for {
			count, e := t.Read([][]byte{buf}, sizes, 0)
			if e != nil {
				return
			}
			if count > 0 {
				_ = output(buf[:sizes[0]])
			}
		}
	}()
	return d, nil
}
func (d *dnsStack) serve(w wire.ResponseWriter, q *wire.Msg) {
	fail := func() { r := new(wire.Msg); r.SetRcode(q, wire.RcodeServerFailure); _ = w.WriteMsg(r) }
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	default:
		fail()
		return
	}
	d.mu.RLock()
	p := d.plan
	d.mu.RUnlock()
	if len(q.Question) != 1 || q.Response || q.Opcode != wire.OpcodeQuery {
		fail()
		return
	}
	name := strings.ToLower(q.Question[0].Name)
	suffix := strings.ToLower(strings.TrimSuffix(p.Suffix, ".")) + "."
	if p.Suffix == "" || (name != suffix && !strings.HasSuffix(name, "."+suffix)) {
		fail()
		return
	}
	transport := "udp"
	if strings.HasPrefix(w.LocalAddr().Network(), "tcp") {
		transport = "tcp"
	}
	client := &wire.Client{Net: transport, Timeout: 3 * time.Second}
	answer, _, err := client.Exchange(q, net.JoinHostPort(p.Address, strconv.Itoa(int(p.Port))))
	if err != nil {
		fail()
		return
	}
	_ = w.WriteMsg(answer)
}
func (d *dnsStack) inject(p []byte) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if !d.closed {
		_, _ = d.tun.Write([][]byte{p}, 0)
	}
}
func (d *dnsStack) setPlan(p model.DNSPlan) { d.mu.Lock(); d.plan = p; d.mu.Unlock() }
func (d *dnsStack) close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = d.udp.ShutdownContext(ctx)
	_ = d.tcp.ShutdownContext(ctx)
	_ = d.tun.Close()
}

func (e *Engine) configureLocked(p *model.ClientPlan) error {
	if e.stopping {
		// iOS removes the provider's routes when stopTunnel completes. Calling
		// setTunnelNetworkSettings during that stop can stall its completion.
		return nil
	}
	if e.userspace {
		return nil
	} // DNS is explicitly queried by the app, never installed system-wide.
	if p == nil && e.dnsPlan.Suffix == "" {
		e.networkSettings = ""
		return e.system.Call("network.remove", nil, nil)
	}
	settings := map[string]any{"clientAddress": "198.18.0.54/32", "allowedIPs": []string{}, "mtu": 1280}
	routes := []string{}
	if p != nil {
		settings["clientAddress"] = p.ClientAddress
		settings["mtu"] = p.MTU
		routes = append(routes, p.AllowedIPs...)
	}
	if e.dnsPlan.Suffix != "" {
		if !e.dnsIP.IsValid() {
			snapshot, err := NewNetwork().Snapshot()
			if err != nil {
				return err
			}
			for _, candidate := range []string{"198.18.0.53", "198.19.0.53", "192.0.2.53"} {
				ip := netip.MustParseAddr(candidate)
				conflict := false
				for _, prefix := range snapshot.Prefixes {
					if prefix.Contains(ip) || prefix.Contains(ip.Next()) {
						conflict = true
					}
				}
				if p != nil {
					for _, route := range p.AllowedIPs {
						prefix, err := netip.ParsePrefix(route)
						if err != nil {
							return err
						}
						if prefix.Contains(ip) || prefix.Contains(ip.Next()) {
							conflict = true
						}
					}
				}
				if !conflict {
					e.dnsIP = ip
					break
				}
			}
			if !e.dnsIP.IsValid() {
				return errors.New("no non-overlapping iOS DNS address")
			}
		}
		if e.dns == nil {
			d, err := newDNS(e.dnsIP, e.system.WritePacket)
			if err != nil {
				return err
			}
			e.dns = d
		}
		e.dns.setPlan(e.dnsPlan)
		if p == nil {
			settings["clientAddress"] = e.dnsIP.Next().String() + "/32"
		}
		routes = append(routes, e.dnsIP.String()+"/32")
		settings["dnsIP"] = e.dnsIP.String()
		settings["dnsSuffix"] = e.dnsPlan.Suffix
	}
	settings["allowedIPs"] = routes
	encoded, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	if string(encoded) == e.networkSettings {
		return nil
	}
	// An unsuccessful platform call may have partially changed OS state. Force
	// the rollback to reinstall its settings instead of trusting the old cache.
	e.networkSettings = ""
	if err := e.system.Call("network.apply", settings, nil); err != nil {
		return err
	}
	e.networkSettings = string(encoded)
	return nil
}
func (e *Engine) ApplyDNS(ctx context.Context, p model.DNSPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.Address != "127.0.0.1" || p.Port == 0 || p.Suffix == "" {
		return errors.New("invalid DNS plan")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping {
		return errors.New("iOS tunnel stopping")
	}
	old := e.dnsPlan
	e.dnsPlan = p
	if err := e.configureLocked(e.plan); err != nil {
		e.dnsPlan = old
		return errors.Join(err, e.configureLocked(e.plan))
	}
	return nil
}
func (e *Engine) RenewDNS(ctx context.Context, p model.DNSPlan) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopping || e.dnsPlan.Suffix == "" || e.dnsPlan != p {
		return errors.New("iOS DNS plan is no longer active")
	}
	// The extension owns this plan for its lifetime; unlike the privileged
	// desktop service it has no expiring cross-process lease to refresh.
	return nil
}
func (e *Engine) RemoveDNS(ctx context.Context, p model.DNSPlan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.dnsPlan.Generation != p.Generation {
		return nil
	}
	old := e.dnsPlan
	e.dnsPlan = model.DNSPlan{}
	if err := e.configureLocked(e.plan); err != nil {
		e.dnsPlan = old
		return errors.Join(err, e.configureLocked(e.plan))
	}
	if e.dns != nil {
		e.dns.close()
		e.dns = nil
	}
	return nil
}
func (e *Engine) PrepareShutdown() {
	e.mu.Lock()
	e.stopping = true
	e.dnsPlan = model.DNSPlan{}
	e.mu.Unlock()
}
