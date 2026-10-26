//go:build !android && !ios

package main

import (
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"os"
)

func platformClient() daemonClient {
	if socket := os.Getenv("FNCPN_CLIENT_SOCKET"); socket != "" {
		return ipc.Client{SocketPath: socket}
	}
	return unavailableClient{reason: tr("桌面预览：设置 FNCPN_CLIENT_SOCKET 可连接现有 daemon。")}
}
