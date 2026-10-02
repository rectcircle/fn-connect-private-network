package client

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestRelayReconnectLogsAndReportsPermanentRejection(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) > 1 {
			http.Error(w, "relay policy rejected", http.StatusForbidden)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close(websocket.StatusGoingAway, "reconnect")
	}))
	defer server.Close()
	var output diagnosticBuffer
	bridge := &RelayBridge{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	defer bridge.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := bridge.Start(ctx, "ws"+strings.TrimPrefix(server.URL, "http"),
		func() ([]Cookie, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("reconnect rejection was not reported")
		case err := <-bridge.Events():
			if err == nil || model.AsError(err).Retryable {
				continue
			}
			failure := model.PublicError(err)
			if failure.Code != model.ErrorPermissionDenied || failure.HTTPStatus != 403 ||
				!strings.Contains(failure.Detail, "relay policy rejected") {
				t.Fatalf("reconnect error = %+v", failure)
			}
			if !strings.Contains(output.String(), "relay policy rejected") {
				t.Fatal("reconnect error was not logged")
			}
			return
		}
	}
}

func TestRelayReconnectSuccessIsLogged(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		if attempts.Add(1) == 1 {
			_ = connection.Close(websocket.StatusGoingAway, "reconnect fixture")
			return
		}
		_, _, _ = connection.Read(r.Context())
	}))
	defer server.Close()
	var output diagnosticBuffer
	bridge := &RelayBridge{Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	defer bridge.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := bridge.Start(ctx, "ws"+strings.TrimPrefix(server.URL, "http"),
		func() ([]Cookie, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("relay did not reconnect")
		case err := <-bridge.Events():
			if err != nil {
				continue
			}
			for _, message := range []string{"relay WebSocket reconnecting", "relay WebSocket reconnected"} {
				if !strings.Contains(output.String(), `"msg":"`+message+`"`) {
					t.Fatalf("missing reconnect milestone %q: %s", message, output.String())
				}
			}
			if attempts.Load() != 2 {
				t.Fatalf("unexpected reconnect count: %d", attempts.Load())
			}
			return
		}
	}
}

func TestRelayBridgeCarriesWireGuardDatagrams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		cookie, err := request.Cookie("fnos-token")
		if err != nil || cookie.Value != "relay-cookie-canary" {
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

	var output diagnosticBuffer
	bridge := &RelayBridge{ListenAddress: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(&output, nil))}
	endpoint, err := bridge.Start(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http")+"/?token=relay-query-canary",
		func() ([]Cookie, error) {
			return []Cookie{{Name: "fnos-token", Value: "relay-cookie-canary"}}, nil
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
	bridge.Stop()
	for _, message := range []string{
		"relay WebSocket connecting", "relay WebSocket established", "relay bridge ready", "relay bridge stopped",
	} {
		if !strings.Contains(output.String(), `"msg":"`+message+`"`) {
			t.Fatalf("missing relay milestone %q: %s", message, output.String())
		}
	}
	for _, secret := range []string{"relay-cookie-canary", "relay-query-canary", string(payload)} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("relay logs contain a cookie, URL query or packet payload")
		}
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
