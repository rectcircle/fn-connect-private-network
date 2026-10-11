//go:build windows

package windows

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	wgconfig "github.com/rectcircle/fn-connect-private-network/internal/wireguard"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

type WireGuardRuntime struct {
	mu     sync.Mutex
	logger *slog.Logger
	device *device.Device
	name   string
	mtu    int
}

func NewWireGuardRuntime(logger *slog.Logger) *WireGuardRuntime {
	if logger == nil {
		logger = slog.Default()
	}
	return &WireGuardRuntime{logger: logger}
}

func (r *WireGuardRuntime) Start(ctx context.Context, plan model.ClientPlan) (string, error) {
	normalized, err := wgconfig.NormalizeClientPlan(plan)
	if err != nil {
		return "", err
	}
	uapi, err := wgconfig.ClientUAPI(normalized)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
	tunnel, err := tun.CreateTUN("FnCPN", normalized.MTU)
	if err != nil {
		return "", fmt.Errorf("create Wintun adapter (verify the signed Wintun DLL is beside fncpn.exe): %w", err)
	}
	name, err := tunnel.Name()
	if err != nil {
		_ = tunnel.Close()
		return "", fmt.Errorf("read Wintun adapter name: %w", err)
	}
	logger := &device.Logger{Verbosef: device.DiscardLogf, Errorf: func(format string, args ...any) {
		r.logger.Error("WireGuard device", "message", fmt.Sprintf(format, args...))
	}}
	wgDevice := device.NewDevice(tunnel, conn.NewDefaultBind(), logger)
	if err := wgDevice.IpcSet(uapi); err != nil {
		wgDevice.Close()
		return "", fmt.Errorf("configure WireGuard: %w", err)
	}
	if err := wgDevice.Up(); err != nil {
		wgDevice.Close()
		return "", fmt.Errorf("start WireGuard: %w", err)
	}
	if err := ctx.Err(); err != nil {
		wgDevice.Close()
		return "", err
	}
	r.device, r.name, r.mtu = wgDevice, name, normalized.MTU
	return name, nil
}

func (r *WireGuardRuntime) Stop() { r.mu.Lock(); defer r.mu.Unlock(); r.stopLocked() }

func (r *WireGuardRuntime) Status() model.ClientPrivilegedStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := model.ClientPrivilegedStatus{Active: r.device != nil, Interface: r.name, MTU: r.mtu}
	if r.device == nil {
		return status
	}
	uapi, err := r.device.IpcGet()
	if err != nil {
		status.Degraded = true
		status.LastError = model.PublicError(model.WithOperation(err, "wireguard.status"))
		return status
	}
	status.LastHandshake = parseLastHandshake(uapi)
	status.ListenPort = parseListenPort(uapi)
	return status
}

func (r *WireGuardRuntime) stopLocked() {
	if r.device != nil {
		r.device.Close()
	}
	r.device, r.name, r.mtu = nil, "", 0
}

func parseLastHandshake(uapi string) *time.Time {
	var seconds, nanoseconds int64
	for _, line := range strings.Split(uapi, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		if key == "last_handshake_time_sec" && number > seconds {
			seconds, nanoseconds = number, 0
		}
		if key == "last_handshake_time_nsec" {
			nanoseconds = number
		}
	}
	if seconds == 0 {
		return nil
	}
	value := time.Unix(seconds, nanoseconds).UTC()
	return &value
}

func parseListenPort(uapi string) uint16 {
	for _, line := range strings.Split(uapi, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "listen_port" {
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			return uint16(port)
		}
	}
	return 0
}
