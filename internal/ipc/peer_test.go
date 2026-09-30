//go:build darwin || linux

package ipc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentifyPeer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	result := make(chan PeerIdentity, 1)
	errs := make(chan error, 1)
	go func() {
		connection, err := listener.AcceptUnix()
		if err != nil {
			errs <- err
			return
		}
		defer connection.Close()
		identity, err := IdentifyPeer(connection)
		if err != nil {
			errs <- err
			return
		}
		result <- identity
	}()

	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	select {
	case err := <-errs:
		t.Fatalf("identify peer: %v", err)
	case identity := <-result:
		if identity.UID != uint32(os.Getuid()) {
			t.Fatalf("UID = %d, want %d", identity.UID, os.Getuid())
		}
		if identity.PID != os.Getpid() {
			t.Fatalf("PID = %d, want %d", identity.PID, os.Getpid())
		}
	}
}
