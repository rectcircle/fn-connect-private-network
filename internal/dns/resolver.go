package dns

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/net/ipv4"
)

type Environment struct {
	Upstreams, Search, Prefixes []string
	NASHostname                 string
	NASAddresses                []string
	LANPrefixes                 []string
	Revision                    string
}
type Exchange func(context.Context, string, uint16, Environment) (model.DNSAnswer, error)
type cacheEntry struct {
	answer model.DNSAnswer
	until  time.Time
}
type Resolver struct {
	mu        sync.Mutex
	cache     map[string]cacheEntry
	slots     chan struct{}
	LAN, MDNS Exchange
}

func NewResolver() *Resolver {
	return &Resolver{cache: map[string]cacheEntry{}, slots: make(chan struct{}, 128), LAN: queryLAN, MDNS: queryMDNS}
}
func (r *Resolver) Resolve(ctx context.Context, q model.DNSQuery, env Environment) (model.DNSAnswer, error) {
	name := q.Name
	var err error
	if name != "" {
		name, err = NormalizeName(name)
	}
	if err != nil {
		return model.DNSAnswer{}, model.NewError(model.ErrorInvalidArgument, err.Error(), false)
	}
	if q.Type != "A" {
		return model.DNSAnswer{}, model.NewError(model.ErrorInvalidArgument, "only IPv4 A queries are supported", false)
	}
	typ := uint16(wire.TypeA)
	now := time.Now()
	key := env.Revision + "|" + name
	r.mu.Lock()
	// Bound the address cache; expired entries can be discarded.
	if len(r.cache) >= 4096 {
		for k, v := range r.cache {
			if !now.Before(v.until) {
				delete(r.cache, k)
			}
		}
	}
	ck := key + "|" + q.Type
	if cached, ok := r.cache[ck]; ok && now.Before(cached.until) {
		ans := cached.answer
		ans.Addresses = slices.Clone(ans.Addresses)
		ans.TTL = uint32(time.Until(cached.until).Seconds())
		r.mu.Unlock()
		return ans, nil
	}
	r.mu.Unlock()
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return model.DNSAnswer{}, model.NewError(model.ErrorResourceExhausted, "DNS concurrency exceeded", true)
	}
	if name == "" || env.NASHostname != "" && name == env.NASHostname {
		addresses := []string{}
		for _, value := range env.NASAddresses {
			ip, err := netip.ParseAddr(value)
			if err == nil && ip.Is4() && AllowedAddress(value, env.LANPrefixes) {
				addresses = append(addresses, value)
			}
		}
		result := "answer"
		if len(addresses) == 0 {
			result = "nodata"
		}
		return model.DNSAnswer{Name: name, Type: q.Type, Result: result, Source: "nas", ResolvedName: name, Addresses: addresses, TTL: 0, Revision: env.Revision}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	explicitMDNS := strings.HasSuffix(name, ".local")
	var ans model.DNSAnswer
	if explicitMDNS {
		ans, err = r.MDNS(ctx, nameForMDNS(name), typ, env)
	} else {
		lanCtx, stop := context.WithTimeout(ctx, 900*time.Millisecond)
		ans, err = r.LAN(lanCtx, name, typ, env)
		stop()
		if !strings.Contains(name, ".") && (err != nil || ans.Result == "nxdomain" || ans.Result == "nodata") {
			ans, err = r.MDNS(ctx, name+".local", typ, env)
		}
	}
	if err != nil {
		return model.DNSAnswer{}, model.WrapError(model.ErrorUnavailable, "LAN name resolution unavailable", true, err)
	}
	filtered := make([]string, 0, len(ans.Addresses))
	for _, v := range ans.Addresses {
		if AllowedAddress(v, env.Prefixes) {
			filtered = append(filtered, v)
		}
	}
	if ans.Result == "answer" && len(filtered) == 0 {
		failure := model.NewError(model.ErrorUnavailable, "DNS answer has no address in enabled networks", true)
		failure.RemoteCode = "DNS_NO_REACHABLE_ADDRESS"
		return model.DNSAnswer{}, failure
	}
	ans.Addresses = filtered
	ans.Name = name
	ans.Type = q.Type
	ans.Revision = env.Revision
	ans.TTL = min(ans.TTL, 3600)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ans.TTL > 0 && len(r.cache) < 4096 {
		r.cache[ck] = cacheEntry{ans, time.Now().Add(time.Duration(ans.TTL) * time.Second)}
	}
	return ans, nil
}
func nameForMDNS(name string) string {
	if strings.HasSuffix(name, ".local") {
		return name
	}
	return name + ".local"
}

func DetectEnvironment(settings model.ServerSettings) Environment {
	return detectEnvironment(settings, []string{"/etc/resolv.conf", "/run/systemd/resolve/resolv.conf"})
}
func detectEnvironment(settings model.ServerSettings, paths []string) Environment {
	env := Environment{LANPrefixes: slices.Clone(settings.LANCIDRs), Prefixes: append(slices.Clone(settings.LANCIDRs), settings.OverlayCIDR)}
	if hostname, err := os.Hostname(); err == nil {
		env.NASHostname, _ = NormalizeName(hostname)
	}
	var detected []string
	var stubs []string
	var search []string
	for _, path := range paths {
		c, err := wire.ClientConfigFromFile(path)
		if err != nil {
			continue
		}
		for _, v := range c.Servers {
			if a, e := netip.ParseAddr(v); e == nil && a.Is4() && a.IsLoopback() {
				stubs = append(stubs, v)
			} else if AllowedAddress(v, settings.LANCIDRs) {
				detected = append(detected, v)
			}
		}
		if len(search) == 0 {
			for _, v := range c.Search {
				if n, e := NormalizeName(v); e == nil {
					search = append(search, n)
				}
			}
		}
	}
	if len(env.Upstreams) == 0 {
		env.Upstreams = slices.Compact(detected)
		if len(env.Upstreams) == 0 {
			env.Upstreams = slices.Compact(stubs)
		}
	}
	if len(env.Search) == 0 {
		env.Search = search
	}
	env.Upstreams = env.Upstreams[:min(3, len(env.Upstreams))]
	env.Search = env.Search[:min(10, len(env.Search))]
	interfaces, _ := net.Interfaces()
	var links []string
	for _, iface := range interfaces {
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			p, e := netip.ParsePrefix(a.String())
			if e == nil && AllowedAddress(p.Addr().String(), env.LANPrefixes) {
				env.NASAddresses = append(env.NASAddresses, p.Addr().String())
				links = append(links, fmt.Sprint(iface.Index, "/", iface.Name, "/", iface.Flags, "/", a.String()))
			}
		}
	}
	slices.Sort(links)
	env.Revision = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%x/%s", shaEnvironment(env), strings.Join(links, "|")))))
	return env
}
func shaEnvironment(e Environment) [32]byte {
	return sha256.Sum256([]byte(e.NASHostname + "|" + strings.Join(append(append(slices.Clone(e.Upstreams), e.Search...), e.Prefixes...), "|")))
}

func queryLAN(ctx context.Context, name string, typ uint16, env Environment) (model.DNSAnswer, error) {
	if len(env.Upstreams) == 0 {
		return model.DNSAnswer{}, errors.New("no LAN DNS detected")
	}
	names := []string{name}
	if !strings.Contains(name, ".") {
		names = nil
		for _, s := range env.Search {
			if s != "local" && !strings.HasSuffix(s, ".local") {
				names = append(names, name+"."+s)
			}
		}
		names = append(names, name)
	}
	last := model.DNSAnswer{Result: "nxdomain", Source: "lan-dns", TTL: 0}
	for _, n := range names {
		if _, err := NormalizeName(n); err != nil {
			continue
		}
		var response model.DNSAnswer
		var err error
		for _, upstream := range env.Upstreams {
			response, err = queryChain(ctx, n, typ, net.JoinHostPort(upstream, "53"), env)
			if err == nil {
				break
			}
		}
		if err != nil {
			return model.DNSAnswer{}, err
		}
		last = response
		if response.Result != "nxdomain" {
			return response, nil
		}
	}
	return last, nil
}
func queryChain(ctx context.Context, name string, typ uint16, server string, env Environment) (model.DNSAnswer, error) {
	seen := map[string]bool{}
	ttl := uint32(3600)
	for range 8 {
		if seen[name] {
			return model.DNSAnswer{}, errors.New("CNAME cycle")
		}
		seen[name] = true
		msg := new(wire.Msg)
		msg.SetQuestion(wire.Fqdn(name), typ)
		msg.SetEdns0(1232, false)
		client := &wire.Client{Net: "udp", Timeout: 700 * time.Millisecond}
		reply, _, err := client.ExchangeContext(ctx, msg, server)
		if err != nil {
			return model.DNSAnswer{}, err
		}
		if reply.Truncated {
			client.Net = "tcp"
			reply, _, err = client.ExchangeContext(ctx, msg, server)
			if err != nil {
				return model.DNSAnswer{}, err
			}
		}
		result := model.DNSAnswer{Source: "lan-dns", ResolvedName: name, Addresses: []string{}, TTL: 0}
		switch reply.Rcode {
		case wire.RcodeNameError:
			result.Result = "nxdomain"
			result.TTL = negativeTTL(reply)
			return result, nil
		case wire.RcodeSuccess:
		default:
			return result, fmt.Errorf("LAN DNS rcode %d", reply.Rcode)
		}
		owner := wire.Fqdn(name)
		next := ""
		// Follow an in-message CNAME chain before accepting address records, never unrelated answers.
		for range 8 {
			found := ""
			for _, rr := range reply.Answer {
				if c, ok := rr.(*wire.CNAME); ok && strings.EqualFold(c.Hdr.Name, owner) {
					found = c.Target
					ttl = min(ttl, c.Hdr.Ttl)
					break
				}
			}
			if found == "" {
				break
			}
			if strings.EqualFold(found, owner) {
				return result, errors.New("CNAME cycle")
			}
			owner = found
			next = strings.TrimSuffix(strings.ToLower(found), ".")
		}
		for _, rr := range reply.Answer {
			if !strings.EqualFold(rr.Header().Name, owner) {
				continue
			}
			var addr string
			switch v := rr.(type) {
			case *wire.A:
				if typ == wire.TypeA {
					addr = v.A.String()
				}

			}
			if addr != "" {
				result.Addresses = append(result.Addresses, addr)
				ttl = min(ttl, rr.Header().Ttl)
			}
		}
		if len(result.Addresses) > 0 {
			result.Result = "answer"
			result.TTL = ttl
			result.Addresses = result.Addresses[:min(32, len(result.Addresses))]
			return result, nil
		}
		if next != "" {
			name = next
			continue
		}
		result.Result = "nodata"
		result.TTL = negativeTTL(reply)
		return result, nil
	}
	return model.DNSAnswer{}, errors.New("CNAME chain too long")
}
func negativeTTL(m *wire.Msg) uint32 {
	for _, rr := range m.Ns {
		if soa, ok := rr.(*wire.SOA); ok {
			return min(30, soa.Hdr.Ttl, soa.Minttl)
		}
	}
	return 0
}

func queryMDNS(ctx context.Context, name string, typ uint16, env Environment) (model.DNSAnswer, error) {
	ctx, cancel := context.WithTimeout(ctx, 900*time.Millisecond)
	defer cancel()
	ifaces, err := net.Interfaces()
	if err != nil {
		return model.DNSAnswer{}, err
	}
	type found struct {
		a   model.DNSAnswer
		err error
	}
	results := make(chan found, len(ifaces))
	count := 0
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, v := range addrs {
			p, e := netip.ParsePrefix(v.String())
			if e != nil || !p.Addr().Is4() || !AllowedAddress(p.Addr().String(), env.LANPrefixes) {
				continue
			}
			count++
			go func(iface net.Interface, addr netip.Addr) {
				a, e := mdnsInterface(ctx, iface, addr, name, typ, env)
				results <- found{a, e}
			}(iface, p.Addr())
			break
		}
	}
	if count == 0 {
		return model.DNSAnswer{}, errors.New("no enabled multicast LAN interface")
	}
	var failures error
	timedOut := false
	for range count {
		select {
		case got := <-results:
			if got.err != nil {
				var timeout net.Error
				if errors.As(got.err, &timeout) && timeout.Timeout() {
					timedOut = true
				} else {
					failures = errors.Join(failures, got.err)
				}
			}
			if got.err == nil && got.a.Result == "answer" {
				return got.a, nil
			}
		case <-ctx.Done():
			return model.DNSAnswer{Result: "nodata", Source: "mdns", ResolvedName: name, TTL: 0, Addresses: []string{}}, nil
		}
	}
	if failures != nil && !timedOut {
		return model.DNSAnswer{}, failures
	}
	return model.DNSAnswer{Result: "nodata", Source: "mdns", ResolvedName: name, TTL: 0, Addresses: []string{}}, nil
}
func mdnsInterface(ctx context.Context, iface net.Interface, addr netip.Addr, name string, typ uint16, env Environment) (model.DNSAnswer, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(addr.AsSlice())})
	if err != nil {
		return model.DNSAnswer{}, err
	}
	defer conn.Close()
	packet := ipv4.NewPacketConn(conn)
	if err = packet.SetMulticastInterface(&iface); err != nil {
		return model.DNSAnswer{}, err
	}
	_ = packet.SetMulticastTTL(255)
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	msg := new(wire.Msg)
	msg.SetQuestion(wire.Fqdn(name), typ)
	msg.RecursionDesired = false
	data, _ := msg.Pack()
	if _, err = conn.WriteToUDP(data, &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}); err != nil {
		return model.DNSAnswer{}, err
	}
	buf := make([]byte, 9000)
	for {
		n, from, e := conn.ReadFromUDP(buf)
		if e != nil {
			return model.DNSAnswer{}, e
		}
		if from.Port != 5353 || !AllowedAddress(from.IP.String(), env.LANPrefixes) {
			continue
		}
		var reply wire.Msg
		if reply.Unpack(buf[:n]) != nil || !reply.Response || reply.Id != msg.Id || reply.Rcode != wire.RcodeSuccess {
			continue
		}
		result := model.DNSAnswer{Source: "mdns", Result: "answer", ResolvedName: name, Addresses: []string{}, TTL: 0}
		for _, rr := range append(reply.Answer, reply.Extra...) {
			if !strings.EqualFold(rr.Header().Name, wire.Fqdn(name)) || rr.Header().Ttl == 0 {
				continue
			}
			switch v := rr.(type) {
			case *wire.A:
				if typ == wire.TypeA {
					result.Addresses = append(result.Addresses, v.A.String())
				}

			}
		}
		if len(result.Addresses) > 0 {
			result.Addresses = result.Addresses[:min(32, len(result.Addresses))]
			return result, nil
		}
	}
}
