package iosvpn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type Secrets struct{ System System }

func secretKey(service, account string) string {
	sum := sha256.Sum256([]byte(service + "\x00" + account))
	return hex.EncodeToString(sum[:])
}
func (s Secrets) Get(service, account string) ([]byte, bool, error) {
	var r struct {
		Value []byte `json:"value"`
		Found bool   `json:"found"`
	}
	err := s.System.Call("secret.get", map[string]string{"key": secretKey(service, account)}, &r)
	return r.Value, r.Found, err
}
func (s Secrets) Put(service, account string, value []byte) error {
	return s.System.Call("secret.put", map[string]any{"key": secretKey(service, account), "value": value}, nil)
}
func (s Secrets) Delete(service, account string) error {
	return s.System.Call("secret.delete", map[string]string{"key": secretKey(service, account)}, nil)
}

type Network struct {
	client.SystemLocalProbe
	mu      sync.Mutex
	changed chan struct{}
}

func NewNetwork() *Network { return &Network{changed: make(chan struct{})} }
func (n *Network) Notify() {
	n.mu.Lock()
	close(n.changed)
	n.changed = make(chan struct{})
	n.mu.Unlock()
}
func (n *Network) Events(ctx context.Context) <-chan model.NetworkChange {
	out := make(chan model.NetworkChange, 1)
	go func() {
		defer close(out)
		for {
			n.mu.Lock()
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
