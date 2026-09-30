package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestRelayBridgeCarriesWireGuardDatagrams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		cookie, err := request.Cookie("fnos-token")
		if err != nil || cookie.Value != "token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for {
			messageType, payload, err := connection.Read(request.Context())
			if err != nil {
				return
			}
			if err := connection.Write(request.Context(), messageType, payload); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	bridge := &RelayBridge{ListenAddress: "127.0.0.1:0"}
	endpoint, err := bridge.Start(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http"),
		func() ([]Cookie, error) {
			return []Cookie{{Name: "fnos-token", Value: "token"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("start bridge: %v", err)
	}
	defer bridge.Stop()

	connection, err := net.Dial("udp", endpoint)
	if err != nil {
		t.Fatalf("dial bridge: %v", err)
	}
	defer connection.Close()
	if err := bridge.BindPeer(uint16(connection.LocalAddr().(*net.UDPAddr).Port)); err != nil {
		t.Fatalf("bind peer: %v", err)
	}
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	payload := []byte("wireguard packet")
	if _, err := connection.Write(payload); err != nil {
		t.Fatalf("write packet: %v", err)
	}
	response := make([]byte, 128)
	count, err := connection.Read(response)
	if err != nil {
		t.Fatalf("read packet: %v", err)
	}
	if string(response[:count]) != string(payload) {
		t.Fatalf("response = %q", response[:count])
	}
}

func TestRelayBridgeRestartsDeadSessionWithFreshCookies(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		requests.Add(1)
		cookie, err := request.Cookie("fnos-token")
		if err != nil || cookie.Value != "fresh-token" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(request.Context())
	}))
	defer server.Close()

	staleUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen stale UDP: %v", err)
	}
	_, staleCancel := context.WithCancel(context.Background())
	staleCancel()
	staleDone := make(chan struct{})
	close(staleDone)
	relayURL := "ws" + strings.TrimPrefix(server.URL, "http")
	bridge := &RelayBridge{
		cancel:   staleCancel,
		udp:      staleUDP,
		done:     staleDone,
		relayURL: relayURL,
		events:   make(chan error, 1),
	}
	bridge.connected.Store(false)

	endpoint, err := bridge.Start(
		context.Background(),
		relayURL,
		func() ([]Cookie, error) {
			return []Cookie{{Name: "fnos-token", Value: "fresh-token"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("restart bridge: %v", err)
	}
	defer bridge.Stop()
	if endpoint == "" || requests.Load() != 1 || !bridge.Connected() {
		t.Fatalf(
			"bridge was not restarted: endpoint=%q requests=%d connected=%t",
			endpoint,
			requests.Load(),
			bridge.Connected(),
		)
	}
}
