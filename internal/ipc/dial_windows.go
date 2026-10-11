//go:build windows

package ipc

import (
	"context"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

func dialIPC(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := ipcDialContext(ctx, timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, path)
}

func ipcDialContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return parent, func() {}
	}
	// WithTimeout keeps an earlier parent deadline but also caps a longer one.
	// Named-pipe establishment must not consume the whole method/watch budget.
	return context.WithTimeout(parent, timeout)
}
