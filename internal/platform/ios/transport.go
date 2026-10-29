package iosvpn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Private API HTTP must explicitly enter the provider's own tunnel on iOS.
// Control/WSS/LOCAL probes intentionally keep using the physical network.
func (e *Engine) DialPrivate(ctx context.Context, network, address string) (net.Conn, error) {
	e.mu.Lock()
	remote := e.plan != nil && !e.stopping
	stack := e.stack
	userspace := e.userspace
	var routes []string
	if e.plan != nil {
		routes = append(routes, e.plan.AllowedIPs...)
	}
	e.mu.Unlock()
	if !remote {
		return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, parseErr := netip.ParseAddr(host)
	number, portErr := strconv.Atoi(port)
	private := ip.IsPrivate() || (userspace && netip.MustParsePrefix("100.64.0.0/10").Contains(ip))
	if parseErr != nil || !ip.Is4() || !private || portErr != nil || number < 1 || number > 65535 {
		return nil, errors.New("invalid private API destination")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if userspace {
		allowed := false
		for _, route := range routes {
			if prefix, e := netip.ParsePrefix(route); e == nil && prefix.Contains(ip) {
				allowed = true
				break
			}
		}
		if !allowed || stack == nil {
			return nil, errors.New("destination is outside the simulator tunnel routes")
		}
		return stack.DialContext(ctx, network, address)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err = e.system.Call("tcp.open", map[string]string{"host": host, "port": port}, &result); err != nil {
		return nil, err
	}
	if result.ID == "" {
		return nil, errors.New("missing iOS TCP connection")
	}
	c := &privateConn{system: e.system, id: result.ID, address: address}
	if err = ctx.Err(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

type privateConn struct {
	system                      System
	id, address                 string
	mu                          sync.Mutex
	readDeadline, writeDeadline time.Time
	closed                      bool
	readMu, writeMu             sync.Mutex
}

func (c *privateConn) timeout(read bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	d := c.writeDeadline
	if read {
		d = c.readDeadline
	}
	timeout := 3 * time.Second
	if !d.IsZero() {
		if remaining := time.Until(d); remaining < timeout {
			timeout = remaining
		}
	}
	if timeout <= 0 {
		return 0, context.DeadlineExceeded
	}
	return max(1, int(timeout.Milliseconds())), nil
}
func (c *privateConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	timeout, err := c.timeout(true)
	if err != nil {
		return 0, err
	}
	var r struct {
		Data []byte `json:"data"`
	}
	if err = c.system.Call("tcp.read", map[string]any{"id": c.id, "limit": min(len(p), 16384), "timeoutMs": timeout}, &r); err != nil {
		return 0, err
	}
	if len(r.Data) == 0 {
		return 0, io.EOF
	}
	if len(r.Data) > len(p) {
		return 0, io.ErrShortBuffer
	}
	return copy(p, r.Data), nil
}
func (c *privateConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		timeout, err := c.timeout(false)
		if err != nil {
			return written, err
		}
		n := min(len(p), 16384)
		if err = c.system.Call("tcp.write", map[string]any{"id": c.id, "data": p[:n], "timeoutMs": timeout}, nil); err != nil {
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}
func (c *privateConn) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.system.Call("tcp.close", map[string]string{"id": c.id}, nil)
}
func (c *privateConn) LocalAddr() net.Addr { return &net.TCPAddr{} }
func (c *privateConn) RemoteAddr() net.Addr {
	host, port, _ := net.SplitHostPort(c.address)
	number, _ := strconv.Atoi(port)
	return &net.TCPAddr{IP: net.ParseIP(host), Port: number}
}
func (c *privateConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	c.writeDeadline = t
	return nil
}
func (c *privateConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.readDeadline = t
	return nil
}
func (c *privateConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writeDeadline = t
	return nil
}
