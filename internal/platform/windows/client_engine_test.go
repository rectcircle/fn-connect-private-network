//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
	"github.com/rectcircle/fn-connect-private-network/internal/privileged"
)

func TestNotifyConsoleSessionChangeCoalescesBursts(t *testing.T) {
	for len(consoleSessionChanges) > 0 {
		<-consoleSessionChanges
	}
	NotifyConsoleSessionChange()
	NotifyConsoleSessionChange()
	if got := len(consoleSessionChanges); got != 1 {
		t.Fatalf("queued session-change notifications = %d, want 1 coalesced event", got)
	}
	<-consoleSessionChanges
	if got := len(consoleSessionChanges); got != 0 {
		t.Fatalf("queued session-change notifications after consume = %d, want 0", got)
	}
}

func TestPowerResumeTraversesPrivilegedLifecycleIPC(t *testing.T) {
	engine := NewClientEngine(slog.Default(), t.TempDir())
	service := privileged.NewClientService(engine, nil)

	NotifyPowerResume()
	request, err := ipc.NewRequest("resume", privileged.MethodWatchLifecycle, privileged.WatchRequest{After: 0})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response := service.Handle(ctx, request)
	if !response.OK {
		t.Fatalf("watch lifecycle failed: %+v", response.Error)
	}
	var result privileged.WatchResult[privileged.ClientStatus]
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode lifecycle result: %v", err)
	}
	if !result.Changed || result.Status.PowerResumeGeneration != 1 {
		t.Fatalf("resume lifecycle result = %+v, want changed generation 1", result)
	}
}

func TestPowerResumeBeforeLifecycleWatcherIsInstalledIsPublished(t *testing.T) {
	engine := &ClientEngine{}
	engine.NotifyPowerResume()
	notified := false
	engine.SetLifecycleNotifier(func() { notified = true })
	if !notified || engine.PowerResumeGeneration() != 1 {
		t.Fatalf("resume notification after lifecycle setup = %v, generation = %d", notified, engine.PowerResumeGeneration())
	}
}

func TestMonitorConsoleSessionEventsChecksImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan struct{}, 1)
	checked := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		monitorConsoleSessionEvents(ctx, make(chan time.Time), events, func() { checked <- struct{}{} })
		close(done)
	}()
	events <- struct{}{}
	select {
	case <-checked:
	case <-time.After(time.Second):
		t.Fatal("session change event did not trigger an immediate owner check")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session monitor did not stop after cancellation")
	}
}

func TestSameConsoleOwnerRequiresSIDAndSessionMatch(t *testing.T) {
	tests := []struct {
		name           string
		ownerSID       string
		ownerSession   uint32
		consoleSID     string
		consoleSession uint32
		want           bool
	}{
		{name: "same user and session", ownerSID: "S-1-5-21-100", ownerSession: 4, consoleSID: "S-1-5-21-100", consoleSession: 4, want: true},
		{name: "different user reused session", ownerSID: "S-1-5-21-100", ownerSession: 4, consoleSID: "S-1-5-21-200", consoleSession: 4},
		{name: "same user new session", ownerSID: "S-1-5-21-100", ownerSession: 4, consoleSID: "S-1-5-21-100", consoleSession: 5},
		{name: "identity unavailable", ownerSID: "S-1-5-21-100", ownerSession: 4, consoleSession: 4},
		{name: "no active console", ownerSID: "S-1-5-21-100", ownerSession: 4, consoleSID: "S-1-5-21-100", consoleSession: 0xffffffff},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sameConsoleOwner(test.ownerSID, test.ownerSession, test.consoleSID, test.consoleSession); got != test.want {
				t.Fatalf("sameConsoleOwner() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAuthorizedConsolePeerRequiresCurrentSIDAndSession(t *testing.T) {
	tests := []struct {
		name           string
		peer           ipc.PeerIdentity
		consoleSID     string
		consoleSession uint32
		want           bool
	}{
		{
			name:       "active console owner",
			peer:       ipc.PeerIdentity{SID: "S-1-5-21-100", SessionID: 4},
			consoleSID: "S-1-5-21-100", consoleSession: 4, want: true,
		},
		{
			name:       "stale user after fast switch",
			peer:       ipc.PeerIdentity{SID: "S-1-5-21-100", SessionID: 4},
			consoleSID: "S-1-5-21-200", consoleSession: 4,
		},
		{
			name:       "stale session after reconnect",
			peer:       ipc.PeerIdentity{SID: "S-1-5-21-100", SessionID: 4},
			consoleSID: "S-1-5-21-100", consoleSession: 5,
		},
		{
			name:       "console identity unavailable",
			peer:       ipc.PeerIdentity{SID: "S-1-5-21-100", SessionID: 4},
			consoleSID: "", consoleSession: 4,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := authorizedConsolePeer(test.peer, test.consoleSID, test.consoleSession); got != test.want {
				t.Fatalf("authorizedConsolePeer() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConsoleReconciliationCannotRemoveTunnelAppliedByNewOwner(t *testing.T) {
	engine := &ClientEngine{
		network:       &NetworkConfigurator{path: filepath.Join(t.TempDir(), "network-state.json")},
		logger:        slog.Default(),
		interfaceName: "FnCPN",
		ownerSID:      "S-1-5-21-old",
		ownerSession:  4,
	}
	identityStarted := make(chan struct{})
	continueIdentity := make(chan struct{})
	reconcileDone := make(chan struct{})
	go func() {
		engine.reconcileConsoleOwnerWith(func() (uint32, string) {
			close(identityStarted)
			<-continueIdentity
			return 4, "S-1-5-21-new"
		})
		close(reconcileDone)
	}()
	<-identityStarted

	updateAttempted := make(chan struct{})
	updateDone := make(chan struct{})
	go func() {
		close(updateAttempted)
		engine.mu.Lock()
		engine.interfaceName = "FnCPN"
		engine.ownerSID = "S-1-5-21-new"
		engine.ownerSession = 4
		engine.mu.Unlock()
		close(updateDone)
	}()
	<-updateAttempted
	select {
	case <-updateDone:
		t.Fatal("new owner updated tunnel state while console reconciliation held the engine lock")
	default:
	}
	close(continueIdentity)
	select {
	case <-reconcileDone:
	case <-time.After(time.Second):
		t.Fatal("console reconciliation did not finish")
	}
	select {
	case <-updateDone:
	case <-time.After(time.Second):
		t.Fatal("new owner could not apply after stale tunnel cleanup completed")
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.interfaceName != "FnCPN" || engine.ownerSID != "S-1-5-21-new" || engine.ownerSession != 4 {
		t.Fatalf("new owner's tunnel state was lost: interface=%q owner=%q session=%d", engine.interfaceName, engine.ownerSID, engine.ownerSession)
	}
}
