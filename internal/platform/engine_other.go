//go:build !darwin && !linux

package platform

import (
	"log/slog"

	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func NewClientEngine(_ *slog.Logger, _ string) privileged.ClientEngine {
	return privileged.NewUnavailableClientEngine(
		"platform network engine is unsupported",
	)
}

func NewServerEngine(_ *slog.Logger, _ string) privileged.ServerEngine {
	return privileged.NewUnavailableServerEngine(
		"platform network engine is unsupported",
	)
}
