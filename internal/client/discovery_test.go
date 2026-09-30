package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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
			"nonce=00112233445566778899aabbccddeeff&timestamp=1700000000000&sign=366c9fcbf558b004fb1a3617cf5006e8" {
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
			return "00112233445566778899aabbccddeeff", nil
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
