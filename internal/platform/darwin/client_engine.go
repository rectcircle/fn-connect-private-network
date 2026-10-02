//go:build darwin

package darwin

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/wireguard"
)

type DeviceRuntime interface {
	Start(context.Context, model.ClientPlan) (string, error)
	Stop()
	Status() model.ClientPrivilegedStatus
}

type NetworkConfigurator interface {
	Available() bool
	Recover(context.Context) error
	Apply(context.Context, string, model.ClientPlan) error
	Remove(context.Context, string) error
}

type consoleUserEvent struct {
	Err error
}

type ClientEngine struct {
	mu              sync.Mutex
	runtime         DeviceRuntime
	network         NetworkConfigurator
	logger          *slog.Logger
	interfaceName   string
	ownerUID        uint32
	monitorOnce     sync.Once
	degraded        bool
	lifecycleNotify func()
}

func (e *ClientEngine) SetLifecycleNotifier(notify func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lifecycleNotify = notify
}

func NewClientEngine(
	runtime DeviceRuntime,
	network NetworkConfigurator,
	loggers ...*slog.Logger,
) *ClientEngine {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return &ClientEngine{
		runtime: runtime,
		network: network,
		logger:  logger,
	}
}

func (e *ClientEngine) Available() bool {
	return e.runtime != nil && e.network != nil && e.network.Available()
}

func (e *ClientEngine) Status() model.ClientPrivilegedStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime == nil {
		return model.ClientPrivilegedStatus{}
	}
	status := e.runtime.Status()
	status.Degraded = status.Degraded || e.degraded
	return status
}

func (e *ClientEngine) Recover(ctx context.Context) error {
	if !e.Available() {
		return unavailableNetworkError()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runtime.Status().Active {
		return model.NewError(
			model.ErrorConflict,
			"WireGuard runtime is already active",
			false,
		)
	}
	if err := e.network.Recover(ctx); err != nil {
		return err
	}
	e.monitorOnce.Do(func() {
		go e.monitorConsoleOwner(ctx)
	})
	return nil
}

func (e *ClientEngine) Apply(
	ctx context.Context,
	plan model.ClientPlan,
) error {
	normalized, err := wireguard.NormalizeClientPlan(plan)
	if err != nil {
		return model.WrapError(model.ErrorInvalidArgument, "invalid client network plan", false, err)
	}
	if !e.Available() {
		return unavailableNetworkError()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.degraded || e.interfaceName != "" || e.runtime.Status().Active {
		if err := e.cleanupToEmptyLocked(); err != nil {
			return err
		}
	}
	if identity, ok := ipc.PeerIdentityFromContext(ctx); ok && identity.UID != 0 {
		consoleUID, err := currentConsoleUID()
		if err != nil || identity.UID != consoleUID {
			return model.NewError(
				model.ErrorPermissionDenied,
				"only the current console user may apply network state",
				false,
			)
		}
		e.ownerUID = identity.UID
		if setter, ok := e.network.(interface{ SetOwnerUID(uint32) }); ok {
			setter.SetOwnerUID(identity.UID)
		}
	}
	name, err := e.runtime.Start(ctx, normalized)
	if err != nil {
		return e.failToEmptyLocked(err)
	}
	e.interfaceName = name
	status := e.runtime.Status()
	if !status.Active || status.Interface == "" {
		return e.failToEmptyLocked(
			model.NewError(
				model.ErrorFailedPrecondition,
				"WireGuard runtime did not become active",
				true,
			),
		)
	}
	e.interfaceName = status.Interface
	if err := e.network.Apply(ctx, e.interfaceName, normalized); err != nil {
		return e.failToEmptyLocked(err)
	}
	e.degraded = false
	return nil
}

func (e *ClientEngine) failToEmptyLocked(cause error) error {
	cleanupErr := e.cleanupToEmptyLocked()
	if cleanupErr == nil {
		return cause
	}
	e.degraded = true
	return errors.Join(cause, cleanupErr)
}

func (e *ClientEngine) Remove(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.cleanupToEmptyLocked(); err != nil {
		return err
	}
	return nil
}

func (e *ClientEngine) cleanupToEmptyLocked() error {
	interfaceName := e.interfaceName
	if interfaceName == "" && e.runtime != nil {
		interfaceName = e.runtime.Status().Interface
	}
	var cleanupErr error
	if interfaceName != "" {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cleanupErr = e.network.Remove(cleanupContext, interfaceName)
		cancel()
	}
	if cleanupErr != nil {
		e.interfaceName = interfaceName
		e.degraded = true
		return cleanupErr
	}
	if e.runtime != nil {
		e.runtime.Stop()
	}
	e.interfaceName = ""
	e.ownerUID = 0
	e.degraded = false
	return nil
}

func unavailableNetworkError() error {
	return model.NewError(
		model.ErrorUnavailable,
		"macOS network configuration is unavailable",
		false,
	)
}

func (e *ClientEngine) monitorConsoleOwner(ctx context.Context) {
	events := consoleUserEvents(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if event.Err != nil {
				e.logger.Error(
					"monitor console user",
					"error",
					event.Err,
				)
			}
			e.mu.Lock()
			ownerUID := e.ownerUID
			e.mu.Unlock()
			if ownerUID == 0 {
				continue
			}
			consoleUID, err := currentConsoleUID()
			if err == nil && consoleUID == ownerUID {
				continue
			}
			cleanupContext, cancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			err = e.Remove(cleanupContext)
			cancel()
			e.mu.Lock()
			notify := e.lifecycleNotify
			e.mu.Unlock()
			if notify != nil {
				notify()
			}
			if err != nil {
				continue
			}
		}
	}
}
