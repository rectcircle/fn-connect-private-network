//go:build darwin && !cgo

package darwin

import (
	"context"
	"errors"
	"time"
)

func consoleUserEvents(ctx context.Context) <-chan consoleUserEvent {
	events := make(chan consoleUserEvent, 1)
	events <- consoleUserEvent{
		Err: errors.New("ConsoleUser change monitor requires CGO"),
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				select {
				case events <- consoleUserEvent{}:
				default:
				}
			}
		}
	}()
	return events
}
