package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

func TestCookieUpdatesDoNotOverwriteOtherRequests(t *testing.T) {
	store := NewConfigStore(filepath.Join(t.TempDir(), "config.json"), newMemorySecretStore())
	before := []Cookie{
		{Name: "ost", Value: "old", Domain: "home.fnos.net", Path: "/"},
		{Name: "osrt", Value: "long", Domain: "home.fnos.net", Path: "/"},
	}
	if err := store.SaveCookies("home", before); err != nil {
		t.Fatal(err)
	}
	newSession := slices.Clone(before)
	newSession[0].Value = "new"
	if err := store.MergeCookies("home", before, newSession); err != nil {
		t.Fatal(err)
	}
	staleResponse := slices.Clone(before)
	staleResponse[1].Value = "renewed-long"
	if err := store.MergeCookies("home", before, staleResponse); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadCookies("home")
	if err != nil || got[0].Value != "new" || got[1].Value != "renewed-long" {
		t.Fatalf("independent updates lost: %v", err)
	}
	staleResponse[0].Value = "obsolete"
	if err := store.MergeCookies("home", before, staleResponse); err != nil {
		t.Fatal(err)
	}
	got, _ = store.LoadCookies("home")
	if got[0].Value != "new" {
		t.Fatal("stale refresh overwrote newer credentials")
	}
	if err := store.MergeCookies("home", got, got[1:]); err != nil {
		t.Fatal(err)
	}
	got, _ = store.LoadCookies("home")
	if len(got) != 1 || got[0].Name != "osrt" {
		t.Fatal("cookie deletion was not persisted")
	}
}

func TestRemoteAndRelayDoNotFollowLoginRedirects(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			redirected.Add(1)
			t.Error("credentials were forwarded to login page")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	cookies := []Cookie{{Name: "ost", Value: "canary", Domain: origin.Hostname(), Path: "/"}}
	remote, err := NewRemoteClient(server.URL+gatewayApplicationPath, cookies, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, apiErr := remote.Bootstrap(context.Background())
	target, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http") + gatewayApplicationPath + "/relay/v1/wireguard")
	_, wsErr := dialRelay(context.Background(), target, func() ([]Cookie, error) { return cookies, nil })
	for _, err := range []error{apiErr, wsErr} {
		if err == nil || model.AsError(err).Code != model.ErrorAuthRequired {
			t.Fatalf("login redirect classification: %v", err)
		}
	}
	if redirected.Load() != 0 {
		t.Fatal("login redirect followed")
	}
}

func TestRelayPersistsCookiesFromUpgrade(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "ost", Value: "renewed", Path: "/"})
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		_, _, _ = connection.Read(r.Context())
	}))
	defer server.Close()
	origin, _ := url.Parse(server.URL)
	cookies := []Cookie{{Name: "ost", Value: "old", Domain: origin.Hostname(), Path: "/"}}
	target, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http") + "/relay")
	var updated []Cookie
	connection, err := dialRelay(context.Background(), target, func() ([]Cookie, error) {
		return cookies, nil
	}, func(before, after []Cookie) error {
		if len(before) != 1 || before[0].Value != "old" {
			t.Fatal("incorrect update baseline")
		}
		updated = after
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	connection.CloseNow()
	if len(updated) != 1 || updated[0].Value != "renewed" {
		t.Fatal("upgrade discarded the renewed cookie")
	}
}

func TestAuthenticationRetriesOnlyOnceWithRenewedCookies(t *testing.T) {
	for _, transport := range []string{"api", "watch", "relay"} {
		for _, alwaysReject := range []bool{false, true} {
			t.Run(transport+map[bool]string{true: "_rejected", false: "_recovered"}[alwaysReject], func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if transport == "api" && r.URL.Path == "/api/v1/bootstrap" {
						_ = json.NewEncoder(w).Encode(Bootstrap{ServerVersion: version.Current, Administrator: true})
						return
					}
					count := calls.Add(1)
					if count == 1 || alwaysReject {
						http.SetCookie(w, &http.Cookie{Name: "ost", Value: "renewed", Path: "/"})
						_, _ = w.Write([]byte("invalid token"))
						return
					}
					cookie, err := r.Cookie("ost")
					if err != nil || cookie.Value != "renewed" {
						t.Error("retry did not use renewed credentials")
					}
					if transport == "relay" {
						connection, err := websocket.Accept(w, r, nil)
						if err != nil {
							return
						}
						defer connection.CloseNow()
						_, _, _ = connection.Read(r.Context())
					} else if transport == "watch" {
						_ = json.NewEncoder(w).Encode(ConfigurationWatchResult{Cursor: "ready"})
					} else {
						_ = json.NewEncoder(w).Encode(Bootstrap{ServerVersion: version.Current, Administrator: true})
					}
				}))
				defer server.Close()
				origin, _ := url.Parse(server.URL)
				records := []Cookie{{Name: "ost", Value: "old", Domain: origin.Hostname(), Path: "/"}}
				var err error
				if transport == "relay" {
					target, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http") + "/relay")
					connection, dialErr := dialRelay(context.Background(), target,
						func() ([]Cookie, error) { return records, nil },
						func(_, after []Cookie) error { records = after; return nil })
					err = dialErr
					if connection != nil {
						connection.CloseNow()
					}
				} else {
					remote, createErr := NewRemoteClient(server.URL, records, server.Client(), nil)
					if createErr != nil {
						t.Fatal(createErr)
					}
					if transport == "watch" {
						_, err = remote.WatchConfiguration(context.Background(), "device", "")
					} else {
						_, err = remote.Bootstrap(context.Background())
					}
				}
				if calls.Load() != 2 || (alwaysReject && (err == nil || model.AsError(err).Code != model.ErrorAuthRequired)) ||
					(!alwaysReject && err != nil) {
					t.Fatalf("calls=%d alwaysReject=%v error=%v", calls.Load(), alwaysReject, err)
				}
			})
		}
	}
}

func TestCookieSaveFailureCannotBeHiddenByAuthenticationRetry(t *testing.T) {
	for _, transport := range []string{"api", "relay"} {
		t.Run(transport, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.SetCookie(w, &http.Cookie{Name: "ost", Value: "renewed", Path: "/"})
				http.Error(w, "invalid token", http.StatusUnauthorized)
			}))
			defer server.Close()
			origin, _ := url.Parse(server.URL)
			records := []Cookie{{Name: "ost", Value: "old", Domain: origin.Hostname(), Path: "/"}}
			saveErr := errors.New("credential storage unavailable")
			var err error
			if transport == "api" {
				remote, createErr := NewRemoteClient(server.URL, records, server.Client(), func([]Cookie) error { return saveErr })
				if createErr != nil {
					t.Fatal(createErr)
				}
				_, err = remote.Bootstrap(context.Background())
			} else {
				target, _ := url.Parse("ws" + strings.TrimPrefix(server.URL, "http") + "/relay")
				_, err = dialRelay(context.Background(), target, func() ([]Cookie, error) { return records, nil },
					func(_, after []Cookie) error { records = after; return saveErr })
			}
			if !errors.Is(err, saveErr) || calls.Load() != 1 || !strings.HasPrefix(model.AsError(err).Operation, "credentials.") {
				t.Fatalf("save failure hidden: calls=%d error=%v", calls.Load(), err)
			}
		})
	}
}
