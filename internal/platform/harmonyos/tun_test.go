//go:build unix || openharmony

package harmonyos

import (
	"net/netip"
	"os"
	"testing"
	"time"
)

func TestTUNReplacementAndCloseUnblockReader(t *testing.T) {
	dnsIP := netip.MustParseAddr("198.18.0.53")
	dns := make(chan []byte, 1)
	tunnel := newPacketTUN(1280, dnsIP, func(p []byte) { dns <- p })
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := tunnel.replace(reader); err != nil {
		t.Fatal(err)
	}
	packet := make([]byte, 20)
	packet[0] = 0x45
	copy(packet[16:], dnsIP.AsSlice())
	if _, err := writer.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dns:
	case <-time.After(time.Second):
		t.Fatal("DNS not intercepted")
	}
	second, secondWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer secondWriter.Close()
	if err := tunnel.replace(second); err != nil {
		t.Fatal(err)
	}
	packet[19] = 54
	if _, err := secondWriter.Write(packet); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1280)
	sizes := make([]int, 1)
	if n, err := tunnel.Read([][]byte{buf}, sizes, 0); err != nil || n != 1 || sizes[0] != 20 {
		t.Fatalf("read %d %v", n, err)
	}
	done := make(chan error, 1)
	go func() { _, err := tunnel.Read([][]byte{buf}, sizes, 0); done <- err }()
	if err := tunnel.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("read succeeded after close")
		}
	case <-time.After(time.Second):
		t.Fatal("TUN close blocked reader")
	}
	if err := tunnel.Close(); err != nil {
		t.Fatal(err)
	}
}
