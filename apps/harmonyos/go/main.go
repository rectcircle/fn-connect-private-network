//go:build openharmony

package main

/*
#include "bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	platform "github.com/rectcircle/fn-connect-private-network/internal/platform/harmonyos"
	"golang.org/x/sys/unix"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unsafe"
)

type system struct{}

func (system) Call(method string, params, result any) error {
	raw, err := json.Marshal(map[string]any{"version": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	request := C.CString(string(raw))
	defer C.free(unsafe.Pointer(request))
	response := C.harmony_system_call(request)
	if response == nil {
		return errors.New("HarmonyOS system adapter unavailable")
	}
	defer C.free(unsafe.Pointer(response))
	return platform.DecodeReply([]byte(C.GoString(response)), result)
}

type hostState struct {
	sync.Mutex
	host     *clienthost.Host
	engine   *platform.Engine
	network  *platform.Network
	listener net.Listener
	log      io.Closer
}

var state hostState
var lifecycle sync.Mutex

func start(directory string, applicationOnly bool) error {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	state.Lock()
	defer state.Unlock()
	if state.host != nil {
		return nil
	}
	if !filepath.IsAbs(directory) {
		return errors.New("absolute application directory required")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	writer, err := logging.Open(filepath.Join(directory, "logs/client.log"), 2<<20, 2)
	if err != nil {
		return err
	}
	keep := false
	var activeHost *clienthost.Host
	defer func() {
		if !keep {
			if activeHost != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				activeHost.Close(ctx)
				cancel()
			}
			client.SetHarmonyOSSocketProtector(nil)
			client.SetHarmonyOSHostResolver(nil)
			writer.Close()
		}
	}()
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).With("platform", "harmonyos")
	sys := system{}
	network := &platform.Network{System: sys}
	client.SetHarmonyOSSocketProtector(network.Protect)
	client.SetHarmonyOSHostResolver(network.Resolve)
	engine := &platform.Engine{System: sys, Logger: logger, ApplicationOnly: applicationOnly, AdoptFD: func(fd int) (*os.File, error) {
		if fd < 0 {
			return nil, errors.New("invalid TUN fd")
		}
		owned, err := unix.Dup(fd)
		if err != nil {
			return nil, err
		}
		unix.CloseOnExec(owned)
		if err = unix.SetNonblock(owned, true); err != nil {
			unix.Close(owned)
			return nil, err
		}
		return os.NewFile(uintptr(owned), "harmony-vpn"), nil
	}}
	store := client.NewConfigStore(filepath.Join(directory, "client.json"), platform.Secrets{System: sys})
	var dnsHTTP *http.Client
	if applicationOnly {
		dnsHTTP = &http.Client{Transport: &http.Transport{DialContext: engine.DialPrivate}, Timeout: 3 * time.Second}
	}
	var deviceName string
	if err := sys.Call("device.name", nil, &deviceName); err != nil || deviceName == "" {
		deviceName = "HarmonyOS device"
	}
	manager, err := client.NewManager(client.ManagerOptions{DNSHTTPClient: dnsHTTP, Store: store, Logger: logger, Discoverer: client.DiscoveryClient{}, Remote: func(id string, cookies []client.Cookie, onCookies func([]client.Cookie) error) (client.RemoteService, error) {
		endpoint, err := client.DefaultRemoteURL(id)
		if err != nil {
			return nil, err
		}
		return client.NewRemoteClient(endpoint, cookies, nil, onCookies)
	}, Privileged: engine, Bridge: &client.RelayBridge{Logger: logger}, Probe: network, Monitor: network, DeviceName: deviceName, IPv6RouteChecker: network.HasIPv6Route})
	if err != nil {
		return err
	}
	host, err := clienthost.New(manager)
	if err != nil {
		return err
	}
	activeHost = host
	if err = host.Start(context.Background()); err != nil {
		return err
	}
	socket := filepath.Join(directory, "control.sock")
	if info, e := os.Lstat(socket); e == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("invalid control socket path")
		}
		os.Remove(socket)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		host.Close(context.Background())
		return err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		listener.Close()
		host.Close(context.Background())
		return err
	}
	state.host, state.engine, state.network, state.listener, state.log = host, engine, network, listener, writer
	keep = true
	go serve(listener, host)
	return nil
}
func stop() error {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	state.Lock()
	host, engine, listener, writer := state.host, state.engine, state.listener, state.log
	state.Unlock()
	if host == nil {
		return nil
	}
	engine.PrepareShutdown()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := host.Close(ctx)
	if err != nil {
		return err
	}
	writer.Close()
	client.SetHarmonyOSSocketProtector(nil)
	client.SetHarmonyOSHostResolver(nil)
	state.Lock()
	state.host = nil
	state.engine = nil
	state.network = nil
	state.listener = nil
	state.log = nil
	state.Unlock()
	return nil
}

type request struct {
	Version   int             `json:"version"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	Directory string          `json:"directory,omitempty"`
}

func reply(result any, err error) []byte {
	if err != nil {
		raw, _ := json.Marshal(map[string]any{"version": 1, "ok": false, "error": model.AsError(err)})
		return raw
	}
	raw, _ := json.Marshal(map[string]any{"version": 1, "ok": true, "result": result})
	return raw
}
func serve(listener net.Listener, host *clienthost.Host) {
	slots := make(chan struct{}, 16)
	for {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			connection.Close()
			continue
		}
		go func() {
			defer func() { <-slots; connection.Close() }()
			connection.SetDeadline(time.Now().Add(40 * time.Second))
			raw, err := readFrame(connection)
			if err != nil {
				return
			}
			var r request
			if json.Unmarshal(raw, &r) != nil || r.Version != 1 {
				writeFrame(connection, reply(nil, errors.New("local protocol mismatch")))
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			var result any
			if r.Method == "export-support" {
				var diagnostic model.ClientDiagnostics
				err = host.Call(ctx, client.MethodDiagnose, nil, &diagnostic)
				if err == nil {
					encoded, _ := json.Marshal(diagnostic)
					result, err = exportSupport(filepath.Dir(listener.Addr().String()), encoded)
				}
			} else if r.Method == "nas-web-url" || r.Method == "application-probe" {
				var input struct {
					Address string `json:"address"`
					Target  string `json:"target"`
				}
				err = json.Unmarshal(r.Params, &input)
				if err == nil {
					state.Lock()
					engine := state.engine
					state.Unlock()
					if r.Method == "application-probe" {
						result, err = applicationProbe(ctx, engine, input.Target)
					} else {
						transport := &http.Transport{Proxy: nil}
						if engine != nil && engine.ApplicationOnly {
							transport.DialContext = engine.DialApplication
						}
						result, err = resolveNASWebURL(ctx, input.Address, transport)
						transport.CloseIdleConnections()
					}
				}
			} else {
				err = host.Call(ctx, r.Method, r.Params, &result)
			}
			writeFrame(connection, reply(result, err))
		}()
	}
}
func call(raw []byte) ([]byte, error) {
	var r request
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	if r.Version != 1 || !filepath.IsAbs(r.Directory) {
		return nil, errors.New("invalid local request")
	}
	connection, err := net.DialTimeout("unix", filepath.Join(r.Directory, "control.sock"), time.Second)
	if err != nil {
		if r.Method == "export-support" {
			archive, e := exportSupport(r.Directory, []byte("{}"))
			return reply(archive, e), nil
		}
		return nil, model.NewError(model.ErrorUnavailable, "VPN service unavailable; authorize VPN and retry", true)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(40 * time.Second))
	if err = writeFrame(connection, raw); err != nil {
		return nil, err
	}
	return readFrame(connection)
}

//export HarmonyHost
func HarmonyHost(action C.int, input *C.char) *C.char {
	raw := []byte(C.GoString(input))
	if len(raw) > 256*1024 {
		return C.CString(string(reply(nil, errors.New("message too large"))))
	}
	var result any
	var err error
	switch int(action) {
	case 0:
		err = start(string(raw), false)
	case 5:
		if !filepath.IsAbs(string(raw)) {
			err = errors.New("absolute application directory required")
			break
		}
		var config *client.LocalConfig
		store := client.NewConfigStore(filepath.Join(string(raw), "client.json"), platform.Secrets{System: system{}})
		config, err = store.Load()
		if config != nil {
			result = map[string]bool{"configured": true, "autoConnect": config.AutoConnect}
		} else {
			result = map[string]bool{"configured": false, "autoConnect": false}
		}
	case 4:
		err = start(string(raw), true)
	case 1:
		var response []byte
		response, err = call(raw)
		if err == nil {
			return C.CString(string(response))
		}
	case 2:
		err = stop()
	case 3:
		state.Lock()
		network := state.network
		state.Unlock()
		if network != nil {
			network.Notify()
		}
	default:
		err = errors.New("unknown host operation")
	}
	return C.CString(string(reply(result, err)))
}

//export HarmonyFree
func HarmonyFree(value *C.char) { C.free(unsafe.Pointer(value)) }
