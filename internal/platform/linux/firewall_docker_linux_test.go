//go:build linux

package linux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/google/nftables/userdata"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func TestUnconditionalDockerReturn(t *testing.T) {
	for _, test := range []struct {
		name  string
		exprs []expr.Any
		want  bool
	}{
		{"return", []expr.Any{&expr.Verdict{Kind: expr.VerdictReturn}}, true},
		{"counter_return", []expr.Any{&expr.Counter{}, &expr.Verdict{Kind: expr.VerdictReturn}}, true},
		{"accept", []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}}, false},
		{"counter", []expr.Any{&expr.Counter{}}, false},
		{"conditional", []expr.Any{&expr.Meta{}, &expr.Verdict{Kind: expr.VerdictReturn}}, false},
		{"empty", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := unconditionalReturn(&nftables.Rule{Exprs: test.exprs}); got != test.want {
				t.Fatalf("unconditionalReturn = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDockerForwardingIntegration(t *testing.T) {
	if os.Getenv("FNCPN_NETFILTER_TEST") != "1" {
		t.Skip("set FNCPN_NETFILTER_TEST=1 and run as root on Linux")
	}
	if os.Geteuid() != 0 {
		t.Fatal("network namespace test requires root")
	}
	current, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FNCPN_NETFILTER_CHILD") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestDockerForwardingIntegration$", "-test.v", "-test.timeout=90s")
		command.Env = append(os.Environ(), "FNCPN_NETFILTER_CHILD=1", "FNCPN_NETFILTER_PARENT_NS="+current)
		// Isolation is established before the child Go runtime starts, so every
		// thread and netlink socket inherits the test namespace, never the host.
		command.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNET}
		output, err := command.CombinedOutput()
		t.Logf("%s", output)
		if err != nil {
			t.Fatalf("isolated test failed (no host-network fallback): %v", err)
		}
		return
	}
	parent := os.Getenv("FNCPN_NETFILTER_PARENT_NS")
	if parent == "" || parent == current {
		t.Fatal("refusing to test without a distinct network namespace")
	}
	t.Logf("isolated network namespace: parent=%s test=%s", parent, current)
	for _, name := range []string{"iptables", "nsenter", "ping"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatal(err)
		}
	}
	if version := firewallTestCommand(t, "iptables", "--version"); !strings.Contains(version, "nf_tables") {
		t.Fatalf("this test requires iptables-nft, got %s", version)
	}
	clientNS := newFirewallTestNamespace(t)
	gatewayNS := newFirewallTestNamespace(t)
	t.Log("initialize test links using NETLINK_ROUTE only")
	addFirewallTestLink(t, "fncpn0", "client0", "10.253.203.1/24", clientNS, "10.253.203.2/24")
	addFirewallTestLink(t, "lan0", "gateway0", "192.168.71.2/24", gatewayNS, "192.168.71.1/24")
	addFirewallTestAddress(t, nil, "lan0", "192.168.72.2/24")
	gateway, err := netlink.NewHandleAt(gatewayNS, syscall.NETLINK_ROUTE)
	if err != nil {
		t.Fatalf("open gateway NETLINK_ROUTE handle: %v", err)
	}
	addFirewallTestAddress(t, gateway, "gateway0", "192.168.72.1/24")
	gateway.Close()
	client, err := netlink.NewHandleAt(clientNS, syscall.NETLINK_ROUTE)
	if err != nil {
		t.Fatalf("open client NETLINK_ROUTE handle: %v", err)
	}
	if err := client.RouteAdd(&netlink.Route{Gw: net.ParseIP("10.253.203.1")}); err != nil {
		client.Close()
		t.Fatalf("add client default route: %v", err)
	}
	client.Close()
	if _, err := (fileForwardingManager{path: ipv4ForwardPath}).Enable(context.Background()); err != nil {
		t.Fatalf("enable forwarding in test namespace: %v", err)
	}
	plan := model.ServerPlan{
		OverlayCIDR: "10.253.203.0/24",
		ListenPort:  54789,
		LANCIDRs:    []string{"192.168.71.0/24"},
	}
	const owner = "fncpn_netfilter_test"
	manager := systemFirewallManager{}
	apply := func() {
		t.Helper()
		if err := manager.Apply(context.Background(), owner, "fncpn0", plan); err != nil {
			t.Fatal(err)
		}
	}
	ping := func(destination string, wantSuccess bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "nsenter",
			fmt.Sprintf("--net=/proc/%d/fd/%d", os.Getpid(), clientNS),
			"--", "ping", "-n", "-c", "1", "-W", "1", destination)
		output, err := command.CombinedOutput()
		if ctx.Err() != nil || (err == nil) != wantSuccess {
			t.Fatalf("ping %s success=%v, want %v: %v\n%s", destination, err == nil, wantSuccess, err, output)
		}
		if !wantSuccess {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != 1 {
				t.Fatalf("ping failed for a reason other than missing replies: %v\n%s", err, output)
			}
		}
		t.Logf("ping %s success=%v", destination, err == nil)
	}

	apply()
	ping("192.168.71.1", true)
	t.Log("PASS: forwarding and NAT without Docker; gateway has no overlay return route")

	firewallTestCommand(t, "iptables", "-N", "DOCKER-USER")
	firewallTestCommand(t, "iptables", "-N", "DOCKER-FORWARD")
	firewallTestCommand(t, "iptables", "-A", "FORWARD", "-j", "DOCKER-USER")
	firewallTestCommand(t, "iptables", "-A", "FORWARD", "-j", "DOCKER-FORWARD")
	firewallTestCommand(t, "iptables", "-P", "FORWARD", "DROP")
	firewallTestCommand(t, "iptables", "-A", "DOCKER-USER", "-s", "203.0.113.0/24",
		"-m", "comment", "--comment", "fncpn_another_owner:forward", "-j", "DROP")
	firewallTestCommand(t, "iptables", "-A", "DOCKER-USER", "-j", "RETURN")
	beforeRules := firewallTestCommand(t, "iptables", "-S", "DOCKER-USER")
	beforeForward := firewallTestCommand(t, "iptables", "-S", "FORWARD")
	ping("192.168.71.1", false)
	t.Log("PASS: reproduced DROP despite the earlier FnCPN base-chain ACCEPT")

	checkRules := func(want int) {
		t.Helper()
		connection, err := nftables.New()
		if err != nil {
			t.Fatal(err)
		}
		table := &nftables.Table{Name: "filter", Family: nftables.TableFamilyIPv4}
		rules, err := connection.GetRules(table, &nftables.Chain{Name: "DOCKER-USER", Table: table})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, rule := range rules {
			if comment, _ := userdata.GetString(rule.UserData, userdata.TypeComment); comment == owner+":forward" {
				found++
			}
		}
		if found != want {
			t.Fatalf("owned Docker rule count = %d, want %d", found, want)
		}
		// iptables-nft must still be able to decode and manage this shared table.
		listing := firewallTestCommand(t, "iptables", "-S", "DOCKER-USER")
		if !strings.Contains(listing, "fncpn_another_owner:forward") ||
			!strings.HasSuffix(strings.TrimSpace(listing), "-A DOCKER-USER -j RETURN") {
			t.Fatalf("administrator rules or terminal RETURN changed:\n%s", listing)
		}
		if got := firewallTestCommand(t, "iptables", "-S", "FORWARD"); got != beforeForward {
			t.Fatalf("Docker FORWARD chain changed:\n%s", got)
		}
	}
	apply()
	checkRules(2)
	ping("192.168.71.1", true)
	ping("192.168.72.1", false)
	t.Log("PASS: allowed LAN passes; other LAN remains blocked; iptables-nft compatibility preserved")

	apply()
	checkRules(2)
	ping("192.168.71.1", true)
	t.Log("PASS: repeated apply creates no duplicate rules")

	connection, err := nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := stageDockerForwarding(connection, owner, "", model.ServerPlan{}); err != nil {
		t.Fatal(err)
	}
	connection.AddRule(&nftables.Rule{
		Table: &nftables.Table{Name: "filter", Family: nftables.TableFamilyIPv4},
		Chain: &nftables.Chain{Name: "fncpn_missing_chain"},
		Exprs: []expr.Any{&expr.Verdict{Kind: expr.VerdictAccept}},
	})
	if err := connection.Flush(); err == nil {
		t.Fatal("invalid transaction unexpectedly succeeded")
	}
	checkRules(2)
	ping("192.168.71.1", true)
	t.Log("PASS: failed transaction does not partially remove existing rules")

	plan.LANCIDRs = []string{"192.168.72.0/24"}
	apply()
	checkRules(2)
	ping("192.168.72.1", true)
	ping("192.168.71.1", false)
	t.Log("PASS: LAN change removes stale permissions")

	firewallTestCommand(t, "iptables", "-I", "DOCKER-USER", "1", "-d", "192.168.72.1", "-j", "DROP")
	apply()
	checkRules(2)
	ping("192.168.72.1", false)
	firewallTestCommand(t, "iptables", "-D", "DOCKER-USER", "-d", "192.168.72.1", "-j", "DROP")
	ping("192.168.72.1", true)
	t.Log("PASS: administrator DROP ahead of our rules is respected")

	plan.LANCIDRs = nil
	apply()
	checkRules(0)
	ping("192.168.72.1", false)
	if got := firewallTestCommand(t, "iptables", "-S", "DOCKER-USER"); got != beforeRules {
		t.Fatalf("disabling LAN changed another owner's rules:\n%s", got)
	}
	t.Log("PASS: disabling LAN removes only our forwarding permissions")

	plan.LANCIDRs = []string{"192.168.71.0/24"}
	apply()
	connection, err = nftables.New()
	if err != nil {
		t.Fatal(err)
	}
	connection.DelTable(&nftables.Table{Name: owner, Family: nftables.TableFamilyINet})
	if err := connection.Flush(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := (systemFirewallManager{}).Remove(context.Background(), owner); err != nil {
			t.Fatal(err)
		}
		checkRules(0)
	}
	if got := firewallTestCommand(t, "iptables", "-S", "DOCKER-USER"); got != beforeRules {
		t.Fatalf("cleanup changed another owner's rules:\n%s", got)
	}
	ping("192.168.71.1", false)
	t.Log("PASS: fresh manager cleans residual rules even with private table absent; cleanup is idempotent")
}

func firewallTestCommand(t *testing.T, name string, arguments ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, arguments...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, arguments, err, output)
	}
	return string(output)
}

func newFirewallTestNamespace(t *testing.T) netns.NsHandle {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	original, err := netns.Get()
	if err != nil {
		t.Fatalf("open current test namespace: %v", err)
	}
	defer original.Close()
	namespace, err := netns.New()
	if restoreErr := netns.Set(original); restoreErr != nil {
		// Do not return a thread in the wrong namespace to the Go runtime.
		// This process already lives in the outer, isolated test namespace.
		fmt.Fprintln(os.Stderr, "restore test network namespace:", restoreErr)
		os.Exit(1)
	}
	if err != nil {
		t.Fatalf("create test peer namespace: %v", err)
	}
	t.Cleanup(func() { namespace.Close() })
	return namespace
}

func addFirewallTestLink(
	t *testing.T,
	name, peer, address string,
	namespace netns.NsHandle,
	peerAddress string,
) {
	t.Helper()
	link := &netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: name}, PeerName: peer}
	if err := netlink.LinkAdd(link); err != nil {
		t.Fatalf("create test veth %s/%s: %v", name, peer, err)
	}
	peerLink, err := netlink.LinkByName(peer)
	if err != nil {
		t.Fatalf("find test peer %s: %v", peer, err)
	}
	if err := netlink.LinkSetNsFd(peerLink, int(namespace)); err != nil {
		t.Fatalf("move test peer %s into namespace: %v", peer, err)
	}
	addFirewallTestAddress(t, nil, name, address)
	handle, err := netlink.NewHandleAt(namespace, syscall.NETLINK_ROUTE)
	if err != nil {
		t.Fatalf("open NETLINK_ROUTE handle for peer %s: %v", peer, err)
	}
	defer handle.Close()
	addFirewallTestAddress(t, handle, peer, peerAddress)
}

func addFirewallTestAddress(t *testing.T, handle *netlink.Handle, name, address string) {
	t.Helper()
	if handle == nil {
		var err error
		// The library default also opens XFRM/IPsec, which this fixture does
		// not use and a NAS kernel may not support.
		handle, err = netlink.NewHandle(syscall.NETLINK_ROUTE)
		if err != nil {
			t.Fatalf("open NETLINK_ROUTE handle for %s: %v", name, err)
		}
		defer handle.Close()
	}
	link, err := handle.LinkByName(name)
	if err != nil {
		t.Fatalf("find test interface %s: %v", name, err)
	}
	parsed, err := netlink.ParseAddr(address)
	if err != nil {
		t.Fatalf("parse test address %s: %v", address, err)
	}
	if err := handle.AddrAdd(link, parsed); err != nil {
		t.Fatalf("add test address %s to %s: %v", address, name, err)
	}
	if err := handle.LinkSetUp(link); err != nil {
		t.Fatalf("activate test interface %s: %v", name, err)
	}
}
