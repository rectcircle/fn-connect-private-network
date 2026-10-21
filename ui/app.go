package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Reply preserves structured daemon failures, including access-code and OTP challenges.
type Reply struct {
	Result map[string]any `json:"result,omitempty"`
	Error  *model.Error   `json:"error,omitempty"`
}
type App struct {
	ctx                context.Context
	cancel             context.CancelFunc
	client             ipc.Client
	mu                 sync.Mutex
	started            bool
	pendingURL         string
	domReady           bool
	privilegedNotified bool
	startupError       string
	smokeResult        string
}

var desktopApp *App

func NewApp() *App {
	return &App{client: ipc.Client{SocketPath: uiSocketPath()}}
}
func (a *App) startup(ctx context.Context) {
	a.ctx, a.cancel = context.WithCancel(ctx)
	desktopApp = a
	nativeInit()
	// Package smoke tests never start services or request privileges.
	if a.smokeResult != "" {
		return
	}
	go func() {
		if err := a.startDaemon(); err != nil {
			a.mu.Lock()
			a.startupError = err.Error()
			a.mu.Unlock()
			runtime.EventsEmit(a.ctx, "daemon-error", err.Error())
		}
		a.watch()
	}()
}
func (a *App) ready(context.Context) {
	if a.smokeResult != "" {
		return
	}
	for _, arg := range platformArguments() {
		a.handleURL(arg)
	}
}

// Ready is called only after vanilla JS has registered its event listeners.
// DOM-ready alone is too early for initial Linux URL scheme activation.
func (a *App) Ready() {
	if a.smokeResult != "" {
		a.finishSmoke()
		return
	}
	a.mu.Lock()
	a.domReady = true
	pending := a.pendingURL
	a.pendingURL = ""
	a.mu.Unlock()
	if pending != "" {
		a.handleURL(pending)
	}
}

func (a *App) shutdown(context.Context) {
	a.cancel()
	a.mu.Lock()
	started := a.started
	a.mu.Unlock()
	if started {
		a.stopPlatformDaemon()
	}

	nativeDispose()
}
func (a *App) startDaemon() error {
	if uiPlatform() == "linux" {
		return a.startPlatformDaemon()
	}
	conn, err := net.DialTimeout("unix", a.client.SocketPath, time.Second)
	if err == nil {
		conn.Close()
		return nil
	}
	return a.startPlatformDaemon()
}

var allowedMethods = map[string]bool{"diagnose": true, "status": true, "connect": true, "disconnect": true, "retry": true, "continue-remote": true, "authorization-begin": true, "authorization-cancel": true, "authorize-native": true, "detect-access-code": true}

func (a *App) Call(method string, params map[string]any) Reply {
	if a.smokeResult != "" {
		return Reply{Error: model.NewError(model.ErrorUnavailable, "UI smoke test runs without client services", false)}
	}
	if !allowedMethods[method] {
		return Reply{Error: model.NewError(model.ErrorInvalidArgument, "unsupported UI operation", false)}
	}
	client := a.client
	if method == "authorize-native" {
		client.Timeout = 310 * time.Second
	} else if method == "connect" || method == "retry" {
		client.Timeout = 45 * time.Second
	}
	var result map[string]any
	if err := client.Call(a.ctx, method, params, &result); err != nil {
		return Reply{Error: model.PublicError(err)}
	}
	if method == "diagnose" {
		if available, ok := result["privilegedAvailable"].(bool); ok && !available {
			a.mu.Lock()
			notify := !a.privilegedNotified
			a.privilegedNotified = true
			a.mu.Unlock()
			if notify {
				nativeNotifyRoot()
			}
		}
	}
	return Reply{Result: result}
}
func (a *App) watch() {
	var generation uint64
	for a.ctx.Err() == nil {
		var result struct {
			Generation uint64 `json:"generation"`
		}
		client := a.client
		client.Timeout = 310 * time.Second
		err := client.Call(a.ctx, "watch-client-status", map[string]any{"after": generation}, &result)
		if err == nil {
			generation = result.Generation
			runtime.EventsEmit(a.ctx, "status-changed")
		}
		if err != nil {
			generation = 0
			select {
			case <-a.ctx.Done():
				return
			case <-time.After(2 * time.Second):
				runtime.EventsEmit(a.ctx, "status-changed")
			}
		}
	}
}
func (a *App) Info() map[string]any {
	a.mu.Lock()
	startupError := a.startupError
	a.mu.Unlock()
	return map[string]any{"startupError": startupError, "version": version.Current, "autoStart": nativeAutoStart(), "language": nativeLanguage(), "symbols": nativeSymbols(), "platform": uiPlatform()}
}
func (a *App) SetAutoStart(enabled bool) string { return nativeSetAutoStart(enabled) }
func (a *App) UpdateTray(state, title, action string, needsLogin, busy bool) {
	nativeTray(state, title, action, needsLogin, busy)
}
func (a *App) OpenAdmin() Reply {
	var result map[string]any
	if err := a.client.Call(a.ctx, "admin-proxy", nil, &result); err != nil {
		return Reply{Error: model.PublicError(err)}
	}
	address, _ := result["url"].(string)
	if err := validateAdminURL(address); err != nil {
		return Reply{Error: model.NewError(model.ErrorProtocol, err.Error(), false)}
	}
	nativeAdmin(address)
	return Reply{Result: map[string]any{}}
}
func validateAdminURL(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Hostname() != "127.0.0.1" || u.Port() == "" {
		return fmt.Errorf("管理代理地址无效")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1 || port > 65535 {
		return fmt.Errorf("管理代理端口无效")
	}
	return nil
}
func (a *App) OpenNAS(address string) error {
	ip := net.ParseIP(address)
	if ip == nil {
		return fmt.Errorf("NAS IP 无效")
	}
	host := ip.String()
	if ip.To4() == nil {
		host = "[" + host + "]"
	}
	runtime.BrowserOpenURL(a.ctx, "http://"+host)
	return nil
}
func (a *App) OpenLogs() error { return uiOpenLogs() }

func (a *App) Copy(text string) error { return runtime.ClipboardSetText(a.ctx, text) }
func (a *App) Quit()                  { runtime.Quit(a.ctx) }
func (a *App) handleURL(address string) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "fncpn" || u.Host != "authorize" {
		return
	}
	id := strings.Trim(u.Path, "/")
	request := u.Query().Get("request")
	if id == "" || request == "" {
		return
	}
	a.mu.Lock()
	ready := a.domReady
	if !ready {
		a.pendingURL = address
	}
	a.mu.Unlock()
	if ready {
		runtime.WindowShow(a.ctx)
		runtime.EventsEmit(a.ctx, "authorize", map[string]string{"fnId": id, "requestId": request})
	}
}
func nativeAction(action string) {
	a := desktopApp
	if a == nil {
		return
	}
	switch action {
	case "show":
		runtime.WindowShow(a.ctx)
	case "quit":
		a.Quit()
	case "homepage":
		runtime.BrowserOpenURL(a.ctx, "https://fncpn.rectcircle.cn/#donate")
	default:
		runtime.EventsEmit(a.ctx, "tray-action", action)
	}
}
