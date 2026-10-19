package server

import (
	"net/http"

	"github.com/rectcircle/fn-connect-private-network/internal/dns"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func (s *HTTPServer) dnsTest(w http.ResponseWriter, r *http.Request) {
	s.resolveDNS(w, r)
}
func (s *HTTPServer) resolveDNS(w http.ResponseWriter, r *http.Request) {
	var query model.DNSQuery
	if err := decodeBody(r, &query); err != nil {
		s.writeError(w, r, err)
		return
	}
	env := dns.DetectEnvironment(s.Snapshot().Settings)
	result, err := s.dns.Resolve(r.Context(), query, env)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, result)
}
