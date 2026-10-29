package androidvpn

import (
	"errors"
	"testing"

	"golang.zx2c4.com/wireguard/conn"
)

type peekBind struct {
	conn.Bind
	closed bool
}

func (b *peekBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) { return nil, 1234, nil }
func (b *peekBind) Close() error                                    { b.closed = true; return nil }
func (b *peekBind) PeekLookAtSocketFd4() (int, error)               { return 4, nil }
func (b *peekBind) PeekLookAtSocketFd6() (int, error)               { return 6, nil }

func TestProtectFailureClosesWireGuardSockets(t *testing.T) {
	bind := &peekBind{}
	protected := 0
	adapter := &protectedBind{Bind: bind, system: rpcFunc(func(method string, p, r any) error {
		if method != "network.protect" {
			t.Fatalf("unexpected call: %s", method)
		}
		protected++
		if protected == 2 {
			return errors.New("network revoked")
		}
		return nil
	})}
	if _, _, err := adapter.Open(0); err == nil {
		t.Fatal("unprotected bind was admitted")
	}
	if !bind.closed || protected != 2 {
		t.Fatal("failed sockets not closed")
	}
}
