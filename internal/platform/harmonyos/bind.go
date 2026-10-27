package harmonyos

import (
	"errors"
	"golang.zx2c4.com/wireguard/conn"
	"net"
	"net/netip"
	"sync"
	"syscall"
)

// udpBind deliberately avoids Linux GRO/GSO and sticky sockets. The community
// runtime inherits Linux build tags but has a distinct runtime.GOOS identity.
// All sockets are protected by protectedBind before the device can use them.
type udpBind struct {
	mu     sync.Mutex
	v4, v6 *net.UDPConn
}
type endpoint struct{ address netip.AddrPort }

func (e *endpoint) ClearSrc()           {}
func (e *endpoint) SrcToString() string { return "" }
func (e *endpoint) DstToString() string { return e.address.String() }
func (e *endpoint) DstToBytes() []byte  { raw, _ := e.address.MarshalBinary(); return raw }
func (e *endpoint) DstIP() netip.Addr   { return e.address.Addr() }
func (e *endpoint) SrcIP() netip.Addr   { return netip.Addr{} }
func (b *udpBind) ParseEndpoint(value string) (conn.Endpoint, error) {
	address, err := netip.ParseAddrPort(value)
	if err != nil {
		return nil, err
	}
	return &endpoint{address}, nil
}
func (b *udpBind) BatchSize() int { return 1 }
func (b *udpBind) SetMark(mark uint32) error {
	if mark != 0 {
		return errors.New("socket marks are unavailable in application VPN")
	}
	return nil
}
func receive(socket *net.UDPConn) conn.ReceiveFunc {
	return func(packets [][]byte, sizes []int, endpoints []conn.Endpoint) (int, error) {
		if len(packets) == 0 || len(sizes) == 0 || len(endpoints) == 0 {
			return 0, errors.New("missing receive buffer")
		}
		n, peer, err := socket.ReadFromUDPAddrPort(packets[0])
		if err != nil {
			return 0, err
		}
		sizes[0] = n
		endpoints[0] = &endpoint{peer}
		return 1, nil
	}
}
func (b *udpBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.v4 != nil || b.v6 != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	for tries := 0; tries < 10; tries++ {
		v4, err := net.ListenUDP("udp4", &net.UDPAddr{Port: int(port)})
		if err != nil {
			return nil, 0, err
		}
		actual := v4.LocalAddr().(*net.UDPAddr).Port
		v6, err := net.ListenUDP("udp6", &net.UDPAddr{Port: actual})
		if err != nil && port == 0 && errors.Is(err, syscall.EADDRINUSE) {
			v4.Close()
			continue
		}
		if err != nil && !errors.Is(err, syscall.EAFNOSUPPORT) {
			v4.Close()
			return nil, 0, err
		}
		b.v4, b.v6 = v4, v6
		fns := []conn.ReceiveFunc{receive(v4)}
		if v6 != nil {
			fns = append(fns, receive(v6))
		}
		return fns, uint16(actual), nil
	}
	return nil, 0, errors.New("cannot allocate WireGuard UDP port")
}
func (b *udpBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	var err error
	if b.v4 != nil {
		err = b.v4.Close()
		b.v4 = nil
	}
	if b.v6 != nil {
		err = errors.Join(err, b.v6.Close())
		b.v6 = nil
	}
	return err
}
func (b *udpBind) Send(packets [][]byte, peer conn.Endpoint) error {
	e, ok := peer.(*endpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}
	b.mu.Lock()
	socket := b.v6
	if e.address.Addr().Is4() {
		socket = b.v4
	}
	b.mu.Unlock()
	if socket == nil {
		return net.ErrClosed
	}
	for _, packet := range packets {
		if _, err := socket.WriteToUDPAddrPort(packet, e.address); err != nil {
			return err
		}
	}
	return nil
}
func (b *udpBind) peek(ipv6 bool) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	socket := b.v4
	if ipv6 {
		socket = b.v6
	}
	if socket == nil {
		return -1, net.ErrClosed
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return -1, err
	}
	fd := -1
	err = raw.Control(func(value uintptr) { fd = int(value) })
	return fd, err
}
func (b *udpBind) PeekLookAtSocketFd4() (int, error) { return b.peek(false) }
func (b *udpBind) PeekLookAtSocketFd6() (int, error) { return b.peek(true) }
