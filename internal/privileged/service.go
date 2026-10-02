package privileged

import (
	"context"
	"runtime"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/notify"
)

const (
	MethodStatus         = "status"
	MethodWatchLifecycle = "watch-lifecycle"
	MethodApply          = "apply"
	MethodRemove         = "remove"
)

type WatchRequest struct {
	After uint64 `json:"after"`
}

type WatchResult[Status any] struct {
	Changed    bool   `json:"changed"`
	Generation uint64 `json:"generation"`
	Status     Status `json:"status"`
}

type ClientStatus struct {
	Role      string                       `json:"role"`
	Platform  string                       `json:"platform"`
	Available bool                         `json:"available"`
	Network   model.ClientPrivilegedStatus `json:"network"`
}

type ServerStatus struct {
	Role      string                       `json:"role"`
	Platform  string                       `json:"platform"`
	Available bool                         `json:"available"`
	Network   model.ServerPrivilegedStatus `json:"network"`
}

type ClientEngine interface {
	Available() bool
	Status() model.ClientPrivilegedStatus
	Recover(context.Context) error
	Apply(context.Context, model.ClientPlan) error
	Remove(context.Context) error
}

type ServerEngine interface {
	Available() bool
	Status() model.ServerPrivilegedStatus
	Recover(context.Context) error
	Apply(context.Context, model.ServerPlan) error
	Remove(context.Context) error
}

type unavailableEngine struct {
	message string
}

func (e unavailableEngine) Available() bool {
	return false
}

func (e unavailableEngine) Recover(context.Context) error {
	return nil
}

func (e unavailableEngine) Remove(context.Context) error {
	return nil
}

func (e unavailableEngine) applyError() error {
	return model.NewError(model.ErrorUnavailable, e.message, false)
}

type unavailableClientEngine struct {
	unavailableEngine
}

func NewUnavailableClientEngine(message string) ClientEngine {
	return unavailableClientEngine{
		unavailableEngine: unavailableEngine{message: message},
	}
}

func (unavailableClientEngine) Status() model.ClientPrivilegedStatus {
	return model.ClientPrivilegedStatus{}
}

func (e unavailableClientEngine) Apply(context.Context, model.ClientPlan) error {
	return e.applyError()
}

type unavailableServerEngine struct {
	unavailableEngine
}

func NewUnavailableServerEngine(message string) ServerEngine {
	return unavailableServerEngine{
		unavailableEngine: unavailableEngine{message: message},
	}
}

func (unavailableServerEngine) Status() model.ServerPrivilegedStatus {
	return model.ServerPrivilegedStatus{}
}

func (e unavailableServerEngine) Apply(context.Context, model.ServerPlan) error {
	return e.applyError()
}

type lifecycleEngine interface {
	Available() bool
	Recover(context.Context) error
	Remove(context.Context) error
}

type applyFunc func(context.Context, ipc.Request) error

type Service[NetworkStatus, StatusEnvelope any] struct {
	engine        lifecycleEngine
	apply         applyFunc
	networkStatus func() NetworkStatus
	status        func() StatusEnvelope
	changes       *notify.Change
}

type ClientService struct {
	*Service[model.ClientPrivilegedStatus, ClientStatus]
	secrets *SecretStore
}

func NewClientService(
	engine ClientEngine,
	secrets *SecretStore,
) *ClientService {
	service := &Service[model.ClientPrivilegedStatus, ClientStatus]{
		engine:  engine,
		changes: notify.New(),
		apply: func(ctx context.Context, request ipc.Request) error {
			plan, err := ipc.DecodeParams[model.ClientPlan](request)
			if err != nil {
				return err
			}
			logger := logging.FromContext(ctx, nil)
			logger.Info("privileged client network apply requested")
			if err := engine.Apply(ctx, plan); err != nil {
				return err
			}
			logger.Info("privileged client network applied", "mode", plan.Mode,
				"address", plan.ClientAddress, "route_count", len(plan.AllowedIPs), "mtu", plan.MTU)
			return nil
		},
		networkStatus: func() model.ClientPrivilegedStatus {
			return engine.Status()
		},
		status: func() ClientStatus {
			return ClientStatus{
				Role:      "client",
				Platform:  runtime.GOOS,
				Available: engine.Available(),
				Network:   engine.Status(),
			}
		},
	}
	if source, ok := engine.(interface{ SetLifecycleNotifier(func()) }); ok {
		source.SetLifecycleNotifier(func() {
			service.changes.Notify()
		})
	}
	return &ClientService{Service: service, secrets: secrets}
}

func (s *ClientService) Handle(ctx context.Context, request ipc.Request) ipc.Response {
	switch request.Method {
	case MethodGetSecret, MethodPutSecret, MethodDeleteSecret:
		if s.secrets == nil {
			return ipc.Failure(request.ID, model.NewError(
				model.ErrorUnavailable, "client credential storage is unavailable", false,
			))
		}
		return s.secrets.Handle(ctx, request)
	default:
		return s.Service.Handle(ctx, request)
	}
}

func NewServerService(
	engine ServerEngine,
) *Service[model.ServerPrivilegedStatus, ServerStatus] {
	return &Service[model.ServerPrivilegedStatus, ServerStatus]{
		engine:  engine,
		changes: notify.New(),
		apply: func(ctx context.Context, request ipc.Request) error {
			plan, err := ipc.DecodeParams[model.ServerPlan](request)
			if err != nil {
				return err
			}
			return engine.Apply(ctx, plan)
		},
		networkStatus: func() model.ServerPrivilegedStatus {
			return engine.Status()
		},
		status: func() ServerStatus {
			return ServerStatus{
				Role:      "server",
				Platform:  runtime.GOOS,
				Available: engine.Available(),
				Network:   engine.Status(),
			}
		},
	}
}

func (s *Service[NetworkStatus, StatusEnvelope]) Recover(ctx context.Context) error {
	return s.engine.Recover(ctx)
}

func (s *Service[NetworkStatus, StatusEnvelope]) Close(ctx context.Context) error {
	return s.engine.Remove(ctx)
}

func (s *Service[NetworkStatus, StatusEnvelope]) Handle(
	ctx context.Context,
	request ipc.Request,
) ipc.Response {
	switch request.Method {
	case MethodStatus:
		return ipc.Success(request.ID, s.status())
	case MethodWatchLifecycle:
		input, err := ipc.DecodeParams[WatchRequest](request)
		if err != nil {
			return ipc.Failure(request.ID, err)
		}
		generation, changed := s.changes.Wait(ctx, input.After)
		return ipc.Success(request.ID, WatchResult[StatusEnvelope]{
			Changed:    changed,
			Generation: generation,
			Status:     s.status(),
		})
	case MethodApply:
		if err := s.apply(ctx, request); err != nil {
			return ipc.Failure(request.ID, err)
		}
		s.changes.Notify()
		return ipc.Success(request.ID, s.networkStatus())
	case MethodRemove:
		if err := s.engine.Remove(ctx); err != nil {
			return ipc.Failure(request.ID, err)
		}
		s.changes.Notify()
		return ipc.Success(request.ID, s.networkStatus())
	default:
		return ipc.Failure(
			request.ID,
			model.NewError(
				model.ErrorInvalidArgument,
				"unsupported privileged method",
				false,
			),
		)
	}
}
