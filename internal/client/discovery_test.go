package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestDiscoveryClientSignsRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != `{"fnId":"home-nas"}` {
			t.Errorf("body = %s", body)
		}
		if value := request.Header.Get("fn-sign"); value !=
			"1487475ba1ea4ec5d5ead8b9fd7af8865a351a2558271060fac318b4d5ad893c" {
			t.Errorf("fn-sign = %q", value)
		}
		if value := request.Header.Get("authx"); value !=
			"nonce=123456&timestamp=1700000000000&sign=fb9fc402bf3a5bd5ef415788163ecc85" {
			t.Errorf("authx = %q", value)
		}
		_, _ = writer.Write([]byte(`{
			"code": 0,
			"msg": "",
			"data": {
				"ipv4": ["192.168.71.2"],
				"ipv6": [],
				"publicIpv4": [],
				"publicIpv6": ["2001:db8::2"],
				"fn": ["home-nas.fnos.net:443"],
				"port": {"httpsPort": 5667, "httpPort": 5666},
				"checkSum": "1",
				"ver": "3.0.0",
				"forbbidPublicIpv6": false
			}
		}`))
	}))
	defer server.Close()

	client := DiscoveryClient{
		URL:        server.URL,
		HTTPClient: server.Client(),
		Now: func() time.Time {
			return time.UnixMilli(1700000000000)
		},
		Nonce: func() (string, error) {
			return "123456", nil
		},
	}
	result, err := client.Discover(context.Background(), "https://HOME-NAS.fnos.net/")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(result.PublicIPv6) != 1 ||
		result.PublicIPv6[0] != "2001:db8::2" ||
		result.Port.HTTPS != 5667 {
		t.Fatalf("discovery = %+v", result)
	}
}

func TestDiscoveryDefaultNonceMatchesBrowserProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != discoveryPath {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		auth, err := url.ParseQuery(request.Header.Get("authx"))
		if err != nil {
			t.Errorf("decode authx: %v", err)
		}
		nonce := auth.Get("nonce")
		value, err := strconv.Atoi(nonce)
		if err != nil || len(nonce) != 6 || value < 100000 || value > 999999 {
			_, _ = writer.Write([]byte(`{"code":5000,"msg":"invalid sign"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"code":0,"data":{"fn":["home-nas.fnos.net:443"]}}`))
	}))
	defer server.Close()

	// Do not inject Nonce: this must exercise the generator used on real devices.
	client := DiscoveryClient{URL: server.URL + discoveryPath, HTTPClient: server.Client()}
	result, err := client.Discover(context.Background(), "home-nas")
	if err != nil {
		t.Fatalf("discover with default nonce: %v", err)
	}
	if len(result.FN) != 1 || result.FN[0] != "home-nas.fnos.net:443" {
		t.Fatalf("discovery = %+v", result)
	}
}

func TestDiscoveryNonceUsesSixDecimalDigits(t *testing.T) {
	for range 128 {
		nonce, err := discoveryNonce()
		if err != nil {
			t.Fatal(err)
		}
		value, err := strconv.Atoi(nonce)
		if err != nil || len(nonce) != 6 || value < 100000 || value > 999999 {
			t.Fatalf("nonce %q does not match the browser's six-digit decimal format", nonce)
		}
	}
}

func TestDiscoveryAuthSignBrowserVectors(t *testing.T) {
	// Independently computed from the public 1MPg8Gvv7C7Lrf46.js signing formula.
	// Source SHA256: e2c352f8dd576c2442319fdfdf3da85e383ac0a9712046c8e39b6a826b4baee7.
	for _, test := range []struct {
		nonce string
		sign  string
	}{
		{"100000", "3fd425082116754fbf513baffb6454ff"},
		{"123456", "fb9fc402bf3a5bd5ef415788163ecc85"},
		{"999999", "ad13236f321d2b041bf172507bfb7c1a"},
	} {
		t.Run(test.nonce, func(t *testing.T) {
			if got := discoveryAuthSign([]byte(`{"fnId":"home-nas"}`), test.nonce, "1700000000000"); got != test.sign {
				t.Fatalf("authx sign = %q, want %q", got, test.sign)
			}
		})
	}
}
