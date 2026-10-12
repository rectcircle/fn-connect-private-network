package peer

import "context"

// Identity describes the local process authenticated by a transport adapter.
type Identity struct {
	UID           uint32
	GID           uint32
	PID           int
	SID           string
	SessionID     uint32
	Authenticated bool
}

type identityContextKey struct{}

func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity)
}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	return identity, ok
}
