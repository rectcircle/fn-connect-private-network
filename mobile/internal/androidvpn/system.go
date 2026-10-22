// Package androidvpn adapts Android VPN capabilities to the shared Go client.
// This package has no Fyne dependency.
package androidvpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

// RPC is the fixed Java system adapter. Implementations must not log payloads:
// credential methods contain encrypted-store plaintext within this boundary.
type RPC interface{ Call(string, any, any) error }

type Secrets struct{ System RPC }

func secretKey(service, account string) string {
	sum := sha256.Sum256([]byte(service + "\x00" + account))
	return hex.EncodeToString(sum[:])
}
func (s Secrets) Get(service, account string) ([]byte, bool, error) {
	var result struct {
		Found bool   `json:"found"`
		Value []byte `json:"value"`
	}
	err := s.System.Call("secret.get", map[string]any{"key": secretKey(service, account)}, &result)
	return result.Value, result.Found, err
}
func (s Secrets) Put(service, account string, value []byte) error {
	return s.System.Call("secret.put", map[string]any{"key": secretKey(service, account), "value": value}, nil)
}
func (s Secrets) Delete(service, account string) error {
	return s.System.Call("secret.delete", map[string]any{"key": secretKey(service, account)}, nil)
}

type Network struct {
	System  RPC
	mu      sync.Mutex
	changed chan struct{}
}

func (n *Network) Notify() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.changed != nil {
		close(n.changed)
	}
	n.changed = make(chan struct{})
}
func (n *Network) Events(ctx context.Context) <-chan model.NetworkChange {
	out := make(chan model.NetworkChange, 1)
	go func() {
		defer close(out)
		for {
			n.mu.Lock()
			if n.changed == nil {
				n.changed = make(chan struct{})
			}
			changed := n.changed
			n.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-changed:
			}
			select {
			case out <- model.NetworkChange{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
func (n *Network) Snapshot() (client.NetworkSnapshot, error) {
	var info struct {
		Name      string   `json:"name"`
		Index     int      `json:"index"`
		Identity  string   `json:"identity"`
		Prefixes  []string `json:"prefixes"`
		Addresses []string `json:"addresses"`
	}
	if err := n.System.Call("network.snapshot", nil, &info); err != nil {
		return client.NetworkSnapshot{}, err
	}
	if info.Index <= 0 {
		return client.NetworkSnapshot{}, errors.New("no physical network")
	}
	s := client.NetworkSnapshot{InterfaceName: info.Name, InterfaceIndex: info.Index}
	link := client.PhysicalLink{Name: info.Name, Index: info.Index, Identity: info.Identity}
	for _, value := range info.Prefixes {
		p, e := netip.ParsePrefix(value)
		if e != nil {
			return s, e
		}
		s.Prefixes = append(s.Prefixes, p.Masked())
		link.Prefixes = append(link.Prefixes, p.Masked())
	}
	for _, value := range info.Addresses {
		a, e := netip.ParseAddr(value)
		if e != nil {
			return s, e
		}
		s.Addresses = append(s.Addresses, a)
		link.Addresses = append(link.Addresses, a)
		if wgconfig.IsPublicIPv6(a) {
			s.HasPublicIPv6 = true
			s.IPv6Networks = append(s.IPv6Networks, a.String())
		}
	}
	s.Links = []client.PhysicalLink{link}
	return s, nil
}
func (n *Network) Reachable(ctx context.Context, cfg model.PrivateAPIConfiguration, id string, snapshot client.NetworkSnapshot) (bool, error) {
	return (client.SystemLocalProbe{}).Reachable(ctx, cfg, id, snapshot)
}
func (n *Network) Protect(fd int) error {
	return n.System.Call("network.protect", map[string]int{"fd": fd}, nil)
}
func (n *Network) Resolve(host string) ([]string, error) {
	var addresses []string
	err := n.System.Call("network.resolve", map[string]string{"host": host}, &addresses)
	return addresses, err
}

// DecodeReply is shared by the JNI adapter and host-side protocol tests.
func DecodeReply(raw []byte, result any) error {
	var reply struct {
		Version int             `json:"version"`
		OK      bool            `json:"ok"`
		Result  json.RawMessage `json:"result"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return errors.New("invalid Android system reply")
	}
	if reply.Version != 1 {
		return errors.New("Android bridge version mismatch; complete the app update")
	}
	if !reply.OK {
		return errors.New(reply.Error)
	}
	if result == nil || len(reply.Result) == 0 || string(reply.Result) == "null" {
		return nil
	}
	return json.Unmarshal(reply.Result, result)
}
