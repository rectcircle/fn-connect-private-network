//go:build windows

// Package windowsinstall contains only the operations needed by Windows Installer.
package windowsinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	platformwindows "github.com/rectcircle/fn-connect-private-network/internal/platform/windows"
	"golang.org/x/sys/windows"
)

func IsLocalSystem() bool {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && user.User.Sid.String() == "S-1-5-18"
}

func ClientSocketForSession(session uint32) string {
	return `\\.\pipe\FnCPN.Client.v1.` + strconv.FormatUint(uint64(session), 10)
}

func StopCurrentClientDaemon() error {
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return err
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	var closeErr error
	callback := windows.NewCallback(func(window uintptr, _ uintptr) uintptr {
		var class [128]uint16
		user32.NewProc("GetClassNameW").Call(window, uintptr(unsafe.Pointer(&class[0])), 128)
		if windows.UTF16ToString(class[:]) != "FnCPN.NativeWindow" {
			return 1
		}
		var pid, windowSession uint32
		user32.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&pid)))
		if windows.ProcessIdToSessionId(pid, &windowSession) != nil || windowSession != session {
			return 1
		}
		var result uintptr
		ok, _, _ := user32.NewProc("SendMessageTimeoutW").Call(window, 0x11, 0, 1, 2, 5000, uintptr(unsafe.Pointer(&result)))
		if ok == 0 || result == 0 {
			closeErr = errors.New("FnCPN UI did not accept installer shutdown")
			return 0
		}
		ok, _, _ = user32.NewProc("SendMessageTimeoutW").Call(window, 0x16, 1, 1, 2, 5000, uintptr(unsafe.Pointer(&result)))
		if ok == 0 {
			closeErr = errors.New("FnCPN UI did not complete installer shutdown")
			return 0
		}
		return 1
	})
	user32.NewProc("EnumWindows").Call(callback, 0)
	if closeErr != nil {
		return closeErr
	}
	return stopClientDaemonInSession(session)
}

func CleanupNetwork(ctx context.Context) error {
	if !IsLocalSystem() {
		return errors.New("cleanup-network must run as LocalSystem")
	}
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	state := filepath.Join(programData, "FnCPN", "Service")
	return cleanupNetwork(ctx, state, func(ctx context.Context) error {
		// A fresh installer process owns no live WireGuard runtime to stop.
		return platformwindows.NewNetworkConfigurator(state).Recover(ctx)
	})
}

func cleanupNetwork(ctx context.Context, state string, recover func(context.Context) error) error {
	if err := PrepareDataDirectories(state, filepath.Join(state, "Logs", "service.log")); err != nil {
		return fmt.Errorf("secure privileged cleanup data directories: %w", err)
	}
	return recover(ctx)
}

func StopAllClientDaemons() error {
	if !IsLocalSystem() {
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
	return stopClientDaemonAt(ClientSocketForSession(sessionID))
}

func stopClientDaemonAt(socket string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	return ipc.StopDaemonForMaintenance(ctx, socket)
}
