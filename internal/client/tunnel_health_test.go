package client

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestTunnelHealthAuthenticatedProbe(t *testing.T) {
	for _, state := range []model.ClientState{model.ClientDirect, model.ClientRelay} {
		t.Run(string(state), func(t *testing.T) {
			for _, failCount := range []int{0, 1, 2} {
				calls := 0
				key := make([]byte, sha256.Size)
				m, physicalProbe, network, _, _ := coordinationFixture(t)
				physicalProbe.reachable.Store(false)
				m.privateAPIConfig = &model.PrivateAPIConfiguration{Endpoints: []string{"192.168.71.2:54790"}, Key: base64.RawURLEncoding.EncodeToString(key)}
				m.lastPlan = &model.ClientPlan{AllowedIPs: []string{"192.168.71.0/24"}}
				m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.String() != "http://192.168.71.2:54790/api/v1/probe" || r.Header.Get("Cookie") != "" {
						t.Fatalf("unexpected probe: %v", r)
					}
					if calls <= failCount {
						return nil, errors.New("unreachable")
					}
					var input model.LocalProbeRequest
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Fatal(err)
					}
					nonce, _ := base64.RawURLEncoding.DecodeString(input.Nonce)
					mac := hmac.New(sha256.New, key)
					mac.Write([]byte(model.LocalProbeDomain))
					mac.Write(nonce)
					body, _ := json.Marshal(model.LocalProbeResponse{ServerVersion: version.Current, Proof: base64.RawURLEncoding.EncodeToString(model.VersionedProbeProof(mac.Sum(nil), version.Current))})
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
				})}
				old := time.Now().Add(-10 * time.Minute)
				if err := clientNetworkHealthError(state, model.ClientPrivilegedStatus{Active: true, LastHandshake: &old}, nil, true); err != nil {
					t.Fatal(err)
				}
				err := m.tunnelHealthError(context.Background(), &LocalConfig{DeviceID: "device-1"})
				if (err != nil) != (failCount == 2) {
					t.Fatalf("failCount=%d err=%v", failCount, err)
				}
				if calls != min(failCount+1, 2) {
					t.Fatalf("calls=%d", calls)
				}
				if failCount == 0 {
					network.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", LastHandshake: &old}
					m.bridge.(*coordinatedBridge).connected.Store(true)
					m.directRetryAfter = time.Now().Add(time.Hour)
					m.setStatus(state, "existing", "utun9", nil)
					m.maintainHealth(t.Context())
					if m.Status().State != state || network.applies.Load() != 0 || network.removes.Load() != 0 || calls != 2 {
						t.Fatalf("healthy aged tunnel was changed: state=%s applies=%d removes=%d calls=%d", m.Status().State, network.applies.Load(), network.removes.Load(), calls)
					}
				}
			}
		})
	}
}

func TestTunnelHealthDoesNotProbeExcludedLAN(t *testing.T) {
	m := &Manager{privateAPIConfig: &model.PrivateAPIConfiguration{Endpoints: []string{"192.168.71.2:54790"}}, lastPlan: &model.ClientPlan{AllowedIPs: []string{"10.253.203.1/32"}}, dnsHTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("physical LAN bypass"); return nil, nil })}}
	if err := m.tunnelHealthError(t.Context(), &LocalConfig{}); err != nil {
		t.Fatal(err)
	}
}

func healthResponse(t *testing.T, request *http.Request) *http.Response {
	t.Helper()
	var input model.LocalProbeRequest
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		t.Fatal(err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(input.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, make([]byte, sha256.Size))
	mac.Write([]byte(model.LocalProbeDomain))
	mac.Write(nonce)
	body, _ := json.Marshal(model.LocalProbeResponse{ServerVersion: version.Current,
		Proof: base64.RawURLEncoding.EncodeToString(model.VersionedProbeProof(mac.Sum(nil), version.Current))})
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}
}

func healthManager(t *testing.T) (*Manager, *coordinatedNetwork) {
	t.Helper()
	m, p, n, _, _ := coordinationFixture(t)
	p.reachable.Store(false)
	m.privateAPIConfig = &model.PrivateAPIConfiguration{Endpoints: []string{"192.168.71.2:54790"},
		Key: base64.RawURLEncoding.EncodeToString(make([]byte, sha256.Size))}
	m.lastPlan = &model.ClientPlan{AllowedIPs: []string{"192.168.71.0/24"}}
	now := time.Now()
	n.status = model.ClientPrivilegedStatus{Active: true, Interface: "utun9", LastHandshake: &now}
	m.setStatus(model.ClientDirect, "ipv6", "utun9", nil)
	return m, n
}

func TestTunnelHealthAllowsRemoteLatency(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n := healthManager(t)
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			select {
			case <-time.After(time.Second):
				return healthResponse(t, r), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		})}
		m.maintainHealth(t.Context())
		if m.Status().State != model.ClientDirect || n.removes.Load() != 0 || m.tunnelHealthFailures != 0 {
			t.Fatal("remote latency was treated as a broken IPv6 tunnel")
		}
	})
}

func TestTransientTunnelProbeFailureDoesNotBlacklistIPv6(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n := healthManager(t)
		fail := true
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if fail {
				return nil, errors.New("temporary radio timeout")
			}
			return healthResponse(t, r), nil
		})}
		m.maintainHealth(t.Context())
		if m.Status().State != model.ClientDirect || n.applies.Load() != 0 || n.removes.Load() != 0 || !m.directRetryAfter.IsZero() {
			t.Fatal("one failed probe round downgraded IPv6 or started cooldown")
		}
		fail = false
		m.maintainHealth(t.Context())
		if m.tunnelHealthFailures != 0 {
			t.Fatal("successful probe did not clear suspicion")
		}
		fail = true
		m.maintainHealth(t.Context())
		if n.removes.Load() != 0 || m.tunnelHealthFailures != 1 {
			t.Fatal("nonconsecutive failures were accumulated")
		}
	})
}

func TestPersistentTunnelProbeFailureStillFallsBack(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n := healthManager(t)
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("persistent outage")
		})}
		m.maintainHealth(t.Context())
		time.Sleep(15 * time.Second)
		m.maintainHealth(t.Context())
		if m.Status().State != model.ClientRelay || n.applies.Load() == 0 || m.directRetryAfter.IsZero() {
			t.Fatalf("persistent failure did not recover through relay: %s", m.Status().State)
		}
		if m.tunnelHealthFailures != 0 {
			t.Fatal("new connection inherited failed probes")
		}
	})
}

func TestHardTunnelFailureDoesNotWaitForProbeConfirmation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n := healthManager(t)
		n.status.Active = false
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("inactive tunnel must not be probed")
			return nil, nil
		})}
		m.maintainHealth(t.Context())
		if m.Status().State != model.ClientRelay || n.applies.Load() == 0 {
			t.Fatal("hard tunnel failure was incorrectly delayed")
		}
	})
}

func TestTunnelHealthAdmissionFailureIsNotDebounced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, n := healthManager(t)
		m.tunnelHealthFailures = 1
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, model.NewError(model.ErrorVersionIncompatible, "incompatible server", false)
		})}
		m.maintainHealth(t.Context())
		status := m.Status()
		if status.LastError == nil || status.LastError.Code != model.ErrorVersionIncompatible ||
			m.tunnelHealthFailures != 0 || n.applies.Load() != 0 || !m.directRetryAfter.IsZero() {
			t.Fatal("version rejection was delayed or treated as IPv6 congestion")
		}
	})
}

func TestTunnelHealthProbeTimeoutRemainsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := healthManager(t)
		m.dnsHTTPClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		start := time.Now()
		config, _ := m.store.Load()
		if err := m.tunnelHealthError(t.Context(), config); err == nil {
			t.Fatal("unresponsive endpoint was accepted")
		}
		want := time.Duration(localProbeHealthChecks)*tunnelHealthProbeTimeout +
			time.Duration(localProbeHealthChecks-1)*localProbeHealthRetryInterval
		if elapsed := time.Since(start); elapsed != want {
			t.Fatalf("probe budget = %v, want %v", elapsed, want)
		}
	})
}
