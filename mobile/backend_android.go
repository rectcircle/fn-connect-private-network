//go:build android

package main

import (
	"context"

	"github.com/rectcircle/fn-connect-private-network/mobile/internal/androidruntime"
)

type androidClient struct{}

func (androidClient) SetKeyboardAction(action int) { androidruntime.SetKeyboardAction(action) }
func (androidClient) SetKeyboardSubmitHandler(handler func()) {
	androidruntime.SetKeyboardSubmitHandler(handler)
}

func (androidClient) OpenAdmin(url string) error      { return androidruntime.OpenAdmin(url) }
func (androidClient) Share(text string) error         { return androidruntime.Share(text) }
func (androidClient) ExportSupport(text string) error { return androidruntime.ExportSupport(text) }

func (androidClient) Call(ctx context.Context, method string, params, result any) error {
	return androidruntime.Call(ctx, method, params, result)
}
func platformClient() daemonClient {
	return androidClient{}
}

func (androidClient) OpenLogs() error              { return androidruntime.OpenLogs() }
func (androidClient) OpenBrowser(url string) error { return androidruntime.OpenBrowser(url) }
func (androidClient) Exit() error                  { return androidruntime.Exit() }

func (androidClient) SetBackHandler(handler func()) { androidruntime.SetBackHandler(handler) }

func (androidClient) Background() error        { return androidruntime.Background() }
func (androidClient) OpenNAS(url string) error { return androidruntime.OpenNAS(url) }
