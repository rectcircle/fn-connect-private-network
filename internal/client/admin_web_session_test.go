package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func webRecoverySession() AdminWebSession {
	return AdminWebSession{Version: adminWebSessionVersion, FNID: "home-nas", Username: "admin", DeviceID: "web-device", Token: "old-web", LongToken: "web-long", Secret: base64.StdEncoding.EncodeToString([]byte("web-signing-key")), UpdatedAt: time.Now().UTC()}
}

func TestWebRecoveryUsesSignedBrowserIdentity(t *testing.T) {
	session := webRecoverySession()
	for _, fallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "short authentication", true: "long token fallback"}[fallback], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				for i := 0; i < 1+map[bool]int{false: 0, true: 1}[fallback]; i++ {
					si := nativeTestRequest(t, r.Context(), conn, "")
					if si["req"] != "util.getSI" {
						t.Error("missing session integrity request")
					}
					nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "web-si"})
					request := nativeTestRequest(t, r.Context(), conn, session.Secret)
					if request["si"] != "web-si" {
						t.Error("missing si")
					}
					if i == 0 {
						if request["req"] != "user.authToken" || request["token"] != session.Token || request["main"] != true {
							t.Error("incorrect short-token authentication")
						}
						if fallback {
							nativeTestResponse(t, r.Context(), conn, request, map[string]any{"result": "fail", "errno": 1001})
							continue
						}
					} else if request["req"] != "user.tokenLogin" || request["token"] != session.LongToken || request["deviceType"] != "Browser" || request["did"] != session.DeviceID {
						t.Error("recovery changed Browser identity")
					}
					nativeTestResponse(t, r.Context(), conn, request, map[string]any{"result": "succ", "token": "new-web"})
				}
			}))
			defer server.Close()
			got, err := newNativeTestClient("ws"+strings.TrimPrefix(server.URL, "http")).RecoverWeb(context.Background(), session, "Test Mac")
			if err != nil || got.Token != "new-web" || got.LongToken != session.LongToken || got.Secret != session.Secret {
				t.Fatalf("Web recovery failed: %v", err)
			}
		})
	}
}

func TestWebRecoveryTransportFailureDoesNotTryLongToken(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		si := nativeTestRequest(t, r.Context(), conn, "")
		nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "si"})
		nativeTestRequest(t, r.Context(), conn, webRecoverySession().Secret)
	}))
	defer server.Close()
	_, err := newNativeTestClient("ws"+strings.TrimPrefix(server.URL, "http")).RecoverWeb(context.Background(), webRecoverySession(), "Mac")
	if err == nil || model.AsError(err).Code == model.ErrorAuthRequired || calls.Load() != 1 {
		t.Fatalf("transport failure misclassified: %v", err)
	}
}

type recoveryWebAuth struct {
	recover func(context.Context, AdminWebSession, string) (AdminWebSession, error)
}

func (f recoveryWebAuth) WebLogin(context.Context, NativeLogin) (AdminWebSession, error) {
	panic("unexpected password login")
}
func (f recoveryWebAuth) RecoverWeb(ctx context.Context, s AdminWebSession, name string) (AdminWebSession, error) {
	return f.recover(ctx, s, name)
}

func recoveryManager(t *testing.T, auth recoveryWebAuth) *Manager {
	t.Helper()
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), newMemorySecretStore())
	if err := store.SaveAdminWebSession(webRecoverySession()); err != nil {
		t.Fatal(err)
	}
	return &Manager{store: store, webAuthenticator: auth, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestManagerWebRecoveryConcurrentRequestsAndLateResults(t *testing.T) {
	for _, action := range []string{"recover", "logout", "forget", "new login"} {
		t.Run(action, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			m := recoveryManager(t, recoveryWebAuth{recover: func(_ context.Context, s AdminWebSession, _ string) (AdminWebSession, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				<-release
				s.Token = "new-web"
				s.UpdatedAt = time.Now().UTC()
				return s, nil
			}})
			var group sync.WaitGroup
			errs := make(chan error, 12)
			for i := 0; i < 12; i++ {
				group.Add(1)
				go func() {
					defer group.Done()
					_, err := m.recoverAdminWeb(context.Background(), "home-nas", "old-web")
					errs <- err
				}()
			}
			<-started
			switch action {
			case "logout":
				if err := m.store.ClearAdminWebSession("home-nas"); err != nil {
					t.Fatal(err)
				}
			case "forget":
				if err := m.store.Forget("home-nas"); err != nil {
					t.Fatal(err)
				}
			case "new login":
				s := webRecoverySession()
				s.Token = "new-login"
				if err := m.store.SaveAdminWebSession(s); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			group.Wait()
			close(errs)
			for err := range errs {
				if action == "recover" && err != nil {
					t.Fatal(err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("recovery calls=%d", calls.Load())
			}
			s, found, err := m.store.LoadAdminWebSession("home-nas")
			if err != nil {
				t.Fatal(err)
			}
			if (action == "logout" || action == "forget") && found {
				t.Fatal("late recovery resurrected credentials")
			}
			if action == "new login" && s.Token != "new-login" {
				t.Fatal("late recovery overwrote new login")
			}
			if action == "recover" && s.Token != "new-web" {
				t.Fatal("new token was not persisted")
			}
		})
	}
}

func TestManagerWebRecoveryRetainsCredentialsOnTemporaryFailure(t *testing.T) {
	for _, code := range []model.ErrorCode{model.ErrorUnavailable, model.ErrorFailedPrecondition, model.ErrorAuthRequired} {
		t.Run(string(code), func(t *testing.T) {
			m := recoveryManager(t, recoveryWebAuth{recover: func(context.Context, AdminWebSession, string) (AdminWebSession, error) {
				return AdminWebSession{}, model.NewError(code, "failure", false)
			}})
			_, err := m.recoverAdminWeb(context.Background(), "home-nas", "old-web")
			if err == nil {
				t.Fatal("expected failure")
			}
			_, found, err := m.store.LoadAdminWebSession("home-nas")
			if err != nil {
				t.Fatal(err)
			}
			if found == (code == model.ErrorAuthRequired) {
				t.Fatal("wrong credential retention policy")
			}
		})
	}
}

func TestHistoricalWebSessionLoadsWithoutRewriting(t *testing.T) {
	// Frozen v1 shape used by the published login-only implementation.
	data := []byte(`{"version":1,"fnId":"home-nas","username":"admin","deviceId":"historical-web-device","token":"historical-short","updatedAt":"2026-10-24T10:45:04Z"}`)
	secrets := newMemorySecretStore()
	if err := secrets.Put(adminWebSecret, "home-nas", data); err != nil {
		t.Fatal(err)
	}
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), secrets)
	s, found, err := store.LoadAdminWebSession("home-nas")
	if err != nil || !found || s.Token != "historical-short" || s.LongToken != "" {
		t.Fatalf("historical Web session rejected: %v", err)
	}
	after, _, err := secrets.Get(adminWebSecret, "home-nas")
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("load rewrote historical credentials")
	}
}

func TestWebActivityKeepsBrowserSocketUntilCanceled(t *testing.T) {
	session := webRecoverySession()
	active := make(chan struct{})
	heartbeat := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		si := nativeTestRequest(t, r.Context(), conn, "")
		nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "activity-si"})
		auth := nativeTestRequest(t, r.Context(), conn, session.Secret)
		if auth["req"] != "user.authToken" || auth["token"] != session.Token || auth["main"] != true {
			t.Error("wrong activity session")
		}
		nativeTestResponse(t, r.Context(), conn, auth, map[string]any{"result": "succ"})
		request := nativeTestRequest(t, r.Context(), conn, session.Secret)
		if request["req"] != "user.active" {
			t.Error("missing signed activity")
		}
		nativeTestResponse(t, r.Context(), conn, request, map[string]any{"result": "succ"})
		close(active)
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		if string(data) != `{"req":"ping"}` {
			t.Error("missing browser heartbeat")
			return
		}
		if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"res":"pong"}`)); err != nil {
			return
		}
		close(heartbeat)
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- newNativeTestClient("ws"+strings.TrimPrefix(server.URL, "http")).maintainWeb(ctx, session, 20*time.Millisecond, time.Minute)
	}()
	select {
	case <-active:
	case <-time.After(3 * time.Second):
		t.Fatal("no activity on authenticated socket")
	}
	select {
	case <-heartbeat:
	case <-time.After(3 * time.Second):
		t.Fatal("missing Web heartbeat")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Web activity did not stop")
	}
}

func TestManagerAdminProxyReprobesWithRecoveredCookies(t *testing.T) {
	m := recoveryManager(t, recoveryWebAuth{recover: func(_ context.Context, s AdminWebSession, _ string) (AdminWebSession, error) {
		s.Token = "fresh-web"
		s.UpdatedAt = time.Now().UTC()
		return s, nil
	}})
	if err := m.store.Save(LocalConfig{Version: localConfigVersion, FNID: "home-nas", Administrator: true}); err != nil {
		t.Fatal(err)
	}
	var probes int
	m.adminWebProbe = func(_ context.Context, _ string, cookies []Cookie) error {
		probes++
		for _, cookie := range cookies {
			if cookie.Name == "fnos-token" && cookie.Value == "fresh-web" {
				return nil
			}
		}
		return adminWebAuthRequired()
	}
	target, err := m.AdminProxyURL(context.Background())
	defer m.closeAdminProxy()
	if err != nil || target == "" || probes != 2 {
		t.Fatalf("open after recovery: probes=%d err=%v", probes, err)
	}
}

func TestWebRecoveryClassifiesLongTokenRejection(t *testing.T) {
	for _, test := range []struct {
		name  string
		reply map[string]any
		code  model.ErrorCode
	}{
		{"revoked", map[string]any{"result": "fail", "errno": 1001}, model.ErrorAuthRequired},
		{"needs OTP", map[string]any{"result": "succ", "accessToken": "challenge", "isTwofaEnforced": true, "isBindTwofaSecret": true}, model.ErrorFailedPrecondition},
		{"incomplete", map[string]any{"result": "succ"}, model.ErrorProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := webRecoverySession()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for i := 0; i < 2; i++ {
					si := nativeTestRequest(t, r.Context(), conn, "")
					nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "si"})
					req := nativeTestRequest(t, r.Context(), conn, session.Secret)
					reply := test.reply
					if i == 0 {
						reply = map[string]any{"result": "fail", "errno": 1001}
					}
					nativeTestResponse(t, r.Context(), conn, req, reply)
				}
			}))
			defer server.Close()
			_, err := newNativeTestClient("ws"+strings.TrimPrefix(server.URL, "http")).RecoverWeb(context.Background(), session, "Mac")
			if err == nil || model.AsError(err).Code != test.code {
				t.Fatalf("wrong recovery failure: %v", err)
			}
			if test.code == model.ErrorFailedPrecondition && !RequiresAdditionalCredential(err) {
				t.Fatal("lost OTP challenge")
			}
		})
	}
}

type activityWebAuth struct {
	recoveryWebAuth
	started chan struct{}
	stopped chan struct{}
}

func (f *activityWebAuth) MaintainWeb(ctx context.Context, _ AdminWebSession) error {
	close(f.started)
	<-ctx.Done()
	close(f.stopped)
	return ctx.Err()
}

func TestManagerStopsWebActivityWithProxyLifecycle(t *testing.T) {
	auth := &activityWebAuth{started: make(chan struct{}), stopped: make(chan struct{})}
	m := recoveryManager(t, recoveryWebAuth{})
	m.webAuthenticator = auth
	proxy := &AdminProxy{
		FNIDProvider:   func() (string, error) { return "home-nas", nil },
		CookieProvider: func(string) ([]Cookie, error) { return nativeGatewayCookies("home-nas", "old-web"), nil },
		Activity:       m.touchAdminWebActivity,
	}
	if _, err := proxy.Start(); err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	proxy.recordActivity("home-nas")
	select {
	case <-auth.started:
	case <-time.After(time.Second):
		t.Fatal("activity did not start")
	}
	if err := proxy.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-auth.stopped:
	case <-time.After(time.Second):
		t.Fatal("proxy close leaked activity socket")
	}
	if _, found, err := m.store.LoadAdminWebSession("home-nas"); err != nil || !found {
		t.Fatal("activity cancellation cleared login")
	}
}
