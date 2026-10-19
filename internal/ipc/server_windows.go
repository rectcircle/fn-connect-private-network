//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

const (
	defaultHandlerTimeout = 5 * time.Minute
	responseWriteGrace    = 5 * time.Second
	// Upstream has fixed one listener-close race in go-winio v0.6.3, but its
	// related pending ConnectNamedPipe issue remains open. Keep service cleanup
	// bounded in case listener Close cannot complete promptly.
	listenerCloseTimeout = 2 * time.Second
)

type Handler interface {
	Handle(context.Context, Request) Response
}

type HandlerFunc func(context.Context, Request) Response

func (function HandlerFunc) Handle(ctx context.Context, request Request) Response {
	return function(ctx, request)
}

type Server struct {
	// PrepareMaintenanceShutdown validates shutdown and returns a callback run after acknowledgment.
	PrepareMaintenanceShutdown func() (func(), error)
	SocketPath                 string
	Mode                       os.FileMode
	PipeSecurityDescriptor     string
	PeerSID                    string
	Authorize                  Authorizer
	Handler                    Handler
	Logger                     *slog.Logger
	Timeout                    time.Duration
	OnReady                    func()
	listenPipe                 func(string, *winio.PipeConfig) (net.Listener, error)
}

func (s Server) Serve(ctx context.Context) error {
	if !strings.HasPrefix(strings.ToLower(s.SocketPath), `\\.\pipe\`) {
		return errors.New("Windows IPC endpoint must be a local named pipe")
	}
	if s.Handler == nil {
		return errors.New("IPC handler is required")
	}
	if s.PipeSecurityDescriptor == "" {
		descriptor, err := CurrentUserPipeSecurityDescriptor()
		if err != nil {
			return fmt.Errorf("create user SID pipe ACL: %w", err)
		}
		s.PipeSecurityDescriptor = descriptor
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	listenPipe := s.listenPipe
	if listenPipe == nil {
		listenPipe = winio.ListenPipe
	}
	listener, err := listenPipe(s.SocketPath, &winio.PipeConfig{
		SecurityDescriptor: s.PipeSecurityDescriptor,
	})
	if err != nil {
		return fmt.Errorf("listen on Windows named pipe: %w", err)
	}
	logger.Info("IPC named pipe ready", "pipe", s.SocketPath)
	if s.OnReady != nil {
		s.OnReady()
	}
	defer logger.Info("IPC named pipe stopped", "pipe", s.SocketPath)
	type acceptResult struct {
		connection net.Conn
		err        error
	}
	accepted := make(chan acceptResult)
	go func() {
		for {
			if ctx.Err() != nil {
				return
			}
			connection, err := listener.Accept()
			select {
			case accepted <- acceptResult{connection: connection, err: err}:
			case <-ctx.Done():
				if connection != nil {
					_ = connection.Close()
				}
				return
			}
			if err != nil && (ctx.Err() != nil || errors.Is(err, net.ErrClosed)) {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			closeDone := make(chan error, 1)
			go func() { closeDone <- listener.Close() }()
			timer := time.NewTimer(listenerCloseTimeout)
			select {
			case err := <-closeDone:
				if err != nil && !errors.Is(err, net.ErrClosed) {
					logger.Error("close IPC named pipe listener", "pipe", s.SocketPath, "error", err)
				}
			case <-timer.C:
				logger.Error("timed out closing IPC named pipe listener; process exit will release the pipe", "pipe", s.SocketPath, "timeout", listenerCloseTimeout)
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case result := <-accepted:
			if result.err != nil {
				if ctx.Err() != nil || errors.Is(result.err, net.ErrClosed) {
					return nil
				}
				logger.Error("accept IPC named pipe", "error", result.err)
				continue
			}
			if ctx.Err() != nil {
				_ = result.connection.Close()
				continue
			}
			identity, err := IdentifyPeer(result.connection)
			if err != nil {
				_ = result.connection.Close()
				logger.Error("identify IPC named pipe peer", "error", err)
				continue
			}
			if !pipeACLAllowsSID(s.PipeSecurityDescriptor, identity.SID, identity.Authenticated) ||
				(s.PeerSID != "" && identity.SID != s.PeerSID) {
				_ = result.connection.Close()
				logger.Error("reject IPC named pipe peer outside configured ACL identity", "peer_sid", identity.SID)
				continue
			}
			go s.handleConnection(ctx, result.connection, identity, logger)
		}
	}
}

func (s Server) handleConnection(parent context.Context, connection net.Conn, identity PeerIdentity, logger *slog.Logger) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(defaultHandlerTimeout))
	if s.Authorize != nil {
		if err := s.Authorize(parent, identity); err != nil {
			s.writeFailure(connection, "", err, logger)
			return
		}
		parent = WithPeerIdentity(parent, identity)
	}
	payload, err := ReadFrame(connection)
	if err != nil {
		s.writeFailure(connection, "", normalizeRequestFailure(err), logger)
		return
	}
	if s.handleMaintenance(connection, payload) {
		return
	}
	request, err := decodeRequest(payload)
	if err != nil {
		s.writeFailure(connection, "", err, logger)
		return
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultHandlerTimeout
	}
	_ = connection.SetDeadline(time.Now().Add(timeout + responseWriteGrace))
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	go func() {
		var extra [1]byte
		_, _ = connection.Read(extra[:])
		cancel()
	}()
	ctx = logging.WithLogger(ctx, logger.With(
		"ipc_request_id", model.SafeRequestID(request.ID), "ipc_method", request.Method,
	))
	response := s.Handler.Handle(ctx, request)
	if response.ProductVersion == "" {
		response.ProductVersion = version.Current
	}
	if response.Version == 0 {
		response.Version = ProtocolVersion
	}
	if response.ID == "" {
		response.ID = request.ID
	}
	if !response.OK && response.Error != nil {
		response.Error = model.WithOperation(response.Error, "ipc."+request.Method)
		attrs := append(logging.ErrorAttrs(response.Error), "method", request.Method, "ipc_request_id", request.ID)
		if response.Error.Code == model.ErrorCanceled {
			logger.Debug("IPC request canceled", attrs...)
		} else {
			logger.Error("IPC request failed", attrs...)
		}
	}
	if err := WriteResponse(connection, response); err != nil {
		if request.Method == "watch-client-status" || request.Method == "watch-lifecycle" {
			logger.Debug("IPC watcher disconnected", "method", request.Method, "request_id", request.ID)
			return
		}
		logger.Error("write IPC response", "method", request.Method, "request_id", request.ID, "error", err)
	}
}

func (s Server) writeFailure(connection net.Conn, id string, err error, logger *slog.Logger) {
	logger.Error("reject IPC request", logging.ErrorAttrs(model.WithOperation(err, "ipc.read_or_authorize"))...)
	if writeErr := WriteResponse(connection, Failure(id, err)); writeErr != nil {
		logger.Error("write failed IPC response", "error", writeErr)
	}
}
