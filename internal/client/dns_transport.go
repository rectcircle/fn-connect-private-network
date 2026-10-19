package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

// privateDNSClient uses the same NAS LAN endpoint in LOCAL, DIRECT and RELAY.
// The OS routes it over the physical LAN or WireGuard; no gateway or TLS fallback.
type privateDNSClient struct {
	endpoints  []string
	httpClient *http.Client
	allowed    func(string) bool
}

func newPrivateDNSClient(endpoints []string, allowed func(string) bool, httpClient *http.Client) (*privateDNSClient, error) {
	validEndpoints := []string{}
	for _, endpoint := range endpoints {
		host, port, err := net.SplitHostPort(endpoint)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			continue
		}
		if value, err := strconv.ParseUint(port, 10, 16); err != nil || value == 0 {
			continue
		}
		if !slices.Contains(validEndpoints, endpoint) {
			validEndpoints = append(validEndpoints, endpoint)
		}
	}
	if len(validEndpoints) == 0 || len(validEndpoints) > 32 {
		return nil, model.NewError(model.ErrorUnavailable, "no valid NAS LAN DNS endpoint", true)
	}
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext
		httpClient = &http.Client{Transport: transport, Timeout: 3 * time.Second}
	}
	cloned := *httpClient
	cloned.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	cloned.Jar = nil
	return &privateDNSClient{endpoints: validEndpoints, httpClient: &cloned, allowed: allowed}, nil
}
func (c *privateDNSClient) Close() {
	if transport, ok := c.httpClient.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}
func (c *privateDNSClient) QueryDNS(ctx context.Context, q model.DNSQuery) (model.DNSAnswer, error) {
	name := q.Name
	var err error
	if name != "" {
		name, err = dns.NormalizeName(name)
	}
	if err != nil || q.Type != "A" {
		return model.DNSAnswer{}, model.NewError(model.ErrorInvalidArgument, "invalid DNS query", false)
	}
	q.Name = name
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var failures []error
	for _, endpoint := range c.endpoints {
		host, _, _ := net.SplitHostPort(endpoint)
		if !c.allowed(host) {
			continue
		}
		attempt, cancelAttempt := context.WithTimeout(ctx, min(2500*time.Millisecond, remainingBudget(ctx)))
		answer, err := c.queryEndpoint(attempt, endpoint, q)
		cancelAttempt()
		if err == nil {
			return answer, nil
		}
		if !model.AsError(err).Retryable {
			return model.DNSAnswer{}, err
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	if len(failures) == 0 {
		return model.DNSAnswer{}, model.NewError(model.ErrorUnavailable, "NAS LAN DNS endpoint is unavailable or conflicts with the local network", true)
	}
	return model.DNSAnswer{}, model.WrapError(model.ErrorUnavailable, "NAS private DNS query failed", true, errors.Join(failures...))
}
func remainingBudget(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return max(time.Millisecond, time.Until(deadline))
	}
	return 3 * time.Second
}
func (c *privateDNSClient) queryEndpoint(ctx context.Context, endpoint string, q model.DNSQuery) (model.DNSAnswer, error) {
	path := model.DNSQueryPath
	body, err := json.Marshal(q)
	if err != nil {
		return model.DNSAnswer{}, err
	}
	target := url.URL{Scheme: "http", Host: endpoint, Path: path}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return model.DNSAnswer{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-FnCPN-Client-Version", version.Current)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return model.DNSAnswer{}, model.WrapError(model.ErrorUnavailable, "NAS private DNS endpoint is unreachable", true, err)
	}
	defer response.Body.Close()
	if serverVersion := response.Header.Get("X-FnCPN-Version"); serverVersion != "" {
		if err := version.Check(version.Current, serverVersion); err != nil {
			return model.DNSAnswer{}, err
		}
	}
	if response.StatusCode != http.StatusOK {
		return model.DNSAnswer{}, HTTPResponseError(response, "dns.query", nil)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRemoteResponseSize+1))
	if err != nil || len(data) > maxRemoteResponseSize {
		return model.DNSAnswer{}, model.WrapError(model.ErrorProtocol, "invalid private DNS response size", false, err)
	}
	var answer model.DNSAnswer
	if err := model.DecodeResponse(data, &answer); err != nil {
		return model.DNSAnswer{}, model.WrapError(model.ErrorProtocol, "invalid private DNS response", false, err)
	}
	if answer.Name != q.Name || answer.Type != q.Type || len(answer.Addresses) > 32 || answer.TTL > 3600 {
		return model.DNSAnswer{}, model.NewError(model.ErrorProtocol, "private DNS answer does not match query", false)
	}
	switch answer.Result {
	case "answer", "nodata", "nxdomain":
	default:
		return model.DNSAnswer{}, model.NewError(model.ErrorProtocol, "invalid private DNS result", false)
	}
	return answer, nil
}
