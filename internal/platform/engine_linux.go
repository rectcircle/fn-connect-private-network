//go:build linux

package platform

import (
	"log/slog"

	linuxplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/linux"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func NewClientEngine(_ *slog.Logger, _ string) privileged.ClientEngine {
	return privileged.NewUnavailableClientEngine(
		"client network engine is only available on macOS",
	)
}

func NewServerEngine(
	logger *slog.Logger,
	stateDirectory string,
) privileged.ServerEngine {
	return linuxplatform.NewSystemServerEngine(logger, stateDirectory)
}
