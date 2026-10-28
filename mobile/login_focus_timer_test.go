package main

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func init() {
	// Headless UI tests advance delayed focus explicitly; their driver has no UI
	// event loop to serialize a real timer with direct widget assertions/cleanup.
	scheduleLoginReveal = func(time.Duration, func()) {}
}

func TestDelayedLoginRevealRequiresCurrentFocus(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	original := scheduleLoginReveal
	defer func() { scheduleLoginReveal = original }()
	var pending []func()
	scheduleLoginReveal = func(delay time.Duration, callback func()) {
		if delay != 350*time.Millisecond {
			t.Fatalf("unexpected keyboard delay: %v", delay)
		}
		pending = append(pending, callback)
	}
	calls := 0
	entry := newLoginEntry(false, func(object fyne.CanvasObject) {
		if object != nil {
			calls++
		}
	})
	window := a.NewWindow("focus")
	defer window.Close()
	window.SetContent(entry)
	window.Canvas().Focus(entry)
	if len(pending) != 1 || calls != 0 {
		t.Fatalf("focus must defer reveal: pending=%d calls=%d", len(pending), calls)
	}
	pending[0]()
	if calls != 1 {
		t.Fatal("focused field was not revealed")
	}
	window.Canvas().Focus(nil)
	window.Canvas().Focus(entry)
	window.Canvas().Focus(nil)
	if len(pending) != 2 {
		t.Fatalf("expected a new focus callback, got %d", len(pending))
	}
	pending[1]()
	if calls != 1 {
		t.Fatal("stale callback revealed a field after focus was lost")
	}
}
