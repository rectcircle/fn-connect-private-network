package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestAdminProxyLocalAccessToken(t *testing.T) {
	proxy := &AdminProxy{token: "access-token"}
	request := httptest.NewRequest(http.MethodGet, "/app/fncpn?fncpn_proxy=access-token", nil)
	response := httptest.NewRecorder()
	if proxy.authorizeLocalRequest(response, request) {
		t.Fatal("bootstrap request must redirect before proxying")
	}
	if response.Code != http.StatusSeeOther {
		t.Fatalf("bootstrap status = %d", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "fncpn-proxy" ||
		!cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("bootstrap cookie = %+v", cookies)
	}

	request = httptest.NewRequest(http.MethodGet, "/app/fncpn", nil)
	request.AddCookie(cookies[0])
	if !proxy.authorizeLocalRequest(httptest.NewRecorder(), request) {
		t.Fatal("valid local proxy cookie was rejected")
	}

	response = httptest.NewRecorder()
	if proxy.authorizeLocalRequest(response, httptest.NewRequest(http.MethodGet, "/app/fncpn", nil)) ||
		response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized request status = %d", response.Code)
	}
}

func TestAdminProxyDirectorReplacesBrowserCookies(t *testing.T) {
	proxy := &AdminProxy{
		FNIDProvider: func() (string, error) { return "home-nas", nil },
		CookieProvider: func(string) ([]Cookie, error) {
			return nativeGatewayCookies("home-nas", "web-token"), nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/app/fncpn", nil)
	request.Header.Set("Cookie", "fnos-token=browser-token; ost=stale")
	proxy.director(request)
	header := request.Header.Get("Cookie")
	if !strings.Contains(header, "fnos-token=web-token") ||
		strings.Contains(header, "browser-token") || strings.Contains(header, "ost=") {
		t.Fatalf("upstream cookie header = %q", header)
	}
	expected := (&url.URL{
		Scheme: "https", Host: "home-nas.fnos.net", Path: "/app/fncpn",
	}).String()
	if request.URL.String() != expected {
		t.Fatalf("upstream URL = %s", request.URL)
	}
}

func TestAdminProxyRejectsPathsOutsideApplication(t *testing.T) {
	proxy := &AdminProxy{
		FNIDProvider: func() (string, error) { return "home-nas", nil },
		CookieProvider: func(string) ([]Cookie, error) {
			return nativeGatewayCookies("home-nas", "web-token"), nil
		},
	}
	bootstrap, err := proxy.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Get(bootstrap.String())
	if err != nil {
		t.Fatal(err)
	}
	cookies := response.Cookies()
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("bootstrap response = %d, cookies = %+v", response.StatusCode, cookies)
	}
	root := *bootstrap
	root.Path = "/"
	root.RawQuery = ""
	request, _ := http.NewRequest(http.MethodGet, root.String(), nil)
	request.AddCookie(cookies[0])
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("root status = %d", response.StatusCode)
	}
}

func TestAdminWebSessionProbeClassifiesOnlyAuthenticationFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		code int
		body string
		want model.ErrorCode
	}{
		{name: "valid", code: http.StatusOK, body: `{"devices":[]}`},
		{name: "expired", code: http.StatusOK, body: "invalid token", want: model.ErrorAuthRequired},
		{name: "access-code challenge", code: http.StatusOK, body: "<html>fetch('/access_code_verify')</html>", want: model.ErrorFailedPrecondition},
		{name: "unavailable", code: http.StatusServiceUnavailable, body: "offline", want: model.ErrorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Cookie"), "fnos-token=web-token") ||
					!strings.Contains(r.Header.Get("Cookie"), "os-access-code=access-session") {
					t.Errorf("probe cookie = %q", r.Header.Get("Cookie"))
				}
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := probeAdminWebSessionWithClient(
				context.Background(), server.Client(), server.URL,
				[]Cookie{{Name: "mode", Value: "relay", Domain: "127.0.0.1", Path: "/"},
					{Name: "fnos-token", Value: "web-token", Domain: "127.0.0.1", Path: "/"},
					{Name: accessCodeCookieName, Value: "access-session", Domain: "127.0.0.1", Path: "/"}},
			)
			if test.want == "" {
				if err != nil {
					t.Fatalf("probe error = %v", err)
				}
				return
			}
			if test.want == model.ErrorFailedPrecondition && !RequiresAdditionalCredential(err) {
				t.Fatalf("access-code challenge was accepted or lost: %v", err)
			}
			if err == nil || model.AsError(err).Code != test.want {
				t.Fatalf("probe error = %#v, want %s", err, test.want)
			}
		})
	}
}

func TestAdminProxyRetriesOnlyDefiniteGatewayRejection(t *testing.T) {
	for _, test := range []struct {
		name, method, reply string
		status              int
		recover             bool
	}{
		{"read expired", http.MethodGet, "invalid token", 200, true},
		{"write expired", http.MethodPost, "invalid token", 200, true},
		{"application unauthorized", http.MethodPost, `{"error":{"code":"AUTH_REQUIRED"}}`, 401, false},
		{"application forbidden", http.MethodPost, "permission denied", 403, false},
		{"gateway offline", http.MethodPost, "invalid token", 503, false},
		{"large response", http.MethodGet, strings.Repeat("x", 9000), 200, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts, recoveries int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts++
				body, _ := io.ReadAll(r.Body)
				if test.method == http.MethodPost && string(body) != `{"enabled":true}` {
					t.Error("write body changed during retry")
				}
				token, _ := r.Cookie("fnos-token")
				if token == nil {
					t.Error("missing Web cookie")
					return
				}
				if token.Value == "fresh-web" {
					_, _ = w.Write([]byte(`{"ok":true}`))
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.reply))
			}))
			defer upstream.Close()
			upstreamURL, _ := url.Parse(upstream.URL)
			proxy := &AdminProxy{
				FNIDProvider:   func() (string, error) { return "home-nas", nil },
				CookieProvider: func(string) ([]Cookie, error) { return nativeGatewayCookies("home-nas", "old-web"), nil },
				Recover: func(_ context.Context, fnID, token string) ([]Cookie, error) {
					recoveries++
					if fnID != "home-nas" || token != "old-web" {
						t.Error("wrong rejected session")
					}
					return nativeGatewayCookies("home-nas", "fresh-web"), nil
				},
				upstreamTransport: adminTestTransport(func(r *http.Request) (*http.Response, error) {
					clone := r.Clone(r.Context())
					clone.URL.Scheme = upstreamURL.Scheme
					clone.URL.Host = upstreamURL.Host
					return upstream.Client().Transport.RoundTrip(clone)
				}),
			}
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
			target.Path = "/app/fncpn/api/v1/admin/network"
			var input io.Reader
			if test.method == http.MethodPost {
				input = strings.NewReader(`{"enabled":true}`)
			}
			request, _ := http.NewRequest(test.method, target.String(), input)
			request.AddCookie(cookies[0])
			response, err = client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if test.recover {
				if attempts != 2 || recoveries != 1 || string(body) != `{"ok":true}` {
					t.Fatalf("attempts=%d recoveries=%d response=%s", attempts, recoveries, body)
				}
			} else if attempts != 1 || recoveries != 0 || string(body) != test.reply {
				t.Fatal("ambiguous response was replayed or changed")
			}
		})
	}
}

type adminTestTransport func(*http.Request) (*http.Response, error)

func (f adminTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAdminProxyDoesNotReplayUnbufferedBodyAndRetriesAtMostOnce(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		attempts, recoveries := 0, 0
		proxy := &AdminProxy{Recover: func(context.Context, string, string) ([]Cookie, error) {
			recoveries++
			return nativeGatewayCookies("home-nas", "new-web"), nil
		}}
		transport := adminProxyTransport{proxy: proxy, upstream: adminTestTransport(func(r *http.Request) (*http.Response, error) {
			attempts++
			if r.Body != nil {
				_, _ = io.Copy(io.Discard, r.Body)
				r.Body.Close()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("invalid token")), Request: r}, nil
		})}
		req, _ := http.NewRequest(http.MethodPost, "https://home-nas.fnos.net/app/fncpn/api/v1/admin/network", strings.NewReader("body"))
		req.AddCookie(&http.Cookie{Name: "fnos-token", Value: "old-web"})
		if streaming {
			req.GetBody = nil
		}
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		expected := 2
		if streaming {
			expected = 1
		}
		if attempts != expected || recoveries != expected-1 {
			t.Fatalf("streaming=%v attempts=%d recoveries=%d", streaming, attempts, recoveries)
		}
	}
}
