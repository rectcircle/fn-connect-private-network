//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func privateDNSFixture(t *testing.T) (*PrivateAPIService, model.Device, *atomic.Int32) {
	t.Helper()
	store, e := OpenService(t.TempDir(), nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	lans := []string{"192.168.50.0/24"}
	if _, e = store.UpdateNetworks(t.Context(), NetworkUpdate{LANCIDRs: &lans}); e != nil {
		t.Fatal(e)
	}
	device, _, e := createTestDevice(store, "Mac", testPublicKey(1))
	if e != nil {
		t.Fatal(e)
	}
	service := &PrivateAPIService{store: store, endpoints: []string{"192.168.50.10:54790"}}
	calls := new(atomic.Int32)
	store.dns.LAN = func(context.Context, string, uint16, dns.Environment) (model.DNSAnswer, error) {
		calls.Add(1)
		return model.DNSAnswer{Result: "answer", Source: "lan-dns", Addresses: []string{"192.168.50.20"}}, nil
	}
	return service, device, calls
}

func servePrivateDNS(t *testing.T, s *PrivateAPIService, input model.DNSQuery) *httptest.ResponseRecorder {
	t.Helper()
	data, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, model.DNSQueryPath, bytes.NewReader(data))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestPrivateDNSAcceptsRepeatedQueriesWithoutAuthentication(t *testing.T) {
	s, _, _ := privateDNSFixture(t)
	for i := 0; i < 80; i++ {
		w := servePrivateDNS(t, s, model.DNSQuery{Name: "abc", Type: "A"})
		var answer model.DNSAnswer
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer.Name != "abc" || len(answer.Addresses) != 1 {
			t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(w.Body.Bytes(), &fields)
		for _, extra := range []string{"signature", "nonce", "payload"} {
			if _, ok := fields[extra]; ok {
				t.Fatalf("extra authentication field %s", extra)
			}
		}
	}
}
func TestPrivateDNSDoesNotDependOnDeviceRegistration(t *testing.T) {
	s, _, _ := privateDNSFixture(t)
	view := s.store.snapshot()
	view.state.Devices = nil
	s.store.publish(view)
	w := servePrivateDNS(t, s, model.DNSQuery{Name: "abc", Type: "A"})
	if w.Code != http.StatusOK {
		t.Fatalf("DNS depended on device registration: %d %s", w.Code, w.Body.String())
	}
}
func TestPrivateDNSRejectsUnsupportedQueries(t *testing.T) {
	for _, queryType := range []string{"TXT", "AAAA"} {
		s, _, calls := privateDNSFixture(t)
		w := servePrivateDNS(t, s, model.DNSQuery{Name: "abc", Type: queryType})
		if w.Code != http.StatusBadRequest || calls.Load() != 0 {
			t.Fatalf("unsupported query accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestPrivateDNSApexDoesNotQueryUpstream(t *testing.T) {
	s, _, calls := privateDNSFixture(t)
	w := servePrivateDNS(t, s, model.DNSQuery{Name: "", Type: "A"})
	var answer model.DNSAnswer
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &answer) != nil || answer.Source != "nas" || answer.Name != "" || calls.Load() != 0 {
		t.Fatalf("apex queried upstream or failed: %d %s", w.Code, w.Body.String())
	}
}
