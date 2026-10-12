package ipc

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/peer"
)

type PeerIdentity = peer.Identity

type Authorizer func(context.Context, PeerIdentity) error

func WithPeerIdentity(ctx context.Context, identity PeerIdentity) context.Context {
	return peer.WithIdentity(ctx, identity)
}

func PeerIdentityFromContext(ctx context.Context) (PeerIdentity, bool) {
	return peer.IdentityFromContext(ctx)
}

func RejectPeer(message string) error {
	return model.NewError(model.ErrorPermissionDenied, message, false)
}
