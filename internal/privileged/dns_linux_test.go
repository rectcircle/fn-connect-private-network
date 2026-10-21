//go:build linux

package privileged

import (
	"fmt"
	mdns "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
	"github.com/vishvananda/netlink"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLinuxDNSPlanAndDedicatedLink(t *testing.T) {
	p := model.DNSPlan{Suffix: "home.fncpn", Address: "127.0.0.1", Port: 15353, Generation: strings.Repeat("a", 32)}
	if err := validateDNSPlan(p); err != nil {
		t.Fatal(err)
	}
	name, address := linuxDNSLink(p)
	if len(name) > 15 || !strings.HasPrefix(address.String(), "169.254.") {
		t.Fatal(name, address)
	}
	p.Port = 53
	if validateDNSPlan(p) == nil {
		t.Fatal("accepted privileged forwarder port")
	}
}
func TestLinuxDNSUsesExplicitServiceUID(t *testing.T) {
	r := NewDNSRules(t.TempDir()).WithAuthorizer(AuthorizePeer("client", 1000))
	if err := r.authorize(t.Context(), peer.Identity{UID: 1000, PID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.authorize(t.Context(), peer.Identity{UID: 1001, PID: 1}); err == nil {
		t.Fatal("accepted another user")
	}
}
func TestLinuxDNSLeasePreservesOwnerAndGeneration(t *testing.T) {
	r := NewDNSRules(t.TempDir())
	r.install = func(model.DNSPlan) error { return nil }
	r.remove = func(model.DNSPlan) error { return nil }
	r.verify = func(model.DNSPlan) error { return nil }
	owner := peer.WithIdentity(t.Context(), peer.Identity{UID: 1000, PID: 321})
	p := model.DNSPlan{Suffix: "home.fncpn", Address: "127.0.0.1", Port: 15353, Generation: strings.Repeat("a", 32)}
	q, _ := ipc.NewRequest("x", MethodApplyDNS, p)
	if reply := r.Handle(owner, q); !reply.OK {
		t.Fatalf("apply %+v", reply)
	}
	q, _ = ipc.NewRequest("y", MethodRenewDNS, p)
	other := peer.WithIdentity(t.Context(), peer.Identity{UID: 1001, PID: 321})
	if r.Handle(other, q).OK {
		t.Fatal("foreign user renewed DNS lease")
	}
	p.Generation = strings.Repeat("b", 32)
	q, _ = ipc.NewRequest("z", MethodRemoveDNS, p)
	if r.Handle(owner, q).OK {
		t.Fatal("stale generation removed DNS")
	}
}
func TestKernelDNSBridgeCapability(t *testing.T) {
	if os.Getenv("FNCPN_KERNEL_TESTS") != "1" {
		t.Skip("opt-in root isolated network test")
	}
	loop, err := netlink.LinkByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	if err = netlink.LinkSetUp(loop); err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &mdns.Server{PacketConn: udp, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, q *mdns.Msg) {
		r := new(mdns.Msg)
		r.SetReply(q)
		rr, _ := mdns.NewRR(q.Question[0].Name + " 30 IN A 10.254.250.1")
		r.Answer = []mdns.RR{rr}
		w.WriteMsg(r)
	})}
	go server.ActivateAndServe()
	defer server.Shutdown()
	tcp, err := net.Listen("tcp4", udp.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	tcpServer := &mdns.Server{Listener: tcp, Handler: server.Handler}
	go tcpServer.ActivateAndServe()
	defer tcpServer.Shutdown()

	p := model.DNSPlan{Suffix: "home.fncpn", Address: "127.0.0.1", Port: uint16(udp.LocalAddr().(*net.UDPAddr).Port), Generation: strings.Repeat("a", 32)}
	originalReady := linuxResolvedReady
	linuxResolvedReady = func() error { return nil }
	defer func() { linuxResolvedReady = originalReady }()
	original := linuxResolverCommand
	var calls [][]string
	linuxResolverCommand = func(args ...string) error { calls = append(calls, args); return nil }
	defer func() { linuxResolverCommand = original }()
	if err = installDNSRule(p); err != nil {
		t.Fatal(err)
	}
	defer removeDNSRule(p)
	if len(calls) != 5 || calls[0][0] != "SetLinkDNSEx" || calls[1][0] != "SetLinkDomains" || calls[1][5] != "true" {
		t.Fatalf("resolved configuration: %+v", calls)
	}
	_, address := linuxDNSLink(p)
	q := new(mdns.Msg)
	q.SetQuestion("nas.home.fncpn.", mdns.TypeA)
	client := mdns.Client{Timeout: time.Second, Dialer: &net.Dialer{LocalAddr: &net.UDPAddr{IP: address}}}
	reply, _, err := client.Exchange(q, net.JoinHostPort(address.String(), "53"))
	if err != nil || len(reply.Answer) != 1 {
		t.Fatalf("restricted DNS bridge: %v %+v", err, reply)
	}

	tcpClient := mdns.Client{Net: "tcp", Timeout: time.Second, Dialer: &net.Dialer{LocalAddr: &net.TCPAddr{IP: address}}}
	reply, _, err = tcpClient.Exchange(q, net.JoinHostPort(address.String(), "53"))
	if err != nil || len(reply.Answer) != 1 {
		t.Fatalf("TCP bridge: %v %+v", err, reply)
	}
	if err = verifyDNSRule(p); err != nil || len(calls) != 10 {
		t.Fatalf("lease did not reassert resolved settings: %v %+v", err, calls)
	}
	q.SetQuestion("example.com.", mdns.TypeA)
	reply, _, err = client.Exchange(q, net.JoinHostPort(address.String(), "53"))
	if err != nil || reply.Rcode != mdns.RcodeRefused {
		t.Fatal("bridge forwarded an unrelated DNS name", err)
	}
	if err = removeDNSRule(p); err != nil {
		t.Fatal(err)
	}
	if err = verifyDNSRule(p); !os.IsNotExist(err) {
		t.Fatalf("DNS link not removed: %v", err)
	}
	fmt.Println("DNS bridge and per-link resolved arguments verified; real resolved service is not required by this capability test")
}

func TestLinuxDNSRefusesToReportReadyForUnmanagedResolver(t *testing.T) {
	for _, data := range []string{"nameserver 8.8.8.8\n", "# empty\n", "nameserver 127.0.0.53\nnameserver 1.1.1.1\n"} {
		if checkLinuxStubResolver([]byte(data)) == nil {
			t.Fatal("accepted unmanaged resolver")
		}
	}
	if err := checkLinuxStubResolver([]byte("nameserver 127.0.0.53\noptions edns0\n")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxDNSFailedCleanupRetainsRecoveryJournal(t *testing.T) {
	directory := t.TempDir()
	rules := NewDNSRules(directory)
	rules.install = func(model.DNSPlan) error { return linuxDNSCleanupError{fmt.Errorf("cleanup failed")} }
	owner := peer.WithIdentity(t.Context(), peer.Identity{UID: 1000, PID: 321})
	p := model.DNSPlan{Suffix: "home.fncpn", Address: "127.0.0.1", Port: 15353, Generation: strings.Repeat("a", 32)}
	q, _ := ipc.NewRequest("x", MethodApplyDNS, p)
	if rules.Handle(owner, q).OK {
		t.Fatal("accepted failed install")
	}
	if _, err := os.Stat(rules.path); err != nil {
		t.Fatal("lost recovery journal", err)
	}
	if rules.current == nil {
		t.Fatal("lost pending cleanup")
	}
	rules.remove = func(model.DNSPlan) error { return nil }
	if err := rules.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rules.path); !os.IsNotExist(err) {
		t.Fatal("cleanup left journal", err)
	}
}
