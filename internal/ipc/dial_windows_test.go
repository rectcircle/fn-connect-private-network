//go:build windows

package ipc

import (
	"context"
	"testing"
	"time"
)

func TestIPCDialContextCapsLongParentDeadline(t *testing.T) {
	started := time.Now()
	parent, cancelParent := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelParent()
	ctx, cancel := ipcDialContext(parent, 5*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("dial context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 5*time.Second || deadline.Sub(started) > 5*time.Second+100*time.Millisecond {
		t.Fatalf("dial deadline = %v (%v remaining), want a 5-second cap", deadline, remaining)
	}
	parentDeadline, _ := parent.Deadline()
	if !parentDeadline.After(deadline) {
		t.Fatalf("dial deadline %v was not earlier than parent deadline %v", deadline, parentDeadline)
	}
}

func TestIPCDialContextKeepsEarlierParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelParent()
	parentDeadline, _ := parent.Deadline()

	ctx, cancel := ipcDialContext(parent, 5*time.Second)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.Equal(parentDeadline) {
		t.Fatalf("dial deadline = %v, want earlier parent deadline %v", deadline, parentDeadline)
	}
}

func TestIPCDialContextWithoutTimeoutPreservesParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	ctx, cancel := ipcDialContext(parent, 0)
	defer cancel()
	if ctx != parent {
		t.Fatal("zero dial timeout should preserve the parent context")
	}
}
