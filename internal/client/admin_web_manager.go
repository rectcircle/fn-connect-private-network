package client

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func (m *Manager) recoverAdminWeb(ctx context.Context, fnID, rejectedToken string) ([]Cookie, error) {
	return m.recoverAdminWebWithCause(ctx, fnID, rejectedToken, false)
}
func (m *Manager) recoverRejectedAdminWeb(ctx context.Context, fnID, rejectedToken string) ([]Cookie, error) {
	return m.recoverAdminWebWithCause(ctx, fnID, rejectedToken, true)
}
func (m *Manager) recoverAdminWebWithCause(ctx context.Context, fnID, rejectedToken string, knownInvalid bool) ([]Cookie, error) {
	// Key by the rejected credential as well as NAS, so a later independent
	// session cannot join an older recovery. Credentials never appear in logs.
	key := fmt.Sprintf("%s:%x", fnID, sha256.Sum256([]byte(rejectedToken)))
	result := m.adminRecovery.DoChan(key, func() (any, error) {
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		defer cancel()
		session, found, err := m.store.LoadAdminWebSession(fnID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, adminWebAuthRequired()
		}
		if session.Token != rejectedToken {
			return m.adminGatewayCookies(fnID, session.Token)
		}
		recoverer, ok := m.webAuthenticator.(adminWebRecoverer)
		if !ok || session.LongToken == "" || session.Secret == "" {
			_, err := m.store.replaceAdminWebSession(session, nil)
			if err != nil {
				return nil, err
			}
			return nil, adminWebAuthRequired()
		}
		m.logger.Info("admin auth: Web session recovery started", "fn_id", fnID)
		recoveryCtx := logging.WithLogger(work, logging.FromContext(work, m.logger))
		var recovered AdminWebSession
		if direct, ok := m.webAuthenticator.(rejectedAdminWebAuthenticator); knownInvalid && ok {
			recovered, err = direct.RecoverRejectedWebToken(recoveryCtx, session, m.deviceName)
		} else {
			recovered, err = recoverer.RecoverWeb(recoveryCtx, session, m.deviceName)
		}
		if err != nil {
			if model.AsError(err).Code == model.ErrorAuthRequired {
				if _, clearErr := m.store.replaceAdminWebSession(session, nil); clearErr != nil {
					return nil, clearErr
				}
			}
			m.logger.Warn("admin auth: Web session recovery failed", "fn_id", fnID, "code", model.AsError(err).Code)
			return nil, err
		}
		saved, err := m.store.replaceAdminWebSession(session, &recovered)
		if err != nil {
			return nil, err
		}
		if !saved {
			return nil, adminWebAuthRequired()
		}
		m.stopAdminWebActivity()
		m.logger.Info("admin auth: Web session recovered", "fn_id", fnID)
		return m.adminGatewayCookies(fnID, recovered.Token)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.([]Cookie), nil
	}
}

// Polling from a visible management page counts as use. The socket stops two
// minutes after its last proxy request, on logout, or when its token is replaced.
func (m *Manager) touchAdminWebActivity(parent context.Context, fnID string) {
	if parent.Err() != nil {
		return
	}
	maintainer, ok := m.webAuthenticator.(adminWebMaintainer)
	if !ok {
		return
	}
	m.adminActivityMu.Lock()
	defer m.adminActivityMu.Unlock()
	m.adminActivityLast = time.Now()
	if m.adminActivityCancel != nil || time.Now().Before(m.adminActivityNext) {
		return
	}
	session, found, err := m.store.LoadAdminWebSession(fnID)
	if err != nil || !found || session.Secret == "" {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.adminActivityCancel = cancel
	m.adminActivityGeneration++
	generation := m.adminActivityGeneration
	go func() {
		done := make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					m.adminActivityMu.Lock()
					idle := time.Since(m.adminActivityLast) >= 2*time.Minute
					m.adminActivityMu.Unlock()
					if idle {
						cancel()
						return
					}
				}
			}
		}()
		err := maintainer.MaintainWeb(ctx, session)
		canceled := ctx.Err() != nil
		cancel()
		<-done
		m.adminActivityMu.Lock()
		if m.adminActivityGeneration == generation {
			m.adminActivityCancel = nil
			m.adminActivityNext = time.Now().Add(time.Minute)
		}
		m.adminActivityMu.Unlock()
		if err != nil && !canceled {
			m.logger.Warn("admin auth: Web activity stopped", "fn_id", fnID, "code", model.AsError(err).Code)
		}
	}()
}

func (m *Manager) stopAdminWebActivity() {
	m.adminActivityMu.Lock()
	defer m.adminActivityMu.Unlock()
	if m.adminActivityCancel != nil {
		m.adminActivityCancel()
	}
	m.adminActivityCancel = nil
	m.adminActivityNext = time.Time{}
	m.adminActivityGeneration++
}
