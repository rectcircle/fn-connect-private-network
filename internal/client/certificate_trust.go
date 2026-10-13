package client

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// Official self-signed ISRG Root X1: https://letsencrypt.org/certs/isrgrootx1.der
// The pinned root is used only by fnOS transports, never installed in the OS.
//
//go:embed certificates/isrgrootx1.der
var isrgRootDER []byte

func isrgRootCertificate() (*x509.Certificate, error) {
	digest := sha256.Sum256(isrgRootDER)
	if hex.EncodeToString(digest[:]) != "96bcec06264976f37460779acf28c5a7cfe8a3c0aae11a8ffcee05c0bddf08c6" {
		return nil, errors.New("bundled ISRG Root X1 fingerprint mismatch")
	}
	return x509.ParseCertificate(isrgRootDER)
}

func isFNOSTrustHost(host string) bool {
	host = strings.ToLower(host)
	if host == "fnos.net" {
		return true
	}
	if !strings.HasSuffix(host, ".fnos.net") {
		return false
	}
	id := strings.TrimSuffix(host, ".fnos.net")
	normalized, err := normalizeFNID(id)
	return err == nil && normalized == id
}

// Separate transports keep the additional root out of unrelated requests,
// including redirects. Both use ordinary TLS hostname, date and EKU checks.
type fnosTrustTransport struct {
	system *http.Transport
	fnos   *http.Transport
	err    error
}

func newFNOSTransport(base *http.Transport) *fnosTrustTransport {
	root, err := isrgRootCertificate()
	return newFNOSTransportWithRoot(base, root, err)
}

func newFNOSTransportWithRoot(base *http.Transport, root *x509.Certificate, rootErr error) *fnosTrustTransport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	transport := &fnosTrustTransport{system: base.Clone(), fnos: base.Clone()}
	if rootErr != nil {
		transport.err = rootErr
		return transport
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	// Do not mutate a caller's TLS configuration or certificate pool.
	config := transport.fnos.TLSClientConfig
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
		if config.RootCAs != nil {
			roots = config.RootCAs.Clone()
		}
	}
	roots.AddCert(root)
	config.RootCAs = roots
	transport.fnos.TLSClientConfig = config
	// A custom TLS config otherwise disables HTTP/2, even if ALPN still
	// advertises h2 (for example after cloning an already-used transport).
	transport.fnos.ForceAttemptHTTP2 = true
	return transport
}

func (t *fnosTrustTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme == "https" && isFNOSTrustHost(request.URL.Hostname()) {
		if t.err != nil {
			return nil, t.err
		}
		return t.fnos.RoundTrip(request)
	}
	return t.system.RoundTrip(request)
}

func (t *fnosTrustTransport) CloseIdleConnections() {
	t.system.CloseIdleConnections()
	t.fnos.CloseIdleConnections()
}
