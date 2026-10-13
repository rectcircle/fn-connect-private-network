//go:build windows

package command

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/windowsinstall"
	"golang.org/x/sys/windows"
)

func ensureClientDaemon(ctx context.Context, socket string) error {
	return ensureClientDaemonWithStarter(ctx, socket, func() error {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.Command(executable, "client", "daemon", "--socket", socket)
		command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW | windows.DETACHED_PROCESS}
		if err := command.Start(); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if command.Process != nil {
			_ = command.Process.Release()
		}
		return nil
	})
}

func ensureClientDaemonWithStarter(ctx context.Context, socket string, start func() error) error {
	probe := ipc.Client{SocketPath: socket, Timeout: time.Second}
	var status model.ClientStatus
	if err := probe.Call(ctx, client.MethodStatus, nil, &status); err == nil {
		return nil
	}
	if start == nil {
		return errors.New("client daemon starter is required")
	}
	if err := start(); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := probe.Call(ctx, client.MethodStatus, nil, &status); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return model.NewError(model.ErrorUnavailable, "client daemon did not start; check the FnCPN service", true)
}

func runStopAllClientDaemons() error {
	return windowsinstall.StopAllClientDaemons()
}
