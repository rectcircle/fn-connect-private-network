package server

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestRelayRequiresGatewayIdentity(t *testing.T) {
	handler := newRelayHandler(staticNetworkStatus{}, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/relay/v1/wireguard", nil),
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestRelayAllowsOriginsAfterGatewayHostRewrite(t *testing.T) {
	handler := newRelayHandler(staticNetworkStatus{
		status: model.ServerPrivilegedStatus{Active: true, ListenPort: 54789},
	}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Host = "gateway-internal"
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	for _, origin := range []string{"", "https://nas.fnos.net", "https://other.example", "null"} {
		headers := http.Header{"X-Trim-Userid": {"user"}}
		if origin != "" {
			headers.Set("Origin", origin)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		connection, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"),
			&websocket.DialOptions{HTTPHeader: headers})
		if err != nil {
			cancel()
			t.Fatalf("origin %q rejected: %v (%v)", origin, err, response)
		}
		_ = connection.Close(websocket.StatusNormalClosure, "test complete")
		cancel()
	}
}

func TestRelayLimitsGlobalConnections(t *testing.T) {
	handler := newRelayHandler(staticNetworkStatus{
		status: model.ServerPrivilegedStatus{Active: true, ListenPort: 54789},
	}, nil)
	if capacity := cap(handler.slots); capacity != 256 {
		t.Fatalf("relay capacity = %d", capacity)
	}
	for index := 0; index < maxRelayConnections; index++ {
		handler.slots <- struct{}{}
	}
	defer func() {
		for index := 0; index < maxRelayConnections; index++ {
			<-handler.slots
		}
	}()
	request := httptest.NewRequest(http.MethodGet, "/relay/v1/wireguard", nil)
	request.Header.Set("X-Trim-Userid", "user")
	request.Header.Set("Origin", "https://example.com")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("relay over capacity status = %d", response.Code)
	}
}

func TestRelayBridgesBinaryDatagrams(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer udp.Close()
	go func() {
		buffer := make([]byte, maxRelayDatagramSize)
		for {
			size, address, err := udp.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			_, _ = udp.WriteToUDP(buffer[:size], address)
		}
	}()

	port := uint16(udp.LocalAddr().(*net.UDPAddr).Port)
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := newRelayHandler(staticNetworkStatus{status: model.ServerPrivilegedStatus{
		Active:     true,
		ListenPort: port,
	}}, logger)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(done)
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	headers := http.Header{}
	headers.Set("X-Trim-Userid", "user")
	headers.Set("Origin", server.URL)
	headers.Set("Cookie", "session=server-relay-cookie-canary")
	connection, _, err := websocket.Dial(
		context.Background(),
		"ws"+strings.TrimPrefix(server.URL, "http"),
		&websocket.DialOptions{HTTPHeader: headers},
	)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer connection.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	payload := []byte("wireguard datagram")
	if err := connection.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("write relay: %v", err)
	}
	messageType, response, err := connection.Read(ctx)
	if err != nil {
		t.Fatalf("read relay: %v", err)
	}
	if messageType != websocket.MessageBinary || string(response) != string(payload) {
		t.Fatalf("relay response type=%v payload=%q", messageType, response)
	}
	_ = connection.Close(websocket.StatusNormalClosure, "test complete")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("relay handler did not finish")
	}
	for _, message := range []string{"relay WebSocket upgrade requested", "relay session established", "relay session ended"} {
		if !strings.Contains(output.String(), `"msg":"`+message+`"`) {
			t.Fatalf("missing server relay milestone %q: %s", message, output.String())
		}
	}
	if !strings.Contains(output.String(), `"normal":true`) ||
		strings.Contains(output.String(), "server-relay-cookie-canary") || strings.Contains(output.String(), string(payload)) {
		t.Fatal("relay closure was misclassified or log exposed a cookie/packet")
	}
}
