package client

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestAuthorizationUIFailureIsRetainedAndSanitized(t *testing.T) {
	service := NewWithRuntime(&recordingRuntime{})
	request, _ := ipc.NewRequest("begin", MethodAuthorizationBegin, AuthorizationRequest{FNID: "home-nas"})
	begin := service.Handle(context.Background(), request)
	var pending AuthorizationResult
	if err := json.Unmarshal(begin.Result, &pending); err != nil {
		t.Fatal(err)
	}
	failure := model.NewError(model.ErrorPermissionDenied, "UI request rejected", false)
	failure.HTTPStatus = 403
	failure.Detail = "https://user:secret@home-nas.fnos.net/app/fncpn?token=canary"
	request, _ = ipc.NewRequest("cancel", MethodAuthorizationCancel, AuthorizationCancelRequest{
		RequestID: pending.RequestID, Failure: failure,
	})
	response := service.Handle(context.Background(), request)
	if response.OK || response.Error == nil || response.Error.HTTPStatus != 403 ||
		response.Error.Operation != "authorization.ui" {
		t.Fatalf("failure response = %+v", response)
	}
	request, _ = ipc.NewRequest("status", MethodAuthorizationStatus, AuthorizationRequest{RequestID: pending.RequestID})
	response = service.Handle(context.Background(), request)
	var result AuthorizationResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.State != "failed" || result.Error == nil || result.Error.Code != model.ErrorPermissionDenied ||
		strings.Contains(result.Error.Detail, "secret") || strings.Contains(result.Error.Detail, "canary") {
		t.Fatalf("recorded UI failure = %+v", result)
	}
}

func TestServiceDelegatesCommandsToRuntime(t *testing.T) {
	runtime := &recordingRuntime{status: model.ClientStatus{State: model.ClientPaused}}
	service := NewWithRuntime(runtime)
	for _, method := range []string{
		MethodConnect, MethodDisconnect, MethodRetry, MethodLogout, MethodForget,
	} {
		response := service.Handle(context.Background(), ipc.Request{ID: method, Method: method})
		if !response.OK || runtime.lastCommand != method {
			t.Fatalf("method=%s response=%+v delegated=%s", method, response, runtime.lastCommand)
		}
	}
	response := service.Handle(context.Background(), ipc.Request{ID: "status", Method: MethodStatus})
	var status model.ClientStatus
	if err := json.Unmarshal(response.Result, &status); err != nil || status.State != runtime.status.State {
		t.Fatalf("status response=%+v error=%v", response, err)
	}
}

func TestRuntimeServiceReturnsAdminProxyURL(t *testing.T) {
	runtime := &recordingRuntime{adminProxyURL: "http://127.0.0.1:12345/app/fncpn?fncpn_proxy=nonce"}
	response := NewWithRuntime(runtime).Handle(
		context.Background(), ipc.Request{ID: "admin", Method: MethodAdminProxy},
	)
	if !response.OK {
		t.Fatalf("admin proxy response = %+v", response)
	}
	var result map[string]string
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result["url"] != runtime.adminProxyURL {
		t.Fatalf("admin proxy URL = %q", result["url"])
	}
}

func TestRuntimeServiceAcceptsAuthorizationFromApp(t *testing.T) {
	runtime := &recordingRuntime{
		status: model.ClientStatus{State: model.ClientPaused},
	}
	service := NewWithRuntime(runtime)
	beginRequest, err := ipc.NewRequest(
		"begin",
		MethodAuthorizationBegin,
		AuthorizationRequest{FNID: "home-nas"},
	)
	if err != nil {
		t.Fatalf("new begin request: %v", err)
	}
	beginResponse := service.Handle(context.Background(), beginRequest)
	if !beginResponse.OK {
		t.Fatalf("begin response = %+v", beginResponse)
	}
	var pending AuthorizationResult
	if err := json.Unmarshal(beginResponse.Result, &pending); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}
	request, err := ipc.NewRequest(
		"authorize",
		MethodAuthorize,
		Authorization{
			RequestID: pending.RequestID,
			FNID:      "home-nas",
			Cookies: []Cookie{
				{Name: "fnos-token", Value: "secret"},
			},
		},
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if !response.OK {
		t.Fatalf("authorize response = %+v", response)
	}
	if runtime.fnID != "home-nas" ||
		len(runtime.cookies) != 1 ||
		runtime.cookies[0].Value != "secret" {
		t.Fatalf("authorization = %q %+v", runtime.fnID, runtime.cookies)
	}
	statusRequest, err := ipc.NewRequest(
		"status",
		MethodAuthorizationStatus,
		AuthorizationRequest{RequestID: pending.RequestID},
	)
	if err != nil {
		t.Fatalf("new status request: %v", err)
	}
	statusResponse := service.Handle(context.Background(), statusRequest)
	if !statusResponse.OK {
		t.Fatalf("authorization status response = %+v", statusResponse)
	}
	var result AuthorizationResult
	if err := json.Unmarshal(statusResponse.Result, &result); err != nil {
		t.Fatalf("decode authorization status: %v", err)
	}
	if result.State != "succeeded" {
		t.Fatalf("authorization state = %q", result.State)
	}
}

func TestRuntimeServiceAcceptsNativeAuthorizationFromApp(t *testing.T) {
	runtime := &recordingRuntime{status: model.ClientStatus{State: model.ClientPaused}}
	service := NewWithRuntime(runtime)
	beginRequest, _ := ipc.NewRequest(
		"begin",
		MethodAuthorizationBegin,
		AuthorizationRequest{FNID: "home-nas"},
	)
	beginResponse := service.Handle(context.Background(), beginRequest)
	var pending AuthorizationResult
	if err := json.Unmarshal(beginResponse.Result, &pending); err != nil {
		t.Fatal(err)
	}
	request, _ := ipc.NewRequest(
		"authorize",
		MethodAuthorizeNative,
		NativeAuthorization{
			RequestID: pending.RequestID,
			FNID:      "home-nas",
			Username:  "admin",
			Password:  "password-canary",
		},
	)
	response := service.Handle(context.Background(), request)
	if !response.OK || runtime.fnID != "home-nas" ||
		runtime.username != "admin" || runtime.password != "password-canary" {
		t.Fatalf("native authorization was not delegated: response=%+v", response)
	}
}

func TestRuntimeServiceRejectsExpiredAuthorization(t *testing.T) {
	runtime := &recordingRuntime{
		status: model.ClientStatus{State: model.ClientPaused},
	}
	service := NewWithRuntime(runtime)
	service.authorizationTTL = 10 * time.Millisecond
	beginRequest, err := ipc.NewRequest(
		"begin",
		MethodAuthorizationBegin,
		AuthorizationRequest{FNID: "home-nas"},
	)
	if err != nil {
		t.Fatalf("new begin request: %v", err)
	}
	beginResponse := service.Handle(context.Background(), beginRequest)
	if !beginResponse.OK {
		t.Fatalf("begin response = %+v", beginResponse)
	}
	var pending AuthorizationResult
	if err := json.Unmarshal(beginResponse.Result, &pending); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for {
		service.mu.RLock()
		state := service.pending[pending.RequestID].state
		service.mu.RUnlock()
		if state == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authorization did not expire")
		}
		time.Sleep(time.Millisecond)
	}

	request, err := ipc.NewRequest(
		"authorize",
		MethodAuthorize,
		Authorization{
			RequestID: pending.RequestID,
			FNID:      "home-nas",
			Cookies:   []Cookie{{Name: "fnos-token", Value: "secret"}},
		},
	)
	if err != nil {
		t.Fatalf("new authorize request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if response.OK ||
		response.Error == nil ||
		response.Error.Code != model.ErrorTimeout {
		t.Fatalf("expired authorization response = %+v", response)
	}
	if runtime.fnID != "" {
		t.Fatalf("expired authorization reached runtime for %q", runtime.fnID)
	}
}

func TestRuntimeServiceCancelInterruptsAuthorization(t *testing.T) {
	started := make(chan struct{})
	runtime := &recordingRuntime{
		status: model.ClientStatus{State: model.ClientPaused},
		authorize: func(ctx context.Context, _ string, _ []Cookie) error {
			close(started)
			<-ctx.Done()
			return nil
		},
	}
	service := NewWithRuntime(runtime)
	beginRequest, err := ipc.NewRequest(
		"begin",
		MethodAuthorizationBegin,
		AuthorizationRequest{FNID: "home-nas"},
	)
	if err != nil {
		t.Fatalf("new begin request: %v", err)
	}
	beginResponse := service.Handle(context.Background(), beginRequest)
	if !beginResponse.OK {
		t.Fatalf("begin response = %+v", beginResponse)
	}
	var pending AuthorizationResult
	if err := json.Unmarshal(beginResponse.Result, &pending); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}
	authorizeRequest, err := ipc.NewRequest(
		"authorize",
		MethodAuthorize,
		Authorization{
			RequestID: pending.RequestID,
			FNID:      "home-nas",
			Cookies:   []Cookie{{Name: "fnos-token", Value: "secret"}},
		},
	)
	if err != nil {
		t.Fatalf("new authorize request: %v", err)
	}
	authorizeResponse := make(chan ipc.Response, 1)
	go func() {
		authorizeResponse <- service.Handle(context.Background(), authorizeRequest)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("authorization did not start")
	}

	cancelRequest, err := ipc.NewRequest(
		"cancel",
		MethodAuthorizationCancel,
		AuthorizationRequest{RequestID: pending.RequestID},
	)
	if err != nil {
		t.Fatalf("new cancel request: %v", err)
	}
	cancelResponse := service.Handle(context.Background(), cancelRequest)
	if !cancelResponse.OK {
		t.Fatalf("cancel response = %+v", cancelResponse)
	}
	select {
	case response := <-authorizeResponse:
		if response.OK ||
			response.Error == nil ||
			response.Error.Code != model.ErrorCanceled {
			t.Fatalf("authorize response after cancel = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("authorization was not interrupted")
	}

	statusRequest, err := ipc.NewRequest(
		"status",
		MethodAuthorizationStatus,
		AuthorizationRequest{RequestID: pending.RequestID},
	)
	if err != nil {
		t.Fatalf("new status request: %v", err)
	}
	statusResponse := service.Handle(context.Background(), statusRequest)
	if !statusResponse.OK {
		t.Fatalf("authorization status response = %+v", statusResponse)
	}
	var result AuthorizationResult
	if err := json.Unmarshal(statusResponse.Result, &result); err != nil {
		t.Fatalf("decode authorization status: %v", err)
	}
	if result.State != "canceled" ||
		result.Error == nil ||
		result.Error.Code != model.ErrorCanceled {
		t.Fatalf("authorization result = %+v", result)
	}
}

func TestRuntimeServiceAuthorizationWaitReturnsCompletion(t *testing.T) {
	runtime := &recordingRuntime{
		status: model.ClientStatus{State: model.ClientPaused},
	}
	service := NewWithRuntime(runtime)
	beginRequest, err := ipc.NewRequest(
		"begin",
		MethodAuthorizationBegin,
		AuthorizationRequest{FNID: "home-nas"},
	)
	if err != nil {
		t.Fatalf("new begin request: %v", err)
	}
	beginResponse := service.Handle(context.Background(), beginRequest)
	var pending AuthorizationResult
	if err := json.Unmarshal(beginResponse.Result, &pending); err != nil {
		t.Fatalf("decode begin response: %v", err)
	}
	waitRequest, err := ipc.NewRequest(
		"wait",
		MethodAuthorizationWait,
		AuthorizationRequest{RequestID: pending.RequestID},
	)
	if err != nil {
		t.Fatalf("new wait request: %v", err)
	}
	result := make(chan ipc.Response, 1)
	go func() {
		result <- service.Handle(context.Background(), waitRequest)
	}()
	authorizeRequest, err := ipc.NewRequest(
		"authorize",
		MethodAuthorize,
		Authorization{
			RequestID: pending.RequestID,
			FNID:      "home-nas",
			Cookies:   []Cookie{{Name: "fnos-token", Value: "secret"}},
		},
	)
	if err != nil {
		t.Fatalf("new authorize request: %v", err)
	}
	if response := service.Handle(context.Background(), authorizeRequest); !response.OK {
		t.Fatalf("authorize response = %+v", response)
	}
	select {
	case response := <-result:
		if !response.OK {
			t.Fatalf("wait response = %+v", response)
		}
		var authorization AuthorizationResult
		if err := json.Unmarshal(response.Result, &authorization); err != nil {
			t.Fatalf("decode wait response: %v", err)
		}
		if authorization.State != "succeeded" {
			t.Fatalf("authorization state = %q", authorization.State)
		}
	case <-time.After(time.Second):
		t.Fatal("authorization wait was not notified")
	}
}

func TestRuntimeServiceWatchesStatus(t *testing.T) {
	runtime := &watchingRuntime{
		recordingRuntime: recordingRuntime{
			status: model.ClientStatus{State: model.ClientPaused},
		},
	}
	service := NewWithRuntime(runtime)
	request, err := ipc.NewRequest(
		"watch",
		MethodWatchStatus,
		StatusWatchRequest{After: 7},
	)
	if err != nil {
		t.Fatalf("new watch request: %v", err)
	}
	response := service.Handle(context.Background(), request)
	if !response.OK || runtime.after != 7 {
		t.Fatalf("watch response = %+v after=%d", response, runtime.after)
	}
	var result StatusWatchResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode watch response: %v", err)
	}
	if !result.Changed ||
		result.Generation != 8 ||
		result.Status.State != model.ClientDirect {
		t.Fatalf("watch result = %+v", result)
	}
}

type recordingRuntime struct {
	lastCommand     string
	status          model.ClientStatus
	adminProxyURL   string
	fnID            string
	cookies         []Cookie
	username        string
	password        string
	authorize       func(context.Context, string, []Cookie) error
	authorizeNative func(context.Context, string, string, string) error
}

func (r *recordingRuntime) Status() model.ClientStatus {
	return r.status
}

func (r *recordingRuntime) Diagnose(context.Context) model.ClientDiagnostics {
	return model.ClientDiagnostics{Status: r.status}
}

func (r *recordingRuntime) AdminProxyURL(context.Context) (string, error) {
	return r.adminProxyURL, nil
}

func (r *recordingRuntime) Authorize(
	ctx context.Context,
	fnID string,
	cookies []Cookie,
) error {
	r.fnID = fnID
	r.cookies = cookies
	if r.authorize != nil {
		return r.authorize(ctx, fnID, cookies)
	}
	return nil
}

func (r *recordingRuntime) AuthorizeNative(
	ctx context.Context,
	fnID string,
	username string,
	password string,
) error {
	r.fnID = fnID
	r.username = username
	r.password = password
	if r.authorizeNative != nil {
		return r.authorizeNative(ctx, fnID, username, password)
	}
	return nil
}

func (r *recordingRuntime) Connect(context.Context) error {
	r.lastCommand = MethodConnect
	return nil
}

func (r *recordingRuntime) Disconnect(context.Context) error {
	r.lastCommand = MethodDisconnect
	return nil
}

func (r *recordingRuntime) Retry(context.Context) error {
	r.lastCommand = MethodRetry
	return nil
}

func (r *recordingRuntime) Logout(context.Context) error {
	r.lastCommand = MethodLogout
	return nil
}

func (r *recordingRuntime) Forget(context.Context) error {
	r.lastCommand = MethodForget
	return nil
}

type watchingRuntime struct {
	recordingRuntime
	after uint64
}

func (r *watchingRuntime) WatchStatus(
	_ context.Context,
	after uint64,
) StatusWatchResult {
	r.after = after
	return StatusWatchResult{
		Changed:    true,
		Generation: after + 1,
		Status:     model.ClientStatus{State: model.ClientDirect},
	}
}
