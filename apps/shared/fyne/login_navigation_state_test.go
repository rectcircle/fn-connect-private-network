package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestRequiredLoginSurvivesConnectionStates(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.window = a.NewWindow("test")
	v.window.SetContent(v.content)
	v.applyStatus(model.ClientStatus{State: model.ClientUnconfigured})
	v.fnID.SetText("example")
	v.username.SetText("user")
	v.message.SetText("authentication service unavailable")
	v.message.Show()
	for _, state := range []model.ClientState{model.ClientAuthorizing, model.ClientReconnecting, model.ClientError, model.ClientPaused, model.ClientProbing, model.ClientAuthRequired} {
		v.applyStatus(model.ClientStatus{State: state})
		v.settlePage(v.editing)
		if !v.editing || !v.accountPanel.Visible() || v.mainPanel.Visible() || !v.requiresLogin() {
			t.Fatalf("login abandoned at %s", state)
		}
		if v.fnID.Text != "example" || v.username.Text != "user" || !v.message.Visible() {
			t.Fatalf("failed form lost at %s", state)
		}
	}
	// The root login keeps a menu, even when a retry temporarily changes state.
	v.applyStatus(model.ClientStatus{State: model.ClientReconnecting})
	v.navigate()
	if !v.editing || v.drawer == nil || !v.drawer.visible {
		t.Fatal("required root login navigated back instead of opening menu")
	}
	v.drawer.Dispose()
}

type loginNavigationRuntime struct {
	client.Runtime
	failure error
}

func (r *loginNavigationRuntime) Start() error                { return nil }
func (r *loginNavigationRuntime) Run(ctx context.Context)     { <-ctx.Done() }
func (r *loginNavigationRuntime) Close(context.Context) error { return nil }
func (r *loginNavigationRuntime) Status() model.ClientStatus {
	return model.ClientStatus{State: model.ClientPaused}
}
func (r *loginNavigationRuntime) Diagnose(context.Context) model.ClientDiagnostics {
	return model.ClientDiagnostics{Status: r.Status(), FNID: "example", Username: "user"}
}
func (r *loginNavigationRuntime) AuthorizeNative(context.Context, client.NativeCredentials) error {
	return r.failure
}

// The headless Fyne driver runs Do inline; wait for execute's context to close
// after its callback, rather than racing widget reads against the worker.
type loginNavigationBackend struct {
	host    *clienthost.Host
	started chan context.Context
	once    sync.Once
}

func (b *loginNavigationBackend) Call(ctx context.Context, method string, params, result any) error {
	if method == client.MethodAuthorizationBegin {
		b.once.Do(func() { b.started <- ctx })
	}
	return b.host.Call(ctx, method, params, result)
}

func TestRequiredLoginLeavesOnlyAfterFormAuthorization(t *testing.T) {
	challenge := model.NewError(model.ErrorFailedPrecondition, "verification required", false)
	challenge.RemoteCode = "TWO_FACTOR_REQUIRED"
	for _, tc := range []struct {
		name          string
		failure       error
		wantLogin     bool
		wantChallenge bool
	}{
		{"retryable failure", model.NewError(model.ErrorUnavailable, "unavailable", true), true, false},
		{"additional credential", challenge, true, true},
		{"success", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := test.NewApp()
			defer a.Quit()
			runtime := &loginNavigationRuntime{failure: tc.failure}
			host, err := clienthost.New(runtime)
			if err != nil {
				t.Fatal(err)
			}
			if err := host.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := host.Close(ctx); err != nil {
					t.Error(err)
				}
			}()
			backend := &loginNavigationBackend{host: host, started: make(chan context.Context, 1)}
			v := newClientView(backend)
			defer v.cancel()
			v.window = a.NewWindow("test")
			v.window.SetContent(v.content)
			v.applyStatus(model.ClientStatus{State: model.ClientUnconfigured})
			v.fnID.SetText("example")
			v.username.SetText("user")
			v.password.SetText("test-password")
			v.loginSubmit.(*touchButton).OnTapped()
			var operation context.Context
			select {
			case operation = <-backend.started:
			case <-time.After(2 * time.Second):
				t.Fatal("authorization did not start")
			}
			select {
			case <-operation.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("authorization did not complete")
			}
			fyne.DoAndWait(func() {
				// Model the background poll that previously caused a failed login to jump.
				v.applyStatus(model.ClientStatus{State: model.ClientReconnecting})
				v.settlePage(v.editing)
				if v.editing != tc.wantLogin || v.loginRequired != tc.wantLogin {
					t.Errorf("editing=%v required=%v, want login=%v", v.editing, v.loginRequired, tc.wantLogin)
				}
				if v.challenge.Visible() != tc.wantChallenge {
					t.Error("additional credential page changed unexpectedly")
				}
				if tc.wantLogin && (!v.message.Visible() || v.message.Text == "") {
					t.Error("authorization error disappeared")
				}
			})
		})
	}
}
