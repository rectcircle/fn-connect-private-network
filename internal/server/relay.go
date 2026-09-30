package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	maxRelayDatagramSize = 65535
	maxRelayConnections  = 256
)

type relayHandler struct {
	network interface {
		NetworkSnapshot() model.ServerNetworkSnapshot
	}
	logger *slog.Logger
	slots  chan struct{}
}

func newRelayHandler(
	network interface {
		NetworkSnapshot() model.ServerNetworkSnapshot
	},
	logger *slog.Logger,
) *relayHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &relayHandler{
		network: network,
		logger:  logger,
		slots:   make(chan struct{}, maxRelayConnections),
	}
}

func (h *relayHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	userID := request.Header.Get("X-Trim-Userid")
	if userID == "" {
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.network == nil {
		http.Error(writer, "server network unavailable", http.StatusServiceUnavailable)
		return
	}
	snapshot := h.network.NetworkSnapshot()
	if !snapshot.Fresh {
		http.Error(writer, "server network unavailable", http.StatusServiceUnavailable)
		return
	}
	status := snapshot.Network
	if !status.Active || status.Degraded || status.ListenPort == 0 {
		http.Error(writer, "server network unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := validateRelayOrigin(request); err != nil {
		http.Error(writer, "forbidden origin", http.StatusForbidden)
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(writer, "relay is at capacity", http.StatusServiceUnavailable)
		return
	}

	udp, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: int(status.ListenPort),
	})
	if err != nil {
		h.logger.Error("open relay UDP socket", "error", err)
		http.Error(writer, "relay unavailable", http.StatusServiceUnavailable)
		return
	}
	defer udp.Close()

	connection, err := websocket.Accept(writer, request, relayAcceptOptions(request))
	if err != nil {
		h.logger.Error("accept relay websocket", "error", err)
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxRelayDatagramSize)

	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	errorsChannel := make(chan error, 2)
	go func() {
		errorsChannel <- relayWebSocketToUDP(
			ctx,
			connection,
			udp,
		)
	}()
	go func() {
		errorsChannel <- relayUDPToWebSocket(ctx, connection, udp)
	}()
	bridgeError := <-errorsChannel
	cancel()
	_ = udp.Close()
	_ = connection.Close(websocket.StatusNormalClosure, "relay closed")
	if !expectedRelayClose(bridgeError) {
		h.logger.Error("relay session failed", "user_id", userID, "error", bridgeError)
	}
}

func relayWebSocketToUDP(
	ctx context.Context,
	connection *websocket.Conn,
	udp *net.UDPConn,
) error {
	for {
		messageType, payload, err := connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("read relay websocket: %w", err)
		}
		if messageType != websocket.MessageBinary {
			return errors.New("relay accepts binary datagrams only")
		}
		if len(payload) == 0 || len(payload) > maxRelayDatagramSize {
			return fmt.Errorf("invalid relay datagram size %d", len(payload))
		}
		if _, err := udp.Write(payload); err != nil {
			return fmt.Errorf("write relay UDP datagram: %w", err)
		}
	}
}

func relayUDPToWebSocket(
	ctx context.Context,
	connection *websocket.Conn,
	udp *net.UDPConn,
) error {
	buffer := make([]byte, maxRelayDatagramSize)
	for {
		if err := udp.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		size, err := udp.Read(buffer)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				continue
			}
			return fmt.Errorf("read relay UDP datagram: %w", err)
		}
		writeContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = connection.Write(
			writeContext,
			websocket.MessageBinary,
			buffer[:size],
		)
		cancel()
		if err != nil {
			return fmt.Errorf("write relay websocket: %w", err)
		}
	}
}

func relayAcceptOptions(request *http.Request) *websocket.AcceptOptions {
	return &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
		OriginPatterns:  []string{request.Host},
	}
}

func validateRelayOrigin(request *http.Request) error {
	origin, err := url.Parse(request.Header.Get("Origin"))
	if err != nil ||
		(origin.Scheme != "http" && origin.Scheme != "https") ||
		origin.Host == "" ||
		!strings.EqualFold(origin.Host, request.Host) {
		return errors.New("relay origin does not match request host")
	}
	return nil
}

func expectedRelayClose(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure ||
		status == websocket.StatusGoingAway
}
