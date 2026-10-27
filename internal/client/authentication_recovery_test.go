package client

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func explicitTokenRejection() error {
	return gatewayAuthenticationError(&http.Response{StatusCode: http.StatusOK}, []byte("invalid token"))
}
func TestRejectedFNOSTokenRequiresKnownResponse(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{"known response", explicitTokenRejection(), true},
		{"decorated", model.WithOperation(explicitTokenRejection(), "configuration.watch"), true},
		{"generic application auth", model.NewError(model.ErrorAuthRequired, "application login required", false), false},
		{"network", model.NewError(model.ErrorUnavailable, "network unavailable", true), false},
		{"access code", accessCodeRequiredError(), false},
		{"nil", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := rejectedFNOSToken(test.err); got != test.want {
				t.Fatalf("rejected=%v want=%v", got, test.want)
			}
		})
	}
}

func TestRejectedTokenRecoverySkipsShortRPC(t *testing.T) {
	for _, kind := range []string{"cli", "web"} {
		t.Run(kind, func(t *testing.T) {
			session := webRecoverySession()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				si := nativeTestRequest(t, r.Context(), conn, "")
				nativeTestResponse(t, r.Context(), conn, si, map[string]any{"result": "succ", "si": "si"})
				req := nativeTestRequest(t, r.Context(), conn, session.Secret)
				requests.Add(1)
				if req["req"] != "user.tokenLogin" || req["token"] != session.LongToken {
					t.Error("rejected short token was retried")
				}
				deviceType := "CLI"
				if kind == "web" {
					deviceType = "Browser"
				}
				if req["deviceType"] != deviceType {
					t.Error("changed session identity")
				}
				nativeTestResponse(t, r.Context(), conn, req, map[string]any{"result": "succ", "token": "fresh"})
			}))
			defer server.Close()
			client := newNativeTestClient("ws" + strings.TrimPrefix(server.URL, "http"))
			if kind == "web" {
				got, err := client.RecoverRejectedWebToken(t.Context(), session, "Mac")
				if err != nil || got.Token != "fresh" {
					t.Fatalf("Web long-token recovery: %v", err)
				}
			} else {
				got, err := client.RecoverRejectedToken(t.Context(), nativeRecoveryFixture(), "Mac")
				if err != nil || got.Token != "fresh" {
					t.Fatalf("CLI long-token recovery: %v", err)
				}
			}
			if requests.Load() != 1 {
				t.Fatal("unexpected recovery RPC count")
			}
		})
	}
}
func nativeRecoveryFixture() NativeSession {
	web := webRecoverySession()
	return NativeSession{Version: nativeSessionVersion, FNID: web.FNID, Username: web.Username, DeviceID: web.DeviceID, Token: web.Token, LongToken: web.LongToken, Secret: web.Secret, UpdatedAt: web.UpdatedAt}
}

type rejectingNativeAuth struct {
	regular, rejected atomic.Int32
	call              func(context.Context) (NativeSession, error)
}

func (f *rejectingNativeAuth) Login(context.Context, NativeLogin) (NativeSession, error) {
	return NativeSession{}, errors.New("unexpected login")
}
func (f *rejectingNativeAuth) Recover(ctx context.Context, _ NativeSession, _ string) (NativeSession, error) {
	f.regular.Add(1)
	return f.call(ctx)
}
func (f *rejectingNativeAuth) RecoverRejectedToken(ctx context.Context, _ NativeSession, _ string) (NativeSession, error) {
	f.rejected.Add(1)
	return f.call(ctx)
}

func TestManagerOnlySkipsShortTokenForExplicitRejection(t *testing.T) {
	for _, known := range []bool{false, true} {
		store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), newMemorySecretStore())
		old := nativeRecoveryFixture()
		if err := store.SaveNativeSession(old); err != nil {
			t.Fatal(err)
		}
		next := old
		next.Token = "fresh"
		auth := &rejectingNativeAuth{call: func(context.Context) (NativeSession, error) { return next, nil }}
		m := &Manager{store: store, authenticator: auth, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		cause := error(model.NewError(model.ErrorAuthRequired, "generic application auth failure", false))
		if known {
			cause = explicitTokenRejection()
		}
		if _, err := m.recoverNativeSession(t.Context(), old.FNID, true, cause); err != nil {
			t.Fatal(err)
		}
		if (auth.rejected.Load() == 1) != known || (auth.regular.Load() == 1) == known {
			t.Fatal("wrong recovery policy selected")
		}
	}
}

func TestAuthenticationRecoveryIsQuietUntilActualFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		old := nativeRecoveryFixture()
		if err := m.store.SaveNativeSession(old); err != nil {
			t.Fatal(err)
		}
		next := old
		next.Token = "fresh"
		entered, release := make(chan struct{}), make(chan struct{})
		auth := &rejectingNativeAuth{call: func(ctx context.Context) (NativeSession, error) {
			close(entered)
			select {
			case <-release:
				return next, nil
			case <-ctx.Done():
				return NativeSession{}, ctx.Err()
			}
		}}
		m.authenticator = auth
		var output diagnosticBuffer
		m.logger = slog.New(slog.NewJSONHandler(&output, nil))
		var watches atomic.Int32
		m.remote = func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
			return &fakeWatchingRemoteService{fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()}, watch: func(ctx context.Context, _, cursor string) (ConfigurationWatchResult, error) {
				watches.Add(1)
				for _, cookie := range cookies {
					if cookie.Name == "fnos-token" && cookie.Value == "fresh" {
						<-ctx.Done()
						return ConfigurationWatchResult{}, ctx.Err()
					}
				}
				return ConfigurationWatchResult{}, explicitTokenRejection()
			}}, nil
		}
		m.setStatus(model.ClientLocal, "local", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		synctest.Wait()
		<-entered
		if m.Status().LastError != nil || m.Status().State != model.ClientLocal {
			t.Fatal("queued recovery surfaced as a connection failure")
		}
		advanceCoordinator(3 * time.Second)
		if watches.Load() != 1 {
			t.Fatal("watch polled rejected credentials while recovery was pending")
		}
		close(release)
		synctest.Wait()
		if auth.rejected.Load() != 1 || auth.regular.Load() != 0 || watches.Load() != 2 {
			t.Fatalf("rejected=%d regular=%d watches=%d", auth.rejected.Load(), auth.regular.Load(), watches.Load())
		}
		// The next long poll is still blocked: status must already be healthy.
		if m.Status().LastError != nil || m.Status().State != model.ClientLocal {
			t.Fatal("successful recovery waited for a 25-second poll to clear status")
		}
		if strings.Contains(output.String(), "gateway authentication recovery pending") {
			t.Fatal("synthetic pending error is still logged")
		}
	})
}

func TestAuthenticationRecoveryTransientFailureCanRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		old := nativeRecoveryFixture()
		if err := m.store.SaveNativeSession(old); err != nil {
			t.Fatal(err)
		}
		next := old
		next.Token = "fresh"
		var calls atomic.Int32
		auth := &rejectingNativeAuth{call: func(context.Context) (NativeSession, error) {
			if calls.Add(1) == 1 {
				return NativeSession{}, model.NewError(model.ErrorUnavailable, "temporary failure", true)
			}
			return next, nil
		}}
		m.authenticator = auth
		m.remote = func(_ string, cookies []Cookie, _ func([]Cookie) error) (RemoteService, error) {
			return &fakeWatchingRemoteService{fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()}, watch: func(ctx context.Context, _, _ string) (ConfigurationWatchResult, error) {
				for _, cookie := range cookies {
					if cookie.Name == "fnos-token" && cookie.Value == "fresh" {
						<-ctx.Done()
						return ConfigurationWatchResult{}, ctx.Err()
					}
				}
				return ConfigurationWatchResult{}, explicitTokenRejection()
			}}, nil
		}
		m.setStatus(model.ClientLocal, "local", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		synctest.Wait()
		if m.Status().LastError == nil || m.Status().State != model.ClientLocal {
			t.Fatal("real transient failure was hidden or tore down connection")
		}
		advanceCoordinator(1100 * time.Millisecond)
		if calls.Load() != 2 || m.Status().LastError != nil || m.Status().State != model.ClientLocal {
			t.Fatal("transient recovery failure became terminal or was not cleared promptly")
		}
	})
}

func TestPendingAuthenticationRecoveryCanceledOnSuspend(t *testing.T) {
	m := &Manager{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := &connectionCoordinator{manager: m, wake: make(chan struct{}, 1)}
	m.coordinator = c
	first := m.scheduleAuthenticationRecovery(explicitTokenRejection())
	if first == nil || first != m.scheduleAuthenticationRecovery(explicitTokenRejection()) {
		t.Fatal("pending recoveries did not merge")
	}
	m.suspendConnections()
	if err := first.wait(t.Context()); !errors.Is(err, context.Canceled) {
		t.Fatal("suspend left recovery waiter blocked")
	}
	if m.scheduleAuthenticationRecovery(explicitTokenRejection()) != nil {
		t.Fatal("suspended client accepted background authentication work")
	}
}

type rejectingWebAuth struct{ regular, rejected atomic.Int32 }

func (f *rejectingWebAuth) WebLogin(context.Context, NativeLogin) (AdminWebSession, error) {
	return AdminWebSession{}, errors.New("unexpected password login")
}
func (f *rejectingWebAuth) RecoverWeb(_ context.Context, s AdminWebSession, _ string) (AdminWebSession, error) {
	f.regular.Add(1)
	s.Token = "fresh"
	s.UpdatedAt = time.Now().UTC()
	return s, nil
}
func (f *rejectingWebAuth) RecoverRejectedWebToken(_ context.Context, s AdminWebSession, _ string) (AdminWebSession, error) {
	f.rejected.Add(1)
	s.Token = "fresh"
	s.UpdatedAt = time.Now().UTC()
	return s, nil
}

func TestAdminProbeOnlySkipsShortTokenForExplicitRejection(t *testing.T) {
	for _, known := range []bool{false, true} {
		m := recoveryManager(t, recoveryWebAuth{})
		auth := &rejectingWebAuth{}
		m.webAuthenticator = auth
		if err := m.store.Save(LocalConfig{Version: localConfigVersion, FNID: "home-nas", Administrator: true}); err != nil {
			t.Fatal(err)
		}
		m.adminWebProbe = func(_ context.Context, _ string, cookies []Cookie) error {
			for _, cookie := range cookies {
				if cookie.Name == "fnos-token" && cookie.Value == "fresh" {
					return nil
				}
			}
			if known {
				return explicitTokenRejection()
			}
			return model.NewError(model.ErrorAuthRequired, "application auth required", false)
		}
		if _, err := m.AdminProxyURL(t.Context()); err != nil {
			t.Fatal(err)
		}
		m.closeAdminProxy()
		if (auth.rejected.Load() == 1) != known || (auth.regular.Load() == 1) == known {
			t.Fatal("Web recovery did not preserve generic authentication handling")
		}
	}
}

func TestActiveAuthenticationRecoveryCanceledOnDisconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _, _, _, _ := coordinationFixture(t)
		old := nativeRecoveryFixture()
		if err := m.store.SaveNativeSession(old); err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{})
		auth := &rejectingNativeAuth{call: func(ctx context.Context) (NativeSession, error) {
			close(entered)
			<-ctx.Done()
			return NativeSession{}, ctx.Err()
		}}
		m.authenticator = auth
		m.remote = func(string, []Cookie, func([]Cookie) error) (RemoteService, error) {
			return &fakeWatchingRemoteService{fakeRemoteService: &fakeRemoteService{configuration: managerClientConfiguration()}, watch: func(context.Context, string, string) (ConfigurationWatchResult, error) {
				return ConfigurationWatchResult{}, explicitTokenRejection()
			}}, nil
		}
		m.setStatus(model.ClientLocal, "local", "", nil)
		stop := runCoordinated(t, m)
		defer stop()
		<-entered
		if err := m.Disconnect(t.Context()); err != nil {
			t.Fatal(err)
		}
		advanceCoordinator(time.Second)
		if m.Status().State != model.ClientPaused || auth.rejected.Load() != 1 {
			t.Fatal("late authentication recovery resumed a disconnected client")
		}
		stored, found, err := m.store.LoadNativeSession(old.FNID)
		if err != nil || !found || stored.Token != old.Token {
			t.Fatal("canceled recovery changed credentials")
		}
	})
}
