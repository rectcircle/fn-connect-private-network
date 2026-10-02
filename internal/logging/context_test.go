package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestContextLoggerKeepsRequestScope(t *testing.T) {
	var output bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&output, nil))
	parent := context.Background()
	if FromContext(parent, base) != base {
		t.Fatal("fallback logger changed")
	}
	child := WithLogger(parent, base.With("ipc_request_id", "request-1"))
	FromContext(child, nil).Info("child milestone")
	FromContext(parent, base).Info("parent milestone")
	if strings.Count(output.String(), `"ipc_request_id":"request-1"`) != 1 {
		t.Fatalf("request scope leaked or was lost: %s", output.String())
	}
	if FromContext(WithLogger(parent, nil), base) != base {
		t.Fatal("nil context logger did not use fallback")
	}
}
