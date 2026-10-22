package main

import (
	"context"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/clienthost"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// Exercise the real daemon authorization registry, including normalization and
// pending request reuse, rather than a fake that accepts missing request IDs.
type authorizationRuntime struct {
	client.Runtime
	inputs []client.NativeCredentials
}

func (r *authorizationRuntime) Start() error                { return nil }
func (r *authorizationRuntime) Run(ctx context.Context)     { <-ctx.Done() }
func (r *authorizationRuntime) Close(context.Context) error { return nil }
func (r *authorizationRuntime) Status() model.ClientStatus {
	return model.ClientStatus{State: model.ClientPaused}
}
func (r *authorizationRuntime) AuthorizeNative(_ context.Context, input client.NativeCredentials) error {
	r.inputs = append(r.inputs, input)
	if input.TwofaCode == "" {
		failure := model.NewError(model.ErrorFailedPrecondition, "请输入验证码", false)
		failure.RemoteCode = "TWO_FACTOR_REQUIRED"
		return failure
	}
	return nil
}

func TestAuthorizationRetriesOTPThroughRealDaemonService(t *testing.T) {
	runtime := &authorizationRuntime{}
	host, err := clienthost.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := host.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	flow := &authorizationFlow{}
	input := client.NativeAuthorization{FNID: "https://example.fnos.net", Username: "user", Password: "secret"}
	err = flow.submit(context.Background(), host, input)
	if !client.RequiresAdditionalCredential(err) {
		t.Fatalf("expected OTP challenge, got %v", err)
	}
	id := flow.id
	if id == "" {
		t.Fatal("missing pending request")
	}
	input.TwofaCode = "123456"
	input.Username, input.Password = "", ""
	if err := flow.submit(context.Background(), host, input); err != nil {
		t.Fatal(err)
	}
	var result client.AuthorizationResult
	if err := host.Call(context.Background(), client.MethodAuthorizationStatus, client.AuthorizationRequest{RequestID: id}, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "succeeded" {
		t.Fatalf("pending request was not completed: %+v", result)
	}
	if flow.id != "" {
		t.Fatal("completed request retained")
	}
	if len(runtime.inputs) != 2 || runtime.inputs[1].FNID != "example" || runtime.inputs[1].TwofaCode != "123456" || runtime.inputs[1].Password != "secret" {
		t.Fatal("daemon received incorrect normalized credentials")
	}
	if flow.credentials.Password != "" {
		t.Fatal("completed login retained password")
	}
}
