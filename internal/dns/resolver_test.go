package dns

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func testEnv() Environment {
	return Environment{Prefixes: []string{"192.168.50.0/24"}, Revision: "revision-1"}
}
func result(source, address string) model.DNSAnswer {
	return model.DNSAnswer{Source: source, Result: "answer", Addresses: []string{address}, TTL: 30}
}
func TestLANFirstAndFallbackAfterFailure(t *testing.T) {
	for _, mode := range []string{"absent", "timeout", "nxdomain", "nodata", "answer"} {
		t.Run(mode, func(t *testing.T) {
			r := NewResolver()
			mdnsCalls := 0
			lanBroken := false
			r.LAN = func(context.Context, string, uint16, Environment) (model.DNSAnswer, error) {
				if lanBroken || mode == "timeout" || mode == "absent" {
					return model.DNSAnswer{}, errors.New("DNS offline")
				}
				if mode == "answer" {
					return result("lan-dns", "192.168.50.10"), nil
				}
				return model.DNSAnswer{Source: "lan-dns", Result: mode, TTL: 0}, nil
			}
			r.MDNS = func(_ context.Context, name string, _ uint16, _ Environment) (model.DNSAnswer, error) {
				mdnsCalls++
				if name != "abc.local" {
					t.Errorf("mDNS name %s", name)
				}
				a := result("mdns", "192.168.50.20")
				a.TTL = 0
				return a, nil
			}
			a, err := r.Resolve(t.Context(), model.DNSQuery{Name: "abc", Type: "A"}, testEnv())
			if err != nil {
				t.Fatal(err)
			}
			wantMDNS := mode == "absent" || mode == "timeout" || mode == "nxdomain" || mode == "nodata"
			if wantMDNS != (a.Source == "mdns") {
				t.Fatalf("wrong source %+v", a)
			}
			lanBroken = true
			r.mu.Lock()
			clear(r.cache)
			r.mu.Unlock()
			answer, err := r.Resolve(t.Context(), model.DNSQuery{Name: "abc", Type: "A"}, testEnv())
			if err != nil || answer.Source != "mdns" || mdnsCalls == 0 {
				t.Fatalf("LAN failure must fall back without source pinning: %+v %v", answer, err)
			}

		})
	}
}
func TestExplicitNamesAndAddressSafety(t *testing.T) {
	r := NewResolver()
	r.LAN = func(_ context.Context, name string, _ uint16, _ Environment) (model.DNSAnswer, error) {
		if name != "abc.home.lan" {
			t.Fatal(name)
		}
		return result("lan-dns", "8.8.8.8"), nil
	}
	r.MDNS = func(_ context.Context, name string, _ uint16, _ Environment) (model.DNSAnswer, error) {
		if name != "abc.local" {
			t.Fatal(name)
		}
		return result("mdns", "192.168.50.2"), nil
	}
	if _, e := r.Resolve(t.Context(), model.DNSQuery{Name: "abc.home.lan", Type: "A"}, testEnv()); e == nil {
		t.Fatal("public result accepted")
	}
	if a, e := r.Resolve(t.Context(), model.DNSQuery{Name: "abc.local", Type: "A"}, testEnv()); e != nil || a.Source != "mdns" {
		t.Fatalf("%+v %v", a, e)
	}
	for _, bad := range []string{"127.0.0.1", "0.0.0.0", "169.254.1.1", "224.0.0.251", "fe80::1", "8.8.8.8"} {
		if AllowedAddress(bad, []string{"0.0.0.0/0", "::/0"}) && bad != "8.8.8.8" {
			t.Fatal(bad)
		}
	}
}
func TestNamesAndSuffixIsolation(t *testing.T) {
	if DefaultSuffix("myhome") != "myhome.fncpn" {
		t.Fatal("default")
	}
	for _, bad := range []string{"../foo", "local", "my.local", "foo..fncpn", "x/evil.fncpn", "127.0.0.1", "-x.fncpn"} {
		if ValidateSuffix(bad) == nil {
			t.Fatal(bad)
		}
	}
	for _, name := range []string{"abc.notmyhome.fncpn", "abc.myhome.fncpn.evil"} {
		if _, e := Unwrap(name, "myhome.fncpn"); e == nil {
			t.Fatal(name)
		}
	}
	if name, err := Unwrap("MYHOME.FNCPN.", "myhome.fncpn"); err != nil || name != "" {
		t.Fatalf("suffix apex must identify NAS: %q %v", name, err)
	}
	if name, e := Unwrap("ABC.LOCAL.MYHOME.FNCPN.", "myhome.fncpn"); e != nil || name != "abc.local" {
		t.Fatalf("%s %v", name, e)
	}
}
func TestForwarderUDPAndTCP(t *testing.T) {
	var calls atomic.Int32
	f, port, err := Listen("127.0.0.1:0", "myhome.fncpn", func(_ context.Context, q model.DNSQuery) (model.DNSAnswer, error) {
		calls.Add(1)
		a := result("mdns", "192.168.50.20")
		a.Name = q.Name
		a.Type = q.Type
		return a, nil
	}, func(s string) bool { return AllowedAddress(s, []string{"192.168.50.0/24"}) })
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, transport := range []string{"udp", "tcp"} {
		c := &wire.Client{Net: transport, Timeout: time.Second}
		q := new(wire.Msg)
		q.SetQuestion("abc.local.myhome.fncpn.", wire.TypeA)
		a, _, e := c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if e != nil || len(a.Answer) != 1 {
			t.Fatalf("%s %+v %v", transport, a, e)
		}
		if a.Answer[0].Header().Name != q.Question[0].Name {
			t.Fatal("lost wrapped name")
		}
		for range 60 {
			repeated, _, err := c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
			if err != nil || repeated.Rcode != wire.RcodeSuccess || len(repeated.Answer) != 1 {
				t.Fatalf("normal query burst rejected: %+v %v", repeated, err)
			}
		}
		q.SetQuestion("myhome.fncpn.", wire.TypeA)
		a, _, e = c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if e != nil || a.Rcode != wire.RcodeSuccess || len(a.Answer) != 1 || a.Answer[0].Header().Name != "myhome.fncpn." {
			t.Fatalf("suffix apex failed: %+v %v", a, e)
		}
		q.SetQuestion("abc.local.", wire.TypeA)
		a, _, e = c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if e != nil || a.Rcode != wire.RcodeRefused {
			t.Fatalf("outside namespace %+v %v", a, e)
		}
		q.SetQuestion("myhome.fncpn.", wire.TypeAAAA)
		a, _, e = c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if e != nil || a.Rcode != wire.RcodeSuccess || len(a.Answer) != 0 {
			t.Fatalf("AAAA must return local NODATA: %+v %v", a, e)
		}
		q.SetQuestion("abc.myhome.fncpn.", wire.TypeHTTPS)
		a, _, e = c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
		if e != nil || a.Rcode != wire.RcodeSuccess || len(a.Answer) != 0 {
			t.Fatal("unsupported type must not return NXDOMAIN")
		}
	}
	if calls.Load() != 124 {
		t.Fatalf("outside namespace queried upstream: %d", calls.Load())
	}
}
func TestForwarderRejectsUnroutedAnswer(t *testing.T) {
	f, port, e := Listen("127.0.0.1:0", "myhome.fncpn", func(_ context.Context, q model.DNSQuery) (model.DNSAnswer, error) {
		a := result("lan-dns", "192.168.50.20")
		a.Name = q.Name
		a.Type = q.Type
		return a, nil
	}, func(string) bool { return false })
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	q := new(wire.Msg)
	q.SetQuestion("abc.myhome.fncpn.", wire.TypeA)
	c := &wire.Client{Timeout: time.Second}
	a, _, e := c.Exchange(q, net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))))
	if e != nil || a.Rcode != wire.RcodeServerFailure {
		t.Fatalf("%+v %v", a, e)
	}
}

func TestLANWireCNAMEAndTruncatedTCP(t *testing.T) {
	tcp, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	udp, e := net.ListenPacket("udp4", tcp.Addr().String())
	if e != nil {
		tcp.Close()
		t.Fatal(e)
	}
	var tcpCalls atomic.Int32
	handler := wire.HandlerFunc(func(w wire.ResponseWriter, q *wire.Msg) {
		a := new(wire.Msg)
		a.SetReply(q)
		if w.RemoteAddr().Network() == "udp" {
			a.Truncated = true
			_ = w.WriteMsg(a)
			return
		}
		tcpCalls.Add(1)
		switch q.Question[0].Name {
		case "abc.home.lan.":
			a.Answer = []wire.RR{&wire.CNAME{Hdr: wire.RR_Header{Name: "abc.home.lan.", Rrtype: wire.TypeCNAME, Class: wire.ClassINET, Ttl: 5}, Target: "target.home.lan."}, &wire.A{Hdr: wire.RR_Header{Name: "target.home.lan.", Rrtype: wire.TypeA, Class: wire.ClassINET, Ttl: 60}, A: net.IPv4(192, 168, 50, 10)}}
		case "missing.home.lan.":
			a.Rcode = wire.RcodeNameError
			a.Ns = []wire.RR{&wire.SOA{Hdr: wire.RR_Header{Name: "home.lan.", Rrtype: wire.TypeSOA, Class: wire.ClassINET, Ttl: 100}, Ns: "router.home.lan.", Mbox: "hostmaster.home.lan.", Minttl: 15}}
		case "empty.home.lan.":
		case "cycle.home.lan.":
			a.Answer = []wire.RR{&wire.CNAME{Hdr: wire.RR_Header{Name: "cycle.home.lan.", Rrtype: wire.TypeCNAME, Class: wire.ClassINET, Ttl: 30}, Target: "cycle.home.lan."}}
		}
		_ = w.WriteMsg(a)
	})
	ready := make(chan struct{}, 2)
	servers := []*wire.Server{{Listener: tcp, Handler: handler}, {PacketConn: udp, Handler: handler}}
	for _, server := range servers {
		server.NotifyStartedFunc = func() { ready <- struct{}{} }
		go server.ActivateAndServe()
		defer server.Shutdown()
	}
	<-ready
	<-ready
	for _, tc := range []struct {
		name, result string
		ttl          uint32
	}{{"abc.home.lan", "answer", 5}, {"missing.home.lan", "nxdomain", 15}, {"empty.home.lan", "nodata", 0}} {
		a, e := queryChain(t.Context(), tc.name, wire.TypeA, tcp.Addr().String(), testEnv())
		if e != nil || a.Result != tc.result || a.TTL != tc.ttl {
			t.Fatalf("%s %+v %v", tc.name, a, e)
		}
	}
	if _, e = queryChain(t.Context(), "cycle.home.lan", wire.TypeA, tcp.Addr().String(), testEnv()); e == nil {
		t.Fatal("CNAME loop accepted")
	}
	if tcpCalls.Load() != 4 {
		t.Fatalf("truncation did not retry TCP: %d", tcpCalls.Load())
	}
}

func TestDetectLANAndConfiguredLocalStub(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub.conf")
	upstream := filepath.Join(dir, "real.conf")
	if e := os.WriteFile(stub, []byte("nameserver 127.0.0.53\nsearch home.lan\n"), 0600); e != nil {
		t.Fatal(e)
	}
	settings := model.ServerSettings{LANCIDRs: []string{"192.168.50.0/24"}, OverlayCIDR: "10.253.203.0/24"}
	env := detectEnvironment(settings, []string{stub})
	if len(env.Upstreams) != 1 || env.Upstreams[0] != "127.0.0.53" || len(env.Search) != 1 || env.Search[0] != "home.lan" {
		t.Fatalf("configured local resolver lost: %+v", env)
	}
	if e := os.WriteFile(upstream, []byte("nameserver 192.168.50.1\nnameserver 8.8.8.8\n"), 0600); e != nil {
		t.Fatal(e)
	}
	env = detectEnvironment(settings, []string{stub, upstream})
	if len(env.Upstreams) != 1 || env.Upstreams[0] != "192.168.50.1" {
		t.Fatalf("LAN upstream not preferred: %+v", env)
	}
	if e := os.WriteFile(stub, []byte("nameserver 8.8.8.8\n"), 0600); e != nil {
		t.Fatal(e)
	}
	env = detectEnvironment(settings, []string{stub})
	if len(env.Upstreams) != 0 {
		t.Fatal("public system resolver used for private queries")
	}
}

func TestNASAliasUsesEnabledLocalAddressesWithoutUpstream(t *testing.T) {
	r := NewResolver()
	r.LAN = func(context.Context, string, uint16, Environment) (model.DNSAnswer, error) {
		t.Fatal("NAS alias used upstream")
		return model.DNSAnswer{}, nil
	}
	r.MDNS = r.LAN
	env := Environment{Revision: "nas", NASHostname: "mi-nuc", LANPrefixes: []string{"192.168.50.0/24"}, NASAddresses: []string{"192.168.50.2", "10.99.0.2"}}
	for _, name := range []string{"mi-nuc", ""} {
		typ := "A"
		answer, err := r.Resolve(t.Context(), model.DNSQuery{Name: name, Type: typ}, env)
		if err != nil {
			t.Fatal(err)
		}
		if typ == "A" && (len(answer.Addresses) != 1 || answer.Addresses[0] != "192.168.50.2") {
			t.Fatalf("%+v", answer)
		}

	}
}

func TestNASNameIsNotReservedWhenHostnameIsDifferent(t *testing.T) {
	r := NewResolver()
	r.LAN = func(context.Context, string, uint16, Environment) (model.DNSAnswer, error) {
		return model.DNSAnswer{Result: "answer", Source: "lan-dns", Addresses: []string{"192.168.50.9"}}, nil
	}
	env := Environment{Revision: "hostname", NASHostname: "mi-nuc", NASAddresses: []string{"192.168.50.2"}, LANPrefixes: []string{"192.168.50.0/24"}, Prefixes: []string{"192.168.50.0/24"}}
	answer, err := r.Resolve(t.Context(), model.DNSQuery{Name: "nas", Type: "A"}, env)
	if err != nil || len(answer.Addresses) != 1 || answer.Addresses[0] != "192.168.50.9" || answer.Source != "lan-dns" {
		t.Fatalf("%+v %v", answer, err)
	}
}

func TestFilteredAddressHasStructuredDiagnosticReason(t *testing.T) {
	r := NewResolver()
	r.MDNS = func(context.Context, string, uint16, Environment) (model.DNSAnswer, error) {
		return model.DNSAnswer{Result: "answer", Source: "mdns", Addresses: []string{"10.99.0.1"}}, nil
	}
	_, err := r.Resolve(t.Context(), model.DNSQuery{Name: "device.local", Type: "A"}, Environment{Prefixes: []string{"192.168.71.0/24"}, Revision: "ipv6"})
	if err == nil || model.AsError(err).Code != model.ErrorUnavailable || model.AsError(err).RemoteCode != "DNS_NO_REACHABLE_ADDRESS" {
		t.Fatalf("%v", err)
	}
}

func TestResolverRejectsIPv6Queries(t *testing.T) {
	r := NewResolver()
	unexpected := func(context.Context, string, uint16, Environment) (model.DNSAnswer, error) {
		t.Fatal("IPv6 query reached upstream")
		return model.DNSAnswer{}, nil
	}
	r.LAN, r.MDNS = unexpected, unexpected
	if _, err := r.Resolve(t.Context(), model.DNSQuery{Name: "abc", Type: "AAAA"}, testEnv()); err == nil {
		t.Fatal("AAAA query accepted")
	}
	for _, address := range []string{"fd00::1", "::ffff:192.168.50.20"} {
		if AllowedAddress(address, []string{"fd00::/64", "192.168.50.0/24"}) {
			t.Fatalf("IPv6 address accepted: %s", address)
		}
	}
}
