//go:build ios

// This archive is linked only into the Packet Tunnel extension, never the UI app.
package main

/*
#include "bridge.h"
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/ioslogs"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/iosvpn"
)

type system struct{}

func (system) Call(method string, params, result any) error {
	data, err := json.Marshal(map[string]any{"version": 1, "method": method, "params": params})
	if err != nil {
		return err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	output := C.fncpn_system_call(input)
	if output == nil {
		return errors.New("iOS platform callback unavailable")
	}
	defer C.free(unsafe.Pointer(output))
	var reply struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err = json.Unmarshal([]byte(C.GoString(output)), &reply); err != nil {
		return err
	}
	if !reply.OK {
		return errors.New(reply.Error)
	}
	if result != nil {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}
func (system) WritePacket(p []byte) error {
	if len(p) == 0 {
		return nil
	}
	if C.fncpn_packet_write(unsafe.Pointer(&p[0]), C.int(len(p))) != 0 {
		return errors.New("iOS packet write failed")
	}
	return nil
}

var lifecycle sync.Mutex
var platformLogger atomic.Pointer[slog.Logger]

type iosRuntime struct {
	*client.Manager
	pauseOnClose atomic.Bool
}

func (r *iosRuntime) Close(ctx context.Context) error {
	if r.pauseOnClose.Load() {
		return r.Manager.ClosePaused(ctx)
	}
	return r.Manager.Close(ctx)
}

var state struct {
	sync.Mutex
	host             *clienthost.Host
	runtime          *iosRuntime
	engine           *iosvpn.Engine
	network          *iosvpn.Network
	stopping         bool
	writer           *logging.RotatingWriter
	privilegedWriter *logging.RotatingWriter
	privilegedLogger *slog.Logger
	logs             *ioslogs.Store
}

//export fncpn_start
func fncpn_start(directory *C.char) *C.char {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	state.Lock()
	defer state.Unlock()
	if state.stopping {
		return C.CString("client is stopping")
	}
	if state.host != nil {
		return C.CString("")
	}
	writer, err := logging.Open(filepath.Join(C.GoString(directory), "logs/client.log"), 2<<20, 2)
	if err != nil {
		return C.CString("cannot open iOS client log")
	}
	keepLog := false
	defer func() {
		if !keepLog {
			writer.Close()
		}
	}()
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).With("platform", "ios")
	privilegedWriter, err := logging.Open(filepath.Join(C.GoString(directory), "logs/privileged.log"), 2<<20, 2)
	if err != nil {
		return C.CString("cannot open iOS privileged log")
	}
	defer func() {
		if !keepLog {
			privilegedWriter.Close()
		}
	}()
	privilegedLogger := slog.New(slog.NewJSONHandler(privilegedWriter, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).With("platform", "ios")
	platformLogger.Store(privilegedLogger)
	defer func() {
		if !keepLog {
			platformLogger.Store(nil)
		}
	}()
	sys := system{}
	deviceName := "iPhone / iPad"
	if err := sys.Call("device.name", nil, &deviceName); err != nil || deviceName == "" {
		deviceName = "iPhone / iPad"
	}
	for len(deviceName) > 64 {
		runes := []rune(deviceName)
		deviceName = string(runes[:len(runes)-1])
	}
	engine := iosvpn.NewEngine(sys)
	engine.Logger = privilegedLogger
	network := iosvpn.NewNetwork()
	store := client.NewConfigStore(filepath.Join(C.GoString(directory), "client.json"), iosvpn.Secrets{System: sys})
	manager, err := client.NewManager(client.ManagerOptions{Store: store, Logger: logger, DNSHTTPClient: &http.Client{Transport: &http.Transport{DialContext: engine.DialPrivate}, Timeout: 3 * time.Second}, Discoverer: client.DiscoveryClient{}, Remote: func(id string, cookies []client.Cookie, onCookies func([]client.Cookie) error) (client.RemoteService, error) {
		u, e := client.DefaultRemoteURL(id)
		if e != nil {
			return nil, e
		}
		return client.NewRemoteClient(u, cookies, nil, onCookies)
	}, Privileged: engine, Bridge: &client.RelayBridge{Logger: logger}, Probe: network, Monitor: network, DeviceName: deviceName})
	if err != nil {
		return C.CString(model.SafeText(err.Error()))
	}
	runtime := &iosRuntime{Manager: manager}
	host, err := clienthost.New(runtime)
	if err == nil {
		err = host.Start(context.Background())
	}
	if err != nil {
		return C.CString(model.SafeText(err.Error()))
	}
	state.writer = writer
	state.privilegedWriter = privilegedWriter
	state.privilegedLogger = privilegedLogger
	state.logs = ioslogs.NewStore(filepath.Join(C.GoString(directory), "logs"))
	keepLog = true
	state.host = host
	state.runtime = runtime
	state.engine = engine
	state.network = network
	return C.CString("")
}

//export fncpn_call
func fncpn_call(input *C.char) *C.char {
	var r ipc.Request
	data := []byte(C.GoString(input))
	if len(data) > 256*1024 {
		return response("", nil, errors.New("message too large"))
	}
	if err := model.DecodeStrict(data, &r); err != nil {
		return response("", nil, err)
	}
	if r.Version != ipc.ProtocolVersion {
		return response(r.ID, nil, errors.New("local protocol version mismatch"))
	}
	state.Lock()
	h := state.host
	logs := state.logs
	state.Unlock()
	if h == nil {
		return response(r.ID, nil, errors.New("client not started"))
	}
	if r.Method == "ios-log-snapshot" {
		var request ioslogs.Request
		if err := json.Unmarshal(r.Params, &request); err != nil {
			return response(r.ID, nil, err)
		}
		result, err := logs.Handle(request)
		return response(r.ID, result, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var result json.RawMessage
	err := h.Call(ctx, r.Method, r.Params, &result)
	return response(r.ID, result, err)
}
func response(id string, result any, err error) *C.char {
	var value ipc.Response
	if err != nil {
		value = ipc.Failure(id, model.PublicError(err))
	} else {
		value = ipc.Success(id, result)
	}
	b, _ := json.Marshal(value)
	return C.CString(string(b))
}

//export fncpn_stop
func fncpn_stop() *C.char {
	return stop(false)
}

//export fncpn_stop_paused
func fncpn_stop_paused() *C.char {
	return stop(true)
}

func stop(pause bool) *C.char {
	lifecycle.Lock()
	defer lifecycle.Unlock()
	state.Lock()
	h, e := state.host, state.engine
	if state.runtime != nil && pause {
		state.runtime.pauseOnClose.Store(true)
	}
	state.stopping = true
	state.Unlock()
	if e != nil {
		e.PrepareShutdown()
	}
	if h != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := h.Close(ctx)
		cancel()
		if err != nil {
			return C.CString(model.SafeText(err.Error()))
		}
	}
	state.Lock()
	if state.writer != nil {
		state.writer.Close()
		state.writer = nil
	}
	if state.logs != nil {
		state.logs.Close()
		state.logs = nil
	}
	if state.privilegedWriter != nil {
		state.privilegedLogger.Info("extension stopped")
		platformLogger.Store(nil)
		state.privilegedWriter.Close()
		state.privilegedWriter = nil
		state.privilegedLogger = nil
	}
	state.host = nil
	state.runtime = nil
	state.engine = nil
	state.network = nil
	state.stopping = false
	state.Unlock()
	return C.CString("")
}

//export fncpn_platform_event
func fncpn_platform_event(event C.int, code C.int) {
	logger := platformLogger.Load()
	if logger == nil {
		return
	}
	events := map[int]string{1: "extension started", 2: "extension stopping", 3: "network settings applying", 4: "network settings applied", 5: "network settings failed", 6: "platform operation failed"}
	if message, ok := events[int(event)]; ok {
		logger.Info(message, "code", int(code))
	}
}

//export fncpn_receive
func fncpn_receive(p unsafe.Pointer, n C.int) {
	if n <= 0 || n > 65535 {
		return
	}
	state.Lock()
	e := state.engine
	state.Unlock()
	if e != nil {
		e.Inject(C.GoBytes(p, n))
	}
}

//export fncpn_network_changed
func fncpn_network_changed() {
	state.Lock()
	n := state.network
	state.Unlock()
	if n != nil {
		n.Notify()
	}
}
func main() {}
