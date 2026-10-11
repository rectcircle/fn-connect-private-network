//go:build !windows

package client

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func newPlatformSecretStore(ctx context.Context, transport ipc.Client) SecretStore {
	return IPCSecretStore{Context: ctx, Client: transport}
}
