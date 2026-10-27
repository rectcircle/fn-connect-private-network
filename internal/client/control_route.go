package client

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"golang.org/x/sync/singleflight"
)

// Routing is independent of authentication: callers continue using the canonical
// fnOS origin and their existing CLI/Web cookies. Only the transport is changed.
type controlRouteEvidence struct {
	FNID, DeviceID, ClientAddress string
	State                         model.ClientState
	Private                       model.PrivateAPIConfiguration
	AllowedIPs                    []string
	Snapshot                      NetworkSnapshot
}
type controlEndpoint struct {
	origin      url.URL
	httpsOrigin *url.URL
	transport   http.RoundTripper
	generation  uint64
	verifiedAt  time.Time
}
type nasControlRouter struct {
	mu            sync.Mutex
	evidence      controlRouteEvidence
	generation    uint64
	endpoint      *controlEndpoint
	retryAfter    time.Time
	discoverAfter time.Time
	refreshPorts  bool
	ports         map[string]DiscoveryPorts
	resolving     singleflight.Group
	store         *ConfigStore
	discoverer    Discoverer
	privateClient *http.Client
	logger        *slog.Logger
}

func newNASControlRouter(store *ConfigStore, discoverer Discoverer, privateClient *http.Client, logger *slog.Logger) *nasControlRouter {
	return &nasControlRouter{store: store, discoverer: discoverer, privateClient: privateClient, logger: logger, ports: make(map[string]DiscoveryPorts)}
}

func controlConnected(state model.ClientState) bool {
	return state == model.ClientLocal || state == model.ClientDirect || state == model.ClientRelay
}
func (r *nasControlRouter) update(e controlRouteEvidence) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if reflect.DeepEqual(r.evidence, e) {
		return false
	}
	r.closeEndpointLocked()
	if r.evidence.FNID != e.FNID {
		r.discoverAfter = time.Time{}
		r.refreshPorts = false
	}
	r.evidence = e
	r.generation++
	r.retryAfter = time.Time{}
	return true
}
func (r *nasControlRouter) deactivate() { r.update(controlRouteEvidence{}) }
func (r *nasControlRouter) closeEndpointLocked() {
	if r.endpoint != nil {
		if closer, ok := r.endpoint.transport.(interface{ CloseIdleConnections() }); ok {
			closer.CloseIdleConnections()
		}
	}
	r.endpoint = nil
}
func (r *nasControlRouter) remember(fnID string, ports DiscoveryPorts) {
	if ports.HTTP == 0 && ports.HTTPS == 0 {
		return
	}
	r.mu.Lock()
	changed := r.ports[fnID] != ports
	r.ports[fnID] = ports
	if changed && r.evidence.FNID == fnID {
		r.closeEndpointLocked()
		r.generation++
		r.retryAfter = time.Time{}
	}
	r.mu.Unlock()
	if err := r.store.saveFNOSWebPorts(fnID, ports); err != nil {
		r.logger.Warn("cache fnOS Web ports", "fn_id", fnID, "error", err)
	}
}

type controlDiscoverer struct {
	Discoverer
	router *nasControlRouter
}

func (d controlDiscoverer) Discover(ctx context.Context, fnID string) (Discovery, error) {
	value, err := d.Discoverer.Discover(ctx, fnID)
	if err == nil {
		d.router.remember(fnID, value.Port)
	}
	return value, err
}

func (r *nasControlRouter) endpointFor(ctx context.Context, fnID string) (*controlEndpoint, error) {
	r.mu.Lock()
	if r.evidence.FNID != fnID || !controlConnected(r.evidence.State) || time.Now().Before(r.retryAfter) {
		r.mu.Unlock()
		return nil, nil
	}
	if r.endpoint != nil && time.Since(r.endpoint.verifiedAt) < time.Minute {
		endpoint := r.endpoint
		r.mu.Unlock()
		return endpoint, nil
	}
	if r.endpoint != nil {
		r.closeEndpointLocked()
		r.generation++
	}
	generation := r.generation
	r.mu.Unlock()
	result := r.resolving.DoChan(fmt.Sprint(generation), func() (any, error) { return r.resolve(context.WithoutCancel(ctx), fnID, generation) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		endpoint, _ := result.Val.(*controlEndpoint)
		return endpoint, nil
	}
}
func (r *nasControlRouter) resolve(ctx context.Context, fnID string, generation uint64) (*controlEndpoint, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	r.mu.Lock()
	if generation != r.generation {
		r.mu.Unlock()
		return nil, nil
	}
	if r.endpoint != nil {
		endpoint := r.endpoint
		r.mu.Unlock()
		return endpoint, nil
	}
	evidence := r.evidence
	ports := r.ports[fnID]
	discoverAllowed := time.Now().After(r.discoverAfter)
	refreshPorts := r.refreshPorts
	r.mu.Unlock()
	if ports.HTTP == 0 && ports.HTTPS == 0 {
		ports, _ = r.store.loadFNOSWebPorts(fnID)
	}
	if (ports.HTTP == 0 && ports.HTTPS == 0 || refreshPorts) && discoverAllowed {
		r.mu.Lock()
		r.discoverAfter = time.Now().Add(5 * time.Minute)
		r.mu.Unlock()
		stage, stop := context.WithTimeout(ctx, discoveryTimeout)
		discovery, err := r.discoverer.Discover(stage, fnID)
		stop()
		if err == nil && (discovery.Port.HTTP != 0 || discovery.Port.HTTPS != 0) {
			ports = discovery.Port
			r.mu.Lock()
			r.ports[fnID] = ports
			r.refreshPorts = false
			r.mu.Unlock()
			_ = r.store.saveFNOSWebPorts(fnID, ports)
		}
	}
	if ports.HTTP == 0 && ports.HTTPS == 0 {
		r.failedGeneration(generation)
		return nil, nil
	}
	for _, probeEndpoint := range evidence.Private.Endpoints {
		host, _, err := net.SplitHostPort(probeEndpoint)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.Is4() || !ip.IsPrivate() {
			continue
		}
		if evidence.State != model.ClientLocal && !controlIPRouted(ip, evidence.AllowedIPs) {
			continue
		}
		transport, err := r.privateTransport(evidence, host)
		if err != nil {
			continue
		}
		cfg := evidence.Private
		cfg.Endpoints = []string{probeEndpoint}
		check, stop := context.WithTimeout(ctx, localEndpointTimeout)
		ok, probeErr := (SystemLocalProbe{HTTPClient: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}).Reachable(check, cfg, evidence.DeviceID, NetworkSnapshot{})
		stop()
		if !ok {
			if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
			if isAdmissionFailure(probeErr) {
				return nil, probeErr
			}
			continue
		}
		scheme, port := "http", ports.HTTP
		if port == 0 {
			scheme, port = "https", ports.HTTPS
		}
		endpoint := &controlEndpoint{origin: url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(int(port)))}, transport: transport, generation: generation, verifiedAt: time.Now()}
		if scheme == "http" && ports.HTTPS != 0 {
			secure := url.URL{Scheme: "https", Host: net.JoinHostPort(host, strconv.Itoa(int(ports.HTTPS)))}
			endpoint.httpsOrigin = &secure
		}
		r.mu.Lock()
		if generation != r.generation {
			r.mu.Unlock()
			if closer, ok := transport.(interface{ CloseIdleConnections() }); ok {
				closer.CloseIdleConnections()
			}
			return nil, nil
		}
		r.endpoint = endpoint
		r.ports[fnID] = ports
		r.mu.Unlock()
		r.logger.Info("fnOS control route selected", "fn_id", fnID, "route", "nas", "origin", endpoint.origin.String(), "state", evidence.State)
		return endpoint, nil
	}
	r.failedGeneration(generation)
	return nil, nil
}
func controlIPRouted(ip netip.Addr, routes []string) bool {
	for _, route := range routes {
		prefix, err := netip.ParsePrefix(route)
		if err == nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}
func (r *nasControlRouter) privateTransport(e controlRouteEvidence, host string) (http.RoundTripper, error) {
	var base *http.Transport
	if e.State != model.ClientLocal && r.privateClient != nil {
		if transport, ok := r.privateClient.Transport.(*http.Transport); ok {
			base = transport.Clone()
		} else if r.privateClient.Transport != nil {
			return r.privateClient.Transport, nil
		}
	}
	if base == nil {
		if e.State == model.ClientLocal {
			base = localProbeTransport(e.Snapshot.interfaceForEndpoint(net.JoinHostPort(host, "1")))
		} else {
			prefix, err := netip.ParsePrefix(e.ClientAddress)
			if err != nil {
				return nil, err
			}
			base = http.DefaultTransport.(*http.Transport).Clone()
			base.DialContext = (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IP(prefix.Addr().AsSlice())}, Timeout: 2 * time.Second}).DialContext
		}
	}
	base.Proxy = nil
	base.TLSHandshakeTimeout = 3 * time.Second
	if base.TLSClientConfig == nil {
		base.TLSClientConfig = &tls.Config{}
	} else {
		base.TLSClientConfig = base.TLSClientConfig.Clone()
	}
	// The destination IP belongs to this authenticated NAS; keep normal hostname
	// and root verification for the NAS's canonical certificate on HTTPS ports.
	base.TLSClientConfig.ServerName = e.FNID + ".fnos.net"
	// Add the existing fnOS root while retaining TLS verification, even though
	// the URL below contains an IP rather than its certificate hostname.
	trust := newFNOSTransport(base)
	if trust.err != nil {
		return nil, trust.err
	}
	return trust.fnos, nil
}
func (r *nasControlRouter) failedGeneration(generation uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.generation == generation {
		r.retryAfter = time.Now().Add(time.Minute)
	}
}
func (r *nasControlRouter) failed(endpoint *controlEndpoint, fnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.endpoint == endpoint {
		r.closeEndpointLocked()
		r.retryAfter = time.Now().Add(time.Minute)
		// Rediscover changed Web ports on a later attempt, without looping on WAN.
		delete(r.ports, fnID)
		r.refreshPorts = true
		r.logger.Info("fnOS control route fallback", "fn_id", fnID, "route", "fn-connect")
	}
}

func (r *nasControlRouter) transport(gateway http.RoundTripper) http.RoundTripper {
	if gateway == nil {
		gateway = newFNOSTransport(nil)
	}
	return controlTransport{router: r, gateway: gateway}
}

type controlTransport struct {
	router  *nasControlRouter
	gateway http.RoundTripper
}

func (t controlTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	host := request.URL.Hostname()
	if request.URL.Scheme != "https" || !isFNOSTrustHost(host) || !strings.HasSuffix(host, ".fnos.net") {
		return t.gateway.RoundTrip(request)
	}
	// Relay payloads must retain their FN Connect transport, regardless of the
	// control route. Only application HTTP and fnOS authentication sockets move.
	if !strings.HasPrefix(request.URL.Path, gatewayApplicationPath+"/") && request.URL.Path != gatewayApplicationPath && request.URL.Path != "/websocket" {
		return t.gateway.RoundTrip(request)
	}
	if strings.HasSuffix(request.URL.Path, "/relay") {
		return t.gateway.RoundTrip(request)
	}
	fnID := strings.TrimSuffix(host, ".fnos.net")
	endpoint, err := t.router.endpointFor(request.Context(), fnID)
	if err != nil {
		return nil, err
	}
	if endpoint == nil {
		return t.gateway.RoundTrip(request)
	}
	direct := directControlRequest(request, endpoint.origin)
	response, err := endpoint.transport.RoundTrip(direct)
	// An advertised HTTPS port is the only permitted automatic redirect target.
	// No token is forwarded to an arbitrary Location, and business writes are
	// never replayed after a redirect or an ambiguous transport failure.
	readOnly := request.Method == http.MethodGet || request.Method == http.MethodHead
	canTryHTTPS := readOnly && endpoint.httpsOrigin != nil && (request.Body == nil || request.Body == http.NoBody)
	redirectHTTPS := false
	if err == nil && canTryHTTPS && response.StatusCode >= 300 && response.StatusCode < 400 {
		location, parseErr := response.Location()
		redirectHTTPS = parseErr == nil && location.Scheme == "https" && location.Host == endpoint.httpsOrigin.Host && location.Path == request.URL.Path && location.RawQuery == request.URL.RawQuery
	}
	if canTryHTTPS && (err != nil || redirectHTTPS) && request.Context().Err() == nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		response, err = endpoint.transport.RoundTrip(directControlRequest(request, *endpoint.httpsOrigin))
		if err == nil {
			secure := *endpoint
			secure.origin = *endpoint.httpsOrigin
			secure.httpsOrigin = nil
			t.router.mu.Lock()
			if t.router.endpoint == endpoint {
				t.router.endpoint = &secure
			}
			t.router.mu.Unlock()
		}
	}
	if err == nil {
		// Cookie persistence and error reporting use the original logical origin.
		response.Request = request
		return response, nil
	}
	if request.Context().Err() != nil {
		if errors.Is(request.Context().Err(), context.DeadlineExceeded) {
			t.router.failed(endpoint, fnID)
		}
		return nil, err
	}
	t.router.failed(endpoint, fnID)
	// Read requests may fall back. A write is replayable only if dialing failed
	// before bytes could be sent; ambiguous write failures are returned as-is.
	var operation *net.OpError
	safe := request.Method == http.MethodGet || request.Method == http.MethodHead || errors.As(err, &operation) && operation.Op == "dial"
	if !safe {
		return nil, err
	}
	retry := request.Clone(request.Context())
	if request.Body != nil && request.Body != http.NoBody {
		if request.GetBody == nil {
			return nil, err
		}
		retry.Body, err = request.GetBody()
		if err != nil {
			return nil, err
		}
	}
	return t.gateway.RoundTrip(retry)
}

func directControlRequest(request *http.Request, origin url.URL) *http.Request {
	direct := request.Clone(request.Context())
	direct.URL.Scheme = origin.Scheme
	direct.URL.Host = origin.Host
	direct.Host = origin.Host
	direct.Header.Del("Cookie")
	// Browser-domain/Secure cookie selection already happened at the canonical
	// origin. Adapt only the account token; relay/access-code cookies stay on WAN.
	for _, cookie := range request.Cookies() {
		if cookie.Name == "fnos-token" {
			direct.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
			break
		}
	}
	if direct.Header.Get("Origin") != "" {
		direct.Header.Set("Origin", origin.String())
	}
	return direct
}

// Port hints are a new disposable cache, separate from the existing config and
// credential schemas. Historical clients ignore it; no migration is required.
func (s *ConfigStore) fnosWebPortsPath(fnID string) string {
	digest := sha256.Sum256([]byte(fnID))
	return s.path + ".fnos-web-" + hex.EncodeToString(digest[:8]) + ".json"
}
func (s *ConfigStore) saveFNOSWebPorts(fnID string, ports DiscoveryPorts) error {
	if _, err := normalizeFNID(fnID); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Version int            `json:"version"`
		FNID    string         `json:"fnId"`
		Ports   DiscoveryPorts `json:"ports"`
	}{1, fnID, ports})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	return writeClientFileAtomic(s.fnosWebPortsPath(fnID), data)
}
func (s *ConfigStore) loadFNOSWebPorts(fnID string) (DiscoveryPorts, error) {
	if normalized, err := normalizeFNID(fnID); err != nil || normalized != fnID {
		return DiscoveryPorts{}, errors.New("invalid fnOS Web cache account")
	}
	file, err := os.Open(s.fnosWebPortsPath(fnID))
	if err != nil {
		return DiscoveryPorts{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) > 4096 {
		return DiscoveryPorts{}, errors.New("invalid fnOS Web port cache")
	}
	var cache struct {
		Version int            `json:"version"`
		FNID    string         `json:"fnId"`
		Ports   DiscoveryPorts `json:"ports"`
	}
	if err := model.DecodeStrict(data, &cache); err != nil {
		return DiscoveryPorts{}, err
	}
	if cache.Version != 1 || cache.FNID != fnID {
		return DiscoveryPorts{}, errors.New("invalid fnOS Web port cache identity")
	}
	return cache.Ports, nil
}

func (m *Manager) controlTransport() http.RoundTripper {
	if m.control == nil {
		return nil
	}
	return m.control.transport(nil)
}

// The connection worker owns the input evidence. Publish immutable copies so
// background watch/proxy/authentication never reads operation-owned fields.
func (m *Manager) publishControlRoute(config LocalConfig, snapshot NetworkSnapshot) {
	if m.control == nil || m.privateAPIConfig == nil {
		return
	}
	evidence := controlRouteEvidence{FNID: config.FNID, DeviceID: config.DeviceID, ClientAddress: config.Configuration.ClientAddress, State: m.Status().State, Private: *clonePrivateAPIConfiguration(m.privateAPIConfig), Snapshot: snapshot}
	if m.lastPlan != nil {
		evidence.AllowedIPs = append([]string(nil), m.lastPlan.AllowedIPs...)
	}
	if m.control.update(evidence) {
		m.cancelConfigurationWatch()
		m.stopAdminWebActivity()
	}
}
