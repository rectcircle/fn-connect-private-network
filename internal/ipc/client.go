package ipc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func (c Client) Call(ctx context.Context, method string, params, result any) error {
	id, err := requestID()
	if err != nil {
		return fmt.Errorf("generate request ID: %w", err)
	}
	request, err := NewRequest(id, method, params)
	if err != nil {
		return fmt.Errorf("create IPC request: %w", err)
	}

	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	dialer := net.Dialer{Timeout: min(timeout, 5*time.Second)}
	connection, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return model.NormalizeError(
			fmt.Errorf("connect to daemon: %w", err),
			model.ErrorUnavailable,
			"daemon is unavailable",
			true,
		)
	}
	defer connection.Close()
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = connection.SetDeadline(time.Now())
	})
	defer stopCancellation()

	if err := WriteRequest(connection, request); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return model.NormalizeError(
			fmt.Errorf("write IPC request: %w", err),
			model.ErrorUnavailable,
			"daemon is unavailable",
			true,
		)
	}
	response, err := ReadResponse(connection)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return model.NormalizeError(
			err,
			model.ErrorUnavailable,
			"daemon returned an invalid response",
			true,
		)
	}
	if response.ID != request.ID && (response.ID != "" || response.OK) {
		return errors.New("IPC response ID mismatch")
	}
	if !response.OK {
		return response.Error
	}
	if result == nil || len(response.Result) == 0 {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return model.NormalizeError(
			fmt.Errorf("decode IPC result: %w", err),
			model.ErrorInternal,
			"daemon returned an invalid result",
			false,
		)
	}
	return nil
}

func requestID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
