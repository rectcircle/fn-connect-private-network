package iosvpn

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type fakeSystem struct {
	fail  bool
	calls []string
}

func (s *fakeSystem) Call(method string, p, r any) error {
	s.calls = append(s.calls, method)
	if method == "network.apply" && s.fail {
		s.fail = false
		return errors.New("network settings rejected")
	}
	return nil
}
func (*fakeSystem) WritePacket([]byte) error { return nil }
func TestDNSRollbackGenerationAndShutdown(t *testing.T) {
	sys := &fakeSystem{}
	e := NewEngine(sys)
	e.dnsIP = netip.MustParseAddr("198.18.0.53")
	ctx := context.Background()
	defer func() { e.PrepareShutdown(); _ = e.Remove(ctx) }()
	first := model.DNSPlan{Address: "127.0.0.1", Port: 12345, Suffix: "example.fncpn", Generation: "first"}
	if err := e.ApplyDNS(ctx, first); err != nil {
		t.Fatal(err)
	}
	next := first
	next.Port++
	next.Suffix = "next.fncpn" // A suffix change must reach the OS; port-only refreshes reuse its settings.
	next.Generation = "next"
	sys.fail = true
	if err := e.ApplyDNS(ctx, next); err == nil {
		t.Fatal("expected settings failure")
	}
	if e.dnsPlan != first || e.dns == nil {
		t.Fatal("last working DNS not restored")
	}
	if err := e.RemoveDNS(ctx, next); err != nil {
		t.Fatal(err)
	}
	if e.dnsPlan != first {
		t.Fatal("stale generation removed DNS")
	}
	status, _ := e.Status(ctx)
	if status.Active {
		t.Fatal("LOCAL DNS reported WireGuard active")
	}
	e.PrepareShutdown()
	if err := e.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if e.dns != nil || e.device != nil {
		t.Fatal("shutdown leaked packet resources")
	}
	if err := e.ApplyDNS(ctx, first); err == nil {
		t.Fatal("shutdown accepted new DNS")
	}
}
