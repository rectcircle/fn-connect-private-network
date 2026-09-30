//go:build darwin

package ipc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func IdentifyPeer(connection *net.UnixConn) (PeerIdentity, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return PeerIdentity{}, fmt.Errorf("get peer socket: %w", err)
	}
	var (
		identity  PeerIdentity
		socketErr error
	)
	if err := raw.Control(func(fd uintptr) {
		credential, err := unix.GetsockoptXucred(
			int(fd),
			unix.SOL_LOCAL,
			unix.LOCAL_PEERCRED,
		)
		if err != nil {
			socketErr = err
			return
		}
		pid, err := unix.GetsockoptInt(
			int(fd),
			unix.SOL_LOCAL,
			unix.LOCAL_PEERPID,
		)
		if err != nil {
			socketErr = err
			return
		}
		identity = PeerIdentity{
			UID: credential.Uid,
			GID: credential.Groups[0],
			PID: pid,
		}
	}); err != nil {
		return PeerIdentity{}, fmt.Errorf("inspect peer socket: %w", err)
	}
	if socketErr != nil {
		return PeerIdentity{}, fmt.Errorf("read peer credential: %w", socketErr)
	}
	return identity, nil
}
