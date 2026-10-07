package client

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestNativeSessionLoginAndLongTokenRecovery(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("native-session-secret"))
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		switch connections.Add(1) {
		case 1:
			request := nativeTestRequest(t, r.Context(), connection, "")
			if request["req"] != "user.login" || request["user"] != "admin" ||
				request["password"] != "password-canary" || request["did"] != "stable-device" {
				t.Error("invalid native login request")
			}
			nativeTestResponse(t, r.Context(), connection, request, map[string]any{
				"result": "succ", "token": "short-one", "longToken": "long-one",
				"secret": secret, "backId": "back-one",
			})
		case 2:
			si := nativeTestRequest(t, r.Context(), connection, "")
			nativeTestResponse(t, r.Context(), connection, si, map[string]any{
				"result": "succ", "si": "si-short",
			})
			request := nativeTestRequest(t, r.Context(), connection, secret)
			if request["req"] != "user.authToken" || request["token"] != "short-one" ||
				request["si"] != "si-short" || request["main"] != true || request["active"] != true {
				t.Error("invalid short-token recovery request")
			}
			nativeTestResponse(t, r.Context(), connection, request, map[string]any{
				"result": "fail", "errno": 65534,
			})
		case 3:
			si := nativeTestRequest(t, r.Context(), connection, "")
			nativeTestResponse(t, r.Context(), connection, si, map[string]any{
				"result": "succ", "si": "si-long",
			})
			request := nativeTestRequest(t, r.Context(), connection, secret)
			if request["req"] != "user.tokenLogin" || request["token"] != "long-one" ||
				request["si"] != "si-long" || request["deviceType"] != "CLI" {
				t.Error("invalid long-token recovery request")
			}
			nativeTestResponse(t, r.Context(), connection, request, map[string]any{
				"result": "succ", "token": "short-two", "backId": "back-two",
			})
		default:
			t.Error("unexpected native connection")
		}
	}))
	defer server.Close()
	target := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NativeSessionClient{Dial: func(ctx context.Context, _ string) (*websocket.Conn, error) {
		connection, _, err := websocket.Dial(ctx, target, nil)
		return connection, err
	}}
	session, err := client.Login(
		context.Background(), "home-nas", "admin", "password-canary",
		"stable-device", "FnCPN Test",
	)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := client.Recover(context.Background(), session, "FnCPN Test")
	if err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 3 || recovered.Token != "short-two" ||
		recovered.LongToken != "long-one" || recovered.Secret != secret ||
		recovered.BackID != "back-two" {
		t.Fatalf("recovered session did not preserve long-lived fields: %+v", recovered)
	}
}

func TestNativeSessionLoginRejectsTwoFactorWithoutPersistableSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		request := nativeTestRequest(t, r.Context(), connection, "")
		nativeTestResponse(t, r.Context(), connection, request, map[string]any{
			"result": "fail", "accessToken": "temporary", "isTwofaEnforced": true,
		})
	}))
	defer server.Close()
	target := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NativeSessionClient{Dial: func(ctx context.Context, _ string) (*websocket.Conn, error) {
		connection, _, err := websocket.Dial(ctx, target, nil)
		return connection, err
	}}
	_, err := client.Login(context.Background(), "home-nas", "admin", "password", "device", "FnCPN")
	if err == nil || model.AsError(err).Code != model.ErrorFailedPrecondition {
		t.Fatalf("2FA response was not surfaced: %v", err)
	}
}

func TestAdminWebLoginReturnsIsolatedToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKey}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		request := nativeTestRequest(t, r.Context(), connection, "")
		nativeTestResponse(t, r.Context(), connection, request, map[string]any{
			"result": "succ", "pub": publicPEM, "si": "web-si",
		})
		_, payload, err := connection.Read(r.Context())
		if err != nil {
			t.Fatal(err)
		}
		var frame map[string]any
		if json.Unmarshal(payload, &frame) != nil || frame["req"] != "encrypted" ||
			frame["iv"] == "" || frame["rsa"] == "" || frame["aes"] == "" {
			t.Fatalf("encrypted login frame = %+v", frame)
		}
		response, _ := json.Marshal(map[string]any{
			"result": "succ", "token": "web-token", "backId": "web-back-id",
		})
		if err := connection.Write(r.Context(), websocket.MessageText, response); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()
	target := "ws" + strings.TrimPrefix(server.URL, "http")
	client := NativeSessionClient{Dial: func(ctx context.Context, _ string) (*websocket.Conn, error) {
		connection, _, err := websocket.Dial(ctx, target, nil)
		return connection, err
	}}
	session, err := client.WebLogin(
		context.Background(), "home-nas", "admin", "password", "web-device", "FnCPN Test",
	)
	if err != nil || session.Token != "web-token" || session.DeviceID != "web-device" {
		t.Fatalf("Web session = %+v, error = %v", session, err)
	}
}

func TestManagerNativeAuthorizationPersistsSessionAndRecoversBeforeConnect(t *testing.T) {
	secrets := newMemorySecretStore()
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), secrets)
	secret := base64.StdEncoding.EncodeToString([]byte("manager-native-secret"))
	authenticator := &fakeNativeAuthenticator{
		login: NativeSession{
			Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
			DeviceID: "stable-device", Token: "short-one", LongToken: "long-one",
			Secret: secret, UpdatedAt: time.Now().UTC(),
		},
		recovered: NativeSession{
			Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
			DeviceID: "stable-device", Token: "short-two", LongToken: "long-one",
			Secret: secret, UpdatedAt: time.Now().UTC(),
		},
	}
	configuration := managerClientConfiguration()
	remote := &fakeRemoteService{
		bootstrap:     Bootstrap{Administrator: true},
		configuration: configuration,
		registration: model.DeviceRegistration{
			Device: model.Device{
				ID: "device-1", PublicKey: managerKey(3),
				OverlayAddress: configuration.ClientAddress, Enabled: true,
			},
			Configuration: configuration,
		},
		// First configuration request is rejected as invalid token so that the
		// deferred on-demand session recovery is exercised during Connect.
		failConfigOnce: true,
	}
	var remoteCookieHeaders []string
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
			origin, _ := url.Parse("https://home-nas.fnos.net/app/fncpn")
			remoteCookieHeaders = append(remoteCookieHeaders, cookieHeaderForURL(cookies, origin))
			return remote, nil
		},
		Privileged:    &fakePrivilegedNetwork{},
		Bridge:        &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:         fakeLocalProbe{},
		Authenticator: authenticator,
		DeviceName:    "MacBook",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeNative(
		context.Background(), "home-nas", "admin", "password-canary",
	); err != nil {
		t.Fatal(err)
	}
	if authenticator.loginPassword != "password-canary" {
		t.Fatal("native password was not passed to the login call")
	}
	for _, value := range secrets.values {
		if strings.Contains(string(value), "password-canary") {
			t.Fatal("native password was persisted")
		}
	}
	diagnostics := manager.Diagnose(context.Background())
	if diagnostics.Username != "admin" || diagnostics.FNID != "home-nas" {
		t.Fatalf("native identity missing from diagnostics: %+v", diagnostics)
	}
	session, found, err := store.LoadNativeSession("home-nas")
	if err != nil || !found {
		t.Fatalf("load native session: found=%v error=%v", found, err)
	}
	session.UpdatedAt = time.Now().Add(-time.Hour)
	if err := store.SaveNativeSession(session); err != nil {
		t.Fatal(err)
	}
	if err := manager.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if authenticator.recoverCalls.Load() != 1 {
		t.Fatal("expired native session was not recovered before reconnect")
	}
	if len(remoteCookieHeaders) < 3 ||
		!strings.Contains(remoteCookieHeaders[len(remoteCookieHeaders)-1], "fnos-token=short-two") {
		t.Fatalf("recovered request reused stale cookies: %v", remoteCookieHeaders)
	}
	cookies, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("https://home-nas.fnos.net/app/fncpn")
	if header := cookieHeaderForURL(cookies, origin); !strings.Contains(header, "fnos-token=short-two") {
		t.Fatalf("recovered gateway token was not persisted: %q", header)
	}
}

func TestManagerKeepsNativeAndAdminWebSessionsSeparate(t *testing.T) {
	secrets := newMemorySecretStore()
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), secrets)
	nativeSecret := base64.StdEncoding.EncodeToString([]byte("native-secret"))
	nativeAuth := &fakeNativeAuthenticator{
		login: NativeSession{
			Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
			DeviceID: "native-device", Token: "cli-token", LongToken: "cli-long",
			Secret: nativeSecret, UpdatedAt: time.Now().UTC(),
		},
	}
	webAuth := &fakeAdminWebAuthenticator{
		login: AdminWebSession{
			Version: adminWebSessionVersion, FNID: "home-nas", Username: "admin",
			Token:     "web-token",
			UpdatedAt: time.Now().UTC(),
		},
	}
	configuration := managerClientConfiguration()
	remote := &fakeRemoteService{
		bootstrap:     Bootstrap{Administrator: true},
		configuration: configuration,
		registration: model.DeviceRegistration{
			Device: model.Device{
				ID: "device-1", PublicKey: managerKey(3),
				OverlayAddress: configuration.ClientAddress, Enabled: true,
			},
			Configuration: configuration,
		},
	}
	var remoteCookies [][]Cookie
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
			remoteCookies = append(remoteCookies, append([]Cookie(nil), cookies...))
			return remote, nil
		},
		Privileged:       &fakePrivilegedNetwork{},
		Bridge:           &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:            fakeLocalProbe{},
		Authenticator:    nativeAuth,
		WebAuthenticator: webAuth,
		AdminWebProbe: func(context.Context, string, string) error {
			return nil
		},
		DeviceName: "MacBook",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeNative(
		context.Background(), "home-nas", "admin", "password-canary",
	); err != nil {
		t.Fatal(err)
	}
	nativeSession, found, err := store.LoadNativeSession("home-nas")
	if err != nil || !found {
		t.Fatalf("load native session: found=%v error=%v", found, err)
	}
	if nativeSession.Token != "cli-token" || nativeSession.LongToken != "cli-long" ||
		nativeSession.Secret != nativeSecret || nativeSession.DeviceID != "native-device" {
		t.Fatalf("native session was changed by Web login: %+v", nativeSession)
	}
	adminSession, found, err := store.LoadAdminWebSession("home-nas")
	if err != nil || !found {
		t.Fatalf("load admin Web session: found=%v error=%v", found, err)
	}
	if adminSession.Token != "web-token" {
		t.Fatalf("admin Web session was not kept separate: %+v", adminSession)
	}
	if webAuth.loginDeviceID == nativeSession.DeviceID {
		t.Fatalf("login device IDs are not isolated: native=%q web=%q",
			nativeSession.DeviceID, webAuth.loginDeviceID)
	}
	origin, _ := url.Parse("https://home-nas.fnos.net/app/fncpn")
	for _, cookies := range remoteCookies {
		header := cookieHeaderForURL(cookies, origin)
		if !strings.Contains(header, "fnos-token=cli-token") ||
			strings.Contains(header, "web-token") {
			t.Fatalf("tunnel remote received non-CLI credentials: %q", header)
		}
	}

	proxyURL, err := manager.AdminProxyURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsedProxyURL, err := url.Parse(proxyURL)
	if err != nil || parsedProxyURL.Path != "/app/fncpn" ||
		parsedProxyURL.Query().Get("fncpn_proxy") == "" {
		t.Fatalf("admin proxy URL = %q", proxyURL)
	}
	proxyBaseURL := parsedProxyURL.Scheme + "://" + parsedProxyURL.Host
	defer manager.Close(context.Background())
	if nativeAuth.recoverCalls.Load() != 0 {
		t.Fatal("opening admin proxy recovered the CLI session")
	}
	nativeSession, _, _ = store.LoadNativeSession("home-nas")
	if nativeSession.Token != "cli-token" {
		t.Fatalf("admin proxy changed CLI token to %q", nativeSession.Token)
	}
	cookies, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	if header := cookieHeaderForURL(cookies, origin); !strings.Contains(header, "fnos-token=cli-token") {
		t.Fatalf("admin proxy changed CLI gateway cookies: %q", header)
	}
	manager.mu.RLock()
	proxy := manager.adminProxy
	manager.mu.RUnlock()
	request := httptest.NewRequest(http.MethodGet, proxyBaseURL+"/app/fncpn", nil)
	proxy.director(request)
	if header := request.Header.Get("Cookie"); !strings.Contains(header, "fnos-token=web-token") ||
		strings.Contains(header, "cli-token") {
		t.Fatalf("admin proxy did not use isolated Web token: %q", header)
	}

	statusBeforeProbeFailure := manager.Status()
	manager.adminWebProbe = func(context.Context, string, string) error {
		return model.WithOperation(model.NewError(
			model.ErrorUnavailable, "management probe unavailable", true,
		), "admin_proxy.probe")
	}
	if _, err := manager.AdminProxyURL(context.Background()); err == nil ||
		model.AsError(err).Code != model.ErrorUnavailable {
		t.Fatalf("unavailable admin probe error = %v", err)
	}
	if _, found, err := store.LoadAdminWebSession("home-nas"); err != nil || !found {
		t.Fatalf("network failure cleared admin session: found=%v err=%v", found, err)
	}
	manager.adminWebProbe = func(context.Context, string, string) error {
		return model.WithOperation(model.NewError(
			model.ErrorAuthRequired, "management session expired", false,
		), "admin_proxy.probe")
	}
	if _, err := manager.AdminProxyURL(context.Background()); err == nil ||
		model.AsError(err).Code != model.ErrorAuthRequired {
		t.Fatalf("expired admin session error = %v", err)
	}
	if session, found, err := store.LoadNativeSession("home-nas"); err != nil || !found ||
		session.Token != "cli-token" {
		t.Fatalf("admin expiry changed native session: found=%v session=%+v err=%v",
			found, session, err)
	}
	if _, found, err := store.LoadAdminWebSession("home-nas"); err != nil || found {
		t.Fatalf("expired admin session was retained: found=%v err=%v", found, err)
	}
	if statusAfter := manager.Status(); statusAfter.State != statusBeforeProbeFailure.State ||
		statusAfter.LastError != statusBeforeProbeFailure.LastError {
		t.Fatalf("admin expiry changed client status: before=%+v after=%+v",
			statusBeforeProbeFailure, statusAfter)
	}
}

func TestManagerWebLoginFailureDoesNotChangeNativeAuthorization(t *testing.T) {
	secrets := newMemorySecretStore()
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), secrets)
	secret := base64.StdEncoding.EncodeToString([]byte("native-secret"))
	nativeAuth := &fakeNativeAuthenticator{login: NativeSession{
		Version: nativeSessionVersion, FNID: "home-nas", Username: "admin",
		DeviceID: "native-device", Token: "cli-token", LongToken: "cli-long",
		Secret: secret, UpdatedAt: time.Now().UTC(),
	}}
	webAuth := &fakeAdminWebAuthenticator{loginErr: errors.New("web login unavailable")}
	configuration := managerClientConfiguration()
	manager, err := NewManager(ManagerOptions{
		Store: store,
		Discoverer: fakeDiscoverer{result: Discovery{
			FN: []string{"home-nas.fnos.net:443"},
		}},
		Remote: func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeRemoteService{
				bootstrap:     Bootstrap{Administrator: true},
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
		Privileged:       &fakePrivilegedNetwork{},
		Bridge:           &fakeBridge{endpoint: "127.0.0.1:51821"},
		Probe:            fakeLocalProbe{},
		Authenticator:    nativeAuth,
		WebAuthenticator: webAuth,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.AuthorizeNative(
		context.Background(), "home-nas", "admin", "password-canary",
	); err != nil {
		t.Fatalf("Web login failure broke CLI authorization: %v", err)
	}
	if _, found, err := store.LoadAdminWebSession("home-nas"); err != nil || found {
		t.Fatalf("failed Web login persisted an admin session: found=%v err=%v", found, err)
	}
	cookies, err := store.LoadCookies("home-nas")
	if err != nil {
		t.Fatal(err)
	}
	origin, _ := url.Parse("https://home-nas.fnos.net/app/fncpn")
	if header := cookieHeaderForURL(cookies, origin); !strings.Contains(header, "fnos-token=cli-token") {
		t.Fatalf("CLI gateway token was not retained: %q", header)
	}
}

type fakeNativeAuthenticator struct {
	login         NativeSession
	recovered     NativeSession
	loginPassword string
	recoverCalls  atomic.Int32
}

func (f *fakeNativeAuthenticator) Login(
	_ context.Context,
	_ string,
	_ string,
	password string,
	_ string,
	_ string,
) (NativeSession, error) {
	f.loginPassword = password
	return f.login, nil
}

func (f *fakeNativeAuthenticator) Recover(
	context.Context,
	NativeSession,
	string,
) (NativeSession, error) {
	f.recoverCalls.Add(1)
	return f.recovered, nil
}

type fakeAdminWebAuthenticator struct {
	login         AdminWebSession
	loginErr      error
	loginDeviceID string
}

func (f *fakeAdminWebAuthenticator) WebLogin(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	deviceID string,
	_ string,
) (AdminWebSession, error) {
	f.loginDeviceID = deviceID
	session := f.login
	session.DeviceID = deviceID
	return session, f.loginErr
}

func nativeTestRequest(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	secret string,
) map[string]any {
	t.Helper()
	messageType, payload, err := connection.Read(ctx)
	if err != nil || messageType != websocket.MessageText {
		t.Fatalf("read native request: type=%v error=%v", messageType, err)
	}
	if secret != "" {
		if len(payload) < 44 {
			t.Fatal("signed native request is too short")
		}
		signature, body := payload[:44], payload[44:]
		key, _ := base64.StdEncoding.DecodeString(secret)
		expected := hmac.New(sha256.New, key)
		_, _ = expected.Write(body)
		if !hmac.Equal(signature, []byte(base64.StdEncoding.EncodeToString(expected.Sum(nil)))) {
			t.Fatal("native request signature mismatch")
		}
		payload = body
	}
	var request map[string]any
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func nativeTestResponse(
	t *testing.T,
	ctx context.Context,
	connection *websocket.Conn,
	request map[string]any,
	response map[string]any,
) {
	t.Helper()
	response["reqid"] = request["reqid"]
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	writeContext, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := connection.Write(writeContext, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}
