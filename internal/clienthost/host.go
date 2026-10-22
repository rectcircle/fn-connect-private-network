// Package clienthost embeds the client daemon in a system-owned lifecycle.
// It has no UI dependencies; closing a UI must never close its Host.
package clienthost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Runtime is implemented by client.Manager. Platform dependencies are injected
// when constructing that manager, before attaching it to the system host.
type Runtime interface {
	client.Runtime
	Start() error
	Run(context.Context)
	Close(context.Context) error
}

// Host is single-use. The Android Service owns Start and Close; UI clients only
// receive the Call interface. A stopped host requires a fresh manager instance.
type Host struct {
	mu      sync.Mutex
	runtime Runtime
	service *client.Service
	ctx     context.Context
	cancel  context.CancelFunc
	runDone chan struct{}
	closed  bool
	calls   sync.WaitGroup
}

func New(runtime Runtime) (*Host, error) {
	if runtime == nil {
		return nil, errors.New("client runtime is required")
	}
	return &Host{runtime: runtime, service: client.NewWithRuntime(runtime)}, nil
}

func (h *Host) Start(parent context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return errors.New("client host is closed")
	}
	if h.ctx != nil {
		return errors.New("client host is already started")
	}
	if err := parent.Err(); err != nil {
		return err
	}
	if err := h.runtime.Start(); err != nil {
		return err
	}
	h.ctx, h.cancel = context.WithCancel(parent)
	h.runDone = make(chan struct{})
	go func() { defer close(h.runDone); h.runtime.Run(h.ctx) }()
	return nil
}

// Call preserves the existing daemon command contract without opening a socket.
// Cancelling an individual UI request does not cancel the host lifetime.
func (h *Host) Call(ctx context.Context, method string, params, result any) error {
	h.mu.Lock()
	if h.ctx == nil || h.closed || h.ctx.Err() != nil {
		h.mu.Unlock()
		return model.NewError(model.ErrorUnavailable, "client service is not running", true)
	}
	h.calls.Add(1)
	hostContext := h.ctx
	h.mu.Unlock()
	defer h.calls.Done()
	callContext, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(hostContext, cancel)
	defer stop()
	defer cancel()
	if err := callContext.Err(); err != nil {
		return err
	}
	request, err := ipc.NewRequest("embedded", method, params)
	if err != nil {
		return err
	}
	response := h.service.Handle(callContext, request)
	if !response.OK {
		return response.Error
	}
	if result == nil || len(response.Result) == 0 {
		return nil
	}
	return json.Unmarshal(response.Result, result)
}

// Close cancels admitted calls and waits for them and the run loop before
// releasing network resources. A deadline leaves cleanup retryable.
func (h *Host) Close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	if h.cancel == nil {
		h.mu.Unlock()
		return nil
	}
	h.cancel()
	done := h.runDone
	h.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	callsDone := make(chan struct{})
	go func() { h.calls.Wait(); close(callsDone) }()
	select {
	case <-callsDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	// Serialize repeated cleanup attempts; Manager.Close is idempotent.
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runtime.Close(ctx)
}
