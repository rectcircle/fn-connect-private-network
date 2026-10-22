//go:build linux && !android

package privileged

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/vishvananda/netlink"
)

// A dedicated dummy link isolates resolved configuration from physical links.
// A root-owned restricted DNS bridge lets resolved use port 53 on that link;
// the existing user daemon continues listening on its unprivileged loopback port.
// No global DNS servers, resolv.conf, NetworkManager or forwarding settings change.
type linuxDNSBridge struct{ udp, tcp *mdns.Server }

var linuxDNSBridges = struct {
	sync.Mutex
	items map[string]*linuxDNSBridge
}{items: make(map[string]*linuxDNSBridge)}
var linuxResolverCommand = func(arguments ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{"--system", "call", "org.freedesktop.resolve1", "/org/freedesktop/resolve1", "org.freedesktop.resolve1.Manager"}, arguments...)
	output, err := exec.CommandContext(ctx, "busctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemd-resolved (%s); install/enable the runtime service and stub resolver: %s: %w", arguments[0], output, err)
	}
	return nil
}

func validatePlatformDNS(p model.DNSPlan) error {
	if p.Port < 1024 {
		return errors.New("Linux DNS forwarder must use an unprivileged port")
	}
	return nil
}
func linuxDNSLink(p model.DNSPlan) (string, net.IP) {
	data, _ := hex.DecodeString(p.Generation)
	return "fnd" + p.Generation[:10], net.IPv4(169, 254, 1+data[0]%254, 1+data[1]%254)
}
func checkLinuxStubResolver(data []byte) error {
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			if fields[1] != "127.0.0.53" {
				return errors.New("system DNS is not using the systemd-resolved stub; configure /etc/resolv.conf to use /run/systemd/resolve/stub-resolv.conf")
			}
			found = true
		}
	}
	if !found {
		return errors.New("systemd-resolved stub nameserver is not configured")
	}
	return nil
}

var linuxResolvedReady = func() error {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return err
	}
	return checkLinuxStubResolver(data)
}

// A failed rollback must keep the journal so restart can retry owned cleanup.
type linuxDNSCleanupError struct{ error }

func (linuxDNSCleanupError) CleanupPending() bool { return true }

func installDNSRule(p model.DNSPlan) (result error) {
	if err := linuxResolvedReady(); err != nil {
		return err
	}
	name, address := linuxDNSLink(p)
	// Refuse to replace any unrelated interface or address, even when names collide.
	if link, err := netlink.LinkByName(name); err == nil {
		if link.Type() != "dummy" || link.Attrs().Alias != "fncpn-dns:"+p.Generation {
			return errors.New("DNS interface is owned by another program")
		}
		if err = removeDNSRule(p); err != nil {
			return err
		}
	} else {
		var missing netlink.LinkNotFoundError
		if !errors.As(err, &missing) {
			return err
		}
	}
	addresses, err := netlink.AddrList(nil, netlink.FAMILY_V4)
	if err != nil {
		return err
	}
	for _, existing := range addresses {
		if existing.IP.Equal(address) {
			return errors.New("DNS link-local address is already in use")
		}
	}
	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name, Alias: "fncpn-dns:" + p.Generation}}
	if err = netlink.LinkAdd(link); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			if cleanupErr := removeDNSRule(p); cleanupErr != nil {
				result = linuxDNSCleanupError{errors.Join(result, cleanupErr)}
			}
		}
	}()
	owned, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	if err = netlink.LinkSetAlias(owned, "fncpn-dns:"+p.Generation); err != nil {
		_ = netlink.LinkDel(owned)
		return err
	}
	if err = netlink.AddrAdd(owned, &netlink.Addr{IPNet: &net.IPNet{IP: address, Mask: net.CIDRMask(32, 32)}}); err != nil {
		return err
	}
	if err = netlink.LinkSetUp(owned); err != nil {
		return err
	}
	bridge, err := startLinuxDNSBridge(p, address)
	if err != nil {
		return err
	}
	linuxDNSBridges.Lock()
	linuxDNSBridges.items[p.Generation] = bridge
	linuxDNSBridges.Unlock()
	return configureLinuxResolved(p, owned.Attrs().Index, address)
}
func configureLinuxResolved(p model.DNSPlan, linkIndex int, address net.IP) error {
	index := strconv.Itoa(linkIndex)
	octets := address.To4()
	commands := [][]string{
		{"SetLinkDNSEx", "ia(iayqs)", index, "1", "2", "4", strconv.Itoa(int(octets[0])), strconv.Itoa(int(octets[1])), strconv.Itoa(int(octets[2])), strconv.Itoa(int(octets[3])), "53", ""},
		{"SetLinkDomains", "ia(sb)", index, "1", p.Suffix, "true"},
		{"SetLinkDefaultRoute", "ib", index, "false"},
		{"SetLinkDNSSEC", "is", index, "no"},
		{"SetLinkDNSOverTLS", "is", index, "no"},
	}
	for _, args := range commands {
		if err := linuxResolverCommand(args...); err != nil {
			return err
		}
	}
	return nil
}
func startLinuxDNSBridge(p model.DNSPlan, address net.IP) (*linuxDNSBridge, error) {
	host := net.JoinHostPort(address.String(), "53")
	udp, err := net.ListenPacket("udp4", host)
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp4", host)
	if err != nil {
		udp.Close()
		return nil, err
	}
	handler := mdns.HandlerFunc(func(w mdns.ResponseWriter, query *mdns.Msg) {
		response := new(mdns.Msg)
		response.SetRcode(query, mdns.RcodeRefused)
		host, _, _ := net.SplitHostPort(w.RemoteAddr().String())
		source := net.ParseIP(host)
		if source != nil && (source.IsLoopback() || source.Equal(address)) && len(query.Question) == 1 && mdns.IsSubDomain(mdns.Fqdn(p.Suffix), query.Question[0].Name) {
			transport := "udp"
			if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
				transport = "tcp"
			}
			client := &mdns.Client{Net: transport, Timeout: 3 * time.Second}
			reply, _, err := client.Exchange(query, net.JoinHostPort(p.Address, strconv.Itoa(int(p.Port))))
			if err == nil {
				response = reply
			} else {
				response.SetRcode(query, mdns.RcodeServerFailure)
			}
		}
		_ = w.WriteMsg(response)
	})
	bridge := &linuxDNSBridge{udp: &mdns.Server{PacketConn: udp, Handler: handler}, tcp: &mdns.Server{Listener: tcp, Handler: handler}}
	go bridge.udp.ActivateAndServe()
	go bridge.tcp.ActivateAndServe()
	return bridge, nil
}
func verifyDNSRule(p model.DNSPlan) error {
	name, address := linuxDNSLink(p)
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return os.ErrNotExist
		}
		return err
	}
	if link.Type() != "dummy" || link.Attrs().Alias != "fncpn-dns:"+p.Generation {
		return errors.New("DNS interface ownership changed")
	}
	linuxDNSBridges.Lock()
	bridge := linuxDNSBridges.items[p.Generation]
	linuxDNSBridges.Unlock()
	if bridge == nil {
		return os.ErrNotExist
	}
	if err := linuxResolvedReady(); err != nil {
		return err
	}
	// resolved can restart independently. Reassert only this owned link's routing
	// domain and DNS target while the lease is alive.
	return configureLinuxResolved(p, link.Attrs().Index, address)
}
func removeDNSRule(p model.DNSPlan) error {
	linuxDNSBridges.Lock()
	bridge := linuxDNSBridges.items[p.Generation]
	delete(linuxDNSBridges.items, p.Generation)
	linuxDNSBridges.Unlock()
	if bridge != nil {
		_ = bridge.udp.Shutdown()
		_ = bridge.tcp.Shutdown()
	}
	name, _ := linuxDNSLink(p)
	link, err := netlink.LinkByName(name)
	if err != nil {
		var missing netlink.LinkNotFoundError
		if errors.As(err, &missing) {
			return nil
		}
		return err
	}
	if link.Type() != "dummy" || link.Attrs().Alias != "fncpn-dns:"+p.Generation {
		return errors.New("refusing to remove an unowned DNS interface")
	}
	// Deleting an owned link also discards resolved's per-link settings, even
	// after resolved stopped/restarted. Do not revert someone else's link.
	return netlink.LinkDel(link)
}
