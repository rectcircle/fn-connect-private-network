package notify

import (
	"context"
	"testing"
	"time"
)

func TestWaitSeesChangeBeforeAndAfterSubscription(t *testing.T) {
	change := New()
	initial := change.Current()
	changed := change.Notify()
	if generation, ok := change.Wait(context.Background(), initial); !ok ||
		generation != changed {
		t.Fatalf("preexisting change = %d, %t", generation, ok)
	}

	result := make(chan uint64, 1)
	go func() {
		generation, _ := change.Wait(context.Background(), changed)
		result <- generation
	}()
	next := change.Notify()
	select {
	case generation := <-result:
		if generation != next {
			t.Fatalf("generation = %d, want %d", generation, next)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not notified")
	}
}

func TestWaitReturnsOnContextCancellation(t *testing.T) {
	change := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if generation, ok := change.Wait(ctx, change.Current()); ok ||
		generation != change.Current() {
		t.Fatalf("canceled wait = %d, %t", generation, ok)
	}
}
