package client

import (
	"context"
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
		{name: "unavailable", code: http.StatusServiceUnavailable, body: "offline", want: model.ErrorUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Cookie"), "fnos-token=web-token") {
					t.Errorf("probe cookie = %q", r.Header.Get("Cookie"))
				}
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := probeAdminWebSessionWithClient(
				context.Background(), server.Client(), server.URL,
				"web-token",
			)
			if test.want == "" {
				if err != nil {
					t.Fatalf("probe error = %v", err)
				}
				return
			}
			if err == nil || model.AsError(err).Code != test.want {
				t.Fatalf("probe error = %#v, want %s", err, test.want)
			}
		})
	}
}
