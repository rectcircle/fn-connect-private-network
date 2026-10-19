package dns

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sync/singleflight"
)

type Lookup func(context.Context, model.DNSQuery) (model.DNSAnswer, error)
type Forwarder struct {
	ctx      context.Context
	cancel   context.CancelFunc
	udp, tcp *wire.Server
	suffix   string
	lookup   Lookup
	allowed  func(string) bool
	slots    chan struct{}
	group    singleflight.Group
}

func Listen(address, suffix string, lookup Lookup, allowed func(string) bool) (*Forwarder, uint16, error) {
	if err := ValidateSuffix(suffix); err != nil {
		return nil, 0, err
	}
	tcp, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, 0, err
	}
	udp, err := net.ListenPacket("udp4", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		return nil, 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &Forwarder{ctx: ctx, cancel: cancel, suffix: suffix, lookup: lookup, allowed: allowed, slots: make(chan struct{}, 128)}
	f.tcp = &wire.Server{Listener: &boundedDNSListener{Listener: tcp, slots: make(chan struct{}, 128)}, Handler: wire.HandlerFunc(f.serve), ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxTCPQueries: 16}
	f.udp = &wire.Server{PacketConn: udp, Handler: wire.HandlerFunc(f.serve), UDPSize: 1232, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	ready := make(chan struct{}, 2)
	f.tcp.NotifyStartedFunc = func() { ready <- struct{}{} }
	f.udp.NotifyStartedFunc = func() { ready <- struct{}{} }
	go f.tcp.ActivateAndServe()
	go f.udp.ActivateAndServe()
	for range 2 {
		select {
		case <-ready:
		case <-time.After(time.Second):
			f.Close()
			return nil, 0, errors.New("DNS listeners did not become ready")
		}
	}
	return f, uint16(tcp.Addr().(*net.TCPAddr).Port), nil
}
func (f *Forwarder) Close() {
	f.cancel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = f.udp.ShutdownContext(ctx)
	_ = f.tcp.ShutdownContext(ctx)
}
func (f *Forwarder) serve(w wire.ResponseWriter, q *wire.Msg) {
	host, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		return
	}
	a, err := netip.ParseAddr(host)
	if err != nil || !a.IsLoopback() {
		return
	}
	answer := new(wire.Msg)
	answer.SetReply(q)
	answer.RecursionAvailable = true
	answer.AuthenticatedData = false
	send := func() {
		size := 512
		if opt := q.IsEdns0(); opt != nil {
			size = max(512, min(1232, int(opt.UDPSize())))
			answer.SetEdns0(uint16(size), false)
		}
		if w.RemoteAddr().Network() == "udp" {
			answer.Truncate(size)
		}
		_ = w.WriteMsg(answer)
	}
	if len(q.Question) != 1 || q.Opcode != wire.OpcodeQuery || q.Response {
		answer.Rcode = wire.RcodeFormatError
		send()
		return
	}
	question := q.Question[0]
	name, err := Unwrap(question.Name, f.suffix)
	if err != nil {
		answer.Rcode = wire.RcodeRefused
		send()
		return
	}
	if question.Qclass != wire.ClassINET {
		answer.Rcode = wire.RcodeRefused
		send()
		return
	}
	if question.Qtype != wire.TypeA {
		send()
		return
	}
	select {
	case f.slots <- struct{}{}:
		defer func() { <-f.slots }()
	default:
		answer.Rcode = wire.RcodeServerFailure
		send()
		return
	}
	typ := wire.TypeToString[question.Qtype]
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	result := f.group.DoChan(name+"|"+typ, func() (any, error) { return f.lookup(ctx, model.DNSQuery{Name: name, Type: typ}) })
	var resolved model.DNSAnswer
	select {
	case got := <-result:
		if got.Err != nil {
			answer.Rcode = wire.RcodeServerFailure
			send()
			return
		}
		resolved = got.Val.(model.DNSAnswer)
	case <-ctx.Done():
		answer.Rcode = wire.RcodeServerFailure
		send()
		return
	}
	if resolved.Name != name || resolved.Type != typ || len(resolved.Addresses) > 32 || resolved.TTL > 3600 {
		answer.Rcode = wire.RcodeServerFailure
		send()
		return
	}
	switch resolved.Result {
	case "nxdomain":
		answer.Rcode = wire.RcodeNameError
	case "nodata":
	case "answer":
		for _, v := range resolved.Addresses {
			ip, e := netip.ParseAddr(v)
			if e != nil || !f.allowed(v) {
				continue
			}
			header := wire.RR_Header{Name: question.Name, Rrtype: question.Qtype, Class: wire.ClassINET, Ttl: min(resolved.TTL, 30)}
			if question.Qtype == wire.TypeA && ip.Is4() {
				answer.Answer = append(answer.Answer, &wire.A{Hdr: header, A: net.IP(ip.AsSlice())})
			}
		}
		if len(answer.Answer) == 0 {
			answer.Rcode = wire.RcodeServerFailure
		}
	default:
		answer.Rcode = wire.RcodeServerFailure
	}
	send()
}

// Limit idle TCP connections as well as active queries.
type boundedDNSListener struct {
	net.Listener
	slots chan struct{}
}
type boundedDNSConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (l *boundedDNSListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &boundedDNSConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}
func (c *boundedDNSConn) Close() error { err := c.Conn.Close(); c.once.Do(c.release); return err }
