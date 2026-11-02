package androidvpn

import (
	"context"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestBridgeRejectsMismatchedVersionAndAcceptsUnknownFields(t *testing.T) {
	for _, raw := range []string{`{"ok":true,"result":7}`, `{"version":2,"ok":true,"result":7}`} {
		var fd int
		if err := DecodeReply([]byte(raw), &fd); err == nil {
			t.Fatal("bridge version mismatch was accepted")
		}
	}
	var fd int
	if err := DecodeReply([]byte(`{"version":1,"ok":true,"result":7,"future":"ignored"}`), &fd); err != nil || fd != 7 {
		t.Fatalf("compatible response: %v", err)
	}
	if err := DecodeReply([]byte(`{"version":1,"ok":false,"error":"VPN revoked"}`), nil); err == nil || err.Error() != "VPN revoked" {
		t.Fatal("failure lost at JNI boundary")
	}
}

type routeSystem struct {
	protected bool
	fail      bool
}

func (s *routeSystem) Call(method string, input, result any) error {
	if method != "network.protect" {
		return errors.New("unexpected operation")
	}
	s.protected = true
	if s.fail {
		return errors.New("cannot bind physical network")
	}
	return nil
}
func TestIPv6RouteUsesProtectedSocketWithoutPublicSourceRequirement(t *testing.T) {
	s := &routeSystem{}
	n := Network{System: s}
	if !n.HasIPv6Route(context.Background(), netip.MustParseAddr("::1"), 51820) || !s.protected {
		t.Fatal("route must use protected socket; no address snapshot required")
	}
	s.fail = true
	if n.HasIPv6Route(context.Background(), netip.MustParseAddr("::1"), 51820) {
		t.Fatal("must reject unprotected socket")
	}
	if n.HasIPv6Route(context.Background(), netip.MustParseAddr("127.0.0.1"), 51820) {
		t.Fatal("IPv4 is not IPv6")
	}
}

type privateSocketSystem struct {
	calls  int
	source string
}

func (s *privateSocketSystem) Call(method string, input any, _ any) error {
	if method != "network.vpn.bind" {
		return errors.New("unexpected physical-network operation")
	}
	s.calls++
	s.source = input.(map[string]any)["source"].(string)
	return errors.New("selected VPN not ready")
}
func TestPrivateDialRequiresSelectedVPNAndNeverFallsBack(t *testing.T) {
	system := &privateSocketSystem{}
	engine := &Engine{System: system}
	if _, err := engine.DialPrivate(t.Context(), "tcp", "127.0.0.1:9"); err == nil || system.calls != 0 {
		t.Fatal("disconnected VPN dial accepted", err)
	}
	engine.plan = &model.ClientPlan{ClientAddress: "127.0.0.1/32"}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if c, err := engine.DialPrivate(t.Context(), "tcp4", listener.Addr().String()); err == nil {
		c.Close()
		t.Fatal("VPN bind failure bypassed")
	} else if !strings.Contains(err.Error(), "selected VPN not ready") {
		t.Fatal(err)
	}
	if system.calls != 1 || system.source != "127.0.0.1" {
		t.Fatal("socket bound to wrong network", system)
	}
}
