package client

import (
	"context"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Only non-secret metadata crosses this logging boundary. RPC success is logged
// separately from the manager's subsequent successful credential persistence.
func beginSessionRecoveryLog(ctx context.Context, kind, fnID, method string) func(bool, bool, error) {
	logger := logging.FromContext(ctx, nil).With("session_kind", kind, "fn_id", fnID, "method", method)
	started := time.Now()
	logger.Info("fnOS session recovery attempt", "outcome", "started")
	return func(tokenChanged, longTokenChanged bool, err error) {
		if err != nil {
			logger.Info("fnOS session recovery attempt", "outcome", "failed", "code", model.AsError(err).Code, "elapsed_ms", time.Since(started).Milliseconds())
			return
		}
		logger.Info("fnOS session recovery attempt", "outcome", "succeeded", "token_changed", tokenChanged, "long_token_changed", longTokenChanged, "elapsed_ms", time.Since(started).Milliseconds())
	}
}
