//go:build !darwin && !linux

package ipc

import (
	"errors"
	"net"
)

func IdentifyPeer(_ *net.UnixConn) (PeerIdentity, error) {
	return PeerIdentity{}, errors.New("peer credentials are unsupported on this platform")
}
