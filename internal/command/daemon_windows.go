//go:build windows

package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"syscall"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
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
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := probe.Call(ctx, client.MethodStatus, nil, &status); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return model.NewError(model.ErrorUnavailable, "client daemon did not start; check the FnCPN service", true)
}

func runStopAllClientDaemons() error {
	if !isPrivilegedProcess() {
		return errors.New("internal-stop-daemons must run as LocalSystem")
	}
	var sessions *windows.WTS_SESSION_INFO
	var count uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &sessions, &count); err != nil {
		return fmt.Errorf("enumerate Windows user sessions: %w", err)
	}
	if sessions != nil {
		defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(sessions)))
	}
	entries := unsafe.Slice(sessions, int(count))
	sessionIDs := make([]uint32, 0, len(entries))
	for _, entry := range entries {
		if entry.SessionID == 0 || (entry.State != windows.WTSActive && entry.State != windows.WTSConnected && entry.State != windows.WTSDisconnected) {
			continue
		}
		sessionIDs = append(sessionIDs, entry.SessionID)
	}
	sort.Slice(sessionIDs, func(i, j int) bool { return sessionIDs[i] < sessionIDs[j] })
	for _, sessionID := range sessionIDs {
		if err := stopClientDaemonInSession(sessionID); err != nil {
			return fmt.Errorf("stop client daemon in session %d: %w", sessionID, err)
		}
	}
	return nil
}

func stopClientDaemonInSession(sessionID uint32) error {
	clientIPC := ipc.Client{SocketPath: defaultClientSocketForSession(sessionID), Timeout: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := clientIPC.Call(ctx, client.MethodShutdown, nil, nil); err != nil {
		var status model.ClientStatus
		statusErr := clientIPC.Call(ctx, client.MethodStatus, nil, &status)
		if statusErr != nil && model.AsError(statusErr).Code == model.ErrorUnavailable {
			return nil // No responsive daemon is listening in this session.
		}
		return fmt.Errorf("request daemon shutdown: %w (status probe: %v)", err, statusErr)
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for daemon shutdown: %w", err)
		}
		var status model.ClientStatus
		if err := clientIPC.Call(ctx, client.MethodStatus, nil, &status); err != nil && model.AsError(err).Code == model.ErrorUnavailable {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
