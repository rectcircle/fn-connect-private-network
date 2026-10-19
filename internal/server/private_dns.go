package server

import (
	"encoding/json"
	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
	"net/http"
)

// privateDNSQuery trusts the private LAN transport; DNS adds no authentication
// or replay protocol. The existing identity probe remains independent.
func (s *PrivateAPIService) privateDNSQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-FnCPN-Version", version.Current)
	reject := func(err error) { writeHTTPError(s.store.logger, w, r, err) }
	if v := r.Header.Get("X-FnCPN-Client-Version"); v != "" {
		if err := version.Check(v, version.Current); err != nil {
			reject(err)
			return
		}
	}
	var query model.DNSQuery
	if err := decodeBodyBounded(r, &query, maxProbeRequestSize); err != nil {
		reject(err)
		return
	}
	answer, err := s.store.dns.Resolve(r.Context(), query, dns.DetectEnvironment(s.store.Snapshot().Settings))
	if err != nil {
		reject(err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(answer); err != nil {
		s.store.logger.Error("write DNS response", "error", err)
	}
}
