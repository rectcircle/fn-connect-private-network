//go:build linux

package platform

import (
	"log/slog"

	linuxplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/linux"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func NewClientEngine(logger *slog.Logger, stateDirectory string) privileged.ClientEngine {
	return linuxplatform.NewSystemClientEngine(logger, stateDirectory)
}

func NewServerEngine(
	logger *slog.Logger,
	stateDirectory string,
) privileged.ServerEngine {
	return linuxplatform.NewSystemServerEngine(logger, stateDirectory)
}
