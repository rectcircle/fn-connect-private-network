package iosvpn

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func TestVirtualDNSUDPAndTCPAndSuffixIsolation(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp4", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var nasQueries atomic.Int32
	handler := wire.HandlerFunc(func(w wire.ResponseWriter, q *wire.Msg) {
		nasQueries.Add(1)
		answer := new(wire.Msg)
		answer.SetReply(q)
		a, _ := wire.NewRR(q.Question[0].Name + " 30 IN A 192.168.1.10")
		answer.Answer = []wire.RR{a}
		_ = w.WriteMsg(answer)
	})
	ready := make(chan struct{}, 2)
	udp := &wire.Server{PacketConn: packet, Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}
	tcp := &wire.Server{Listener: listener, Handler: handler, NotifyStartedFunc: func() { ready <- struct{}{} }}
	go udp.ActivateAndServe()
	go tcp.ActivateAndServe()
	for range 2 {
		select {
		case <-ready:
		case <-time.After(time.Second):
			t.Fatal("upstream not ready")
		}
	}
	defer udp.Shutdown()
	defer tcp.Shutdown()
	clientTUN, clientNet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.20.0.2")}, nil, 1280)
	if err != nil {
		t.Fatal(err)
	}
	dnsIP := netip.MustParseAddr("198.18.0.53")
	stack, err := newDNS(dnsIP, func(p []byte) error { _, err := clientTUN.Write([][]byte{append([]byte(nil), p...)}, 0); return err })
	if err != nil {
		t.Fatal(err)
	}
	stack.setPlan(model.DNSPlan{Suffix: "example.fncpn", Address: "127.0.0.1", Port: uint16(listener.Addr().(*net.TCPAddr).Port)})
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		buf := make([]byte, 65535)
		sizes := make([]int, 1)
		for {
			n, err := clientTUN.Read([][]byte{buf}, sizes, 0)
			if err != nil {
				return
			}
			if n > 0 {
				stack.inject(append([]byte(nil), buf[:sizes[0]]...))
			}
		}
	}()
	defer func() { clientTUN.Close(); <-pumpDone; stack.close() }()
	for _, transport := range []string{"udp", "tcp"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		connection, err := clientNet.DialContext(ctx, transport, net.JoinHostPort(dnsIP.String(), "53"))
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		connection.SetDeadline(time.Now().Add(3 * time.Second))
		co := &wire.Conn{Conn: connection}
		q := new(wire.Msg)
		q.SetQuestion("nas.example.fncpn.", wire.TypeA)
		if err := co.WriteMsg(q); err != nil {
			t.Fatal(err)
		}
		answer, err := co.ReadMsg()
		if err != nil {
			t.Fatal(err)
		}
		if len(answer.Answer) != 1 {
			t.Fatalf("%s response: %+v", transport, answer)
		}
		q.SetQuestion("notexample.fncpn.", wire.TypeA)
		if err := co.WriteMsg(q); err != nil {
			t.Fatal(err)
		}
		answer, err = co.ReadMsg()
		if err != nil {
			t.Fatal(err)
		}
		if answer.Rcode != wire.RcodeServerFailure {
			t.Fatalf("suffix isolation response: %+v", answer)
		}
		connection.Close()
	}
	if nasQueries.Load() != 2 {
		t.Fatalf("NAS queries %d", nasQueries.Load())
	}
}
