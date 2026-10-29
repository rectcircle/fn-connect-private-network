package uifeedback

import (
	"sync"
	"testing"
)

func TestNativeFailuresWaitForSharedUI(t *testing.T) {
	bridge.Lock()
	bridge.handler = nil
	bridge.pending = nil
	bridge.Unlock()
	t.Cleanup(func() { bridge.Lock(); bridge.handler = nil; bridge.pending = nil; bridge.Unlock() })
	Publish("before UI")
	var mu sync.Mutex
	var received []string
	SetHandler(func(text string) { mu.Lock(); received = append(received, text); mu.Unlock() })
	var work sync.WaitGroup
	for i := 0; i < 10; i++ {
		work.Add(1)
		go func() { defer work.Done(); Publish("native failure") }()
	}
	work.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 11 || received[0] != "before UI" {
		t.Fatal(received)
	}
}
