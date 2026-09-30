package model

import (
	"context"
	"errors"
	"testing"
)

func TestNormalizeErrorPreservesTypedErrors(t *testing.T) {
	expected := NewError(ErrorConflict, "conflict", false)
	if actual := NormalizeError(
		expected,
		ErrorUnavailable,
		"unavailable",
		true,
	); actual != expected {
		t.Fatalf("normalized error = %#v, want original", actual)
	}
}

func TestNormalizeErrorMapsContextErrors(t *testing.T) {
	tests := []struct {
		err  error
		code ErrorCode
	}{
		{err: context.Canceled, code: ErrorCanceled},
		{err: context.DeadlineExceeded, code: ErrorTimeout},
	}
	for _, test := range tests {
		actual := NormalizeError(test.err, ErrorInternal, "internal error", false)
		if actual.Code != test.code {
			t.Fatalf("error %v mapped to %s", test.err, actual.Code)
		}
		if !errors.Is(actual, test.err) {
			t.Fatalf("normalized error does not retain cause %v", test.err)
		}
	}
}

func TestPublicErrorRemovesCause(t *testing.T) {
	public := PublicError(WrapError(
		ErrorInternal,
		"internal error",
		false,
		errors.New("private path"),
	))
	if public.Cause != nil {
		t.Fatalf("public error retained cause: %v", public.Cause)
	}
}
