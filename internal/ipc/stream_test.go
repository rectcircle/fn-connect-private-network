//go:build !windows

package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

type measurementStream struct{ closed chan struct{} }

func (s measurementStream) Handle(_ context.Context, r Request) Response {
	return Success(r.ID, "ordinary")
}
func (s measurementStream) HandleStream(ctx context.Context, r Request, emit func(Response) error) bool {
	if r.Method != "measure" {
		return false
	}
	if err := emit(Success(r.ID, map[string]bool{"active": true})); err != nil {
		return true
	}
	if r.Params != nil {
		<-ctx.Done()
		close(s.closed)
		return true
	}
	_ = emit(Success(r.ID, map[string]bool{"active": false}))
	return true
}
func TestStreamingResponseAndCallerDisconnect(t *testing.T) {
	path := fmt.Sprintf("/tmp/fncpn-stream-%d.sock", os.Getpid())
	t.Cleanup(func() { os.Remove(path) })
	serverCtx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan error, 1)
	closed := make(chan struct{})
	go func() {
		done <- (Server{SocketPath: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handler: measurementStream{closed}}).Serve(serverCtx)
	}()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	waitForSocket(t, path)
	client := Client{SocketPath: path, Timeout: 2 * time.Second}
	count := 0
	if err := client.Stream(t.Context(), "measure", nil, func(data json.RawMessage) (bool, error) {
		var progress struct{ Active bool }
		if err := json.Unmarshal(data, &progress); err != nil {
			return false, err
		}
		count++
		return !progress.Active, nil
	}); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := client.Stream(ctx, "measure", map[string]bool{"wait": true}, func(json.RawMessage) (bool, error) { cancel(); return false, nil })
	if err == nil {
		t.Fatal("disconnect did not cancel read")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("daemon request survived caller disconnect")
	}
	var ordinary string
	if err := client.Call(t.Context(), "status", nil, &ordinary); err != nil || ordinary != "ordinary" {
		t.Fatal("ordinary IPC changed", err)
	}
}
