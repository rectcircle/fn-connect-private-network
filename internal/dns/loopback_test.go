package dns

import (
	"context"
	"errors"
	"net"
	"slices"
	"syscall"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestLoopbackSelectionSkipsBusyAddressesAndKeepsBinding(t *testing.T) {
	var tried []string
	selected := new(Forwarder)
	f, address, port, err := listenLoopback("", func(endpoint string) (*Forwarder, uint16, error) {
		tried = append(tried, endpoint)
		if endpoint != "127.0.0.155:53" {
			return nil, 0, syscall.EADDRINUSE
		}
		return selected, 53, nil
	})
	if err != nil || f != selected || address != "127.0.0.155" || port != 53 {
		t.Fatalf("selection: %p %s:%d %v", f, address, port, err)
	}
	if !slices.Equal(tried, []string{"127.0.0.153:53", "127.0.0.154:53", "127.0.0.155:53"}) {
		t.Fatal(tried)
	}
}
func TestLoopbackSelectionReusesPreviousAddress(t *testing.T) {
	var tried []string
	_, address, _, err := listenLoopback("127.0.0.200", func(endpoint string) (*Forwarder, uint16, error) {
		tried = append(tried, endpoint)
		return new(Forwarder), 53, nil
	})
	if err != nil || address != "127.0.0.200" || len(tried) != 1 || tried[0] != "127.0.0.200:53" {
		t.Fatalf("%s %v %v", address, tried, err)
	}
	candidates := loopbackCandidates("127.0.0.200")
	seen := map[string]bool{}
	for _, address := range candidates {
		if seen[address] {
			t.Fatal("duplicate candidate", address)
		}
		seen[address] = true
	}
}
func TestLoopbackSelectionExhaustionIsBounded(t *testing.T) {
	tried := 0
	f, address, port, err := listenLoopback("192.168.1.20", func(string) (*Forwarder, uint16, error) { tried++; return nil, 0, syscall.EADDRINUSE })
	if !errors.Is(err, syscall.EADDRINUSE) || tried != 64 || f != nil || address != "" || port != 0 {
		t.Fatalf("%d %p %s:%d %v", tried, f, address, port, err)
	}
}
func TestDNSListenReleasesTCPWhenUDPIsOccupied(t *testing.T) {
	occupied, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	address := occupied.LocalAddr().String()
	f, _, err := Listen(address, "myhome.fncpn", func(_ context.Context, _ model.DNSQuery) (model.DNSAnswer, error) {
		t.Fatal("unexpected query")
		return model.DNSAnswer{}, nil
	}, func(string) bool { return false })
	if err == nil {
		f.Close()
		t.Fatal("UDP conflict was ignored")
	}
	tcp, err := net.Listen("tcp4", address)
	if err != nil {
		t.Fatalf("failed candidate leaked TCP listener: %v", err)
	}
	tcp.Close()
}
func TestWindowsDNSListenerValidationAllowsProbedIPs(t *testing.T) {
	for _, address := range []string{"127.0.0.153", "127.0.0.155", "127.0.0.200", "127.5.8.9"} {
		if err := ValidateWindowsListener(address, 53); err != nil {
			t.Fatal(err)
		}
	}
	for _, address := range []string{"0.0.0.0", "192.168.1.1", "::1", "::ffff:127.0.0.1", "invalid"} {
		if ValidateWindowsListener(address, 53) == nil {
			t.Fatal("non-IPv4-loopback accepted", address)
		}
	}
	if ValidateWindowsListener("127.0.0.155", 5353) == nil {
		t.Fatal("nonstandard DNS port accepted")
	}
}
