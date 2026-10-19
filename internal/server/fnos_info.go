package server

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
)

const fnConnectSocket = "/run/trim-connect.sock"
const fnOSGatewaySettings = "/usr/trim/etc/network_gateway_setting.conf"

// fnOSInfo contains only the public identity and management endpoint metadata.
// These reads run as the ordinary application user; no credentials or root IPC.
type fnOSInfo struct {
	FNID             string `json:"fnId,omitempty"`
	DefaultDNSSuffix string `json:"defaultDNSSuffix,omitempty"`
	FNConnectState   string `json:"fnConnectState"`
	ManagementScheme string `json:"managementScheme,omitempty"`
	ManagementPort   uint16 `json:"managementPort,omitempty"`
	ManagementState  string `json:"managementState"`
}

type fnOSInfoReader struct {
	client       *http.Client
	settingsPath string
}

func newFNOSInfoReader(socketPath, settingsPath string) *fnOSInfoReader {
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socketPath)
	}, ResponseHeaderTimeout: time.Second, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second}
	return &fnOSInfoReader{client: &http.Client{Transport: transport, Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, settingsPath: settingsPath}
}

func (r *fnOSInfoReader) read(ctx context.Context) fnOSInfo {
	result := fnOSInfo{FNConnectState: "unavailable", ManagementState: "unavailable"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/trimfn", nil)
	if err == nil {
		response, err := r.client.Do(request)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, 1025))
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK && len(data) <= 1024 {
				id := strings.TrimSpace(string(data))
				if id == "" {
					result.FNConnectState = "not-configured"
				} else if validFNOSID(id) {
					result.FNID = strings.ToLower(id)
					result.DefaultDNSSuffix = dns.DefaultSuffix(result.FNID)
					result.FNConnectState = "ready"
				}
			}
		}
	}
	file, err := os.Open(r.settingsPath)
	if err != nil {
		return result
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return result
	}
	var settings struct {
		Schema struct {
			HTTP struct {
				Port int `json:"port"`
			} `json:"http"`
			HTTPS struct {
				Port int `json:"port"`
			} `json:"https"`
		} `json:"schema"`
		ForceHTTPS *bool `json:"force_https"`
	}
	if json.Unmarshal(data, &settings) != nil || settings.ForceHTTPS == nil {
		return result
	}
	scheme, port := "http", settings.Schema.HTTP.Port
	if *settings.ForceHTTPS {
		scheme, port = "https", settings.Schema.HTTPS.Port
	}
	if port < 1 || port > 65535 {
		return result
	}
	result.ManagementScheme = scheme
	result.ManagementPort = uint16(port)
	result.ManagementState = "ready"
	return result
}

func validFNOSID(id string) bool {
	// FN ID is a single ASCII DNS label. Reject HTML/error responses and URLs.
	if len(id) < 1 || len(id) > 63 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}
