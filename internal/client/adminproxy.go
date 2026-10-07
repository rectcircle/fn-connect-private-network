package client

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
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
// backed only by the dedicated Web session, never by the tunnel's CLI session.
type AdminProxy struct {
	ListenAddress  string
	FNIDProvider   func() (string, error)
	CookieProvider func(string) ([]Cookie, error)
	Logger         *slog.Logger

	mu     sync.Mutex
	ln     net.Listener
	server *http.Server
	base   *url.URL
	token  string
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
	proxy := &httputil.ReverseProxy{
		Director:       p.director,
		ModifyResponse: p.modifyResponse,
		ErrorHandler:   p.errorHandler,
		Transport: &http.Transport{
			Proxy:               nil,
			DisableKeepAlives:   false,
			MaxIdleConnsPerHost: 8,
			IdleConnTimeout:     90 * time.Second,
			// The NAS endpoint is served over HTTPS; use the system root pool.
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
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
	if cookie, err := request.Cookie("fncpn-proxy"); err == nil &&
		subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(p.token)) == 1 {
		return true
	}
	query := request.URL.Query()
	if subtle.ConstantTimeCompare([]byte(query.Get("fncpn_proxy")), []byte(p.token)) != 1 {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return false
	}
	query.Del("fncpn_proxy")
	target := *request.URL
	target.RawQuery = query.Encode()
	http.SetCookie(writer, &http.Cookie{
		Name: "fncpn-proxy", Value: p.token, Path: "/",
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
	// Web session loaded by CookieProvider may authenticate an upstream request.
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
	http.Error(w, "FnCPN admin proxy error: "+err.Error(), http.StatusBadGateway)
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
