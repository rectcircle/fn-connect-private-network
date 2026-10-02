package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
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
		writeHTTPError(h.logger, writer, request, model.NewError(model.ErrorAuthRequired, "authenticated user is required", false))
		return
	}
	if h.network == nil {
		writeHTTPError(h.logger, writer, request, model.NewError(model.ErrorUnavailable, "server network provider is unavailable", true))
		return
	}
	snapshot := h.network.NetworkSnapshot()
	if !snapshot.Fresh {
		writeHTTPError(h.logger, writer, request, model.WrapError(model.ErrorUnavailable, "server network status is stale", true, snapshot.LastError))
		return
	}
	status := snapshot.Network
	if !status.Active || status.Degraded || status.ListenPort == 0 {
		writeHTTPError(h.logger, writer, request, model.NewError(model.ErrorUnavailable, "server network is inactive, degraded, or has no listen port", true))
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		writeHTTPError(h.logger, writer, request, model.NewError(model.ErrorResourceExhausted, "relay connection limit reached", true))
		return
	}

	udp, err := net.DialUDP("udp", nil, &net.UDPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: int(status.ListenPort),
	})
	if err != nil {
		writeHTTPError(h.logger, writer, request, model.WithOperation(
			model.WrapError(model.ErrorUnavailable, "cannot open relay UDP socket", true, err), "relay.udp_open"))
		return
	}
	defer udp.Close()
	logger := logging.FromContext(request.Context(), h.logger)
	logger.Info("relay WebSocket upgrade requested", "wireguard_port", status.ListenPort)

	// This endpoint only transports WireGuard ciphertext. Gateway authentication
	// identifies the user; WireGuard authenticates devices and protects plaintext.
	// Origin is not a native-client identity and FN Connect rewrites Host. Do not
	// require an Origin match here; retain authentication and resource limits.
	tracked, ok := writer.(*errorResponseWriter)
	if !ok {
		tracked = &errorResponseWriter{ResponseWriter: writer}
	}
	connection, err := websocket.Accept(tracked, request, &websocket.AcceptOptions{
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true, // Disables the library's Origin check, not TLS.
	})
	if err != nil {
		failure := model.WithOperation(model.WrapError(model.ErrorProtocol, "invalid WebSocket upgrade", false, err), "relay.upgrade")
		failure.HTTPStatus = tracked.status
		failure.RequestID = model.SafeRequestID(writer.Header().Get("X-Request-ID"))
		tracked.logged = true
		h.logger.Error("accept relay websocket", logging.ErrorAttrs(failure)...)
		return
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxRelayDatagramSize)
	started := time.Now()
	logger.Info("relay session established", "wireguard_port", status.ListenPort)

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
	if !expectedRelayClose(bridgeError) {
		failure := model.WithOperation(model.NormalizeError(bridgeError, model.ErrorUnavailable, "relay session failed", true), "relay.session")
		failure.RequestID = model.SafeRequestID(writer.Header().Get("X-Request-ID"))
		h.logger.Error("relay session failed", logging.ErrorAttrs(failure)...)
		_ = connection.Close(websocket.StatusInternalError, "relay session failed")
	} else {
		_ = connection.Close(websocket.StatusNormalClosure, "relay closed")
	}
	logger.Info("relay session ended", "normal", expectedRelayClose(bridgeError),
		"elapsed_ms", time.Since(started).Milliseconds())
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

func expectedRelayClose(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure ||
		status == websocket.StatusGoingAway
}
