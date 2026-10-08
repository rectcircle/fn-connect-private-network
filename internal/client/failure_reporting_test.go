package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

func TestFailureReportedOnceThroughTimeoutWrappers(t *testing.T) {
	m, _, _, _, _ := coordinationFixture(t)
	var output bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&output, nil))
	cause := model.WrapError(model.ErrorTimeout, "relay timed out", true, context.DeadlineExceeded)
	err := m.fail(cause)
	// Outer connection boundaries still recognize and normalize deadlines.
	err = m.fail(fmt.Errorf("connection: %w", err))
	if count := strings.Count(output.String(), "client operation failed"); count != 1 {
		t.Fatalf("one failure produced %d log entries: %s", count, output.String())
	}
	if !errors.Is(err, context.DeadlineExceeded) || model.AsError(err).Code != model.ErrorTimeout || m.Status().State != model.ClientReconnecting {
		t.Fatal("report marker lost the cause or failure state")
	}
	m.fail(model.WrapError(model.ErrorTimeout, "next attempt timed out", true, context.DeadlineExceeded))
	if strings.Count(output.String(), "client operation failed") != 2 {
		t.Fatal("a separate failure was suppressed")
	}
}
