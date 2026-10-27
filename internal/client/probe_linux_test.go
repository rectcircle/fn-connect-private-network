//go:build linux && !android && !openharmony

package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxLocalProbeBindsPhysicalInterface(t *testing.T) {
	// No root or network configuration changes: exercise the real socket option.
	iface, err := net.InterfaceByName("lo")
	if err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = bindSocketToInterface(uintptr(fd), iface.Index); err != nil {
		t.Fatal(err)
	}
	got, err := unix.GetsockoptString(fd, unix.SOL_SOCKET, unix.SO_BINDTODEVICE)
	if err != nil {
		t.Fatal(err)
	}
	if got != iface.Name {
		t.Fatalf("socket interface = %q, want %q", got, iface.Name)
	}
	if err = bindSocketToInterface(uintptr(fd), 0); err == nil {
		t.Fatal("missing interface must fail closed")
	}
	if err = bindSocketToInterface(uintptr(fd), 2147483647); err == nil {
		t.Fatal("nonexistent interface must fail closed")
	}
}

func TestLinuxLocalProbeCannotFollowAnotherInterfaceRoute(t *testing.T) {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	physical := 0
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			addresses, _ := iface.Addrs()
			for _, address := range addresses {
				if ip, _, err := net.ParseCIDR(address.String()); err == nil && ip.To4() != nil {
					physical = iface.Index
					break
				}
			}
		}
	}
	if physical == 0 {
		t.Skip("no non-loopback IPv4 interface")
	}
	// A route via loopback stands in for a NAS route via a different (tunnel)
	// interface: an unbound connection succeeds, a physical-only probe must not.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	transport := localProbeTransport(physical)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 300 * time.Millisecond}
	if response, err = client.Get(server.URL); err == nil {
		response.Body.Close()
		t.Fatal("physical-only probe followed another interface's route")
	}
}
