//go:build linux

package linux

import (
	"context"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientJournalRejectsForeignResourcesAndFutureSchema(t *testing.T) {
	j := clientJournal{Version: 1, Interface: clientLinkName(1000), UID: 1000, Token: strings.Repeat("a", 32)}
	if err := validateClientJournal(j); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*clientJournal){func(j *clientJournal) { j.Version = 2 }, func(j *clientJournal) { j.Interface = "eth0" }, func(j *clientJournal) { j.Token = "../../etc/passwd" }} {
		copy := j
		mutate(&copy)
		if validateClientJournal(copy) == nil {
			t.Fatal("accepted foreign/future journal")
		}
	}
}
func TestClientApplyRequiresIdentityBeforeKernelChanges(t *testing.T) {
	engine := NewSystemClientEngine(nil, t.TempDir())
	key, _ := wgtypes.GeneratePrivateKey()
	server, _ := wgtypes.GeneratePrivateKey()
	err := engine.Apply(context.Background(), model.ClientPlan{Mode: "relay", PrivateKey: key.String(), ServerPublicKey: server.PublicKey().String(), ClientAddress: "10.254.250.2/32", Endpoint: "127.0.0.1:9", AllowedIPs: []string{"10.254.250.0/24"}})
	var failure *model.Error
	if !errors.As(err, &failure) || failure.Code != model.ErrorPermissionDenied {
		t.Fatalf("expected identity refusal: %v", err)
	}
}
func TestClientRecoverRejectsUntrustedJournal(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "client-network.json")
	os.WriteFile(path, []byte(`{"version":9}`), 0600)
	if NewSystemClientEngine(nil, directory).Recover(t.Context()) == nil {
		t.Fatal("accepted future schema")
	}
	os.Chmod(path, 0644)
	if NewSystemClientEngine(nil, directory).Recover(t.Context()) == nil {
		t.Fatal("accepted public journal")
	}
}
func TestKernelClientCapability(t *testing.T) {
	if os.Getenv("FNCPN_KERNEL_TESTS") != "1" {
		t.Skip("opt-in: isolated root kernel capability test")
	}
	if os.Geteuid() != 0 {
		t.Fatal("kernel capability test requires root")
	}
	ctx := peer.WithIdentity(t.Context(), peer.Identity{UID: 1000, PID: os.Getpid()})
	directory := t.TempDir()
	engine := NewSystemClientEngine(nil, directory)
	t.Cleanup(func() { engine.Close(context.Background()) })
	key, _ := wgtypes.GeneratePrivateKey()
	server, _ := wgtypes.GeneratePrivateKey()
	plan := model.ClientPlan{Mode: "relay", PrivateKey: key.String(), ServerPublicKey: server.PublicKey().String(), ClientAddress: "10.254.250.2/32", Endpoint: "127.0.0.1:9", AllowedIPs: []string{"10.254.250.0/24"}, MTU: 1280}
	if err := engine.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	status := engine.Status()
	if !status.Active || status.MTU != 1280 || status.ListenPort == 0 {
		t.Fatalf("kernel status: %+v", status)
	}
	link, err := netlink.LinkByName(clientLinkName(1000))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_V4)
	if err != nil || len(routes) == 0 {
		t.Fatalf("routes missing: %v", err)
	}
	// Simulate a privileged process crash: a fresh engine must recover the same owned journal.
	replacement := NewSystemClientEngine(nil, directory)
	if err = replacement.Recover(ctx); err == nil {
		t.Fatal("a second helper recovered a live helper's network")
	}
	// Emulate process exit without cleanup.
	engine.lease.Close()
	engine.lease = nil
	if err = replacement.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = netlink.LinkByName(clientLinkName(1000)); err == nil {
		t.Fatal("recovery left the link behind")
	}
	engine.state = nil
	if err = replacement.Close(ctx); err != nil {
		t.Fatal(err)
	}
	// Conflicting interfaces are never touched.
	foreign := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: clientLinkName(1000), Alias: "another-application"}}
	if err = netlink.LinkAdd(foreign); err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(foreign)
	if err = engine.Apply(ctx, plan); err == nil {
		t.Fatal("overwrote foreign link")
	}
	if _, err = netlink.LinkByName(clientLinkName(1000)); err != nil {
		t.Fatal("removed foreign link")
	}
}

func TestClientProcessLease(t *testing.T) {
	directory := t.TempDir()
	first := NewSystemClientEngine(nil, directory)
	second := NewSystemClientEngine(nil, directory)
	if err := first.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.Close(context.Background()); second.Close(context.Background()) })
	if err := second.Recover(t.Context()); err == nil {
		t.Fatal("accepted concurrent helper")
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := second.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
}
