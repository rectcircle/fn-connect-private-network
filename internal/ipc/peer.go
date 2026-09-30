package ipc

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type PeerIdentity struct {
	UID uint32
	GID uint32
	PID int
}

type Authorizer func(context.Context, PeerIdentity) error

type peerContextKey struct{}

func WithPeerIdentity(ctx context.Context, identity PeerIdentity) context.Context {
	return context.WithValue(ctx, peerContextKey{}, identity)
}

func PeerIdentityFromContext(ctx context.Context) (PeerIdentity, bool) {
	identity, ok := ctx.Value(peerContextKey{}).(PeerIdentity)
	return identity, ok
}

func RejectPeer(message string) error {
	return model.NewError(model.ErrorPermissionDenied, message, false)
}
