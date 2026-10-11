//go:build windows

package privileged

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"golang.org/x/sys/windows"
)

func defaultClientUID() int { return -1 }

func AuthorizePeer(role string, _ int) ipc.Authorizer {
	return func(_ context.Context, identity ipc.PeerIdentity) error {
		if role != "client" {
			return ipc.RejectPeer("Windows service pipe requires an interactive client SID")
		}
		activeSession := windows.WTSGetActiveConsoleSessionId()
		if activeSession == 0xffffffff {
			return ipc.RejectPeer("only the active local console session may use the service")
		}
		var activeUser windows.Token
		if err := windows.WTSQueryUserToken(activeSession, &activeUser); err != nil {
			return ipc.RejectPeer("cannot verify active local console user")
		}
		defer activeUser.Close()
		user, err := activeUser.GetTokenUser()
		if err != nil {
			return ipc.RejectPeer("cannot verify active local console user")
		}
		if err := validateWindowsClientPeer(identity, activeSession, user.User.Sid.String()); err != nil {
			return ipc.RejectPeer("only the active local console user may use the service")
		}
		return nil
	}
}

func validateWindowsClientPeer(identity ipc.PeerIdentity, activeSession uint32, activeSID string) error {
	if identity.PID <= 0 || identity.SID == "" || activeSID == "" {
		return ipc.RejectPeer("Windows service pipe requires a verifiable interactive client identity")
	}
	if activeSession == 0xffffffff || identity.SessionID != activeSession {
		return ipc.RejectPeer("only the active local console session may use the service")
	}
	if identity.SID != activeSID {
		return ipc.RejectPeer("only the active local console user may use the service")
	}
	return nil
}

func ValidateRole(role string) error {
	if role != "client" {
		return ipc.RejectPeer("Windows only supports the client privileged service")
	}
	return nil
}
