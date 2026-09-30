//go:build darwin

package darwin

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

func (r *WireGuardRuntime) Start(
	ctx context.Context,
	plan model.ClientPlan,
) (string, error) {
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

	tunDevice, err := tun.CreateTUN("utun", normalized.MTU)
	if err != nil {
		return "", fmt.Errorf("create utun: %w", err)
	}
	name, err := tunDevice.Name()
	if err != nil {
		_ = tunDevice.Close()
		return "", fmt.Errorf("read utun name: %w", err)
	}
	logger := &device.Logger{
		Verbosef: device.DiscardLogf,
		Errorf: func(format string, arguments ...any) {
			r.logger.Error("wireguard device", "message", fmt.Sprintf(format, arguments...))
		},
	}
	wireGuardDevice := device.NewDevice(tunDevice, conn.NewDefaultBind(), logger)
	if err := wireGuardDevice.IpcSet(uapi); err != nil {
		wireGuardDevice.Close()
		return "", fmt.Errorf("configure WireGuard device: %w", err)
	}
	if err := wireGuardDevice.Up(); err != nil {
		wireGuardDevice.Close()
		return "", fmt.Errorf("start WireGuard device: %w", err)
	}
	if err := ctx.Err(); err != nil {
		wireGuardDevice.Close()
		return "", err
	}

	r.device = wireGuardDevice
	r.name = name
	r.mtu = normalized.MTU
	return name, nil
}

func (r *WireGuardRuntime) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopLocked()
}

func (r *WireGuardRuntime) Status() model.ClientPrivilegedStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	status := model.ClientPrivilegedStatus{
		Active:    r.device != nil,
		Interface: r.name,
		MTU:       r.mtu,
	}
	if r.device == nil {
		return status
	}
	uapi, err := r.device.IpcGet()
	if err != nil {
		r.logger.Error("read wireguard status", "error", err)
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
	r.device = nil
	r.name = ""
	r.mtu = 0
}

func parseLastHandshake(uapi string) *time.Time {
	var seconds, nanoseconds int64
	for _, line := range strings.Split(uapi, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		switch key {
		case "last_handshake_time_sec":
			if number > seconds {
				seconds = number
				nanoseconds = 0
			}
		case "last_handshake_time_nsec":
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
		key, value, found := strings.Cut(line, "=")
		if !found || key != "listen_port" {
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			return uint16(port)
		}
	}
	return 0
}
