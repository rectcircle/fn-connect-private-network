//go:build android

package main

import (
	"context"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/androidruntime"
)

type androidClient struct{}

func (androidClient) OpenAdmin(url string) error { return androidruntime.OpenAdmin(url) }
func (androidClient) Share(text string) error    { return androidruntime.Share(text) }

func (androidClient) Call(ctx context.Context, method string, params, result any) error {
	return androidruntime.Call(ctx, method, params, result)
}
func platformClient() daemonClient {
	return androidClient{}
}
