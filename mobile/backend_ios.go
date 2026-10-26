//go:build ios

package main

import (
	"context"
	"fyne.io/fyne/v2"
	"time"

	"github.com/rectcircle/fn-connect-private-network/mobile/internal/iosui"
)

type iosClient struct{}

func (iosClient) SetKeyboardAction(action int) { iosui.SetKeyboardAction(action) }

func (iosClient) SetBackHandler(handler func())      { iosui.SetBackHandler(handler) }
func (iosClient) SetBackGestureEnabled(enabled bool) { iosui.SetBackGestureEnabled(enabled) }

func (iosClient) Resume(ctx context.Context) error {
	if iosui.Simulator() {
		return nil // Simulator host already resumes through the shared Manager.
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	for {
		var state struct {
			Pending bool `json:"pending"`
			Running bool `json:"running"`
		}
		if err := iosui.Call(ctx, "ios-resume", nil, &state); err != nil {
			return err
		}
		if state.Running {
			// NEVPNStatus can lag a provider crash. Require a live response,
			// rather than treating the cached system status as a recovered host.
			if err := iosui.Call(ctx, "status", nil, nil); err == nil {
				return nil
			}
		} else if !state.Pending {
			return nil // No existing enabled configuration; do not create one.
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// iOS owns application lifetime; Fyne Quit closes the canvas without exiting.
func (iosClient) CanExit() bool { return false }

func platformClient() daemonClient { return iosClient{} }
func (iosClient) Call(ctx context.Context, method string, params, result any) error {
	return iosui.Call(ctx, method, params, result)
}
func (iosClient) OpenAdmin(url string) error   { return iosui.Open(url, true) }
func (iosClient) OpenNAS(url string) error     { return iosui.Open(url, false) }
func (iosClient) OpenBrowser(url string) error { return iosui.Open(url, false) }
func (iosClient) Share(text string) error      { return iosui.Share(text) }
func (iosClient) NetworkDebugAvailable() bool  { return iosui.Simulator() }
func (iosClient) NetworkProbe(ctx context.Context, target string) (string, error) {
	return iosui.NetworkProbe(ctx, target)
}
func (iosClient) ExportSupport(text string) error {
	return iosui.ExportSupport(text)
}

func (iosClient) EnvironmentNotice() string {
	if iosui.Simulator() {
		return tr("模拟器应用内隧道调试：真实登录与通信，仅本 App 请求进入隧道；系统 VPN 需真机验证。")
	}
	return ""
}

func (iosClient) OpenLogs() error {
	return iosui.OpenLogs()
}

func (iosClient) NativeNavigation() bool                { return iosui.NativeNavigation() }
func (iosClient) SetNavigationHandler(handler func())   { iosui.SetNavigationHandler(handler) }
func (iosClient) SetNavigationState(back, enabled bool) { iosui.SetNavigationState(back, enabled) }

func (iosClient) SetRailMenu(labels []string, icons []fyne.Resource, handler func(int)) {
	iosui.SetRailMenu(labels, icons, handler)
}
func (iosClient) SetRailAdmin(enabled bool) { iosui.SetRailAdmin(enabled) }

func (iosClient) SetRailExpanded(expanded bool) { iosui.SetRailExpanded(expanded) }
