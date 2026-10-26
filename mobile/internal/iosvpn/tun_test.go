package iosvpn

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPacketFlowCopiesAndCloseUnblocksRead(t *testing.T) {
	output := []byte(nil)
	tun := NewPacketTUN(1280, func(p []byte) error { output = append([]byte(nil), p...); return nil })
	packet := make([]byte, 20)
	packet[0] = 0x45
	packet[19] = 1
	if !tun.Inject(packet) {
		t.Fatal("packet rejected")
	}
	packet[19] = 2
	buf := make([]byte, 64)
	sizes := make([]int, 1)
	if n, err := tun.Read([][]byte{buf}, sizes, 4); err != nil || n != 1 || buf[23] != 1 {
		t.Fatalf("read %d %v %v", n, err, buf)
	}
	if _, err := tun.Write([][]byte{buf[:24]}, 4); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output, buf[4:24]) {
		t.Fatal("packet offset changed")
	}
	done := make(chan error, 1)
	go func() { _, err := tun.Read([][]byte{buf}, sizes, 0); done <- err }()
	tun.Close()
	tun.Close()
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked packet reader")
	}
	if tun.Inject(packet) {
		t.Fatal("accepted packet after close")
	}
}
