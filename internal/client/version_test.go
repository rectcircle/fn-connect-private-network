package client

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestBootstrapRejectsBeforeReadingBusinessResponse(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"serverVersion":"99.0.0","future":true}`))
			return
		}
		called = true
		_, _ = w.Write([]byte(`{"settings":"changed type"}`))
	}))
	defer server.Close()
	remote, _ := NewRemoteClient(server.URL, nil, server.Client(), nil)
	_, err := remote.Bootstrap(context.Background())
	if !version.IsFailure(err) || called {
		t.Fatalf("business called=%v error=%v", called, err)
	}
}
func TestLocalProbeVersionIsAuthenticated(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	for _, tampered := range []bool{false, true} {
		t.Run(map[bool]string{false: "incompatible", true: "tampered"}[tampered], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input model.LocalProbeRequest
				_ = json.NewDecoder(r.Body).Decode(&input)
				nonce, _ := base64.RawURLEncoding.DecodeString(input.Nonce)
				mac := hmac.New(sha256.New, key)
				mac.Write([]byte(model.LocalProbeDomain))
				mac.Write(nonce)
				announced := "99.0.0"
				proof := model.VersionedProbeProof(mac.Sum(nil), announced)
				if tampered {
					announced = version.Current
				}
				_ = json.NewEncoder(w).Encode(model.LocalProbeResponse{ServerVersion: announced, Proof: base64.RawURLEncoding.EncodeToString(proof)})
			}))
			defer server.Close()
			// Use the endpoint helper because discovery deliberately restricts addresses to private LANs.
			nonce := []byte("01234567890123456789012345678901")
			mac := hmac.New(sha256.New, key)
			mac.Write([]byte(model.LocalProbeDomain))
			mac.Write(nonce)
			body, _ := json.Marshal(model.LocalProbeRequest{DeviceID: "d", Nonce: base64.RawURLEncoding.EncodeToString(nonce)})
			ok, err := probeLocalEndpoint(context.Background(), server.Client(), 0, server.Listener.Addr().String(), body, mac.Sum(nil))
			if ok || err == nil {
				t.Fatalf("admitted probe: %v", err)
			}
			if !tampered && !version.IsFailure(err) {
				t.Fatalf("lost incompatible version: %v", err)
			}
			if tampered && model.AsError(err).Code != model.ErrorProtocol {
				t.Fatalf("tampered proof accepted: %v", err)
			}
		})
	}
}
func TestCachedConfigurationCannotBypassVersionGate(t *testing.T) {
	store := configuredManagerStore(t)
	remote := &fakeRemoteService{bootstrap: Bootstrap{ServerVersion: "99.0.0"}, configuration: managerClientConfiguration()}
	network := &fakePrivilegedNetwork{}
	manager, err := NewManager(ManagerOptions{Store: store, Discoverer: fakeDiscoverer{}, Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) { return remote, nil }, Privileged: network, Bridge: &fakeBridge{}, Probe: fakeLocalProbe{}})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Connect(context.Background())
	if !version.IsFailure(err) || manager.Status().State != model.ClientError {
		t.Fatalf("cache bypass: %v %+v", err, manager.Status())
	}
	if config, err := store.Load(); err != nil || config == nil {
		t.Fatalf("identity removed: %v", err)
	}
}

func TestLocalSelectionPreservesTerminalVersionError(t *testing.T) {
	store := configuredManagerStore(t)
	mismatch := version.Check(version.Current, "99.0.0")
	network := &fakePrivilegedNetwork{}
	manager, err := NewManager(ManagerOptions{Store: store, Discoverer: fakeDiscoverer{}, Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
		return &fakeRemoteService{configuration: managerClientConfiguration()}, nil
	}, Privileged: network, Bridge: &fakeBridge{}, Probe: rejectingVersionProbe{err: mismatch}})
	if err != nil {
		t.Fatal(err)
	}
	manager.localProbeConfig = &model.LocalProbeConfiguration{Endpoints: []string{"192.168.1.2:54790"}, Key: "cached"}
	err = manager.Connect(context.Background())
	if !version.IsFailure(err) || manager.Status().State != model.ClientError {
		t.Fatalf("lost local rejection: %v %+v", err, manager.Status())
	}
}

type rejectingVersionProbe struct{ err error }

func (p rejectingVersionProbe) Snapshot() (NetworkSnapshot, error) { return NetworkSnapshot{}, nil }
func (p rejectingVersionProbe) Reachable(context.Context, model.LocalProbeConfiguration, string, NetworkSnapshot) (bool, error) {
	return false, p.err
}

func TestHTTPResponseVersionCannotBeHiddenByCachedConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-FnCPN-Version", "99.0.0")
		_ = json.NewEncoder(w).Encode(managerClientConfiguration())
	}))
	defer server.Close()
	remote, _ := NewRemoteClient(server.URL, nil, server.Client(), nil)
	_, err := remote.Configuration(context.Background(), "device-1")
	if !version.IsFailure(err) {
		t.Fatalf("accepted changed server: %v", err)
	}
}

// Admission must recover gateway authentication without skipping or infinitely
// retrying version validation. Use a real RemoteClient to exercise /version.
func TestVersionAdmissionRecoversGatewaySession(t *testing.T) {
	for _, outcome := range []string{"compatible", "incompatible", "rejected"} {
		t.Run(outcome, func(t *testing.T) {
			store := configuredManagerStore(t)
			session := NativeSession{Version: nativeSessionVersion, FNID: "home-nas", Username: "test", DeviceID: "stable-device", Token: "expired", LongToken: "long-token", Secret: base64.StdEncoding.EncodeToString([]byte("session-secret"))}
			if err := store.SaveNativeSession(session); err != nil {
				t.Fatal(err)
			}
			if err := store.SaveNativeGatewayCookies(session.FNID, session.Token); err != nil {
				t.Fatal(err)
			}
			recovered := session
			recovered.Token = "renewed"
			authenticator := &fakeNativeAuthenticator{recovered: recovered}
			versionCalls, configurationCalls := 0, 0
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/version":
					versionCalls++
					if outcome == "rejected" || !strings.Contains(r.Header.Get("Cookie"), "renewed") {
						_, _ = w.Write([]byte("invalid token"))
						return
					}
					serverVersion := version.Current
					if outcome == "incompatible" {
						serverVersion = "99.0.0"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"serverVersion": serverVersion})
				case "/api/v1/devices/device-1/config":
					configurationCalls++
					_ = json.NewEncoder(w).Encode(managerClientConfiguration())
				default:
					http.NotFound(w, r)
				}
			}))
			defer gateway.Close()
			manager, err := NewManager(ManagerOptions{Store: store, Discoverer: fakeDiscoverer{result: Discovery{FN: []string{"home-nas.fnos.net:443"}}}, Remote: func(_ string, cookies []Cookie, save func([]Cookie) error) (RemoteService, error) {
				// The production factory uses fnos.net; retarget only the test cookie domain.
				origin, _ := url.Parse(gateway.URL)
				for i := range cookies {
					cookies[i].Domain = origin.Hostname()
				}
				return NewRemoteClient(gateway.URL, cookies, gateway.Client(), save)
			}, Authenticator: authenticator, Privileged: &fakePrivilegedNetwork{}, Bridge: &fakeBridge{endpoint: "127.0.0.1:51821"}, Probe: fakeLocalProbe{}})
			if err != nil {
				t.Fatal(err)
			}
			err = manager.Connect(context.Background())
			if authenticator.recoverCalls.Load() != 1 || versionCalls != 2 {
				t.Fatalf("recoveries=%d version checks=%d err=%v", authenticator.recoverCalls.Load(), versionCalls, err)
			}
			switch outcome {
			case "compatible":
				if err != nil || configurationCalls != 1 {
					t.Fatalf("recovery did not connect: %v calls=%d", err, configurationCalls)
				}
			case "incompatible":
				if !version.IsFailure(err) || configurationCalls != 0 {
					t.Fatalf("recovery bypassed version gate: %v calls=%d", err, configurationCalls)
				}
			case "rejected":
				if model.AsError(err).Code != model.ErrorAuthRequired || configurationCalls != 0 {
					t.Fatalf("second rejection was not terminal: %v", err)
				}
			}
		})
	}
}

func TestMissingServerVersionIsTerminal(t *testing.T) {
	for _, basePath := range []string{"", gatewayApplicationPath} {
		t.Run(basePath, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
			defer server.Close()
			remote, err := NewRemoteClient(server.URL+basePath, nil, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			failure := model.AsError(remote.CheckVersion(context.Background()))
			if failure.Code != model.ErrorServerUnavailable || failure.Retryable || failure.HTTPStatus != 404 || !isAdmissionFailure(failure) {
				t.Fatalf("missing server classification: %#v", failure)
			}
			network := &fakePrivilegedNetwork{status: model.ClientPrivilegedStatus{Active: true}}
			bridge := &fakeBridge{connected: true}
			manager, err := NewManager(ManagerOptions{Store: configuredManagerStore(t), Discoverer: fakeDiscoverer{}, Probe: fakeLocalProbe{}, Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) { return remote, nil }, Bridge: bridge, Privileged: network})
			if err != nil {
				t.Fatal(err)
			}
			_ = manager.fail(failure)
			if network.removed == 0 || network.status.Active || bridge.connected {
				t.Fatal("missing server left stale network active")
			}
			status := manager.Status()
			if status.State != model.ClientServerUnavailable {
				t.Fatalf("status: %#v", status)
			}
			if canAutoConnect(&LocalConfig{DeviceID: "device", AutoConnect: true}, status) {
				t.Fatal("missing server must require manual retry")
			}
		})
	}
}
