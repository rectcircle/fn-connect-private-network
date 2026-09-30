//go:build !darwin || !cgo

package platform

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type pollOnlyNetworkMonitor struct{}

func NewNetworkMonitor() pollOnlyNetworkMonitor {
	return pollOnlyNetworkMonitor{}
}

func (pollOnlyNetworkMonitor) Events(context.Context) <-chan model.NetworkChange {
	return nil
}
