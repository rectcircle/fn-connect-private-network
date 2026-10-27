package client

import (
	"context"
	"strings"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Only the known fnOS rejection proves this token was invalid. A generic app
// 401, access-code challenge or network error must not force long-token login.
func rejectedFNOSToken(err error) bool {
	failure := model.AsError(err)
	return failure != nil && failure.Code == model.ErrorAuthRequired && strings.Contains(failure.Detail, "fnOS gateway returned invalid token")
}

type rejectedNativeAuthenticator interface {
	RecoverRejectedToken(context.Context, NativeSession, string) (NativeSession, error)
}
type rejectedAdminWebAuthenticator interface {
	RecoverRejectedWebToken(context.Context, AdminWebSession, string) (AdminWebSession, error)
}

// A shared completion replaces polling a synthetic UNAVAILABLE error. It also
// releases waiters when a pending worker is canceled by disconnect or shutdown.
type authenticationRecovery struct {
	done  chan struct{}
	once  sync.Once
	cause error
	err   error
}

func (r *authenticationRecovery) finish(err error) {
	if r == nil {
		return
	}
	r.once.Do(func() { r.err = err; close(r.done) })
}
func (r *authenticationRecovery) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
		return r.err
	}
}
func (r *authenticationRecovery) pending() bool {
	if r == nil {
		return false
	}
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}
