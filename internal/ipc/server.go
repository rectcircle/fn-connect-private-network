package ipc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	defaultHandlerTimeout = 5 * time.Minute
	responseWriteGrace    = 5 * time.Second
)

type Handler interface {
	Handle(context.Context, Request) Response
}

type HandlerFunc func(context.Context, Request) Response

func (function HandlerFunc) Handle(ctx context.Context, request Request) Response {
	return function(ctx, request)
}

type Server struct {
	SocketPath string
	Mode       os.FileMode
	Authorize  Authorizer
	Handler    Handler
	Logger     *slog.Logger
	Timeout    time.Duration
}

func (s Server) Serve(ctx context.Context) error {
	if s.SocketPath == "" {
		return errors.New("IPC socket path is required")
	}
	if s.Handler == nil {
		return errors.New("IPC handler is required")
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(filepath.Dir(s.SocketPath), 0o700); err != nil {
		return fmt.Errorf("create IPC socket directory: %w", err)
	}
	if err := os.Remove(s.SocketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale IPC socket: %w", err)
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on IPC socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(s.SocketPath)

	mode := s.Mode
	if mode == 0 {
		mode = 0o600
	}
	if err := os.Chmod(s.SocketPath, mode); err != nil {
		return fmt.Errorf("set IPC socket mode: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			logger.Error("accept IPC connection", "error", err)
			continue
		}
		go s.handleConnection(ctx, connection, logger)
	}
}

func (s Server) handleConnection(
	parent context.Context,
	connection *net.UnixConn,
	logger *slog.Logger,
) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(defaultHandlerTimeout))

	if s.Authorize != nil {
		identity, err := IdentifyPeer(connection)
		if err != nil {
			s.writeFailure(connection, "", err, logger)
			return
		}
		if err := s.Authorize(parent, identity); err != nil {
			s.writeFailure(connection, "", err, logger)
			return
		}
		parent = WithPeerIdentity(parent, identity)
	}

	request, err := ReadRequest(connection)
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
	response := s.Handler.Handle(ctx, request)
	if response.Version == 0 {
		response.Version = model.ProtocolVersion
	}
	if response.ID == "" {
		response.ID = request.ID
	}
	if err := WriteResponse(connection, response); err != nil {
		logger.Error(
			"write IPC response",
			"method",
			request.Method,
			"request_id",
			request.ID,
			"error",
			err,
		)
	}
}

func (s Server) writeFailure(
	connection *net.UnixConn,
	id string,
	err error,
	logger *slog.Logger,
) {
	if writeErr := WriteResponse(connection, Failure(id, err)); writeErr != nil {
		logger.Error("write failed IPC response", "error", writeErr)
	}
}
