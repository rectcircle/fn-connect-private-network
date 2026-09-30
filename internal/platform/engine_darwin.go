//go:build darwin

package platform

import (
	"log/slog"

	darwinplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/darwin"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func NewClientEngine(
	logger *slog.Logger,
	stateDirectory string,
) privileged.ClientEngine {
	return darwinplatform.NewClientEngine(
		darwinplatform.NewWireGuardRuntime(logger),
		darwinplatform.NewNetworkConfigurator(stateDirectory),
		logger,
	)
}

func NewServerEngine(_ *slog.Logger, _ string) privileged.ServerEngine {
	return privileged.NewUnavailableServerEngine(
		"server network engine is only available on Linux",
	)
}
