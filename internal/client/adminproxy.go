package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"

	"errors"
	"fmt"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// AdminProxy exposes the fnOS management UI (/app/fncpn) to a local browser or
// WebView without relying on the browser's own fnOS cookies. Its providers are
// backed by the dedicated Web session and gateway access-code cookie, never by
// the tunnel's CLI login token.
type AdminProxy struct {
	ListenAddress  string
	FNIDProvider   func() (string, error)
	CookieProvider func(string) ([]Cookie, error)
	Logger         *slog.Logger
	Recover        func(context.Context, string, string) ([]Cookie, error)
	Activity       func(context.Context, string)

	lifecycle         context.Context
	cancel            context.CancelFunc
	mu                sync.Mutex
	ln                net.Listener
	server            *http.Server
	base              *url.URL
	token             string
	upstreamTransport http.RoundTripper
}

// Start binds a local TCP listener (using ListenAddress, which may contain a
// fixed or an ephemeral port) and begins proxying /app/fncpn to the target NAS.
// It returns a one-time authenticated URL for /app/fncpn.
func (p *AdminProxy) Start() (*url.URL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.FNIDProvider == nil || p.CookieProvider == nil {
		return nil, errors.New("admin proxy: credential providers are required")
	}
	if p.base != nil {
		return p.bootstrapURL(), nil
	}
	var tokenBytes [32]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return nil, fmt.Errorf("admin proxy: generate access token: %w", err)
	}
	p.token = hex.EncodeToString(tokenBytes[:])
	listen := p.ListenAddress
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("admin proxy: listen: %w", err)
	}
	transport := p.upstreamTransport
	if transport == nil {
		transport = newFNOSTransport(&http.Transport{
			Proxy: nil, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		})
	}
	proxy := &httputil.ReverseProxy{
		Director:       p.director,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		// HTTPS verification stays in Go; the WebView uses loopback HTTP.
		Transport: adminProxyTransport{proxy: p, upstream: transport},
	}
	p.lifecycle, p.cancel = context.WithCancel(context.Background())
	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if !p.authorizeLocalRequest(writer, request) {
				return
			}
			if request.URL.Path != "/app/fncpn" &&
				!strings.HasPrefix(request.URL.Path, "/app/fncpn/") {
				http.NotFound(writer, request)
				return
			}
			fnID, providerErr := p.FNIDProvider()
			if providerErr != nil {
				http.Error(writer, "admin proxy unavailable", http.StatusServiceUnavailable)
				return
			}
			cookies, providerErr := p.CookieProvider(fnID)
			if providerErr != nil || len(cookies) == 0 {
				http.Error(writer, "admin Web session is unavailable", http.StatusUnauthorized)
				return
			}
			p.recordActivity(fnID)
			// ReverseProxy cannot recreate an incoming streaming body. Buffer
			// only small management requests; larger bodies are never replayed.
			if p.Recover != nil && request.Body != nil && request.Body != http.NoBody {
				body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
				if err != nil {
					http.Error(writer, "read admin request", http.StatusBadRequest)
					return
				}
				if len(body) <= 1<<20 {
					request.Body.Close()
					request.Body = io.NopCloser(bytes.NewReader(body))
					request.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
				} else {
					request.Body = &adminProxyBody{Reader: io.MultiReader(bytes.NewReader(body), request.Body), Closer: request.Body}
				}
			}
			proxy.ServeHTTP(writer, request)
		}),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		if serveErr := server.Serve(ln); serveErr != nil &&
			!errors.Is(serveErr, http.ErrServerClosed) {
			p.logger().Error("admin proxy server failed", "error", serveErr)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	base := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	p.ln = ln
	p.server = server
	p.base = base
	p.logger().Info("admin proxy started", "address", base.String())
	return p.bootstrapURL(), nil
}

func (p *AdminProxy) bootstrapURL() *url.URL {
	result := *p.base
	result.Path = "/app/fncpn"
	query := result.Query()
	query.Set("fncpn_proxy", p.token)
	result.RawQuery = query.Encode()
	return &result
}

func (p *AdminProxy) authorizeLocalRequest(writer http.ResponseWriter, request *http.Request) bool {
	p.mu.Lock()
	token := p.token
	p.mu.Unlock()
	if token == "" {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return false
	}
	if cookie, err := request.Cookie("fncpn-proxy"); err == nil &&
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) == 1 {
		return true
	}
	query := request.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("fncpn_proxy")), []byte(token)) != 1 {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return false
	}
	query.Del("fncpn_proxy")
	target := *request.URL
	target.RawQuery = query.Encode()
	http.SetCookie(writer, &http.Cookie{
		Name: "fncpn-proxy", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(writer, request, target.String(), http.StatusSeeOther)
	return false
}

// baseURL resolves the NAS origin for the current FN ID.
func (p *AdminProxy) targetOrigin() (*url.URL, error) {
	if p.FNIDProvider == nil {
		return nil, errors.New("admin proxy: FN ID provider is nil")
	}
	fnID, err := p.FNIDProvider()
	if err != nil {
		return nil, err
	}
	return &url.URL{Scheme: "https", Host: fnID + ".fnos.net"}, nil
}

func (p *AdminProxy) director(req *http.Request) {
	// Never forward cookies supplied by the local browser. Only the isolated
	// Web and gateway access-code cookies loaded by CookieProvider may authenticate
	// an upstream request.
	req.Header.Del("Cookie")
	target, err := p.targetOrigin()
	if err != nil {
		// Leave the request untouched; the error handler will surface it.
		req.URL.Scheme, req.URL.Host = "https", req.Host
		return
	}
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.Host = target.Host
	// Inject the dedicated Web session's gateway cookies for the NAS.
	if p.CookieProvider != nil {
		if fnID, _ := p.FNIDProvider(); fnID != "" {
			if cookies, cookieErr := p.CookieProvider(fnID); cookieErr == nil {
				if header := cookieHeaderForURL(cookies, req.URL); header != "" {
					req.Header.Set("Cookie", header)
				}
			}
		}
	}
	if req.Header.Get("Cookie") == "" {
		p.logger().Warn("admin proxy: no gateway cookie available",
			"method", req.Method, "path", req.URL.Path)
	}
}

func (p *AdminProxy) modifyResponse(resp *http.Response) error {
	location := resp.Header.Get("Location")
	if location != "" {
		p.logger().Info("admin proxy redirect",
			"from", resp.Request.URL.Path, "status", resp.StatusCode)
	}
	// Rewrite absolute NAS redirects back through the local proxy so the
	// browser/WebView never leaves the authenticated proxy and hits a stale
	// invalid-token page.
	parsed, err := url.Parse(location)
	if err != nil {
		return nil
	}
	if parsed.Host != "" {
		base, baseErr := p.currentBase()
		if baseErr != nil {
			return nil
		}
		parsed.Scheme = base.Scheme
		parsed.Host = base.Host
		resp.Header.Set("Location", parsed.String())
	}
	return nil
}

func (p *AdminProxy) errorHandler(w http.ResponseWriter, r *http.Request, err error) {
	p.logger().Warn("admin proxy request failed",
		"method", r.Method, "path", r.URL.Path, "error", err)
	failure := model.PublicError(model.WithOperation(err, "admin_proxy.request"))
	status := http.StatusBadGateway
	if failure.Code == model.ErrorAuthRequired {
		status = http.StatusUnauthorized
	}
	if failure.Code == model.ErrorFailedPrecondition {
		status = http.StatusPreconditionFailed
	}
	if failure.Code == model.ErrorUnavailable {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": failure})
}

func (p *AdminProxy) currentBase() (*url.URL, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.base == nil {
		return nil, errors.New("admin proxy is not running")
	}
	cloned := *p.base
	return &cloned, nil
}

func (p *AdminProxy) Close() error {
	p.mu.Lock()
	if p.server == nil {
		p.mu.Unlock()
		return nil
	}
	server := p.server
	if p.cancel != nil {
		p.cancel()
	}
	p.server = nil
	p.ln = nil
	p.base = nil
	p.token = ""
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return server.Shutdown(ctx)
}

func (p *AdminProxy) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}

// adminProxyBody restores a bounded peek without losing Close on the original body.
type adminProxyBody struct {
	io.Reader
	io.Closer
}

type adminProxyTransport struct {
	proxy    *AdminProxy
	upstream http.RoundTripper
}

func (t adminProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.upstream.RoundTrip(req)
	if err != nil || t.proxy.Recover == nil {
		return response, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	response.Body = &adminProxyBody{Reader: io.MultiReader(bytes.NewReader(data), response.Body), Closer: response.Body}
	if err != nil {
		response.Body.Close()
		return nil, err
	}
	// Only the gateway's exact rejection proves that a write did not reach the
	// application. An application 401/403, redirect or ambiguous body is not replayed.
	if len(data) > 8192 || strings.TrimSpace(string(data)) != "invalid token" || gatewayAuthenticationError(response, data) == nil {
		return response, nil
	}
	if req.Body != nil && req.Body != http.NoBody && req.GetBody == nil {
		return response, nil
	}
	fnID := strings.TrimSuffix(req.URL.Hostname(), ".fnos.net")
	rejected := ""
	if cookie, err := req.Cookie("fnos-token"); err == nil {
		rejected = cookie.Value
	}
	cookies, err := t.proxy.Recover(req.Context(), fnID, rejected)
	if err != nil {
		response.Body.Close()
		return nil, err
	}
	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			response.Body.Close()
			return nil, err
		}
	}
	response.Body.Close()
	retry.Header.Set("Cookie", cookieHeaderForURL(cookies, retry.URL))
	t.proxy.recordActivity(fnID)
	return t.upstream.RoundTrip(retry)
}

func (p *AdminProxy) recordActivity(fnID string) {
	if p.Activity == nil {
		return
	}
	p.mu.Lock()
	ctx := p.lifecycle
	p.mu.Unlock()
	if ctx != nil && ctx.Err() == nil {
		p.Activity(ctx, fnID)
	}
}
