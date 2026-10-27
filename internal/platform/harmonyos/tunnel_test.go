package harmonyos

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

// Real WireGuard encryption and a userspace IP stack, with the OS protect call
// replaced by a counter. This does not claim HarmonyOS system VPN validation.
func TestWireGuardTunnelWithHarmonyBind(t *testing.T) {
	key := func() (string, string) {
		private := make([]byte, 32)
		if _, err := rand.Read(private); err != nil {
			t.Fatal(err)
		}
		public, err := curve25519.X25519(private, curve25519.Basepoint)
		if err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(private), hex.EncodeToString(public)
	}
	cp, cu := key()
	sp, su := key()
	ct, cn, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.249.0.1")}, nil, 1280)
	if err != nil {
		t.Fatal(err)
	}
	st, sn, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.249.0.2")}, nil, 1280)
	if err != nil {
		ct.Close()
		t.Fatal(err)
	}
	var protected atomic.Int32
	rpc := rpcFunc(func(method string, _ any, _ any) error {
		if method != "network.protect" {
			return fmt.Errorf("unexpected system method")
		}
		protected.Add(1)
		return nil
	})
	cb, sb := &udpBind{}, &udpBind{}
	client := device.NewDevice(ct, &protectedBind{Bind: cb, system: rpc}, &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf})
	server := device.NewDevice(st, sb, &device.Logger{Verbosef: device.DiscardLogf, Errorf: device.DiscardLogf})
	defer client.Close()
	defer server.Close()
	if err = server.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=10.249.0.1/32\n", sp, cu)); err != nil {
		t.Fatal(err)
	}
	if err = server.Up(); err != nil {
		t.Fatal(err)
	}
	sb.mu.Lock()
	port := sb.v4.LocalAddr().(*net.UDPAddr).Port
	sb.mu.Unlock()
	if err = client.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\nendpoint=127.0.0.1:%d\nallowed_ip=10.249.0.2/32\n", cp, su, port)); err != nil {
		t.Fatal(err)
	}
	if err = client.Up(); err != nil {
		t.Fatal(err)
	}
	socket, err := sn.ListenUDPAddrPort(netip.MustParseAddrPort("10.249.0.2:53535"))
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	socket.SetDeadline(time.Now().Add(5 * time.Second))
	reply := make(chan error, 1)
	go func() {
		buffer := make([]byte, 64)
		n, peer, e := socket.ReadFrom(buffer)
		if e == nil {
			_, e = socket.WriteTo(buffer[:n], peer)
		}
		reply <- e
	}()
	sender, err := cn.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("10.249.0.2:53535"))
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	sender.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = sender.Write([]byte("encrypted-wireguard-probe")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, err := sender.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "encrypted-wireguard-probe" {
		t.Fatal("bad tunnel payload")
	}
	if err = <-reply; err != nil {
		t.Fatal(err)
	}
	if protected.Load() == 0 {
		t.Fatal("WireGuard sockets were not passed to protector")
	}
}
