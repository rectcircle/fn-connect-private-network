//go:build windows

package platform

import (
	"log/slog"

	windowsplatform "github.com/rectcircle/fn-connect-private-network/internal/platform/windows"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func NewClientEngine(logger *slog.Logger, stateDirectory string) privileged.ClientEngine {
	return windowsplatform.NewClientEngine(logger, stateDirectory)
}

func NewServerEngine(_ *slog.Logger, _ string) privileged.ServerEngine {
	return privileged.NewUnavailableServerEngine("fnCPN server engine is only available on Linux")
}

func NotifyConsoleSessionChange() { windowsplatform.NotifyConsoleSessionChange() }
func NotifyPowerResume()          { windowsplatform.NotifyPowerResume() }
