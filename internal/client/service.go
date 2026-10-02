package client

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

const (
	MethodStatus              = "status"
	MethodWatchStatus         = "watch-client-status"
	MethodConnect             = "connect"
	MethodDisconnect          = "disconnect"
	MethodRetry               = "retry"
	MethodAuthorize           = "authorize-complete"
	MethodAuthorizationBegin  = "authorization-begin"
	MethodAuthorizationStatus = "authorization-status"
	MethodAuthorizationWait   = "authorization-wait"
	MethodAuthorizationCancel = "authorization-cancel"
	MethodLogout              = "logout"
	MethodForget              = "forget"
	MethodDiagnose            = "diagnose"
	defaultAuthorizationTTL   = 5 * time.Minute
)

type Runtime interface {
	Status() model.ClientStatus
	Diagnose(context.Context) model.ClientDiagnostics
	Authorize(context.Context, string, []Cookie) error
	Connect(context.Context) error
	Disconnect(context.Context) error
	Retry(context.Context) error
	Logout(context.Context) error
	Forget(context.Context) error
}

type Authorization struct {
	RequestID string   `json:"requestId"`
	FNID      string   `json:"fnId"`
	Cookies   []Cookie `json:"cookies"`
}

type AuthorizationRequest struct {
	RequestID string `json:"requestId,omitempty"`
	FNID      string `json:"fnId,omitempty"`
}

type AuthorizationCancelRequest struct {
	RequestID string       `json:"requestId"`
	Failure   *model.Error `json:"failure,omitempty"`
}

type AuthorizationResult struct {
	RequestID string       `json:"requestId"`
	State     string       `json:"state"`
	Error     *model.Error `json:"error,omitempty"`
}

type StatusWatchRequest struct {
	After uint64 `json:"after"`
}

type StatusWatchResult struct {
	Changed    bool               `json:"changed"`
	Generation uint64             `json:"generation"`
	Status     model.ClientStatus `json:"status"`
}

type pendingAuthorization struct {
	fnID      string
	state     string
	err       *model.Error
	createdAt time.Time
	started   bool
	cancel    context.CancelFunc
	timer     *time.Timer
	changed   chan struct{}
}

type Service struct {
	Logger           *slog.Logger
	mu               sync.RWMutex
	runtime          Runtime
	pending          map[string]pendingAuthorization
	authorizationTTL time.Duration
}

func NewWithRuntime(runtime Runtime) *Service {
	return &Service{
		runtime:          runtime,
		pending:          make(map[string]pendingAuthorization),
		authorizationTTL: defaultAuthorizationTTL,
	}
}

func (s *Service) Handle(
	ctx context.Context,
	request ipc.Request,
) ipc.Response {
	var err error
	switch request.Method {
	case MethodStatus:
		return ipc.Success(request.ID, s.runtime.Status())
	case MethodWatchStatus:
		input, decodeErr := ipc.DecodeParams[StatusWatchRequest](request)
		if decodeErr != nil {
			return ipc.Failure(request.ID, decodeErr)
		}
		watcher, ok := s.runtime.(interface {
			WatchStatus(context.Context, uint64) StatusWatchResult
		})
		if !ok {
			return ipc.Failure(request.ID, model.NewError(
				model.ErrorFailedPrecondition,
				"client status watch is unavailable",
				false,
			))
		}
		return ipc.Success(
			request.ID,
			watcher.WatchStatus(ctx, input.After),
		)
	case MethodDiagnose:
		return ipc.Success(request.ID, s.runtime.Diagnose(ctx))
	case MethodConnect:
		err = s.runtime.Connect(ctx)
	case MethodDisconnect:
		err = s.runtime.Disconnect(ctx)
	case MethodRetry:
		err = s.runtime.Retry(ctx)
	case MethodAuthorizationBegin:
		return s.beginAuthorization(request)
	case MethodAuthorizationStatus:
		return s.authorizationStatus(request)
	case MethodAuthorizationWait:
		return s.authorizationWait(ctx, request)
	case MethodAuthorizationCancel:
		return s.cancelAuthorization(request)
	case MethodAuthorize:
		authorization, decodeErr := ipc.DecodeParams[Authorization](request)
		if decodeErr != nil {
			err = decodeErr
		} else {
			authorizationContext, startErr := s.startAuthorization(
				ctx,
				authorization,
			)
			if startErr != nil {
				err = startErr
			} else {
				err = s.runtime.Authorize(
					authorizationContext,
					authorization.FNID,
					authorization.Cookies,
				)
				err = s.finishAuthorization(authorization.RequestID, err)
			}
		}
	case MethodLogout:
		err = s.runtime.Logout(ctx)
	case MethodForget:
		err = s.runtime.Forget(ctx)
	default:
		err = model.NewError(
			model.ErrorInvalidArgument,
			"unsupported client method",
			false,
		)
	}
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	return ipc.Success(request.ID, s.runtime.Status())
}

func (s *Service) beginAuthorization(request ipc.Request) ipc.Response {
	input, err := ipc.DecodeParams[AuthorizationRequest](request)
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	fnID, err := normalizeFNID(input.FNID)
	if err != nil {
		return ipc.Failure(request.ID, model.WrapError(
			model.ErrorInvalidArgument,
			"invalid FN ID",
			false,
			err,
		))
	}
	id, err := authorizationID()
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	now := time.Now()
	ttl := s.authorizationLifetime()
	s.mu.Lock()
	for pendingID, pending := range s.pending {
		if now.Sub(pending.createdAt) > ttl {
			pending = s.expireAuthorizationLocked(pendingID, pending, now)
			if pending.started {
				continue
			}
			if pending.timer != nil {
				pending.timer.Stop()
			}
			if pending.cancel != nil {
				pending.cancel()
			}
			delete(s.pending, pendingID)
		}
	}
	pending := pendingAuthorization{
		fnID:      fnID,
		state:     "pending",
		createdAt: now,
		changed:   make(chan struct{}),
	}
	pending.timer = time.AfterFunc(ttl, func() {
		s.expireAuthorization(id)
	})
	s.pending[id] = pending
	s.mu.Unlock()
	s.logger().Info("authorization requested", "authorization_id", id, "fn_id", fnID)
	return ipc.Success(request.ID, AuthorizationResult{
		RequestID: id,
		State:     "pending",
	})
}

func (s *Service) authorizationStatus(request ipc.Request) ipc.Response {
	input, err := ipc.DecodeParams[AuthorizationRequest](request)
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[input.RequestID]
	if !ok {
		return ipc.Failure(request.ID, model.NewError(
			model.ErrorNotFound,
			"authorization request was not found",
			false,
		))
	}
	pending = s.expireAuthorizationLocked(input.RequestID, pending, time.Now())
	result := AuthorizationResult{
		RequestID: input.RequestID,
		State:     pending.state,
		Error:     pending.err,
	}
	if pending.state != "pending" && !pending.started {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(s.pending, input.RequestID)
	}
	return ipc.Success(request.ID, result)
}

func (s *Service) authorizationWait(
	ctx context.Context,
	request ipc.Request,
) ipc.Response {
	input, err := ipc.DecodeParams[AuthorizationRequest](request)
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	s.mu.Lock()
	pending, ok := s.pending[input.RequestID]
	if !ok {
		s.mu.Unlock()
		return ipc.Failure(request.ID, model.NewError(
			model.ErrorNotFound,
			"authorization request was not found",
			false,
		))
	}
	pending = s.expireAuthorizationLocked(input.RequestID, pending, time.Now())
	if pending.state != "pending" {
		result := authorizationResult(input.RequestID, pending)
		if !pending.started {
			delete(s.pending, input.RequestID)
		}
		s.mu.Unlock()
		return ipc.Success(request.ID, result)
	}
	changed := pending.changed
	s.mu.Unlock()

	select {
	case <-ctx.Done():
		return ipc.Failure(request.ID, model.AsError(ctx.Err()))
	case <-changed:
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok = s.pending[input.RequestID]
	if !ok {
		return ipc.Failure(request.ID, model.NewError(
			model.ErrorNotFound,
			"authorization request was not found",
			false,
		))
	}
	pending = s.expireAuthorizationLocked(input.RequestID, pending, time.Now())
	result := authorizationResult(input.RequestID, pending)
	if pending.state != "pending" && !pending.started {
		delete(s.pending, input.RequestID)
	}
	return ipc.Success(request.ID, result)
}

func authorizationResult(
	requestID string,
	pending pendingAuthorization,
) AuthorizationResult {
	return AuthorizationResult{
		RequestID: requestID,
		State:     pending.state,
		Error:     pending.err,
	}
}

func (s *Service) cancelAuthorization(request ipc.Request) ipc.Response {
	input, err := ipc.DecodeParams[AuthorizationCancelRequest](request)
	if err != nil {
		return ipc.Failure(request.ID, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[input.RequestID]
	if !ok {
		return ipc.Failure(request.ID, model.NewError(
			model.ErrorNotFound,
			"authorization request was not found",
			false,
		))
	}
	pending = s.expireAuthorizationLocked(input.RequestID, pending, time.Now())
	if pending.state != "pending" {
		return ipc.Success(request.ID, AuthorizationResult{
			RequestID: input.RequestID,
			State:     pending.state,
			Error:     pending.err,
		})
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	if pending.cancel != nil {
		pending.cancel()
		pending.cancel = nil
	}
	pending.state = "canceled"
	pending.err = model.NewError(model.ErrorCanceled, "authorization canceled", false)
	if input.Failure != nil {
		if !model.IsErrorCode(input.Failure.Code) {
			input.Failure.Code = model.ErrorInternal
		}
		pending.state = "failed"
		pending.err = model.PublicError(model.WithOperation(input.Failure, "authorization.browser"))
	}
	closeAuthorizationChange(pending)
	s.pending[input.RequestID] = pending
	if input.Failure != nil {
		return ipc.Failure(request.ID, pending.err)
	}
	s.logger().Info("authorization canceled", "authorization_id", input.RequestID, "fn_id", pending.fnID)
	return ipc.Success(request.ID, AuthorizationResult{
		RequestID: input.RequestID,
		State:     pending.state,
		Error:     pending.err,
	})
}

func (s *Service) startAuthorization(
	ctx context.Context,
	authorization Authorization,
) (context.Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[authorization.RequestID]
	if !ok {
		return nil, model.NewError(
			model.ErrorFailedPrecondition,
			"authorization request is not pending",
			false,
		)
	}
	pending = s.expireAuthorizationLocked(
		authorization.RequestID,
		pending,
		time.Now(),
	)
	if pending.state != "pending" {
		if pending.err != nil {
			return nil, pending.err
		}
		return nil, model.NewError(
			model.ErrorFailedPrecondition,
			"authorization request is not pending",
			false,
		)
	}
	if pending.fnID != authorization.FNID {
		return nil, model.NewError(
			model.ErrorInvalidArgument,
			"authorization FN ID does not match its request",
			false,
		)
	}
	if pending.started {
		return nil, model.NewError(
			model.ErrorFailedPrecondition,
			"authorization request is already being processed",
			false,
		)
	}
	authorizationContext, cancel := context.WithDeadline(
		ctx,
		pending.createdAt.Add(s.authorizationLifetime()),
	)
	pending.started = true
	pending.cancel = cancel
	s.pending[authorization.RequestID] = pending
	logger := logging.FromContext(ctx, s.logger()).With("authorization_id", authorization.RequestID)
	logger.Info("authorization credentials received", "fn_id", pending.fnID)
	authorizationContext = logging.WithLogger(authorizationContext, logger)
	return authorizationContext, nil
}

func (s *Service) finishAuthorization(requestID string, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[requestID]
	if !ok {
		return err
	}
	if pending.cancel != nil {
		pending.cancel()
		pending.cancel = nil
	}
	pending.started = false
	pending = s.expireAuthorizationLocked(requestID, pending, time.Now())
	if pending.state != "pending" {
		s.pending[requestID] = pending
		if pending.err != nil {
			return pending.err
		}
		return model.NewError(
			model.ErrorFailedPrecondition,
			"authorization request is not pending",
			false,
		)
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	if err == nil {
		pending.state = "succeeded"
		pending.err = nil
		s.logger().Info("authorization completed",
			"authorization_id", requestID, "fn_id", pending.fnID,
			"elapsed_ms", time.Since(pending.createdAt).Milliseconds())
	} else {
		pending.state = "failed"
		pending.err = model.PublicError(err)
	}
	closeAuthorizationChange(pending)
	s.pending[requestID] = pending
	return err
}

func (s *Service) expireAuthorization(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[requestID]
	if !ok {
		return
	}
	s.expireAuthorizationLocked(requestID, pending, time.Now())
}

func (s *Service) expireAuthorizationLocked(
	requestID string,
	pending pendingAuthorization,
	now time.Time,
) pendingAuthorization {
	if pending.state != "pending" ||
		now.Before(pending.createdAt.Add(s.authorizationLifetime())) {
		return pending
	}
	if pending.cancel != nil {
		pending.cancel()
		pending.cancel = nil
	}
	pending.state = "failed"
	pending.err = model.NewError(
		model.ErrorTimeout,
		"authorization request expired",
		true,
	)
	pending.err.Operation = "authorization.wait"
	pending.err.RequestID = model.SafeRequestID(requestID)
	s.logger().Error("authorization expired", logging.ErrorAttrs(pending.err)...)
	closeAuthorizationChange(pending)
	s.pending[requestID] = pending
	return pending
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func closeAuthorizationChange(pending pendingAuthorization) {
	if pending.changed != nil {
		close(pending.changed)
	}
}

func (s *Service) authorizationLifetime() time.Duration {
	if s.authorizationTTL <= 0 {
		return defaultAuthorizationTTL
	}
	return s.authorizationTTL
}

func authorizationID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func cloneStatus(status model.ClientStatus) model.ClientStatus {
	cloned := status
	if status.LastError != nil {
		copied := *status.LastError
		cloned.LastError = &copied
	}
	return cloned
}
