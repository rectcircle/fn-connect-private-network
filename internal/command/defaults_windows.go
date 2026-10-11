//go:build windows

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/windowsenv"
	"golang.org/x/sys/windows"
)

func defaultClientSocket() string {
	session, _ := currentWindowsSession()
	return defaultClientSocketForSession(session)
}

func defaultClientSocketForSession(session uint32) string {
	return `\\.\pipe\FnCPN.Client.v1.` + strconv.FormatUint(uint64(session), 10)
}

func defaultPrivilegedSocket(_ string) string {
	return `\\.\pipe\FnCPN.Service.v1`
}

func defaultPrivilegedPipeSecurityDescriptor(role string) string {
	if role == "client" {
		return ipc.InteractivePipeSecurityDescriptor()
	}
	return ""
}

func defaultClientPipeAuthorizer() ipc.Authorizer {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return func(context.Context, ipc.PeerIdentity) error { return fmt.Errorf("read daemon user SID: %w", err) }
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return func(context.Context, ipc.PeerIdentity) error { return fmt.Errorf("read daemon session: %w", err) }
	}
	userSID := user.User.Sid.String()
	return func(_ context.Context, peer ipc.PeerIdentity) error {
		if peer.SID == "S-1-5-18" && peer.SessionID == 0 && peer.PID > 0 {
			return nil
		}
		if peer.SID != userSID || peer.SessionID != session {
			return ipc.RejectPeer("only the current user in this Windows session may use the client daemon")
		}
		return nil
	}
}

func defaultPrivilegedStateDirectory(_ string) string {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "FnCPN", "Service")
}

func defaultClientConfig() string {
	if directory := defaultClientDataDirectory(); directory != "" {
		return filepath.Join(directory, "config.json")
	}
	return ""
}

func defaultClientDataDirectory() string {
	local, err := windowsenv.LocalAppDataDirectory()
	if err != nil {
		return ""
	}
	return filepath.Join(local, "FnCPN")
}

func defaultClientLog() string {
	if directory := defaultClientDataDirectory(); directory != "" {
		return filepath.Join(directory, "Logs", "client.log")
	}
	return ""
}

func defaultPrivilegedLog(_ string) string {
	return filepath.Join(defaultPrivilegedStateDirectory("client"), "Logs", "service.log")
}

func currentWindowsSession() (uint32, error) {
	var session uint32
	err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return session, err
}
