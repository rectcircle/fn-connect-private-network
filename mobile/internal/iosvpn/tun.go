// Package iosvpn adapts the shared client daemon to NetworkExtension. It has no UI dependency.
package iosvpn

import (
	"errors"
	"io"
	"os"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
)

// PacketTUN owns bounded copies of packets supplied by NEPacketTunnelFlow.
// Swift never retains Go pointers; output must copy before returning.
type PacketTUN struct {
	mu      sync.Mutex
	packets chan []byte
	done    chan struct{}
	events  chan tun.Event
	mtu     int
	output  func([]byte) error
	closed  bool
}

func NewPacketTUN(mtu int, output func([]byte) error) *PacketTUN {
	t := &PacketTUN{packets: make(chan []byte, 128), done: make(chan struct{}), events: make(chan tun.Event, 1), mtu: mtu, output: output}
	t.events <- tun.EventUp
	return t
}
func (t *PacketTUN) Inject(packet []byte) bool {
	if len(packet) < 20 || len(packet) > 65535 || (packet[0]>>4 != 4 && packet[0]>>4 != 6) {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	select {
	case t.packets <- append([]byte(nil), packet...):
		return true
	default:
		return false
	}
}
func (t *PacketTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case <-t.done:
		return 0, os.ErrClosed
	case p := <-t.packets:
		if len(bufs) == 0 || len(sizes) == 0 || offset < 0 || offset > len(bufs[0]) || len(p) > len(bufs[0])-offset {
			return 0, io.ErrShortBuffer
		}
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	}
}
func (t *PacketTUN) Write(bufs [][]byte, offset int) (int, error) {
	for i, p := range bufs {
		if offset < 0 || offset > len(p) {
			return i, errors.New("invalid packet offset")
		}
		select {
		case <-t.done:
			return i, os.ErrClosed
		default:
		}
		if err := t.output(p[offset:]); err != nil {
			return i, err
		}
	}
	return len(bufs), nil
}
func (t *PacketTUN) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.closed {
		t.closed = true
		close(t.done)
		close(t.events)
	}
	return nil
}
func (t *PacketTUN) File() *os.File           { return nil }
func (t *PacketTUN) MTU() (int, error)        { return t.mtu, nil }
func (t *PacketTUN) Name() (string, error)    { return "ios-packet-tunnel", nil }
func (t *PacketTUN) Events() <-chan tun.Event { return t.events }
func (t *PacketTUN) BatchSize() int           { return 1 }
