package main

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	platform "github.com/rectcircle/fn-connect-private-network/internal/platform/harmonyos"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Resolve the NAS's configured web port without sharing browser cookies or
// disabling TLS verification. Redirects may only stay on the same private host.
func resolveNASWebURL(ctx context.Context, address string, transport http.RoundTripper) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || !ip.IsPrivate() {
		return "", errors.New("NAS IP 暂不可用")
	}
	host := ip.String()
	if ip.Is6() {
		host = "[" + host + "]"
	}
	origin := "http://" + host + "/"
	return resolveNASRedirects(ctx, origin, transport)
}
func resolveNASRedirects(ctx context.Context, origin string, transport http.RoundTripper) (string, error) {
	base, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 6 {
			return errors.New("NAS 网页重定向循环")
		}
		if req.URL.Hostname() != base.Hostname() || req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return errors.New("NAS 网页跳转到其他主机")
		}
		return nil
	}}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, origin, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		// Let the browser show its own certificate UI for a NAS using a local CA;
		// never disable certificate verification in the probe.
		var unknown x509.UnknownAuthorityError
		var requestError *url.Error
		if errors.As(err, &unknown) && errors.As(err, &requestError) {
			target, parseErr := url.Parse(requestError.URL)
			if parseErr == nil && target.Scheme == "https" && target.Hostname() == base.Hostname() && target.User == nil {
				return target.String(), nil
			}
		}
		return "", err
	}
	defer response.Body.Close()
	target := *response.Request.URL
	target.RawQuery = ""
	target.Fragment = ""
	return target.String(), nil
}

func applicationProbe(ctx context.Context, engine *platform.Engine, target string) (string, error) {
	if engine == nil || !engine.ApplicationOnly {
		return "", errors.New("application probe is simulator-only")
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
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
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
