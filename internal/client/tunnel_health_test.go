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
