//go:build darwin

package privileged

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
)

func TestDNSRulesOwnershipGenerationAndRecovery(t *testing.T) {
	r := NewDNSRules(t.TempDir())
	installed := false
	r.install = func(model.DNSPlan) error { installed = true; return nil }
	r.remove = func(model.DNSPlan) error { installed = false; return nil }
	r.verify = func(model.DNSPlan) error {
		if !installed {
			return os.ErrNotExist
		}
		return nil
	}
	owner := peer.Identity{UID: 501, PID: 123}
	ctx := peer.WithIdentity(t.Context(), owner)
	plan := model.DNSPlan{Suffix: "myhome.fncpn", Address: "127.0.0.1", Port: 15353, Generation: strings.Repeat("a", 32)}
	call := func(ctx context.Context, method string, p model.DNSPlan) ipc.Response {
		q, _ := ipc.NewRequest("test", method, p)
		return r.Handle(ctx, q)
	}
	if response := call(ctx, MethodApplyDNS, plan); !response.OK || !installed {
		t.Fatalf("%+v", response)
	}
	// Renewals extend only the in-memory lease, without inspecting or changing OS rules.
	oldVerify := r.verify
	r.verify = func(model.DNSPlan) error { t.Fatal("renewal inspected OS rules"); return nil }
	expires := r.expires
	if !call(ctx, MethodRenewDNS, plan).OK || !r.expires.After(expires) {
		t.Fatal("lease was not renewed")
	}
	r.verify = oldVerify
	wrong := peer.WithIdentity(t.Context(), peer.Identity{UID: 501, PID: 124})
	if call(wrong, MethodRenewDNS, plan).OK {
		t.Fatal("another process renewed lease")
	}
	if call(wrong, MethodRemoveDNS, plan).OK {
		t.Fatal("another process removed rules")
	}
	stale := plan
	stale.Generation = strings.Repeat("b", 32)
	if call(ctx, MethodRenewDNS, stale).OK {
		t.Fatal("stale generation renewed lease")
	}
	if call(ctx, MethodRemoveDNS, stale).OK {
		t.Fatal("stale generation removed rules")
	}
	installed = false
	if !call(ctx, MethodApplyDNS, plan).OK || !installed {
		t.Fatal("heartbeat did not repair removed rule")
	}
	// A new privileged process cleans the persisted plan, not unrelated files.
	recovered := NewDNSRules(r.path[:len(r.path)-len("dns-state.json")])
	recovered.remove = r.remove
	if e := recovered.Recover(t.Context()); e != nil || installed {
		t.Fatalf("recovery failed: %v", e)
	}
	if _, e := os.Stat(r.path); !os.IsNotExist(e) {
		t.Fatal("journal remains")
	}
}
func TestDNSRulesDoNotClaimFailedInstall(t *testing.T) {
	r := NewDNSRules(t.TempDir())
	r.install = func(model.DNSPlan) error { return errors.New("external resolver file exists") }
	r.remove = func(model.DNSPlan) error { t.Fatal("attempted removal of external rule"); return nil }
	p := model.DNSPlan{Suffix: "myhome.fncpn", Address: "127.0.0.1", Port: 15353, Generation: strings.Repeat("a", 32)}
	request, _ := ipc.NewRequest("test", MethodApplyDNS, p)
	response := r.Handle(peer.WithIdentity(t.Context(), peer.Identity{UID: 501, PID: 123}), request)
	if response.OK || r.current != nil {
		t.Fatal("failed install claimed external rules")
	}
	if _, e := os.Stat(r.path); !os.IsNotExist(e) {
		t.Fatal("failed install retained journal")
	}
	for _, generation := range []string{"../bad", strings.Repeat("a", 31) + "\n", strings.Repeat("g", 32)} {
		p.Generation = generation
		if validateDNSPlan(p) == nil {
			t.Fatal("unsafe generation accepted")
		}
	}
}
