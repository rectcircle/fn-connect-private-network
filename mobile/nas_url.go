package main

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Resolve the NAS's configured web port without sharing browser cookies or
// disabling TLS verification. Redirects may only stay on the same private host.
func resolveNASWebURL(ctx context.Context, address string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil || !ip.IsPrivate() {
		return "", errors.New("NAS IP 暂不可用")
	}
	host := ip.String()
	if ip.Is6() {
		host = "[" + host + "]"
	}
	origin := "http://" + host + "/"
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
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
func (v *clientView) copyDNSSuffix() {
	suffix := strings.TrimSpace(v.current.DNS.Suffix)
	if suffix == "" {
		v.toast("DNS 后缀暂不可用")
		return
	}
	if v.window != nil {
		v.window.Clipboard().SetContent(suffix)
		v.toast("DNS 后缀已复制")
	}
}
