package client

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func trustFixture(t *testing.T, expired bool, usage x509.ExtKeyUsage) (*x509.Certificate, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	rootTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fixture CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"nas.fnos.net", "fnos.net", "external.test"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
	if expired {
		leaf.NotAfter = now.Add(-time.Minute)
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return root, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func trustServer(t *testing.T, cert tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func trustBase(server *httptest.Server) *http.Transport {
	return &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool()},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		}}
}

func TestFNOSTrustTLSIsolation(t *testing.T) {
	root, cert := trustFixture(t, false, x509.ExtKeyUsageServerAuth)
	otherRoot, _ := trustFixture(t, false, x509.ExtKeyUsageServerAuth)
	expiredRoot, expiredCert := trustFixture(t, true, x509.ExtKeyUsageServerAuth)
	clientRoot, clientCert := trustFixture(t, false, x509.ExtKeyUsageClientAuth)
	for _, tc := range []struct {
		name, host string
		root       *x509.Certificate
		cert       tls.Certificate
		want       bool
	}{
		{"NAS without system root", "nas.fnos.net", root, cert, true},
		{"discovery without system root", "fnos.net", root, cert, true},
		{"unrelated domain with valid SAN", "external.test", root, cert, false},
		{"hostname mismatch", "other.fnos.net", root, cert, false},
		{"expired certificate", "nas.fnos.net", expiredRoot, expiredCert, false},
		{"wrong purpose", "nas.fnos.net", clientRoot, clientCert, false},
		{"unrelated CA", "nas.fnos.net", otherRoot, cert, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := trustServer(t, tc.cert, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
			base := trustBase(server)
			transport := newFNOSTransportWithRoot(base, tc.root, nil)
			defer transport.CloseIdleConnections()
			response, err := (&http.Client{Transport: transport}).Get("https://" + tc.host + "/")
			if response != nil {
				response.Body.Close()
			}
			if (err == nil) != tc.want {
				t.Fatalf("TLS result: %v; want success=%v", err, tc.want)
			}
			if len(base.TLSClientConfig.RootCAs.Subjects()) != 0 {
				t.Fatal("caller root pool was mutated")
			}
		})
	}
}

func TestFNOSTrustPreservesExistingRootsAndRedirectBoundary(t *testing.T) {
	root, cert := trustFixture(t, false, x509.ExtKeyUsageServerAuth)
	server := trustServer(t, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://external.test/", http.StatusFound)
			return
		}
		w.Write([]byte("ok"))
	}))
	base := trustBase(server)
	transport := newFNOSTransportWithRoot(base, root, nil)
	defer transport.CloseIdleConnections()
	_, err := (&http.Client{Transport: transport}).Get("https://nas.fnos.net/redirect")
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("redirect must not inherit the extra root: %v", err)
	}
	base.TLSClientConfig.RootCAs.AddCert(root)
	otherRoot, _ := trustFixture(t, false, x509.ExtKeyUsageServerAuth)
	withSystemRoot := newFNOSTransportWithRoot(base, otherRoot, nil)
	defer withSystemRoot.CloseIdleConnections()
	for _, host := range []string{"external.test", "nas.fnos.net"} {
		response, err := (&http.Client{Transport: withSystemRoot}).Get("https://" + host + "/")
		if err != nil {
			t.Fatalf("existing root rejected for %s: %v", host, err)
		}
		response.Body.Close()
	}
}

func TestFNOSTrustWebSocketAndAdminProxy(t *testing.T) {
	root, cert := trustFixture(t, false, x509.ExtKeyUsageServerAuth)
	server := trustServer(t, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/websocket" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			conn.Write(r.Context(), websocket.MessageText, []byte("ready"))
			conn.Read(r.Context())
			return
		}
		w.Write([]byte("management"))
	}))
	transport := newFNOSTransportWithRoot(trustBase(server), root, nil)
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "wss://nas.fnos.net/websocket", &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	_, message, err := conn.Read(ctx)
	conn.CloseNow()
	if err != nil || string(message) != "ready" {
		t.Fatalf("WSS message=%q err=%v", message, err)
	}
	proxy := &AdminProxy{upstreamTransport: transport,
		FNIDProvider:   func() (string, error) { return "nas", nil },
		CookieProvider: func(string) ([]Cookie, error) { return nativeGatewayCookies("nas", "web-token"), nil }}
	bootstrap, err := proxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(bootstrap.String())
	if err != nil {
		t.Fatal(err)
	}
	cookies := response.Cookies()
	response.Body.Close()
	target := *bootstrap
	target.RawQuery = ""
	request, _ := http.NewRequest(http.MethodGet, target.String(), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "management" {
		t.Fatalf("proxy response=%d %q", response.StatusCode, body)
	}
}

func TestBundledRootAndHostScope(t *testing.T) {
	root, err := isrgRootCertificate()
	if err != nil || !root.IsCA || root.Subject.CommonName != "ISRG Root X1" {
		t.Fatalf("unexpected root: %v", err)
	}
	if err := root.CheckSignatureFrom(root); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"fnos.net.evil.test", "evilfnos.net", "nested.nas.fnos.net", "127.0.0.1", "fnos.net."} {
		if isFNOSTrustHost(host) {
			t.Fatalf("out-of-scope host accepted: %s", host)
		}
	}
}
