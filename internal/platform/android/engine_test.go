package androidvpn

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.zx2c4.com/wireguard/conn"
)

func TestDNSPlanRollbackKeepsLastWorkingListener(t *testing.T) {
	files := map[int]*os.File{}
	var writers []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
		for _, f := range writers {
			f.Close()
		}
	}()
	failNext := false
	established := 0
	system := rpcFunc(func(method string, p, r any) error {
		switch method {
		case "network.snapshot":
			return reply(map[string]any{"prefixes": []string{"192.168.1.0/24"}}, r)
		case "vpn.establish":
			if failNext {
				failNext = false
				return errors.New("establish failed")
			}
			read, write, err := os.Pipe()
			if err != nil {
				return err
			}
			files[int(read.Fd())] = read
			writers = append(writers, write)
			established++
			return reply(int(read.Fd()), r)
		case "vpn.remove":
			return nil
		default:
			return errors.New("unexpected system method")
		}
	})
	engine := &Engine{System: system, AdoptFD: func(fd int) (*os.File, error) { return files[fd], nil }}
	ctx := context.Background()
	defer engine.Remove(ctx)
	original := model.DNSPlan{Suffix: "example.fncpn", Address: "127.0.0.1", Port: 12345, Generation: "first"}
	if err := engine.ApplyDNS(ctx, original); err != nil {
		t.Fatal(err)
	}
	failNext = true
	changed := original
	changed.Generation = "second"
	changed.Port++
	if err := engine.ApplyDNS(ctx, changed); err == nil {
		t.Fatal("expected route failure")
	}
	if engine.dnsPlan != original || engine.dns == nil || engine.tun == nil || established != 2 {
		t.Fatal("DNS plan not restored")
	}
	status, err := engine.Status(ctx)
	if err != nil || status.Active {
		t.Fatal("DNS-only LOCAL reported an active WireGuard tunnel")
	}
	if err := engine.RemoveDNS(ctx, changed); err != nil {
		t.Fatal(err)
	}
	if engine.dnsPlan != original {
		t.Fatal("stale generation removed current DNS")
	}
	if err := engine.RemoveDNS(ctx, original); err != nil {
		t.Fatal(err)
	}
	if engine.tun != nil || engine.dns != nil {
		t.Fatal("DNS resources left after removal")
	}
	if err := engine.ApplyDNS(ctx, original); err != nil {
		t.Fatal(err)
	}
	count := established
	engine.PrepareShutdown()
	if err := engine.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if established != count || engine.tun != nil {
		t.Fatal("system shutdown recreated DNS tunnel")
	}
	if err := engine.ApplyDNS(ctx, original); err == nil {
		t.Fatal("stopping service accepted new DNS plan")
	}
}

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
