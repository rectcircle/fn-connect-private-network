package harmonyos

import (
	"encoding/binary"
	"errors"
	"io"
	"net/netip"
	"os"
	"sync"

	"golang.zx2c4.com/wireguard/tun"
)

// packetTUN keeps WireGuard attached while HarmonyOS replaces its fd for route or
// DNS changes. DNS packets are delivered to the bounded userspace DNS stack.
type packetTUN struct {
	mu      sync.Mutex
	file    *os.File
	closed  bool
	failure error
	packets chan []byte
	done    chan struct{}
	events  chan tun.Event
	mtu     int
	dnsIP   netip.Addr
	dns     func([]byte)
	writeMu sync.Mutex
}

func newPacketTUN(mtu int, dnsIP netip.Addr, dns func([]byte)) *packetTUN {
	t := &packetTUN{mtu: mtu, dnsIP: dnsIP, dns: dns, packets: make(chan []byte, 128), done: make(chan struct{}), events: make(chan tun.Event, 1)}
	t.events <- tun.EventUp
	return t
}

// detach releases Go's duplicate before the platform destroys/recreates TUN.
// Keeping the duplicate open can retain the old device while create runs.
func (t *packetTUN) detach() {
	t.mu.Lock()
	old := t.file
	t.file = nil
	t.failure = os.ErrClosed
	t.emitLocked(0)
	t.mu.Unlock()
	if old != nil {
		old.Close()
	}
}
func (t *packetTUN) replace(file *os.File) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		file.Close()
		return os.ErrClosed
	}
	old := t.file
	t.file = file
	wasFailed := t.failure != nil
	t.failure = nil
	if wasFailed {
		t.emitLocked(0)
	}
	t.mu.Unlock()
	if old != nil {
		old.Close()
	}
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := file.Read(buf)
			if err != nil {
				t.mu.Lock()
				if !t.closed && t.file == file {
					t.failure = err
					t.emitLocked(0)
				}
				t.mu.Unlock()
				return
			}
			t.mu.Lock()
			stale := t.file != file || t.closed
			t.mu.Unlock()
			if stale {
				return
			}
			packet := append([]byte(nil), buf[:n]...)
			if dnsDestination(packet, t.dnsIP) {
				t.dns(packet)
				continue
			}
			select {
			case t.packets <- packet:
			case <-t.done:
				return
			default: /* bounded loss, like UDP */
			}
		}
	}()
	return nil
}
func dnsDestination(packet []byte, ip netip.Addr) bool {
	if len(packet) < 20 || packet[0]>>4 != 4 || !ip.Is4() {
		return false
	}
	return binary.BigEndian.Uint32(packet[16:20]) == binary.BigEndian.Uint32(ip.AsSlice())
}
func (t *packetTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
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
func (t *packetTUN) writePacket(packet []byte) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	t.mu.Lock()
	f := t.file
	t.mu.Unlock()
	if f == nil {
		return os.ErrClosed
	}
	n, err := f.Write(packet)
	if err == nil && n != len(packet) {
		err = io.ErrShortWrite
	}
	return err
}
func (t *packetTUN) Write(bufs [][]byte, offset int) (int, error) {
	for i, b := range bufs {
		if offset < 0 || offset > len(b) {
			return i, errors.New("invalid TUN offset")
		}
		if err := t.writePacket(b[offset:]); err != nil {
			return i, err
		}
	}
	return len(bufs), nil
}
func (t *packetTUN) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	close(t.done)
	close(t.events)
	if t.file != nil {
		err := t.file.Close()
		t.file = nil
		return err
	}
	return nil
}
func (t *packetTUN) Name() (string, error)    { return "android-vpn", nil }
func (t *packetTUN) File() *os.File           { return nil }
func (t *packetTUN) Events() <-chan tun.Event { return t.events }
func (t *packetTUN) MTU() (int, error)        { t.mu.Lock(); defer t.mu.Unlock(); return t.mtu, nil }
func (t *packetTUN) setMTU(mtu int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	if t.mtu != mtu {
		t.mtu = mtu
		t.emitLocked(tun.EventMTUUpdate)
	}
}
func (t *packetTUN) BatchSize() int { return 1 }
func (t *packetTUN) healthy() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.closed && t.failure == nil
}

// Coalesce queued changes without losing the latest up/down state. Producers
// hold mu; the consumer may be blocked in MTU(), so sending must not block.
func (t *packetTUN) emitLocked(extra tun.Event) {
	if t.failure == nil {
		extra |= tun.EventUp
	} else {
		extra |= tun.EventDown
	}
	select {
	case t.events <- extra:
		return
	default:
	}
	select {
	case old := <-t.events:
		extra |= old & tun.EventMTUUpdate
	default:
	}
	select {
	case t.events <- extra:
	default:
	}
}
