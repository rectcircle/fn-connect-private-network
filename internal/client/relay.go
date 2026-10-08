package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	defaultRelayListenAddress = "127.0.0.1:0"
	maxRelayDatagramSize      = 65535
	maxRelayQueue             = 256
	// maxRelayBackoff caps the exponential reconnect backoff so a NAS that stays
	// unreachable never tightens its reconnect cadence back below this floor.
	maxRelayBackoff = 30 * time.Second
	// healthyRelayDuration is the minimum a relay session must survive before the
	// reconnect backoff is reset to its initial value. Only a session that lasts
	// this long indicates a genuinely healthy link; short-lived sessions (NAS
	// powered off, gateway rate-limiting) keep the backoff climbing instead of
	// resetting it to 1s and flooding the gateway with 429-inducing requests.
	healthyRelayDuration = 15 * time.Second
)

type CookieProvider func() ([]Cookie, error)

type relayPacket struct {
	payload    []byte
	receivedAt time.Time
}

type RelayBridge struct {
	ListenAddress string
	Logger        *slog.Logger
	mu            sync.Mutex
	cancel        context.CancelFunc
	udp           *net.UDPConn
	done          chan struct{}
	relayURL      string
	cookies       CookieProvider
	expectedPeer  atomic.Uint32
	events        chan error
	connected     atomic.Bool
}

func (b *RelayBridge) Start(
	requestContext context.Context,
	relayURL string,
	cookies CookieProvider,
	onCookies ...func([]Cookie, []Cookie) error,
) (string, error) {
	parsed, err := url.Parse(relayURL)
	if err != nil ||
		(parsed.Scheme != "ws" && parsed.Scheme != "wss") ||
		parsed.Host == "" {
		return "", errors.New("invalid relay URL")
	}
	if cookies == nil {
		return "", errors.New("relay cookie provider is required")
	}
	b.mu.Lock()
	if b.cancel != nil &&
		b.relayURL == relayURL &&
		b.connected.Load() {
		endpoint := b.udp.LocalAddr().String()
		b.mu.Unlock()
		return endpoint, nil
	}
	b.stopLocked()
	b.mu.Unlock()

	logger := logging.FromContext(requestContext, b.logger()).With("relay_url", model.SafeURL(relayURL))
	logger.Info("relay WebSocket connecting")
	connection, err := dialRelay(requestContext, parsed, cookies, onCookies...)
	if err != nil {
		return "", fmt.Errorf("connect relay: %w", err)
	}
	logger.Info("relay WebSocket established")
	listenAddress := valueOrDefault(b.ListenAddress, defaultRelayListenAddress)
	udpAddress, err := net.ResolveUDPAddr("udp", listenAddress)
	if err != nil {
		connection.CloseNow()
		return "", fmt.Errorf("resolve local relay address: %w", err)
	}
	udp, err := net.ListenUDP("udp", udpAddress)
	if err != nil {
		connection.CloseNow()
		return "", fmt.Errorf("listen for local WireGuard: %w", err)
	}

	b.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.cancel = cancel
	b.udp = udp
	b.done = done
	b.relayURL = relayURL
	b.cookies = cookies
	b.expectedPeer.Store(0)
	if b.events == nil {
		b.events = make(chan error, 1)
	}
	events := b.events
	b.connected.Store(true)
	b.mu.Unlock()

	logger.Info("relay bridge ready", "udp_endpoint", udp.LocalAddr().String())
	go b.run(ctx, done, udp, connection, parsed, cookies, events, onCookies...)
	return udp.LocalAddr().String(), nil
}

func (b *RelayBridge) BindPeer(port uint16) error {
	if port == 0 {
		return errors.New("WireGuard listen port is required")
	}
	b.expectedPeer.Store(uint32(port))
	return nil
}

func (b *RelayBridge) Events() <-chan error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.events == nil {
		b.events = make(chan error, 1)
	}
	return b.events
}

func (b *RelayBridge) Stop() {
	b.mu.Lock()
	b.stopLocked()
	b.mu.Unlock()
}

func (b *RelayBridge) Connected() bool {
	return b.connected.Load()
}

func (b *RelayBridge) stopLocked() {
	if b.cancel == nil {
		return
	}
	cancel := b.cancel
	udp := b.udp
	done := b.done
	relayURL := b.relayURL
	b.cancel = nil
	b.udp = nil
	b.done = nil
	b.relayURL = ""
	b.cookies = nil
	b.expectedPeer.Store(0)
	b.connected.Store(false)
	cancel()
	_ = udp.Close()
	<-done
	b.logger().Info("relay bridge stopped", "relay_url", model.SafeURL(relayURL))
}

func (b *RelayBridge) run(
	ctx context.Context,
	done chan struct{},
	udp *net.UDPConn,
	initial *websocket.Conn,
	relayURL *url.URL,
	cookies CookieProvider,
	events chan error,
	onCookies ...func([]Cookie, []Cookie) error,
) {
	defer close(done)
	defer b.connected.Store(false)
	logger := b.logger().With("relay_url", model.SafeURL(relayURL.String()))
	packets := make(chan relayPacket, maxRelayQueue)
	go readWireGuardDatagrams(ctx, udp, packets, &b.expectedPeer, func(err error) {
		if ctx.Err() == nil {
			failure := model.WithOperation(model.WrapError(model.ErrorUnavailable, "local WireGuard receive failed", false, err), "relay.udp_read")
			b.logFailure(failure)
			publishRelayEvent(events, failure)
		}
	})

	connection := initial
	backoff := time.Second
	for ctx.Err() == nil {
		b.connected.Store(true)
		sessionStart := time.Now()
		sessionErr := runRelaySession(ctx, connection, udp, packets, &b.expectedPeer)
		connection.CloseNow()
		b.connected.Store(false)
		if ctx.Err() != nil {
			return
		}
		// Only a session that survived long enough counts as healthy and resets
		// the backoff. A session that dies almost immediately — a NAS that is off
		// or a gateway that is rate-limiting still completes the WSS handshake
		// but drops the session right away — would otherwise reset backoff to 1s
		// on every connect, producing a 1s-per-request reconnect storm that trips
		// the gateway's 429 rate limit.
		if time.Since(sessionStart) >= healthyRelayDuration {
			backoff = time.Second
		}
		failure := model.WithOperation(model.NormalizeError(sessionErr, model.ErrorUnavailable, "relay session disconnected", true), "relay.session")
		b.logFailure(failure)
		publishRelayEvent(events, failure)
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff + retryJitter(backoff)):
			}
			logger.Info("relay WebSocket reconnecting", "backoff_ms", backoff.Milliseconds())
			dialContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			next, dialErr := dialRelay(dialContext, relayURL, cookies, onCookies...)
			cancel()
			if dialErr != nil {
				b.logFailure(dialErr)
				publishRelayEvent(events, dialErr)
				if !model.AsError(dialErr).Retryable {
					return
				}
				if backoff < maxRelayBackoff {
					backoff *= 2
					if backoff > maxRelayBackoff {
						backoff = maxRelayBackoff
					}
				}
				continue
			}
			connection = next
			logger.Info("relay WebSocket reconnected")
			publishRelayEvent(events, nil)
			break
		}
	}
}

func (b *RelayBridge) logFailure(err error) {
	b.logger().Error("relay connection failed", logging.ErrorAttrs(err)...)
}

func (b *RelayBridge) logger() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func readWireGuardDatagrams(
	ctx context.Context,
	udp *net.UDPConn,
	packets chan<- relayPacket,
	expectedPeer *atomic.Uint32,
	onError ...func(error),
) {
	report := func(err error) {
		for _, callback := range onError {
			callback(err)
		}
	}
	buffer := make([]byte, maxRelayDatagramSize)
	for {
		if err := udp.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			report(err)
			return
		}
		size, address, err := udp.ReadFromUDP(buffer)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			report(err)
			return
		}
		if !address.IP.IsLoopback() ||
			uint32(address.Port) != expectedPeer.Load() {
			continue
		}
		packet := relayPacket{
			payload:    append([]byte(nil), buffer[:size]...),
			receivedAt: time.Now(),
		}
		select {
		case packets <- packet:
		default:
		}
	}
}

func runRelaySession(
	parent context.Context,
	connection *websocket.Conn,
	udp *net.UDPConn,
	packets <-chan relayPacket,
	expectedPeer *atomic.Uint32,
) error {
	connection.SetReadLimit(maxRelayDatagramSize)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	errs := make(chan error, 2)
	go func() {
		for {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case packet := <-packets:
				if time.Since(packet.receivedAt) > 5*time.Second {
					continue
				}
				writeContext, writeCancel := context.WithTimeout(ctx, 10*time.Second)
				err := connection.Write(
					writeContext,
					websocket.MessageBinary,
					packet.payload,
				)
				writeCancel()
				if err != nil {
					errs <- fmt.Errorf("write relay websocket: %w", err)
					return
				}
			}
		}
	}()
	go func() {
		for {
			messageType, payload, err := connection.Read(ctx)
			if err != nil {
				errs <- fmt.Errorf("read relay websocket: %w", err)
				return
			}
			if messageType != websocket.MessageBinary {
				errs <- errors.New("relay returned a non-binary message")
				return
			}
			port := expectedPeer.Load()
			if port == 0 {
				continue
			}
			destination := &net.UDPAddr{
				IP:   net.IPv4(127, 0, 0, 1),
				Port: int(port),
			}
			if _, err := udp.WriteToUDP(payload, destination); err != nil {
				errs <- fmt.Errorf("write WireGuard UDP datagram: %w", err)
				return
			}
		}
	}()
	err := <-errs
	cancel()
	connection.CloseNow()
	return err
}

func dialRelay(
	ctx context.Context,
	target *url.URL,
	cookies CookieProvider,
	onCookies ...func([]Cookie, []Cookie) error,
) (*websocket.Conn, error) {
	records, err := cookies()
	if err != nil {
		return nil, fmt.Errorf("load relay cookies: %w", err)
	}
	connection, err := dialRelayOnce(ctx, target, records, onCookies...)
	if err == nil || model.AsError(err).Code != model.ErrorAuthRequired {
		return connection, err
	}
	current, loadErr := cookies()
	if loadErr != nil {
		return nil, errors.Join(err, loadErr)
	}
	if len(current) > 0 && !slices.Equal(records, current) {
		return dialRelayOnce(ctx, target, current, onCookies...)
	}
	return nil, err
}

func dialRelayOnce(
	ctx context.Context,
	target *url.URL,
	records []Cookie,
	onCookies ...func([]Cookie, []Cookie) error,
) (*websocket.Conn, error) {
	headers := make(http.Header)
	originScheme := "http"
	if target.Scheme == "wss" {
		originScheme = "https"
	}
	headers.Set("Origin", originScheme+"://"+target.Host)
	if header := cookieHeaderForURL(records, target); header != "" {
		headers.Set("Cookie", header)
	}
	connection, response, err := websocket.Dial(
		ctx,
		target.String(),
		&websocket.DialOptions{
			HTTPHeader:      headers,
			CompressionMode: websocket.CompressionDisabled,
			HTTPClient: &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
		},
	)
	var cookieErr error
	if response != nil && len(response.Cookies()) > 0 {
		set, setErr := newCookieSet(records)
		cookieErr = setErr
		if setErr == nil && set.ApplyResponse(response) {
			for _, save := range onCookies {
				if save != nil {
					cookieErr = errors.Join(cookieErr, save(records, set.Records()))
				}
			}
		}
	}
	if cookieErr != nil {
		if connection != nil {
			connection.CloseNow()
		}
		failure := model.WithOperation(cookieErr, "credentials.save_relay_response")
		failure.HTTPStatus = response.StatusCode
		return nil, failure
	}
	if err != nil {
		operation := "relay.handshake " + model.SafeURL(target.String())
		if response != nil {
			return nil, HTTPResponseError(response, operation, err)
		}
		return nil, model.WithOperation(model.NormalizeError(
			err,
			model.ErrorUnavailable,
			"FN Connect relay is unavailable",
			true,
		), operation)
	}
	return connection, nil
}

func retryJitter(backoff time.Duration) time.Duration {
	window := backoff / 4
	if window <= 0 {
		return 0
	}
	return time.Duration(time.Now().UnixNano() % int64(window))
}

func publishRelayEvent(events chan error, err error) {
	if events == nil {
		return
	}
	select {
	case events <- err:
	default:
		// Report the latest state, especially a terminal rejection after a
		// transient disconnect, instead of losing it behind a queued event.
		select {
		case <-events:
		default:
		}
		select {
		case events <- err:
		default:
		}
	}
}

func RelayURL(fnID string) (string, error) {
	normalized, err := normalizeFNID(fnID)
	if err != nil {
		return "", err
	}
	return "wss://" + normalized + ".fnos.net" +
		gatewayApplicationPath + "/relay/v1/wireguard", nil
}

func cookieHeaderForURL(cookies []Cookie, target *url.URL) string {
	normalized := append([]Cookie(nil), cookies...)
	for index := range normalized {
		if normalized[index].Domain == "" {
			normalized[index].Domain = target.Hostname()
			normalized[index].HostOnly = true
		}
	}
	set, err := newCookieSet(normalized)
	if err != nil {
		return ""
	}
	return set.Header(target)
}
