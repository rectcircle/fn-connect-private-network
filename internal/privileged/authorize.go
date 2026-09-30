package privileged

import (
	"context"
	"fmt"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func AuthorizePeer(role string, allowedUID int) ipc.Authorizer {
	return func(_ context.Context, identity ipc.PeerIdentity) error {
		if identity.UID == 0 {
			return nil
		}
		targetUID := allowedUID
		if targetUID < 0 && role == "client" {
			targetUID = defaultClientUID()
		}
		if targetUID < 0 ||
			identity.PID <= 0 ||
			identity.UID != uint32(targetUID) {
			return ipc.RejectPeer("unauthorized caller")
		}
		return nil
	}
}

func ValidateRole(role string) error {
	if role != "client" && role != "server" {
		return fmt.Errorf("invalid privileged role %q", role)
	}
	return nil
}
