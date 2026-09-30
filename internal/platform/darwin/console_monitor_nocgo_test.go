//go:build darwin && !cgo

package darwin

import (
	"context"
	"testing"
	"time"
)

func TestConsoleUserEventsFallsBackWithoutCGO(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := consoleUserEvents(ctx)
	select {
	case event := <-events:
		if event.Err == nil {
			t.Fatal("fallback event did not report unavailable CGO monitor")
		}
	case <-time.After(time.Second):
		t.Fatal("fallback event was not published")
	}
}
