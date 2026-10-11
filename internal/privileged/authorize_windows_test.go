//go:build windows

package privileged

import (
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func TestWindowsClientPeerMustMatchActiveConsoleIdentity(t *testing.T) {
	valid := ipc.PeerIdentity{PID: 42, SID: "S-1-5-21-100", SessionID: 3}
	tests := []struct {
		name        string
		identity    ipc.PeerIdentity
		activeSID   string
		activeID    uint32
		wantAllowed bool
	}{
		{name: "active console user", identity: valid, activeSID: valid.SID, activeID: 3, wantAllowed: true},
		{name: "different user", identity: valid, activeSID: "S-1-5-21-200", activeID: 3},
		{name: "different session", identity: valid, activeSID: valid.SID, activeID: 4},
		{name: "no active console", identity: valid, activeSID: valid.SID, activeID: 0xffffffff},
		{name: "missing process identity", identity: ipc.PeerIdentity{SID: valid.SID, SessionID: 3}, activeSID: valid.SID, activeID: 3},
		{name: "missing peer SID", identity: ipc.PeerIdentity{PID: 42, SessionID: 3}, activeSID: valid.SID, activeID: 3},
		{name: "missing active user", identity: valid, activeID: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateWindowsClientPeer(test.identity, test.activeID, test.activeSID)
			if (err == nil) != test.wantAllowed {
				t.Fatalf("validateWindowsClientPeer() error = %v, wantAllowed = %v", err, test.wantAllowed)
			}
		})
	}
}
