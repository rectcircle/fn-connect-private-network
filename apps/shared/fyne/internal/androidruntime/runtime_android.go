//go:build android

// Package androidruntime owns Android's service lifecycle, independently of UI.
package androidruntime

/*
#include <stdlib.h>
char *fncpn_android_rpc(const char *request);
*/
import "C"

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/platform/android"
	"github.com/rectcircle/fn-connect-private-network/internal/support"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/uifeedback"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/uiperf"
	"golang.org/x/sys/unix"
)

type system struct{}

func (system) Call(method string, params, result any) error {
	data, err := json.Marshal(map[string]any{"version": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	request := C.CString(string(data))
	defer C.free(unsafe.Pointer(request))
	reply := C.fncpn_android_rpc(request)
	if reply == nil {
		return errors.New("Android system bridge is unavailable")
	}
	defer C.free(unsafe.Pointer(reply))
	return androidvpn.DecodeReply([]byte(C.GoString(reply)), result)
}

var state struct {
	sync.Mutex
	host         *clienthost.Host
	network      *androidvpn.Network
	notifyCancel context.CancelFunc
	log          *logging.RotatingWriter
	engine       *androidvpn.Engine
	stopping     bool
}

//export fncpnStart
func fncpnStart(directory *C.char) *C.char {
	state.Lock()
	defer state.Unlock()
	if state.host != nil {
		return C.CString("")
	}
	if state.stopping {
		return C.CString("Android service is still stopping; reopen the app")
	}
	sys := system{}
	writer, err := logging.Open(filepath.Join(C.GoString(directory), "logs/client.log"), 2<<20, 2)
	if err != nil {
		return C.CString("Cannot open Android client log")
	}
	keepLog := false
	defer func() {
		if !keepLog {
			writer.Close()
			client.SetAndroidSocketProtector(nil)
			client.SetAndroidHostResolver(nil)
		}
	}()
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).With("platform", "android")
	network := &androidvpn.Network{System: sys}
	client.SetAndroidSocketProtector(network.Protect)
	client.SetAndroidHostResolver(network.Resolve)
	engine := &androidvpn.Engine{System: sys, Logger: logger, AdoptFD: func(fd int) (*os.File, error) {
		if fd < 0 {
			return nil, errors.New("invalid TUN descriptor")
		}
		if err := unix.SetNonblock(fd, true); err != nil {
			unix.Close(fd)
			return nil, err
		}
		return os.NewFile(uintptr(fd), "android-vpn"), nil
	}}
	var deviceName string
	if err := sys.Call("device.name", nil, &deviceName); err != nil || deviceName == "" {
		deviceName = "Android device"
	}
	store := client.NewConfigStore(filepath.Join(C.GoString(directory), "client.json"), androidvpn.Secrets{System: sys})
	manager, err := client.NewManager(client.ManagerOptions{Store: store, Discoverer: client.DiscoveryClient{}, Logger: logger,
		Remote: func(id string, cookies []client.Cookie, onCookies func([]client.Cookie) error) (client.RemoteService, error) {
			url, err := client.DefaultRemoteURL(id)
			if err != nil {
				return nil, err
			}
			return client.NewRemoteClient(url, cookies, nil, onCookies)
		},
		Privileged: engine, Bridge: &client.RelayBridge{Logger: logger}, Probe: network, Monitor: network, DeviceName: deviceName,
		IPv6RouteChecker: network.HasIPv6Route,
	})
	if err != nil {
		return C.CString(err.Error())
	}
	host, err := clienthost.New(manager)
	if err == nil {
		err = host.Start(context.Background())
	}
	if err != nil {
		return C.CString(err.Error())
	}
	state.host, state.network = host, network
	state.engine = engine
	state.log = writer
	keepLog = true
	ctx, cancel := context.WithCancel(context.Background())
	state.notifyCancel = cancel
	go func() {
		var generation uint64
		for {
			result := manager.WatchStatus(ctx, generation)
			if ctx.Err() != nil {
				return
			}
			generation = result.Generation
			_ = sys.Call("notification", map[string]string{"text": string(result.Status.State) + " " + result.Status.Path}, nil)
		}
	}()
	return C.CString("")
}

//export fncpnStop
func fncpnStop(revoked C.int) {
	state.Lock()
	host := state.host
	engine := state.engine
	state.engine = nil
	state.stopping = true
	cancel := state.notifyCancel
	writer := state.log
	state.log = nil
	state.host = nil
	state.network = nil
	state.Unlock()
	if cancel != nil {
		cancel()
	}
	if host == nil {
		state.Lock()
		state.stopping = false
		state.Unlock()
		return
	}
	if engine != nil {
		engine.PrepareShutdown()
	}
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	if revoked != 0 {
		_ = host.Call(ctx, client.MethodDisconnect, nil, nil)
	}
	if err := host.Close(ctx); err != nil {
		if writer != nil {
			slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).Error("Android service cleanup failed", "error", model.SafeText(err.Error()))
		}
	}
	if writer != nil {
		writer.Close()
	}
	client.SetAndroidSocketProtector(nil)
	client.SetAndroidHostResolver(nil)
	state.Lock()
	state.stopping = false
	state.Unlock()
}

//export fncpnNetworkChanged
func fncpnNetworkChanged() {
	state.Lock()
	network := state.network
	state.Unlock()
	if network != nil {
		network.Notify()
	}
}

//export fncpnCommand
func fncpnCommand(method, params *C.char) *C.char {
	var result json.RawMessage
	name := C.GoString(method)
	payload := json.RawMessage(C.GoString(params))
	err := Call(context.Background(), name, payload, &result)
	reply := map[string]any{"version": 1, "ok": err == nil, "result": result}
	if err != nil {
		reply["error"] = model.PublicError(err)
	}
	encoded, _ := json.Marshal(reply)
	return C.CString(string(encoded))
}

// Call is the UI command port. Service creation is delegated to Java, and all
// state remains owned by that Service even when Fyne has no active window.
func Call(ctx context.Context, method string, params, result any) error {
	switch method {
	case client.MethodConnect, client.MethodDisconnect, client.MethodRetry, client.MethodAuthorizationBegin:
		if err := (system{}).Call("service.ensure", nil, nil); err != nil {
			return err
		}
	case client.MethodShutdown:
		return errors.New("only Android may stop the service")
	}
	state.Lock()
	host := state.host
	stopping := state.stopping
	state.Unlock()
	if stopping {
		return errors.New("Android service is stopping; retry after cleanup")
	}
	if host == nil {
		if method == client.MethodStatus {
			var saved struct {
				Configured  bool `json:"configured"`
				AutoConnect bool `json:"autoConnect"`
			}
			if err := (system{}).Call("settings.status", nil, &saved); err != nil {
				return err
			}
			if saved.Configured && saved.AutoConnect {
				if err := (system{}).Call("service.ensure", nil, nil); err != nil {
					return err
				}
				state.Lock()
				restored := state.host
				state.Unlock()
				if restored != nil {
					return restored.Call(ctx, method, params, result)
				}
			}
			status := model.ClientStatus{State: model.ClientUnconfigured}
			if saved.Configured {
				status.State = model.ClientPaused
			}
			if result == nil {
				return nil
			}
			data, err := json.Marshal(status)
			if err != nil {
				return err
			}
			return json.Unmarshal(data, result)
		}
		if method == client.MethodForget {
			return (system{}).Call("settings.forget", nil, nil)
		}
		return model.NewError(model.ErrorUnavailable, "Android VPN service is not running; connect or sign in first", true)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := host.Call(ctx, method, params, result); err != nil {
		return err
	}
	if method == client.MethodForget {
		return (system{}).Call("secret.reset", nil, nil)
	}
	return nil
}

func OpenAdmin(url string) error {
	return (system{}).Call("ui.admin", map[string]string{"url": url}, nil)
}
func Share(text string) error {
	return (system{}).Call("ui.share", map[string]string{"text": text}, nil)
}

// ExportSupport is an explicit UI action; only allowlisted diagnostics leave
// app-private storage. It works even if the connection host has stopped.
func ExportSupport(report string) error {
	var logs map[string]string
	if err := (system{}).Call("support.read", nil, &logs); err != nil {
		return err
	}
	names := []string{"client.log.2", "client.log.1", "client.log", "android.log.1", "android.log"}
	files := make([]map[string]any, 0, len(names))
	for _, name := range names {
		if text, ok := logs[name]; ok {
			files = append(files, map[string]any{"name": name, "size": len(text)})
		}
	}
	metadata, _ := json.Marshal(map[string]any{"format": 1, "capturedAt": time.Now().UTC(), "platform": "android", "files": files})
	var data bytes.Buffer
	err := support.Archive(&data, []byte(report), logs, metadata,
		"FnCPN diagnostics and log snapshots / FnCPN 诊断与日志快照\n\nSee snapshot.json for capture time (UTC). client.log: shared daemon; android.log: Android VPN adapter. Exported logs are filtered.\n采集时间（UTC）见 snapshot.json。client.log：共享客户端；android.log：Android VPN 适配层。导出日志已脱敏。\n",
		names)
	if err != nil {
		return err
	}
	return (system{}).Call("ui.export", map[string]string{"archive": base64.StdEncoding.EncodeToString(data.Bytes())}, nil)
}

func OpenLogs() error { return (system{}).Call("ui.logs", nil, nil) }
func OpenBrowser(url string) error {
	return (system{}).Call("ui.browser", map[string]string{"url": url}, nil)
}
func Exit() error { return (system{}).Call("ui.exit", nil, nil) }

//export fncpnBack
func fncpnBack() { dispatchBack() }

//export fncpnImeSubmit
func fncpnImeSubmit() { dispatchKeyboardSubmit() }

func Background() error        { return (system{}).Call("ui.background", nil, nil) }
func OpenNAS(url string) error { return (system{}).Call("ui.nas", map[string]string{"url": url}, nil) }

//export fncpnFeedback
func fncpnFeedback(text *C.char) { uifeedback.Publish(C.GoString(text)) }

//export fncpnUIProfile
func fncpnUIProfile(directory *C.char) {
	path := C.GoString(directory)
	go uiperf.Capture(path, 45*time.Second)
}
