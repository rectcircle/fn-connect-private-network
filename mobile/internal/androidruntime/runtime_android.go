//go:build android

// Package androidruntime owns Android's service lifecycle, independently of UI.
package androidruntime

/*
#include <stdlib.h>
char *fncpn_android_rpc(const char *request);
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/androidvpn"
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
	logger := slog.New(slog.NewJSONHandler(writer, nil)).With("platform", "android")
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
	store := client.NewConfigStore(filepath.Join(C.GoString(directory), "client.json"), androidvpn.Secrets{System: sys})
	manager, err := client.NewManager(client.ManagerOptions{Store: store, Discoverer: client.DiscoveryClient{}, Logger: logger,
		Remote: func(id string, cookies []client.Cookie, onCookies func([]client.Cookie) error) (client.RemoteService, error) {
			url, err := client.DefaultRemoteURL(id)
			if err != nil {
				return nil, err
			}
			return client.NewRemoteClient(url, cookies, nil, onCookies)
		},
		Privileged: engine, Bridge: &client.RelayBridge{Logger: logger}, Probe: network, Monitor: network, DeviceName: "Android",
		IPv6RouteChecker: func(context.Context, netip.Addr, int) bool {
			s, err := network.Snapshot()
			return err == nil && s.HasPublicIPv6
		},
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
			slog.New(slog.NewJSONHandler(writer, nil)).Error("Android service cleanup failed", "error", model.SafeText(err.Error()))
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
	err := Call(context.Background(), C.GoString(method), json.RawMessage(C.GoString(params)), &result)
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
	case client.MethodConnect, client.MethodRetry, client.MethodAuthorizationBegin:
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
