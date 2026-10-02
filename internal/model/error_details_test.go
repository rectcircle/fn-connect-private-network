package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestErrorDetailsPreserveJoinedCausesAndBoundaryFields(t *testing.T) {
	upstream := WrapError(ErrorUnavailable, "apply failed", true,
		&url.Error{Op: "POST", URL: "https://user:password@nas.example/app?token=secret#private", Err: io.EOF})
	upstream.Operation = "network.apply"
	upstream.HTTPStatus = 503
	upstream.RequestID = "ab0123456789abcdef0123456789abcdef"
	joined := errors.Join(fmt.Errorf("registration: %w", upstream), errors.New("rollback: permission denied"))
	public := PublicError(joined)
	if public.Code != ErrorUnavailable || !public.Retryable || public.Cause != nil ||
		public.Operation != upstream.Operation || public.RequestID != upstream.RequestID || public.HTTPStatus != 503 {
		t.Fatalf("public failure = %+v", public)
	}
	for _, part := range []string{"registration", "EOF", "rollback", "permission denied", "nas.example/app"} {
		if !strings.Contains(public.Detail, part) {
			t.Fatalf("detail %q omits %q", public.Detail, part)
		}
	}
	data, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user:", "password", "token=", "secret", "#private", `"cause"`} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("exposed %q in %s", secret, data)
		}
	}
	if !errors.Is(AsError(joined), io.EOF) || upstream.Detail != "" {
		t.Fatal("error chain or original error was modified")
	}
	if FormatError(public) == public.Message {
		t.Fatal("formatted diagnostic lost context")
	}
}

func TestSafeDiagnosticsDoNotRedactHarmlessDescriptions(t *testing.T) {
	for _, value := range []string{"authorization is required", "read privateKey failed", "load cookies: permission denied", "forbidden origin"} {
		if got := SafeText(value); got != value {
			t.Fatalf("%q became %q", value, got)
		}
	}
	for _, value := range []string{
		"Cookie: session=canary-secret", "Authorization: Bearer canary-secret",
		`{"privateKey":"canary-secret"}`, "password=canary-secret",
		"authx: nonce=1&sign=canary-secret",
	} {
		if got := SafeText(value); strings.Contains(got, "canary-secret") {
			t.Fatalf("leaked %q", got)
		}
	}
	if got := SafeText("https://user:password@nas.example/path?key=secret#fragment"); got != "https://nas.example/path" {
		t.Fatalf("URL diagnostic = %q", got)
	}
	if got := SafeText(strings.Repeat("x ", MaxErrorDetail)); len(got) > MaxErrorDetail+20 {
		t.Fatal("unbounded error detail")
	}
	var nilError *Error
	if ErrorDetail(nilError) != "" || WithOperation(nilError, "test") != nil || PublicError(nilError) != nil {
		t.Fatal("typed nil is not handled")
	}
}

func TestSystemErrorsHaveActionableCodes(t *testing.T) {
	for _, test := range []struct {
		err  error
		code ErrorCode
	}{
		{os.ErrPermission, ErrorPermissionDenied},
		{syscall.ENOSPC, ErrorResourceExhausted},
		{syscall.EMFILE, ErrorResourceExhausted},
	} {
		if got := AsError(test.err); got.Code != test.code || got.Retryable {
			t.Fatalf("%v mapped to %+v", test.err, got)
		}
	}
}
