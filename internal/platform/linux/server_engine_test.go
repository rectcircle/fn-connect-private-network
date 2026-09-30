package linux

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func TestServerEngineApplyUpdateAndRemove(t *testing.T) {
	var events []string
	state := &fakeServerStateStore{events: &events}
	engine := newTestServerEngine(&events, state)

	if err := engine.Apply(context.Background(), serverPlan(1)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := engine.Apply(context.Background(), serverPlan(2)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := engine.Apply(context.Background(), serverPlan(2)); err != nil {
		t.Fatalf("reconcile same plan: %v", err)
	}
	status := engine.Status()
	if !status.Active ||
		status.Interface != defaultInterfaceName ||
		status.PublicKey == "" ||
		status.ListenPort != serverPlan(2).ListenPort {
		t.Fatalf("status = %+v", status)
	}
	if err := engine.Remove(context.Background()); err != nil {
		t.Fatalf("remove: %v", err)
	}

	expected := []string{
		"state.save",
		"device.apply:51001",
		"forwarding.enable",
		"firewall.apply:51001",
		"device.apply:51002",
		"forwarding.set:true",
		"firewall.apply:51002",
		"device.apply:51002",
		"forwarding.set:true",
		"firewall.apply:51002",
		"state.load",
		"firewall.remove",
		"device.remove",
		"state.clear",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
}

func TestServerEngineReportsFailedDeclarativeUpdate(t *testing.T) {
	var events []string
	firewallError := errors.New("firewall failed")
	state := &fakeServerStateStore{events: &events}
	engine := newTestServerEngine(&events, state)
	if err := engine.Apply(context.Background(), serverPlan(1)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	engine.firewall.(*fakeFirewallManager).failPort = serverPlan(2).ListenPort
	engine.firewall.(*fakeFirewallManager).applyErr = firewallError

	err := engine.Apply(context.Background(), serverPlan(2))
	if !errors.Is(err, firewallError) {
		t.Fatalf("update error = %v", err)
	}
	if status := engine.Status(); !status.Active || !status.Degraded {
		t.Fatalf("failed update status = %+v", status)
	}
	expectedTail := []string{
		"device.apply:51002",
		"forwarding.set:true",
		"firewall.apply:51002",
	}
	if !slices.Equal(events[len(events)-len(expectedTail):], expectedTail) {
		t.Fatalf("events = %#v", events)
	}
	engine.firewall.(*fakeFirewallManager).failPort = 0
	if err := engine.Apply(context.Background(), serverPlan(1)); err != nil {
		t.Fatalf("restore previous plan: %v", err)
	}
	if status := engine.Status(); !status.Active ||
		status.Degraded ||
		status.ListenPort != serverPlan(1).ListenPort {
		t.Fatalf("restored status = %+v", status)
	}
}

func TestServerEngineCleansPersistedResourcesOnRecovery(t *testing.T) {
	var events []string
	state := &fakeServerStateStore{
		events: &events,
		loaded: &persistedServerState{
			Version:       serverStateVersion,
			OwnerToken:    "owner",
			InterfaceName: defaultInterfaceName,
			FirewallTable: "fncpn_owner",
		},
	}
	engine := newTestServerEngine(&events, state)

	if err := engine.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if status := engine.Status(); status.Active {
		t.Fatalf("status = %+v", status)
	}
	expected := []string{
		"state.load",
		"firewall.remove",
		"device.remove",
		"state.clear",
	}
	if !slices.Equal(events, expected) {
		t.Fatalf("events = %#v, want %#v", events, expected)
	}
}

func TestServerWireGuardConfigDiffPreservesUnchangedPeers(t *testing.T) {
	plan := serverPlan(1)
	var privateKey wgtypes.Key
	privateKey[0] = 1
	publicKey, err := wgtypes.ParseKey(plan.Peers[0].PublicKey)
	if err != nil {
		t.Fatalf("parse peer key: %v", err)
	}
	_, allowedIP, err := net.ParseCIDR(plan.Peers[0].Address)
	if err != nil {
		t.Fatalf("parse peer address: %v", err)
	}
	device := &wgtypes.Device{
		PublicKey:  privateKey.PublicKey(),
		ListenPort: int(plan.ListenPort),
		Peers: []wgtypes.Peer{{
			PublicKey:         publicKey,
			AllowedIPs:        []net.IPNet{*allowedIP},
			Endpoint:          &net.UDPAddr{IP: net.ParseIP("192.0.2.1"), Port: 51820},
			LastHandshakeTime: time.Now(),
			ReceiveBytes:      1024,
			TransmitBytes:     2048,
		}},
	}
	config, changed, err := serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		t.Fatalf("compare matching configuration: %v", err)
	}
	if changed ||
		config.ReplacePeers ||
		config.PrivateKey != nil ||
		config.ListenPort != nil ||
		config.FirewallMark != nil ||
		len(config.Peers) != 0 {
		t.Fatalf("matching configuration produced diff: %+v", config)
	}

	device.Peers[0].AllowedIPs[0] = *mustCIDR(t, "10.203.0.3/32")
	config, changed, err = serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		t.Fatalf("compare peer address drift: %v", err)
	}
	if !changed ||
		config.ReplacePeers ||
		len(config.Peers) != 1 ||
		config.Peers[0].PublicKey != publicKey ||
		!config.Peers[0].ReplaceAllowedIPs ||
		config.Peers[0].Remove {
		t.Fatalf("peer address diff = %+v", config)
	}
}

func TestServerWireGuardConfigDiffAddsAndRemovesOnlyChangedPeers(t *testing.T) {
	plan := serverPlan(1)
	var privateKey wgtypes.Key
	privateKey[0] = 1
	firstKey, err := wgtypes.ParseKey(plan.Peers[0].PublicKey)
	if err != nil {
		t.Fatalf("parse first peer key: %v", err)
	}
	device := &wgtypes.Device{
		PublicKey:  privateKey.PublicKey(),
		ListenPort: int(plan.ListenPort),
		Peers: []wgtypes.Peer{{
			PublicKey:  firstKey,
			AllowedIPs: []net.IPNet{*mustCIDR(t, plan.Peers[0].Address)},
		}},
	}
	var secondKey wgtypes.Key
	secondKey[0] = 3
	plan.Peers = append(plan.Peers, model.Peer{
		PublicKey: secondKey.String(),
		Address:   "10.203.0.3/32",
	})

	config, changed, err := serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		t.Fatalf("compare added peer: %v", err)
	}
	if !changed ||
		config.ReplacePeers ||
		len(config.Peers) != 1 ||
		config.Peers[0].PublicKey != secondKey ||
		config.Peers[0].Remove {
		t.Fatalf("added peer diff = %+v", config)
	}

	plan.Peers = plan.Peers[1:]
	config, changed, err = serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		t.Fatalf("compare removed peer: %v", err)
	}
	if !changed ||
		config.ReplacePeers ||
		len(config.Peers) != 2 ||
		!peerUpdate(config.Peers, firstKey).Remove ||
		peerUpdate(config.Peers, secondKey).Remove {
		t.Fatalf("removed peer diff = %+v", config)
	}
}

func TestServerWireGuardConfigDiffRepairsPeerAndDeviceDrift(t *testing.T) {
	plan := serverPlan(1)
	var privateKey wgtypes.Key
	privateKey[0] = 1
	publicKey, err := wgtypes.ParseKey(plan.Peers[0].PublicKey)
	if err != nil {
		t.Fatalf("parse peer key: %v", err)
	}
	var presharedKey wgtypes.Key
	presharedKey[0] = 9
	device := &wgtypes.Device{
		PublicKey:    privateKey.PublicKey(),
		ListenPort:   int(plan.ListenPort),
		FirewallMark: 42,
		Peers: []wgtypes.Peer{{
			PublicKey:                   publicKey,
			PresharedKey:                presharedKey,
			PersistentKeepaliveInterval: 25 * time.Second,
			AllowedIPs:                  []net.IPNet{*mustCIDR(t, plan.Peers[0].Address)},
		}},
	}

	config, changed, err := serverWireGuardConfigDiff(device, privateKey, plan)
	if err != nil {
		t.Fatalf("compare peer drift: %v", err)
	}
	update := peerUpdate(config.Peers, publicKey)
	if !changed ||
		config.ReplacePeers ||
		config.FirewallMark == nil ||
		*config.FirewallMark != 0 ||
		update.PresharedKey == nil ||
		*update.PresharedKey != (wgtypes.Key{}) ||
		update.PersistentKeepaliveInterval == nil ||
		*update.PersistentKeepaliveInterval != 0 {
		t.Fatalf("peer and device drift diff = %+v", config)
	}
}

func peerUpdate(peers []wgtypes.PeerConfig, key wgtypes.Key) wgtypes.PeerConfig {
	for _, peer := range peers {
		if peer.PublicKey == key {
			return peer
		}
	}
	return wgtypes.PeerConfig{}
}

func mustCIDR(t *testing.T, value string) *net.IPNet {
	t.Helper()
	_, network, err := net.ParseCIDR(value)
	if err != nil {
		t.Fatalf("parse CIDR %q: %v", value, err)
	}
	return network
}

func newTestServerEngine(
	events *[]string,
	state *fakeServerStateStore,
) *ServerEngine {
	var key wgtypes.Key
	key[0] = 1
	return NewServerEngine(
		defaultInterfaceName,
		fakeKeyStore{key: key},
		&fakeDeviceManager{events: events},
		&fakeForwardingManager{events: events},
		&fakeFirewallManager{events: events},
		state,
	)
}

func serverPlan(sequence uint64) model.ServerPlan {
	var peerKey wgtypes.Key
	peerKey[0] = 2
	return model.ServerPlan{
		OverlayCIDR:   "10.203.0.0/24",
		ServerAddress: "10.203.0.1/24",
		ListenPort:    uint16(51000 + sequence),
		LANCIDRs:      []string{"192.168.71.0/24"},
		Peers: []model.Peer{
			{PublicKey: peerKey.String(), Address: "10.203.0.2/32"},
		},
	}
}

type fakeKeyStore struct {
	key wgtypes.Key
	err error
}

func (s fakeKeyStore) LoadOrCreate() (wgtypes.Key, error) {
	return s.key, s.err
}

type fakeDeviceManager struct {
	events *[]string
}

func (*fakeDeviceManager) Available() bool {
	return true
}

func (m *fakeDeviceManager) Apply(
	_ context.Context,
	_ string,
	_ string,
	_ wgtypes.Key,
	plan model.ServerPlan,
) error {
	*m.events = append(*m.events, fmt.Sprintf("device.apply:%d", plan.ListenPort))
	return nil
}

func (m *fakeDeviceManager) Remove(context.Context, string, string) error {
	*m.events = append(*m.events, "device.remove")
	return nil
}

func (*fakeDeviceManager) PeerStatus(
	context.Context,
	string,
) ([]model.PrivilegedPeerStatus, error) {
	return nil, nil
}

type fakeForwardingManager struct {
	events *[]string
}

func (*fakeForwardingManager) Available() bool {
	return true
}

func (m *fakeForwardingManager) Enable(context.Context) (bool, error) {
	*m.events = append(*m.events, "forwarding.enable")
	return false, nil
}

func (m *fakeForwardingManager) Set(_ context.Context, enabled bool) error {
	*m.events = append(*m.events, fmt.Sprintf("forwarding.set:%t", enabled))
	return nil
}

type fakeFirewallManager struct {
	events   *[]string
	failPort uint16
	applyErr error
}

func (*fakeFirewallManager) Available() bool {
	return true
}

func (m *fakeFirewallManager) Apply(
	_ context.Context,
	_ string,
	_ string,
	plan model.ServerPlan,
) error {
	*m.events = append(*m.events, fmt.Sprintf("firewall.apply:%d", plan.ListenPort))
	if plan.ListenPort == m.failPort {
		return m.applyErr
	}
	return nil
}

func (m *fakeFirewallManager) Remove(context.Context, string) error {
	*m.events = append(*m.events, "firewall.remove")
	return nil
}

type fakeServerStateStore struct {
	events *[]string
	loaded *persistedServerState
}

func (s *fakeServerStateStore) Load() (*persistedServerState, error) {
	*s.events = append(*s.events, "state.load")
	return s.loaded, nil
}

func (s *fakeServerStateStore) Save(state persistedServerState) error {
	*s.events = append(*s.events, "state.save")
	s.loaded = &state
	return nil
}

func (s *fakeServerStateStore) Clear() error {
	*s.events = append(*s.events, "state.clear")
	s.loaded = nil
	return nil
}
