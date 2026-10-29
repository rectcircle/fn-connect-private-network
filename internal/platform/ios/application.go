package iosvpn

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	wire "github.com/miekg/dns"
)

// LookupApplication explicitly asks the shared daemon's suffix-scoped resolver.
// No host OS DNS setting is changed in simulator mode.
func (e *Engine) LookupApplication(ctx context.Context, name string) ([]string, error) {
	e.mu.Lock()
	p := e.dnsPlan
	e.mu.Unlock()
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	suffix := strings.TrimSuffix(strings.ToLower(p.Suffix), ".")
	if suffix == "" || (name != suffix && !strings.HasSuffix(name, "."+suffix)) {
		return nil, errors.New("name is outside the configured private DNS suffix")
	}
	q := new(wire.Msg)
	q.SetQuestion(wire.Fqdn(name), wire.TypeA)
	reply, _, err := (&wire.Client{Timeout: 3 * time.Second}).ExchangeContext(ctx, q, net.JoinHostPort(p.Address, strconv.Itoa(int(p.Port))))
	if err != nil {
		return nil, err
	}
	if reply.Rcode != wire.RcodeSuccess {
		return nil, errors.New("private DNS lookup failed")
	}
	var addresses []string
	for _, answer := range reply.Answer {
		if a, ok := answer.(*wire.A); ok {
			addresses = append(addresses, a.A.String())
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("private DNS returned no IPv4 address")
	}
	return addresses, nil
}

func (e *Engine) DialApplication(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err = netip.ParseAddr(host); err != nil {
		ips, err := e.LookupApplication(ctx, host)
		if err != nil {
			return nil, err
		}
		address = net.JoinHostPort(ips[0], port)
	}
	return e.DialPrivate(ctx, network, address)
}
