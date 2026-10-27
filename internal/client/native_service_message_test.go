package client

import (
	"context"
	"errors"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"strings"
	"testing"
)

func TestNativeConnectFailureOffersActionableChecksWithoutLosingCause(t *testing.T) {
	for _, cause := range []error{errors.New("DNS resolution failed"), context.DeadlineExceeded} {
		err := nativeTransportError("connect", cause)
		failure := model.AsError(err)
		if failure.Operation != "native_session.connect" || !failure.Retryable || !errors.Is(err, cause) {
			t.Fatalf("lost failure semantics: %+v", failure)
		}
		for _, hint := range []string{"network connection", "FN Connect is enabled", "FN Connect ID is correct"} {
			if !strings.Contains(failure.Message, hint) {
				t.Fatalf("missing guidance %q: %s", hint, failure.Message)
			}
		}
	}
	canceled := model.AsError(nativeTransportError("connect", context.Canceled))
	if canceled.Code != model.ErrorCanceled || strings.Contains(canceled.Message, "FN Connect ID") {
		t.Fatalf("cancellation presented as service failure: %+v", canceled)
	}
}
