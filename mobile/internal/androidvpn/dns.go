package androidvpn

import (
	"context"
	"crypto/tls"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// DNS uses WireGuard's maintained gVisor netstack for bounded UDP and TCP DNS,
// not a hand-written TCP implementation. Only the virtual DNS /32 enters it.
type dnsStack struct {
	tun      tun.Device
	mu       sync.RWMutex
	plan     model.DNSPlan
	system   RPC
	udp, tcp *wire.Server
	slots    chan struct{}
	life     sync.RWMutex
	closed   bool
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}
type limitedConnection struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConnection) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConnection{Conn: c, release: func() { <-l.slots }}, nil
		default:
			c.Close()
		}
	}
}

func newDNSStack(ip netip.Addr, mtu int, system RPC, output func([]byte) error) (*dnsStack, error) {
	t, network, err := netstack.CreateNetTUN([]netip.Addr{ip}, nil, mtu)
	if err != nil {
		return nil, err
	}
	udp, err := network.ListenUDPAddrPort(netip.AddrPortFrom(ip, 53))
	if err != nil {
		t.Close()
		return nil, err
	}
	tcp, err := network.ListenTCPAddrPort(netip.AddrPortFrom(ip, 53))
	if err != nil {
		udp.Close()
		t.Close()
		return nil, err
	}
	d := &dnsStack{tun: t, system: system, slots: make(chan struct{}, 64)}
	handler := wire.HandlerFunc(d.serve)
	d.udp = &wire.Server{PacketConn: udp, Handler: handler, UDPSize: 1232, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	d.tcp = &wire.Server{Listener: &limitedListener{Listener: tcp, slots: make(chan struct{}, 32)}, Handler: handler, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxTCPQueries: 16}
	go d.udp.ActivateAndServe()
	go d.tcp.ActivateAndServe()
	go func() {
		buf := make([]byte, 65535)
		sizes := make([]int, 1)
		for {
			n, e := t.Read([][]byte{buf}, sizes, 0)
			if e != nil {
				return
			}
			if n > 0 {
				_ = output(buf[:sizes[0]])
			}
		}
	}()
	return d, nil
}
func (d *dnsStack) setPlan(p model.DNSPlan) { d.mu.Lock(); d.plan = p; d.mu.Unlock() }
func (d *dnsStack) inject(p []byte) {
	d.life.RLock()
	defer d.life.RUnlock()
	if !d.closed {
		_, _ = d.tun.Write([][]byte{p}, 0)
	}
}
func (d *dnsStack) close() {
	d.life.Lock()
	defer d.life.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = d.udp.ShutdownContext(ctx)
	_ = d.tcp.ShutdownContext(ctx)
	_ = d.tun.Close()
}
func (d *dnsStack) serve(w wire.ResponseWriter, q *wire.Msg) {
	fail := func() { answer := new(wire.Msg); answer.SetRcode(q, wire.RcodeServerFailure); _ = w.WriteMsg(answer) }
	select {
	case d.slots <- struct{}{}:
		defer func() { <-d.slots }()
	default:
		fail()
		return
	}
	if len(q.Question) != 1 || q.Response || q.Opcode != wire.OpcodeQuery {
		fail()
		return
	}
	d.mu.RLock()
	p := d.plan
	d.mu.RUnlock()
	name := strings.ToLower(q.Question[0].Name)
	suffix := strings.ToLower(strings.TrimSuffix(p.Suffix, ".")) + "."
	upstream := ""
	var alternatives []string
	tlsName := ""
	private := p.Suffix != "" && (name == suffix || strings.HasSuffix(name, "."+suffix))
	if private {
		upstream = net.JoinHostPort(p.Address, fmtPort(p.Port))
	} else {
		var configuration struct {
			Servers []string `json:"servers"`
			TLSName string   `json:"tlsName"`
		}
		if err := d.system.Call("network.dns", nil, &configuration); err != nil || len(configuration.Servers) == 0 {
			fail()
			return
		}
		tlsName = configuration.TLSName
		port := "53"
		if tlsName != "" {
			port = "853"
		}
		upstream = net.JoinHostPort(configuration.Servers[0], port)
		for _, server := range configuration.Servers[1:min(len(configuration.Servers), 4)] {
			if ip, err := netip.ParseAddr(server); err == nil {
				alternatives = append(alternatives, net.JoinHostPort(ip.String(), port))
			}
		}
	}
	transport := "udp"
	if strings.HasPrefix(w.LocalAddr().Network(), "tcp") {
		transport = "tcp"
	}
	query := q.Copy()
	query.AuthenticatedData = false
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	if !private {
		dialer.Control = socketControl(d.system)
	}
	c := &wire.Client{Net: transport, Timeout: 3 * time.Second, Dialer: dialer}
	if tlsName != "" {
		c.Net = "tcp-tls"
		c.TLSConfig = &tls.Config{ServerName: tlsName, MinVersion: tls.VersionTLS12}
	}
	answer, _, err := c.Exchange(query, upstream)
	if err != nil {
		for _, alternative := range alternatives {
			answer, _, err = c.Exchange(query, alternative)
			if err == nil {
				upstream = alternative
				break
			}
		}
	}
	if err == nil && answer.Truncated && transport == "udp" && tlsName == "" {
		c.Net = "tcp"
		answer, _, err = c.Exchange(query, upstream)
	}
	if err != nil {
		fail()
		return
	}
	answer.AuthenticatedData = false
	_ = w.WriteMsg(answer)
}
