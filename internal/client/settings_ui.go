package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

//go:embed settingsweb/*
var settingsFiles embed.FS

const settingsPath = "/app/fncpn/settings"

type settingsUI struct {
	server         *http.Server
	address, token string
}

// SettingsURL exposes only this device's settings, never a NAS admin session.
// Each test is one cancellable HTTP request; there are no background test jobs.
func (m *Manager) SettingsURL(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settingsClosed {
		return "", errors.New("client is closing")
	}
	if m.settingsUI == nil {
		ui, err := startSettingsUI(m)
		if err != nil {
			return "", err
		}
		m.settingsUI = ui
	}
	return m.settingsUI.url(), nil
}
func (m *Manager) closeSettingsUI() {
	m.mu.Lock()
	ui := m.settingsUI
	m.settingsUI = nil
	m.settingsClosed = true
	m.mu.Unlock()
	if ui != nil {
		_ = ui.server.Close()
	}
}
func (ui *settingsUI) serve(m settingsBackend, w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	if r.Host != ui.address || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+ui.address) {
		http.Error(w, "Forbidden", 403)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, settingsPath)
	if !strings.HasPrefix(r.URL.Path, settingsPath) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet && (path == "/settings.js" || path == "/settings.css" || path == "/en.json") {
		data, err := settingsFiles.ReadFile("settingsweb" + path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		content := map[string]string{"/settings.js": "text/javascript; charset=utf-8", "/settings.css": "text/css; charset=utf-8", "/en.json": "application/json"}
		w.Header().Set("Content-Type", content[path])
		_, _ = w.Write(data)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if path == "" && r.Method == http.MethodGet {
		token = r.URL.Query().Get("fncpn_proxy")
		if token == "" {
			if cookie, err := r.Cookie(ui.cookieName()); err == nil {
				token = cookie.Value
			}
		}
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(ui.token)) != 1 {
		http.Error(w, "Forbidden", 403)
		return
	}
	if path == "" && r.Method == http.MethodGet {
		http.SetCookie(w, &http.Cookie{Name: ui.cookieName(), Value: ui.token, Path: settingsPath, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		data, _ := settingsFiles.ReadFile("settingsweb/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	fail := func(err error) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	switch {
	case path == "/api/state" && r.Method == http.MethodGet:
		links, err := m.LinkSettings(ctx)
		if err != nil {
			fail(err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": version.Current, "commit": version.Revision, "settings": links, "status": m.Status(), "infoOnly": isSettingsInfoOnly(m)})
	case path == "/api/preferences" && r.Method == http.MethodPost:
		var input LinkSettings
		if err := decodeSettingsBody(w, r, &input); err != nil {
			fail(err)
			return
		}
		result, err := m.SetLinkSettings(ctx, input)
		if err != nil {
			fail(err)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	case path == "/api/test" && r.Method == http.MethodPost:
		var input SpeedTestRequest
		if err := decodeSettingsBody(w, r, &input); err != nil {
			fail(err)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		flush := http.NewResponseController(w)
		emit := func(value any) error {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				return err
			}
			return flush.Flush()
		}
		if err := m.RunSpeedTest(ctx, input, func(s SpeedTestStatus) error { return emit(s) }); err != nil {
			_ = emit(map[string]any{"active": false, "stage": "failed", "error": err.Error()})
		}
	default:
		http.NotFound(w, r)
	}
}
func decodeSettingsBody(w http.ResponseWriter, r *http.Request, target any) error {
	reader := http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("unexpected request content")
	}
	return nil
}

// NewSettingsInfoPage opens General without starting or authorizing a VPN. The
// system-owned runtime provides the full page once a connection service exists.
func NewSettingsInfoPage(ctx context.Context) (string, error) {
	ui, err := startSettingsUI(settingsInfoBackend{})
	if err != nil {
		return "", err
	}
	context.AfterFunc(ctx, func() { _ = ui.server.Close() })
	return ui.url(), nil
}

type settingsBackend interface {
	LinkSettings(context.Context) (LinkSettings, error)
	SetLinkSettings(context.Context, LinkSettings) (LinkSettings, error)
	RunSpeedTest(context.Context, SpeedTestRequest, func(SpeedTestStatus) error) error
	Status() model.ClientStatus
}
type settingsInfoBackend struct{}

func (settingsInfoBackend) Status() model.ClientStatus {
	return model.ClientStatus{State: model.ClientUnconfigured}
}
func (settingsInfoBackend) LinkSettings(context.Context) (LinkSettings, error) {
	return LinkSettings{Links: defaultLinks()}, nil
}
func (settingsInfoBackend) SetLinkSettings(context.Context, LinkSettings) (LinkSettings, error) {
	return LinkSettings{}, errors.New("连接服务未启动，请先在主窗口连接 NAS，再重新打开设置。")
}
func (settingsInfoBackend) RunSpeedTest(context.Context, SpeedTestRequest, func(SpeedTestStatus) error) error {
	return errors.New("连接服务未启动，请先在主窗口连接 NAS，再重新打开设置。")
}
func (ui *settingsUI) url() string {
	return "http://" + ui.address + settingsPath + "?fncpn_proxy=" + ui.token
}
func startSettingsUI(backend settingsBackend) (*settingsUI, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ui := &settingsUI{address: ln.Addr().String(), token: hex.EncodeToString(secret)}
	ui.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ui.serve(backend, w, r) })}
	go func() { _ = ui.server.Serve(ln) }()
	return ui, nil
}

func isSettingsInfoOnly(backend settingsBackend) bool {
	_, ok := backend.(settingsInfoBackend)
	return ok
}

func (ui *settingsUI) cookieName() string {
	return "fncpn_settings_" + strings.ReplaceAll(ui.address, ":", "_")
}
