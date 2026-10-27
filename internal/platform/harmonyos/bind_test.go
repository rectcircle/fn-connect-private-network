package harmonyos

import (
	"bytes"
	"golang.zx2c4.com/wireguard/conn"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestBindLoopbackAndClose(t *testing.T) {
	bind := &udpBind{}
	fns, port, err := bind.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	defer bind.Close()
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(time.Second))
	ep, err := bind.ParseEndpoint(peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err = bind.Send([][]byte{[]byte("request")}, ep); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, from, err := peer.ReadFromUDPAddrPort(buf)
	if err != nil || !bytes.Equal(buf[:n], []byte("request")) {
		t.Fatal(err)
	}
	if from.Port() != port {
		t.Fatal("wrong source port")
	}
	peer.WriteToUDPAddrPort([]byte("reply"), netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port))
	sizes := make([]int, 1)
	endpoints := make([]conn.Endpoint, 1)
	n, err = fns[0]([][]byte{buf}, sizes, endpoints)
	if err != nil || n != 1 || string(buf[:sizes[0]]) != "reply" {
		t.Fatal(err)
	}
	bind.Close()
	if err = bind.Send([][]byte{[]byte("closed")}, ep); err == nil {
		t.Fatal("send after close")
	}
}
