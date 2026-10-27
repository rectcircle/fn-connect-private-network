//go:build !windows

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
)

type probeClientTransport struct{ handler http.Handler }

func (p probeClientTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	p.handler.ServeHTTP(response, r)
	return response.Result(), nil
}

// Also run against historical probe.go implementations using go test -overlay.
func TestPrivateProbeClientInteroperability(t *testing.T) {
	store, err := OpenService(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := createTestDevice(store, "health-check", testPublicKey(7))
	if err != nil {
		t.Fatal(err)
	}
	service := &PrivateAPIService{store: store, masterKey: bytes.Repeat([]byte{3}, probeKeySize), endpoints: []string{"192.168.71.2:54790"}}
	cfg, err := service.Configuration(device.ID)
	if err != nil {
		t.Fatal(err)
	}
	probe := client.SystemLocalProbe{HTTPClient: &http.Client{Transport: probeClientTransport{service}}}
	ok, err := probe.Reachable(context.Background(), cfg, device.ID, client.NetworkSnapshot{})
	if err != nil || !ok {
		t.Fatalf("authenticated probe: ok=%v err=%v", ok, err)
	}
}
