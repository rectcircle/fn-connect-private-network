//go:build windows

package command

import "github.com/rectcircle/fn-connect-private-network/internal/ipc"

func privilegedIPCClient(socket string) ipc.Client {
	return ipc.Client{
		SocketPath:   socket,
		VerifyServer: ipc.VerifyLocalSystemServiceServer(windowsClientServiceName),
	}
}
