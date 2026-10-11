//go:build !windows

package ipc

import (
	"context"
	"net"
	"time"
)

func dialIPC(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", path)
}
