package iosvpn

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type settingsSystem struct {
	applies int
	removes int
	fail    bool
}

func (s *settingsSystem) WritePacket([]byte) error { return nil }
func (s *settingsSystem) Call(method string, _, _ any) error {
	if method == "network.remove" {
		s.removes++
	}
	if method == "network.apply" {
		s.applies++
		if s.fail {
			return errors.New("installation failed")
		}
	}
	return nil
}

func TestDNSRefreshDoesNotReinstallTunnelSettings(t *testing.T) {
	s := &settingsSystem{}
	e := NewEngine(s)
	e.dnsIP = netip.MustParseAddr("198.18.0.53")
	e.plan = &model.ClientPlan{ClientAddress: "10.20.0.2/32", AllowedIPs: []string{"192.168.71.2/32"}, MTU: 1280}
	p := model.DNSPlan{Suffix: "nas.fncpn", Address: "127.0.0.1", Port: 5300, Generation: "active"}
	if err := e.ApplyDNS(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	defer e.dns.close()
	for range 3 {
		if err := e.RenewDNS(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if err := e.ApplyDNS(t.Context(), p); err != nil {
			t.Fatal(err)
		}
	}
	if s.applies != 1 {
		t.Fatalf("unchanged DNS refresh reinstalled OS settings %d times", s.applies)
	}
	stale := p
	stale.Generation = "stale"
	if e.RenewDNS(t.Context(), stale) == nil {
		t.Fatal("stale renewal accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(e.RenewDNS(ctx, p), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	p.Port++
	if err := e.ApplyDNS(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if s.applies != 1 {
		t.Fatal("local upstream change reinstalled OS routes")
	}
	p.Suffix = "other.fncpn"
	if err := e.ApplyDNS(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if s.applies != 2 {
		t.Fatal("changed DNS suffix did not update OS settings")
	}
}

func TestFailedSettingsInstallForcesRollback(t *testing.T) {
	s := &settingsSystem{}
	e := NewEngine(s)
	p := model.ClientPlan{ClientAddress: "10.20.0.2/32", AllowedIPs: []string{"192.168.71.2/32"}, MTU: 1280}
	if err := e.configureLocked(&p); err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.MTU = 1200
	s.fail = true
	if e.configureLocked(&changed) == nil {
		t.Fatal("installation failure hidden")
	}
	s.fail = false
	if err := e.configureLocked(&p); err != nil {
		t.Fatal(err)
	}
	if s.applies != 3 {
		t.Fatal("rollback incorrectly skipped OS reinstall")
	}
}

func TestShutdownDoesNotReconfigureOSNetwork(t *testing.T) {
	s := &settingsSystem{}
	e := NewEngine(s)
	e.PrepareShutdown()
	p := model.ClientPlan{ClientAddress: "10.20.0.2/32", MTU: 1280}
	if err := e.configureLocked(&p); err != nil {
		t.Fatal(err)
	}
	if err := e.Remove(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.applies != 0 || s.removes != 0 {
		t.Fatal("shutdown reinstalled OS network settings")
	}
}
