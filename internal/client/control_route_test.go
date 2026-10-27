package client

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type controlDiscovery struct {
	calls atomic.Int32
	ports DiscoveryPorts
}

func (d *controlDiscovery) Discover(context.Context, string) (Discovery, error) {
	d.calls.Add(1)
	return Discovery{Port: d.ports}, nil
}
func controlTestRouter(t *testing.T) *nasControlRouter {
	t.Helper()
	return newNASControlRouter(NewConfigStore(filepath.Join(t.TempDir(), "config.json"), newMemorySecretStore()), &controlDiscovery{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
func controlTestEvidence(state model.ClientState) controlRouteEvidence {
	return controlRouteEvidence{FNID: "home-nas", DeviceID: "device-1", State: state, ClientAddress: "10.253.203.2/32", AllowedIPs: []string{"192.168.71.0/24"}, Private: model.PrivateAPIConfiguration{Endpoints: []string{"192.168.71.2:54790"}, Key: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))}}
}
func controlResponse(req *http.Request, code int, body string) *http.Response {
	return &http.Response{Request: req, StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func controlPin(r *nasControlRouter, state model.ClientState, transport http.RoundTripper) {
	r.update(controlTestEvidence(state))
	r.endpoint = &controlEndpoint{origin: url.URL{Scheme: "http", Host: "192.168.71.2:5666"}, transport: transport, generation: r.generation, verifiedAt: time.Now()}
}

func TestControlRouteDirectRequestsKeepAuthenticationAndAvoidWAN(t *testing.T) {
	for _, state := range []model.ClientState{model.ClientLocal, model.ClientDirect, model.ClientRelay} {
		t.Run(string(state), func(t *testing.T) {
			r := controlTestRouter(t)
			var directCalls, gatewayCalls int
			controlPin(r, state, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				directCalls++
				if req.URL.Host != "192.168.71.2:5666" || req.URL.Scheme != "http" || req.Host != req.URL.Host {
					t.Error("wrong direct destination")
				}
				if req.Header.Get("Cookie") != "fnos-token=cli-token" {
					t.Error("direct route leaked routing/access-code cookies or dropped token")
				}
				if req.Header.Get("X-FnCPN-Client-Version") != version.Current {
					t.Error("version header lost")
				}
				switch req.URL.Path {
				case "/app/fncpn/version":
					return controlResponse(req, 200, `{"serverVersion":"`+version.Current+`"}`), nil
				case "/app/fncpn/api/v1/devices/device-1/config":
					if req.URL.Query().Get("watch") != "1" {
						t.Error("watch query lost")
					}
					return controlResponse(req, 200, `{"changed":false,"cursor":"cursor-2"}`), nil
				default:
					t.Error("unexpected route")
					return nil, errors.New("unexpected route")
				}
			}))
			client, err := NewRemoteClient("https://home-nas.fnos.net/app/fncpn", append(nativeGatewayCookies("home-nas", "cli-token"), Cookie{Name: accessCodeCookieName, Value: "access-cookie", Domain: "home-nas.fnos.net", Path: "/", Secure: true}), &http.Client{Transport: r.transport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gatewayCalls++
				return nil, errors.New("unexpected WAN")
			}))}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := client.CheckVersion(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := client.WatchConfiguration(t.Context(), "device-1", "cursor-1"); err != nil {
				t.Fatal(err)
			}
			if directCalls != 2 || gatewayCalls != 0 {
				t.Fatalf("direct=%d gateway=%d", directCalls, gatewayCalls)
			}
		})
	}
}

func TestControlRouteFallbackDoesNotReplayAmbiguousWrites(t *testing.T) {
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("unreachable")}
	for _, test := range []struct {
		name, method string
		err          error
		code         int
		fallback     bool
	}{
		{"read network failure", "GET", errors.New("closed"), 0, true},
		{"write before send", "POST", dialErr, 0, true},
		{"write possibly committed", "POST", errors.New("unexpected EOF"), 0, false},
		{"expired token", "GET", nil, 200, false},
		{"permission denied", "POST", nil, 403, false},
		{"backend error", "GET", nil, 503, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := controlTestRouter(t)
			calls := 0
			controlPin(r, model.ClientDirect, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					_, _ = io.ReadAll(req.Body)
					req.Body.Close()
				}
				if test.err != nil {
					return nil, test.err
				}
				return controlResponse(req, test.code, "invalid token"), nil
			}))
			transport := r.transport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "home-nas.fnos.net" || req.Header.Get("Cookie") != "fnos-token=account; mode=relay" {
					t.Error("fallback changed canonical credentials")
				}
				body, _ := io.ReadAll(req.Body)
				if string(body) != "payload" {
					t.Error("fallback did not recreate body")
				}
				return controlResponse(req, 200, "ok"), nil
			}))
			req, _ := http.NewRequest(test.method, "https://home-nas.fnos.net/app/fncpn/api/v1/admin/networks", strings.NewReader("payload"))
			req.Header.Set("Cookie", "fnos-token=account; mode=relay")
			response, err := transport.RoundTrip(req)
			if response != nil {
				response.Body.Close()
			}
			if test.err == nil && err != nil {
				t.Fatal(err)
			}
			if (calls == 1) != test.fallback {
				t.Fatalf("WAN calls=%d", calls)
			}
		})
	}
}

func TestControlRouteResolvesAuthenticatedNASAndCachesPorts(t *testing.T) {
	r := controlTestRouter(t)
	discovery := &controlDiscovery{ports: DiscoveryPorts{HTTP: 18080, HTTPS: 18443}}
	r.discoverer = discovery
	evidence := controlTestEvidence(model.ClientDirect)
	r.update(evidence)
	var probes atomic.Int32
	r.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		probes.Add(1)
		if req.Header.Get("Cookie") != "" || req.URL.Host != "192.168.71.2:54790" {
			t.Error("probe leaked credentials or used Web port")
		}
		var input model.LocalProbeRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return nil, err
		}
		nonce, _ := base64.RawURLEncoding.DecodeString(input.Nonce)
		key, _ := base64.RawURLEncoding.DecodeString(evidence.Private.Key)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(model.LocalProbeDomain))
		mac.Write(nonce)
		body, _ := json.Marshal(model.LocalProbeResponse{ServerVersion: version.Current, Proof: base64.RawURLEncoding.EncodeToString(model.VersionedProbeProof(mac.Sum(nil), version.Current))})
		return controlResponse(req, 200, string(body)), nil
	})}
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			endpoint, err := r.endpointFor(t.Context(), "home-nas")
			if err != nil || endpoint == nil {
				t.Errorf("no authenticated route: %v", err)
				return
			}
			if endpoint.origin.Host != "192.168.71.2:18080" {
				t.Error("ignored discovered Web port")
			}
		}()
	}
	wait.Wait()
	if discovery.calls.Load() != 1 || probes.Load() != 1 {
		t.Fatalf("discovery=%d probes=%d", discovery.calls.Load(), probes.Load())
	}
	ports, err := r.store.loadFNOSWebPorts("home-nas")
	if err != nil || ports != discovery.ports {
		t.Fatal("ports were not cached")
	}
	// Restart reuses the disposable cache without introducing a new server call.
	r.deactivate()
	r.update(evidence)
	if endpoint, err := r.endpointFor(t.Context(), "home-nas"); err != nil || endpoint == nil {
		t.Fatal("route failed after restart")
	}
	if discovery.calls.Load() != 1 {
		t.Fatal("restart rediscovered unchanged ports")
	}
	if err := r.store.Forget("home-nas"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.store.fnosWebPortsPath("home-nas")); !os.IsNotExist(err) {
		t.Fatal("forget retained port cache")
	}
}

func TestControlRouteRejectsUnprovenOrExcludedDestinations(t *testing.T) {
	for _, test := range []string{"bad proof", "excluded", "not connected", "different NAS", "version mismatch"} {
		t.Run(test, func(t *testing.T) {
			r := controlTestRouter(t)
			e := controlTestEvidence(model.ClientDirect)
			if test == "excluded" {
				e.AllowedIPs = []string{"10.253.203.0/24"}
			}
			if test == "not connected" {
				e.State = model.ClientReconnecting
			}
			r.update(e)
			r.ports["home-nas"] = DiscoveryPorts{HTTP: 5666}
			r.privateClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if test != "bad proof" && test != "version mismatch" {
					t.Error("unexpected destination probe")
				}
				if req.Header.Get("Cookie") != "" {
					t.Error("credentials sent before identity verification")
				}
				if test == "version mismatch" {
					var input model.LocalProbeRequest
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						return nil, err
					}
					nonce, _ := base64.RawURLEncoding.DecodeString(input.Nonce)
					key, _ := base64.RawURLEncoding.DecodeString(e.Private.Key)
					mac := hmac.New(sha256.New, key)
					mac.Write([]byte(model.LocalProbeDomain))
					mac.Write(nonce)
					body, _ := json.Marshal(model.LocalProbeResponse{ServerVersion: "2.0.0", Proof: base64.RawURLEncoding.EncodeToString(model.VersionedProbeProof(mac.Sum(nil), "2.0.0"))})
					return controlResponse(req, 200, string(body)), nil
				}
				return controlResponse(req, 200, `{"serverVersion":"`+version.Current+`","proof":"wrong"}`), nil
			})}
			name := "home-nas"
			if test == "different NAS" {
				name = "other-nas"
			}
			endpoint, err := r.endpointFor(t.Context(), name)
			if endpoint != nil {
				t.Fatal("unverified destination accepted")
			}
			if test == "version mismatch" && !isAdmissionFailure(err) {
				t.Fatalf("version failure was hidden: %v", err)
			}
		})
	}
}

func TestControlRouteWebSocketUsesNASWithoutGateway(t *testing.T) {
	session := webRecoverySession()
	arrived := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/websocket" || req.URL.Query().Get("type") != "main" {
			t.Error("wrong WebSocket path")
		}
		if req.Header.Get("Cookie") != "fnos-token=web-token" {
			t.Error("wrong WebSocket credential adaptation")
		}
		if req.Header.Get("Origin") != "http://"+req.Host {
			t.Error("WebSocket Origin was not adapted")
		}
		conn, err := websocket.Accept(w, req, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		si := nativeTestRequest(t, req.Context(), conn, "")
		nativeTestResponse(t, req.Context(), conn, si, map[string]any{"result": "succ", "si": "si"})
		auth := nativeTestRequest(t, req.Context(), conn, session.Secret)
		nativeTestResponse(t, req.Context(), conn, auth, map[string]any{"result": "succ", "token": "fresh-web"})
		close(arrived)
	}))
	defer server.Close()
	r := controlTestRouter(t)
	origin, _ := url.Parse(server.URL)
	controlPin(r, model.ClientDirect, server.Client().Transport)
	r.endpoint.origin = *origin
	var wan atomic.Int32
	c := NativeSessionClient{controlTransport: r.transport(roundTripFunc(func(*http.Request) (*http.Response, error) { wan.Add(1); return nil, errors.New("unexpected WAN") })), CookieHeader: func(context.Context, string) (string, error) {
		return "fnos-token=web-token; mode=relay; os-access-code=private", nil
	}}
	recovered, err := c.RecoverWeb(t.Context(), session, "Mac")
	if err != nil || recovered.Token != "fresh-web" || wan.Load() != 0 {
		t.Fatalf("direct WebSocket failed: %v, WAN=%d", err, wan.Load())
	}
	select {
	case <-arrived:
	case <-time.After(time.Second):
		t.Fatal("WebSocket did not arrive")
	}
}

func TestControlRouteLeavesRelayPayloadAndUnrelatedHostsUnchanged(t *testing.T) {
	r := controlTestRouter(t)
	controlPin(r, model.ClientDirect, roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("unexpected direct request"); return nil, nil }))
	transport := r.transport(roundTripFunc(func(req *http.Request) (*http.Response, error) { return controlResponse(req, 200, "ok"), nil }))
	for _, target := range []string{"https://home-nas.fnos.net/app/fncpn/api/v1/relay", "https://other-nas.fnos.net/app/fncpn/version", "https://example.com/app/fncpn/version"} {
		req, _ := http.NewRequest("GET", target, nil)
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
}

func TestControlRouteMissingPortHintsRetainsHistoricalGatewayPath(t *testing.T) {
	r := controlTestRouter(t)
	r.update(controlTestEvidence(model.ClientLocal))
	discovery := &controlDiscovery{}
	r.discoverer = discovery
	calls := 0
	transport := r.transport(roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return controlResponse(req, 200, "legacy"), nil
	}))
	for i := 0; i < 3; i++ {
		req, _ := http.NewRequest("GET", "https://home-nas.fnos.net/app/fncpn/version", nil)
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
	}
	if calls != 3 || discovery.calls.Load() != 1 {
		t.Fatalf("historical fallback: WAN=%d discoveries=%d", calls, discovery.calls.Load())
	}
}

func TestControlRouteHTTPSRetainsCertificateVerification(t *testing.T) {
	r := controlTestRouter(t)
	transport, err := r.privateTransport(controlTestEvidence(model.ClientLocal), "192.168.71.2")
	if err != nil {
		t.Fatal(err)
	}
	native := transport.(*http.Transport)
	defer native.CloseIdleConnections()
	if native.Proxy != nil || native.TLSClientConfig.InsecureSkipVerify || native.TLSClientConfig.ServerName != "home-nas.fnos.net" {
		t.Fatal("direct TLS changed verification or enabled an external proxy")
	}
}

func TestControlRouteOnlyUsesAdvertisedHTTPSRedirect(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		r := controlTestRouter(t)
		calls := 0
		controlPin(r, model.ClientLocal, roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.URL.Scheme == "http" {
				response := controlResponse(req, 301, "")
				target := "https://192.168.71.2:5667/app/fncpn/version"
				if !allowed {
					target = "https://unrelated.example/app/fncpn/version"
				}
				response.Header.Set("Location", target)
				return response, nil
			}
			if !allowed || req.URL.Host != "192.168.71.2:5667" {
				t.Error("credentials followed arbitrary redirect")
			}
			if req.Header.Get("Cookie") != "fnos-token=account" {
				t.Error("redirect changed token")
			}
			return controlResponse(req, 200, "ok"), nil
		}))
		secure := url.URL{Scheme: "https", Host: "192.168.71.2:5667"}
		r.endpoint.httpsOrigin = &secure
		transport := r.transport(roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("unnecessary WAN fallback")
			return nil, errors.New("WAN")
		}))
		req, _ := http.NewRequest("GET", "https://home-nas.fnos.net/app/fncpn/version", nil)
		req.Header.Set("Cookie", "fnos-token=account")
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		expected := 1
		if allowed {
			expected = 2
		}
		if calls != expected {
			t.Fatalf("allowed=%v requests=%d", allowed, calls)
		}
	}
}

func TestControlRoutePathSwitchDropsOldOrigin(t *testing.T) {
	r := controlTestRouter(t)
	controlPin(r, model.ClientDirect, roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("old NAS request"); return nil, nil }))
	next := controlTestEvidence(model.ClientLocal)
	next.FNID = "other-nas"
	next.DeviceID = "other-device"
	if !r.update(next) {
		t.Fatal("NAS switch was ignored")
	}
	if endpoint, err := r.endpointFor(t.Context(), "home-nas"); endpoint != nil || err != nil {
		t.Fatal("old NAS retained its direct route")
	}
	r.deactivate()
	if endpoint, err := r.endpointFor(t.Context(), "other-nas"); endpoint != nil || err != nil {
		t.Fatal("disconnected route remained active")
	}
}
