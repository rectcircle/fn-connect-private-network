//go:build !windows && !linux

package command

import (
	"context"
	"errors"
)

func ensureClientDaemon(context.Context, string) error { return nil }

func runStopAllClientDaemons() error {
	return errors.New("internal-stop-daemons is only available on Windows")
}
