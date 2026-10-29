//go:build ios && fncpn_ios_simulator

package iosui

/*
#include <stdlib.h>
char *fncpn_sim_directory(void);
char *fncpn_sim_secret(const char *request);
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/logging"
	"github.com/rectcircle/fn-connect-private-network/internal/platform/ios"
)

var simulator struct {
	sync.Mutex
	host   *clienthost.Host
	engine *iosvpn.Engine
}

type simulatorSystem struct{}

func (simulatorSystem) WritePacket([]byte) error {
	return errors.New("simulator has no system packet flow")
}
func (simulatorSystem) Call(method string, params, result any) error {
	if !strings.HasPrefix(method, "secret.") {
		return errors.New("unsupported simulator system operation")
	}
	data, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return err
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	output := C.fncpn_sim_secret(input)
	if output == nil {
		return errors.New("simulator Keychain unavailable")
	}
	defer C.free(unsafe.Pointer(output))
	var reply struct {
		OK     bool            `json:"ok"`
		Status int32           `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal([]byte(C.GoString(output)), &reply); err != nil {
		return err
	}
	if !reply.OK {
		return fmt.Errorf("simulator Keychain %s failed (OSStatus %d)", method, reply.Status)
	}
	if result != nil {
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}
func simulatorDirectory() (string, error) {
	p := C.fncpn_sim_directory()
	if p == nil {
		return "", errors.New("simulator storage unavailable")
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoString(p), nil
}
func simulatorHost() (*clienthost.Host, *iosvpn.Engine, error) {
	simulator.Lock()
	defer simulator.Unlock()
	if simulator.host != nil {
		return simulator.host, simulator.engine, nil
	}
	directory, err := simulatorDirectory()
	if err != nil {
		return nil, nil, err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, nil, err
	}
	writer, err := logging.Open(filepath.Join(directory, "logs/client.log"), 2<<20, 2)
	if err != nil {
		return nil, nil, err
	}
	keep := false
	defer func() {
		if !keep {
			writer.Close()
		}
	}()
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{ReplaceAttr: logging.RedactAttr})).With("platform", "ios-simulator-app")
	engine := iosvpn.NewUserspaceEngine()
	engine.Logger = logger
	network := iosvpn.NewNetwork()
	store := client.NewConfigStore(filepath.Join(directory, "client.json"), iosvpn.Secrets{System: simulatorSystem{}})
	manager, err := client.NewManager(client.ManagerOptions{Store: store, Logger: logger,
		DNSHTTPClient: &http.Client{Transport: &http.Transport{DialContext: engine.DialPrivate}, Timeout: 3 * time.Second},
		Discoverer:    client.DiscoveryClient{}, Remote: func(id string, cookies []client.Cookie, onCookies func([]client.Cookie) error) (client.RemoteService, error) {
			u, err := client.DefaultRemoteURL(id)
			if err != nil {
				return nil, err
			}
			return client.NewRemoteClient(u, cookies, nil, onCookies)
		},
		Privileged: engine, Bridge: &client.RelayBridge{Logger: logger}, Probe: network, Monitor: network, DeviceName: "iOS Simulator (application tunnel)"})
	if err != nil {
		return nil, nil, err
	}
	host, err := clienthost.New(manager)
	if err == nil {
		err = host.Start(context.Background())
	}
	if err != nil {
		return nil, nil, err
	}
	simulator.host = host
	simulator.engine = engine
	keep = true
	go func() {
		previous := ""
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			snapshot, err := network.Snapshot()
			if err != nil {
				continue
			}
			next := snapshot.Fingerprint()
			if previous != "" && next != previous {
				engine.NetworkChanged()
				network.Notify()
			}
			previous = next
		}
	}()
	return host, engine, nil
}
func simulatorCall(ctx context.Context, method string, params, result any) error {
	host, _, err := simulatorHost()
	if err != nil {
		return err
	}
	return host.Call(ctx, method, params, result)
}

// NetworkProbe sends real requests through the application stack. Public
// destinations cannot accidentally be interpreted as private tunnel success.
func NetworkProbe(ctx context.Context, target string) (string, error) {
	_, engine, err := simulatorHost()
	if err != nil {
		return "", err
	}
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "dns:") {
		ips, err := engine.LookupApplication(ctx, strings.TrimPrefix(target, "dns:"))
		if err != nil {
			return "", err
		}
		return "应用内 DNS 查询成功：\n" + strings.Join(ips, "\n"), nil
	}
	u, err := url.Parse(target)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", errors.New("请输入 http(s) 私网 URL，或 dns:完整私网域名")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !ip.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return "", errors.New("网络调试只允许私网目标")
	}
	transport := &http.Transport{DialContext: engine.DialApplication}
	defer transport.CloseIdleConnections()
	c := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	status, err := engine.Status(ctx)
	if err != nil {
		return "", err
	}
	path := "物理网络（LOCAL / 尚未建立远程隧道）"
	if status.Active {
		path = "应用内 WireGuard / Relay 隧道"
	}
	return fmt.Sprintf("真实 HTTP 响应：%s\n传输路径：%s\n仅本 App；未接管系统路由或 DNS。", response.Status, path), nil
}

func simulatorLogDirectory() (string, error) { return simulatorDirectory() }
func simulatorBrowserAllowed(target string) error {
	u, err := url.Parse(target)
	if err != nil {
		return err
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !ip.IsPrivate() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
		return nil
	}
	_, engine, err := simulatorHost()
	if err != nil {
		return err
	}
	status, err := engine.Status(context.Background())
	if err != nil {
		return err
	}
	if status.Active {
		return errors.New("模拟器 WebView 不经过应用内隧道；请使用菜单中的应用内网络调试验证私网 HTTP/DNS")
	}
	return nil
}
