package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestHTTPAndWebSocketFailuresRetainReasons(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		code      model.ErrorCode
		retryable bool
		detail    string
	}{
		{"gateway_auth", 200, "invalid token", model.ErrorAuthRequired, false, "invalid token"},
		{"gateway_auth_whitespace", 200, " \ninvalid token\r\n", model.ErrorAuthRequired, false, "invalid token"},
		{"gateway_auth_forbidden", 403, "invalid token", model.ErrorAuthRequired, false, "invalid token"},
		{"unauthorized", 401, "session expired", model.ErrorAuthRequired, false, "session expired"},
		{"forbidden", 403, "forbidden origin", model.ErrorPermissionDenied, false, "forbidden origin"},
		{"not_found", 404, "not found", model.ErrorNotFound, false, "not found"},
		{"revoked", 410, "device disabled", model.ErrorDeviceRevoked, false, "device disabled"},
		{"capacity", 429, "relay full", model.ErrorResourceExhausted, true, "relay full"},
		{"gateway", 502, "bad gateway", model.ErrorUnavailable, true, "bad gateway"},
		{"unavailable", 503, `{"error":{"code":"UNAVAILABLE","message":"network is stale","retryable":true,"operation":"ipc.apply","detail":"nftables permission denied","requestId":"root-request"}}`, model.ErrorUnavailable, true, "nftables permission denied"},
		{"timeout", 504, "gateway timeout", model.ErrorTimeout, true, "gateway timeout"},
		{"html", 403, "<html>Cookie: canary-secret</html>", model.ErrorPermissionDenied, false, "HTML response omitted"},
		{"secret", 403, "Authorization: Bearer canary-secret", model.ErrorPermissionDenied, false, "redacted"},
		{"json", 502, `{"password":"canary-secret"}`, model.ErrorUnavailable, true, "unrecognized JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Request-ID", "http-request")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			remote, err := NewRemoteClient(server.URL, nil, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, httpErr := remote.Bootstrap(context.Background())
			target, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http") + "/relay")
			_, wsErr := dialRelay(context.Background(), target, func() ([]Cookie, error) { return nil, nil })
			for name, err := range map[string]error{"http": httpErr, "websocket": wsErr} {
				if err == nil {
					t.Fatalf("%s accepted HTTP %d", name, test.status)
				}
				failure := model.PublicError(err)
				expectedCode := test.code
				if name == "http" && test.status == 404 {
					expectedCode = model.ErrorServerUnavailable
				}
				if failure.Code != expectedCode || failure.Retryable != test.retryable || failure.HTTPStatus != test.status ||
					!strings.Contains(failure.Detail, test.detail) || failure.Operation == "" || failure.RequestID == "" {
					t.Fatalf("%s failure = %+v", name, failure)
				}
				data, _ := json.Marshal(failure)
				if strings.Contains(string(data), "canary-secret") {
					t.Fatalf("secret leaked: %s", data)
				}
			}
		})
	}
}

func TestDiscoveryBusinessAndDecodeErrorsAreClassified(t *testing.T) {
	for _, test := range []struct {
		body       string
		code       model.ErrorCode
		remoteCode string
	}{
		{`{"code":5000,"msg":"invalid sign"}`, model.ErrorDiscoveryFailed, "5000"},
		{`{"code":3000037,"msg":"Not Found Error"}`, model.ErrorDiscoveryFailed, "3000037"},
		{`<html>login</html>`, model.ErrorProtocol, ""},
		{`{}`, model.ErrorProtocol, ""},
		{`{"code":0}`, model.ErrorProtocol, ""},
		{`{"code":0,"data":{}} {}`, model.ErrorProtocol, ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, test.body)
		}))
		client := DiscoveryClient{URL: server.URL, HTTPClient: server.Client()}
		_, err := client.Discover(context.Background(), "home-nas")
		server.Close()
		if err == nil {
			t.Fatal("invalid discovery succeeded")
		}
		failure := model.PublicError(err)
		if failure.Code != test.code || failure.RemoteCode != test.remoteCode || failure.Operation != "discovery.request" || failure.HTTPStatus != 200 {
			t.Fatalf("discovery error = %+v", failure)
		}
	}
}

func TestResponseReadErrorsAndMalformedJSONArePreserved(t *testing.T) {
	response := &http.Response{StatusCode: 503, Body: io.NopCloser(failingReader{}), Header: make(http.Header)}
	err := HTTPResponseError(response, "test", nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) || !strings.Contains(model.PublicError(err).Detail, "unexpected EOF") {
		t.Fatalf("body read failure was lost: %+v", model.PublicError(err))
	}
	for _, body := range []string{
		`{`, `{} {}`, `"invalid token"`, `invalid token: other failure`,
		`<html>invalid token</html>`, strings.Repeat(" ", maxLocalConfigSize+1),
	} {
		response := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
		var result Bootstrap
		err := decodeResponse(response, &result)
		if err == nil || model.AsError(err).Code != model.ErrorProtocol || model.AsError(err).HTTPStatus != 200 {
			t.Fatalf("malformed JSON error = %v", err)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestTruncatedGatewayBodyIsNotAuthenticationFailure(t *testing.T) {
	for name, body := range map[string]io.Reader{
		"too_large":  strings.NewReader(strings.Repeat(" ", 8193-len("invalid token")) + "invalid token trailing data"),
		"read_error": io.MultiReader(strings.NewReader("invalid token"), failingReader{}),
	} {
		t.Run(name, func(t *testing.T) {
			response := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(body),
				Header:     make(http.Header),
			}
			if failure := HTTPResponseError(response, "test", nil); failure.Code != model.ErrorProtocol {
				t.Fatalf("incomplete body treated as authentication failure: %+v", failure)
			}
		})
	}
}

func TestRelayEventKeepsLatestFailure(t *testing.T) {
	events := make(chan error, 1)
	publishRelayEvent(events, model.NewError(model.ErrorUnavailable, "disconnected", true))
	publishRelayEvent(events, model.NewError(model.ErrorPermissionDenied, "rejected", false))
	if got := <-events; model.AsError(got).Code != model.ErrorPermissionDenied {
		t.Fatal(got)
	}
	publishRelayEvent(events, nil)
	if got := <-events; got != nil {
		t.Fatal("recovery event lost")
	}
}

// A healthy status has a nil *model.Error. Passing LastError through the error
// interface must remain safe when LOCAL probes fail and recovery checks it.
func TestAdmissionFailureWithHealthyStatus(t *testing.T) {
	status := model.ClientStatus{State: model.ClientLocal}
	if isAdmissionFailure(status.LastError) {
		t.Fatal("healthy status reported an admission failure")
	}
}
