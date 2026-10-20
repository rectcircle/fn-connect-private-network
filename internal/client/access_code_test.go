package client

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

// cannedTransport answers with a fixed response so the gateway semantics of the
// access-code endpoints can be exercised without a live FN Connect gateway.
type cannedTransport struct {
	request  *http.Request
	response *http.Response
}

func (c *cannedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	saved := *request
	saved.Body = nil
	c.request = &saved
	response := *c.response
	response.Request = request
	return &response, nil
}

func cannedResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header.Clone(),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// challengeHeader mirrors how net/http stores gateway response headers: the raw
// wire name is canonicalized before it is looked up.
func challengeHeader() http.Header {
	header := http.Header{}
	header.Set(accessCodeChallengeHead, "1")
	return header
}

func TestDetectAccessCodeReportsGatewayChallenge(t *testing.T) {
	cases := []struct {
		name     string
		response *http.Response
		expected bool
	}{
		{
			name:     "challenge header",
			response: cannedResponse(http.StatusUnauthorized, challengeHeader(), "interception page"),
			expected: true,
		},
		{
			name:     "verify endpoint marker in body",
			response: cannedResponse(http.StatusOK, nil, "<html>go to /access_code_verify first</html>"),
			expected: true,
		},
		{
			name: "redirect to the verify endpoint",
			response: cannedResponse(http.StatusFound, http.Header{
				"Location": []string{"/access_code_verify?next=/"},
			}, ""),
			expected: true,
		},
		{
			name:     "gateway is open",
			response: cannedResponse(http.StatusOK, nil, "fnOS dashboard"),
			expected: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &cannedTransport{response: testCase.response}
			detected, err := detectAccessCodeWithClient(
				context.Background(), &http.Client{Transport: transport}, "home-nas",
			)
			if err != nil {
				t.Fatal(err)
			}
			if detected != testCase.expected {
				t.Fatalf("detected = %v, want %v", detected, testCase.expected)
			}
			if transport.request.URL.Path != gatewayApplicationPath+"/version" {
				t.Fatalf("probe path = %q", transport.request.URL.Path)
			}
			if transport.request.Header.Get("Cookie") != "mode=relay" {
				t.Fatal("access-code probe must carry only the relay routing cookie")
			}
		})
	}
}

func TestDetectAccessCodeNeverFollowsRedirect(t *testing.T) {
	transport := &cannedTransport{response: cannedResponse(http.StatusFound, http.Header{
		"Location": []string{"/access_code_verify"},
	}, "")}
	if _, err := detectAccessCodeWithClient(
		context.Background(), &http.Client{Transport: transport}, "home-nas",
	); err != nil {
		t.Fatal(err)
	}
	if transport.request.URL.Path != gatewayApplicationPath+"/version" {
		t.Fatalf("probe followed a redirect into the verify endpoint: %q", transport.request.URL.Path)
	}
}

func TestVerifyAccessCodeExchangesCodeForSessionCookie(t *testing.T) {
	transport := &cannedTransport{response: cannedResponse(http.StatusNoContent, http.Header{
		"Set-Cookie": []string{"os-access-code=gateway-session; Path=/; HttpOnly"},
	}, "")}
	cookie, err := verifyAccessCodeWithClient(
		context.Background(), &http.Client{Transport: transport}, "home-nas", "s3cret-code",
	)
	if err != nil {
		t.Fatal(err)
	}
	if transport.request.Header.Get("x-access-code") !=
		base64.StdEncoding.EncodeToString([]byte("s3cret-code")) {
		t.Fatalf("access-code header = %q", transport.request.Header.Get("x-access-code"))
	}
	if transport.request.Header.Get("Cookie") != "mode=relay" {
		t.Fatal("access-code verification did not select the NAS relay route")
	}
	if transport.request.Header.Get("x-access-source") != "web" {
		t.Fatalf("access-source header = %q", transport.request.Header.Get("x-access-source"))
	}
	if transport.request.URL.Path != accessCodeVerifyPath {
		t.Fatalf("verify path = %q", transport.request.URL.Path)
	}
	if cookie.Name != accessCodeCookieName || cookie.Value != "gateway-session" {
		t.Fatalf("session cookie = %+v", cookie)
	}
	if cookie.Domain != "home-nas.fnos.net" || cookie.Path != "/" {
		t.Fatalf("session cookie scope = %+v", cookie)
	}
	// The gateway omits Secure; FnCPN restricts its replay to HTTPS and WSS.
	if !cookie.Secure {
		t.Fatalf("session cookie was not hardened for HTTPS: %+v", cookie)
	}
}

func TestVerifyAccessCodeStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		remoteCode string
		code       model.ErrorCode
		retryable  bool
	}{
		{name: "invalid", status: http.StatusUnauthorized, remoteCode: "ACCESS_CODE_INVALID", code: model.ErrorFailedPrecondition},
		{name: "forbidden", status: http.StatusForbidden, remoteCode: "ACCESS_CODE_INVALID", code: model.ErrorFailedPrecondition},
		{
			name: "rate limited", status: http.StatusTooManyRequests,
			remoteCode: "ACCESS_CODE_RATE_LIMITED", code: model.ErrorFailedPrecondition, retryable: true,
		},
		{name: "gateway outage", status: http.StatusBadGateway, code: model.ErrorUnavailable, retryable: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			transport := &cannedTransport{response: cannedResponse(testCase.status, nil, "")}
			_, err := verifyAccessCodeWithClient(
				context.Background(), &http.Client{Transport: transport}, "home-nas", "code",
			)
			failure := model.AsError(err)
			if failure == nil || failure.Code != testCase.code || failure.Retryable != testCase.retryable {
				t.Fatalf("error = %+v", failure)
			}
			if testCase.remoteCode != "" && failure.RemoteCode != testCase.remoteCode {
				t.Fatalf("remote code = %q, want %q", failure.RemoteCode, testCase.remoteCode)
			}
		})
	}
}

func TestVerifyAccessCodeRejectsResponseWithoutSession(t *testing.T) {
	transport := &cannedTransport{response: cannedResponse(http.StatusNoContent, nil, "")}
	_, err := verifyAccessCodeWithClient(
		context.Background(), &http.Client{Transport: transport}, "home-nas", "code",
	)
	if model.AsError(err) == nil || model.AsError(err).Code != model.ErrorProtocol {
		t.Fatalf("cookie-less verification error = %v", err)
	}
}

func TestVerifyAccessCodeRejectsMalformedCode(t *testing.T) {
	transport := &cannedTransport{response: cannedResponse(http.StatusNoContent, nil, "")}
	for _, code := range []string{"line\nbreak", strings.Repeat("x", 129)} {
		_, err := verifyAccessCodeWithClient(
			context.Background(), &http.Client{Transport: transport}, "home-nas", code,
		)
		if model.AsError(err) == nil || model.AsError(err).Code != model.ErrorInvalidArgument {
			t.Fatalf("malformed access code %q error = %v", code, err)
		}
	}
	if transport.request != nil {
		t.Fatal("malformed access code was sent to the gateway")
	}
}

func TestValidateTwofaCodeAcceptsDigitsOnly(t *testing.T) {
	if err := validateTwofaCode("123456"); err != nil {
		t.Fatalf("valid two-factor code rejected: %v", err)
	}
	if err := validateTwofaCode(""); err != nil {
		t.Fatalf("absent two-factor code rejected: %v", err)
	}
	for _, code := range []string{"12a456", "12345", "1234567", "１２３４５６", "12345678901234567", "12 456"} {
		if err := validateTwofaCode(code); err == nil {
			t.Fatalf("invalid two-factor code %q accepted", code)
		}
	}
}

func TestManagerAuthorizeNativeVerifiesAccessCodeBeforeLogin(t *testing.T) {
	store := NewConfigStore(t.TempDir()+"/config.json", newMemorySecretStore())
	var order []string
	verifyCalls := 0
	manager := newAccessCodeTestManager(t, store, &order, &verifyCalls, func(
		context.Context, string, string,
	) (Cookie, error) {
		order = append(order, "verify")
		verifyCalls++
		return Cookie{
			Name: accessCodeCookieName, Value: "gateway-session",
			Domain: "home-nas.fnos.net", Path: "/", Secure: true, HostOnly: true,
		}, nil
	})
	if err := manager.AuthorizeNative(context.Background(), NativeCredentials{
		FNID: "home-nas", Username: "admin", Password: "password-canary", AccessCode: "code",
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "verify,login" {
		t.Fatalf("the gateway gate ran after the login RPC: %v", order)
	}
	cookies, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	header := cookieHeaderForURL(cookies, mustParseURL(t, "https://home-nas.fnos.net/websocket"))
	if !strings.Contains(header, accessCodeCookieName+"=gateway-session") {
		t.Fatalf("access-code session was not persisted: %q", header)
	}
	// A retry that supplies no code reuses the stored session instead of
	// spending another attempt against the gateway's failed-attempt budget.
	if err := manager.AuthorizeNative(context.Background(), NativeCredentials{
		FNID: "home-nas", Username: "admin", Password: "password-canary",
	}); err != nil {
		t.Fatal(err)
	}
	if verifyCalls != 1 {
		t.Fatalf("re-authorization re-verified the access code %d times", verifyCalls)
	}
}

func TestManagerAuthorizeNativeStopsOnInvalidAccessCode(t *testing.T) {
	store := NewConfigStore(t.TempDir()+"/config.json", newMemorySecretStore())
	var order []string
	manager := newAccessCodeTestManager(t, store, &order, new(int), func(
		context.Context, string, string,
	) (Cookie, error) {
		order = append(order, "verify")
		failure := model.NewError(model.ErrorFailedPrecondition, "fnOS access code is invalid", false)
		failure.RemoteCode = "ACCESS_CODE_INVALID"
		return Cookie{}, failure
	})
	err := manager.AuthorizeNative(context.Background(), NativeCredentials{
		FNID: "home-nas", Username: "admin", Password: "password-canary", AccessCode: "wrong",
	})
	failure := model.AsError(err)
	if failure == nil || failure.RemoteCode != "ACCESS_CODE_INVALID" {
		t.Fatalf("invalid access code error = %v", err)
	}
	if !RequiresAdditionalCredential(err) {
		t.Fatal("invalid access code must keep the authorization request retryable")
	}
	if strings.Join(order, ",") != "verify" {
		t.Fatalf("login ran despite the gateway rejection: %v", order)
	}
	if _, found, err := store.LoadNativeSession("home-nas"); err != nil || found {
		t.Fatalf("rejected authorization persisted a session: found=%v err=%v", found, err)
	}
}

func newAccessCodeTestManager(
	t *testing.T,
	store *ConfigStore,
	order *[]string,
	verifyCalls *int,
	verify func(context.Context, string, string) (Cookie, error),
) *Manager {
	t.Helper()
	configuration := managerClientConfiguration()
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				bootstrap:     Bootstrap{ServerVersion: version.Current, Administrator: true},
				configuration: configuration,
				registration: model.DeviceRegistration{
					Device: model.Device{
						ID: "device-1", PublicKey: managerKey(3),
						OverlayAddress: configuration.ClientAddress, Enabled: true,
					},
					Configuration: configuration,
				},
			}, nil
		},
		Privileged: &fakePrivilegedNetwork{},
		Bridge:     &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:      fakeLocalProbe{},
		Authenticator: &orderedNativeAuthenticator{
			order: order,
			session: NativeSession{
				Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
				DeviceID: "native-device", Token: "cli-token", LongToken: "cli-long",
				Secret: base64.StdEncoding.EncodeToString([]byte("access-code-secret")),
			},
		},
		AccessCodeVerify: verify,
		DeviceName:       "MacBook",
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

type orderedNativeAuthenticator struct {
	order   *[]string
	session NativeSession
}

func (a *orderedNativeAuthenticator) Login(
	context.Context,
	NativeLogin,
) (NativeSession, error) {
	*a.order = append(*a.order, "login")
	return a.session, nil
}

func (a *orderedNativeAuthenticator) Recover(
	context.Context,
	NativeSession,
	string,
) (NativeSession, error) {
	return a.session, nil
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// Model the gateway routing decision separately from the protected NAS route.
// Without mode=relay, even a valid access-code request gets the public SPA.
func TestAccessCodeUsesNASRouteAndReplaysSession(t *testing.T) {
	var verifyCalls int
	transport := accessCodeGatewayTransport{verifyCalls: &verifyCalls}
	httpClient := &http.Client{Transport: transport}
	ctx := context.Background()
	required, err := detectAccessCodeWithClient(ctx, httpClient, "home-nas")
	if err != nil || !required {
		t.Fatalf("NAS challenge not detected: required=%v err=%v", required, err)
	}
	cookie, err := verifyAccessCodeWithClient(ctx, httpClient, "home-nas", "code-canary")
	if err != nil {
		t.Fatal(err)
	}
	cookies := append(nativeGatewayCookies("home-nas", "token"), cookie)
	remote, err := NewRemoteClient("https://home-nas.fnos.net/app/fncpn", cookies, httpClient, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.CheckVersion(ctx); err != nil {
		t.Fatalf("verified session not replayed to version check: %v", err)
	}
	if verifyCalls != 1 {
		t.Fatalf("probe or version check verified a code: calls=%d", verifyCalls)
	}
}

type accessCodeGatewayTransport struct{ verifyCalls *int }

func (g accessCodeGatewayTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	respond := func(status int, header http.Header, body string) (*http.Response, error) {
		response := cannedResponse(status, header, body)
		response.Request = request
		return response, nil
	}
	if routing, err := request.Cookie("mode"); err != nil || routing.Value != "relay" {
		return respond(200, nil, "<html>FN Connect public landing page</html>")
	}
	if request.URL.Path == accessCodeVerifyPath {
		*g.verifyCalls++
		if request.Header.Get("x-access-code") != base64.StdEncoding.EncodeToString([]byte("code-canary")) {
			return respond(403, nil, "")
		}
		return respond(204, http.Header{"Set-Cookie": {"os-access-code=session; Path=/; HttpOnly"}}, "")
	}
	if cookie, err := request.Cookie(accessCodeCookieName); err == nil && cookie.Value == "session" {
		return respond(200, nil, `{"serverVersion":"`+version.Current+`","future":"/access_code_verify"}`)
	}
	return respond(200, challengeHeader(), "<html>fetch('/access_code_verify')</html>")
}

func TestAccessCodeChallengeRequiresRelogin(t *testing.T) {
	for _, status := range []int{200, 403} {
		response := cannedResponse(status, challengeHeader(), "<html>fetch('/access_code_verify')</html>")
		var err error
		if status == 200 {
			err = decodeResponse(response, &map[string]any{})
		} else {
			err = HTTPResponseError(response, "version.check", nil)
		}
		if !RequiresAdditionalCredential(err) || model.AsError(err).RemoteCode != "ACCESS_CODE_REQUIRED" {
			t.Fatalf("HTTP %d lost access-code classification: %v", status, err)
		}
		m, _, _, _, _ := coordinationFixture(t)
		m.fail(err)
		if m.Status().State != model.ClientAuthRequired {
			t.Fatalf("challenge did not request relogin: %+v", m.Status())
		}
		config, loadErr := m.store.Load()
		if loadErr != nil || config == nil || canAutoConnect(config, m.Status()) {
			t.Fatalf("challenge lost config or left automatic retries enabled: %v", loadErr)
		}
	}
}
