//go:build linux && loongarch_validation

package probe

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
	linuxplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/linux"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func requireVM(t *testing.T) {
	t.Helper()
	if os.Getenv("FNCPN_LOONGARCH_VM") != "1" || runtime.GOARCH != "loong64" || os.Geteuid() != 0 {
		t.Fatal("requires the disposable LoongArch system VM")
	}
	pid1, err := os.ReadFile("/proc/1/comm")
	if err != nil || strings.TrimSpace(string(pid1)) != "systemd" {
		t.Fatal("requires a full systemd guest, not a container")
	}
}

func command(t *testing.T, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, out, err)
	}
	return string(out)
}

func eventually(t *testing.T, check func() error) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = check(); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal(err)
}

// This is a WireGuard peer fixture, not a simulated fnOS authentication server.
func TestPeer(t *testing.T) {
	if os.Getenv("FNCPN_VALIDATION_PEER") != "1" {
		t.Skip("child HTTP peer only")
	}
	requireVM(t)
	server := http.Server{Addr: "10.254.250.1:8080", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "loongarch-wireguard-ok")
	})}
	t.Fatal(server.ListenAndServe())
}

func TestWireGuardTrafficAndReconnect(t *testing.T) {
	requireVM(t)
	command(t, "ip", "netns", "add", "validation-peer")
	t.Cleanup(func() { exec.Command("ip", "netns", "del", "validation-peer").Run() })
	command(t, "ip", "link", "add", "validation0", "type", "veth", "peer", "name", "validation1")
	t.Cleanup(func() { exec.Command("ip", "link", "del", "validation0").Run() })
	command(t, "ip", "link", "set", "validation1", "netns", "validation-peer")
	// Public-form IPv6 is required by the direct-mode contract. These addresses
	// exist only in this NIC-less VM; no packets can leave the guest.
	command(t, "ip", "-6", "addr", "add", "2606:4700:ffff::1/64", "dev", "validation0", "nodad")
	command(t, "ip", "link", "set", "validation0", "up")
	remote := func(args ...string) string {
		return command(t, "ip", append([]string{"netns", "exec", "validation-peer"}, args...)...)
	}
	remote("ip", "link", "set", "lo", "up")
	remote("ip", "-6", "addr", "add", "2606:4700:ffff::2/64", "dev", "validation1", "nodad")
	remote("ip", "link", "set", "validation1", "up")
	remote("ip", "link", "add", "wg-peer", "type", "wireguard")
	remote("ip", "addr", "add", "10.254.250.1/24", "dev", "wg-peer")
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "peer.key")
	if err = os.WriteFile(keyFile, []byte(serverKey.String()), 0600); err != nil {
		t.Fatal(err)
	}
	remote("wg", "set", "wg-peer", "private-key", keyFile, "listen-port", "51820", "peer", key.PublicKey().String(), "allowed-ips", "10.254.250.2/32")
	remote("ip", "link", "set", "wg-peer", "up")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process := exec.Command("ip", "netns", "exec", "validation-peer", executable, "-test.run=^TestPeer$", "-test.timeout=150s")
	process.Env = append(os.Environ(), "FNCPN_VALIDATION_PEER=1")
	process.Stdout, process.Stderr = os.Stdout, os.Stderr
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { process.Process.Kill(); process.Wait() })
	engine := linuxplatform.NewSystemClientEngine(nil, t.TempDir())
	t.Cleanup(func() { engine.Close(context.Background()) })
	ctx := peer.WithIdentity(t.Context(), peer.Identity{UID: 1000, PID: os.Getpid()})
	plan := model.ClientPlan{Mode: "direct", PrivateKey: key.String(), ClientAddress: "10.254.250.2/32", ServerPublicKey: serverKey.PublicKey().String(), Endpoint: "[2606:4700:ffff::2]:51820", AllowedIPs: []string{"10.254.250.0/24"}, MTU: 1280}
	if err = engine.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true, Proxy: nil}}
	check := func() error {
		r, err := client.Get("http://10.254.250.1:8080")
		if err != nil {
			return err
		}
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}
		if r.StatusCode != 200 || string(body) != "loongarch-wireguard-ok" {
			return fmt.Errorf("unexpected peer reply: %s", body)
		}
		return nil
	}
	eventually(t, check)
	control, err := wgctrl.New()
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	device, err := control.Device(engine.Status().Interface)
	if err != nil || len(device.Peers) != 1 || device.Peers[0].LastHandshakeTime.IsZero() || device.Peers[0].ReceiveBytes == 0 {
		t.Fatalf("missing real WireGuard handshake/traffic: %v %+v", err, device)
	}
	command(t, "ip", "link", "set", "validation0", "down")
	if check() == nil {
		t.Fatal("request succeeded with underlay down")
	}
	command(t, "ip", "link", "set", "validation0", "up")
	eventually(t, check)
	name := engine.Status().Interface
	if err = engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if exec.Command("ip", "link", "show", name).Run() == nil {
		t.Fatal("disconnect left interface")
	}
	if err = engine.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	eventually(t, check)
	t.Log("real project client engine: WireGuard handshake, HTTP, underlay recovery, disconnect and reconnect passed")
}

func TestRealResolvedDNS(t *testing.T) {
	requireVM(t)
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &mdns.Server{PacketConn: packet, Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, q *mdns.Msg) {
		r := new(mdns.Msg)
		r.SetReply(q)
		rr, _ := mdns.NewRR(q.Question[0].Name + " 1 IN A 10.254.250.1")
		r.Answer = []mdns.RR{rr}
		w.WriteMsg(r)
	})}
	go server.ActivateAndServe()
	t.Cleanup(func() { server.Shutdown() })
	plan := model.DNSPlan{Suffix: "validation.fncpn", Address: "127.0.0.1", Port: uint16(packet.LocalAddr().(*net.UDPAddr).Port), Generation: strings.Repeat("a", 32)}
	rules := privileged.NewDNSRules(t.TempDir())
	t.Cleanup(func() { rules.Close(context.Background()) })
	ctx := peer.WithIdentity(t.Context(), peer.Identity{UID: 1000, PID: os.Getpid()})
	request, err := ipc.NewRequest("validation", privileged.MethodApplyDNS, plan)
	if err != nil {
		t.Fatal(err)
	}
	if reply := rules.Handle(ctx, request); !reply.OK {
		t.Fatalf("real resolved apply: %+v", reply)
	}
	eventually(t, func() error {
		query := new(mdns.Msg)
		query.SetQuestion("nas.validation.fncpn.", mdns.TypeA)
		reply, _, err := (&mdns.Client{Timeout: 2 * time.Second}).Exchange(query, "127.0.0.53:53")
		if err != nil {
			return err
		}
		if len(reply.Answer) != 1 || !strings.Contains(reply.Answer[0].String(), "10.254.250.1") {
			return fmt.Errorf("wrong resolved response: %v", reply)
		}
		return nil
	})
	if err = rules.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if exec.Command("ip", "link", "show", "fndaaaaaaaaaa").Run() == nil {
		t.Fatal("DNS cleanup left dedicated link")
	}
	t.Log("real systemd-resolved D-Bus, stub DNS query and dedicated-link cleanup passed")
}
