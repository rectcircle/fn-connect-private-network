//go:build windows

package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

type windowsServiceResult struct {
	accepted bool
	exitCode uint32
}

func receiveWindowsServiceStatus(t *testing.T, statuses <-chan svc.Status) svc.Status {
	t.Helper()
	select {
	case status := <-statuses:
		return status
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SCM status")
		return svc.Status{}
	}
}

func runWindowsServiceForTest(
	t *testing.T,
	service windowsClientService,
	requests chan svc.ChangeRequest,
) (<-chan svc.Status, <-chan windowsServiceResult) {
	t.Helper()
	statuses := make(chan svc.Status, 8)
	result := make(chan windowsServiceResult, 1)
	go func() {
		accepted, exitCode := service.Execute(nil, requests, statuses)
		result <- windowsServiceResult{accepted: accepted, exitCode: exitCode}
	}()
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.StartPending || status.CheckPoint != 1 || status.WaitHint != uint32(windowsServiceStartWaitHint/time.Millisecond) {
		t.Fatalf("initial service status = %+v, want StartPending checkpoint 1 with 30s wait hint", status)
	}
	return statuses, result
}

func signalWindowsServiceReady(ready func()) { ready() }

func receiveWindowsServiceResult(t *testing.T, result <-chan windowsServiceResult) windowsServiceResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service Execute to return")
		return windowsServiceResult{}
	}
}

func TestWindowsServiceStopAndShutdownLifecycle(t *testing.T) {
	for _, control := range []struct {
		name string
		cmd  svc.Cmd
	}{
		{name: "stop", cmd: svc.Stop},
		{name: "shutdown", cmd: svc.Shutdown},
	} {
		t.Run(control.name, func(t *testing.T) {
			requests := make(chan svc.ChangeRequest)
			started := make(chan struct{})
			stopped := make(chan struct{})
			service := windowsClientService{
				parent: context.Background(),
				role:   "client",
				runDaemon: func(ctx context.Context, role string, _ []string, _ Environment, ready func()) error {
					if role != "client" {
						return errors.New("unexpected service role")
					}
					signalWindowsServiceReady(ready)
					close(started)
					<-ctx.Done()
					close(stopped)
					return nil
				},
			}
			statuses, result := runWindowsServiceForTest(t, service, requests)
			if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Running {
				t.Fatalf("ready service state = %v, want Running", status.State)
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("service daemon did not start")
			}
			requests <- svc.ChangeRequest{Cmd: svc.Interrogate, CurrentStatus: svc.Status{State: svc.Running}}
			if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Running {
				t.Fatalf("interrogate returned state %v, want Running", status.State)
			}
			requests <- svc.ChangeRequest{Cmd: control.cmd}
			if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.StopPending {
				t.Fatalf("stop control returned state %v, want StopPending", status.State)
			}
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("service daemon did not observe cancellation")
			}
			if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Stopped {
				t.Fatalf("final service state = %v, want Stopped", status.State)
			}
			if value := receiveWindowsServiceResult(t, result); value.accepted || value.exitCode != 0 {
				t.Fatalf("Execute result = (%v, %d), want (false, 0)", value.accepted, value.exitCode)
			}
		})
	}
}

func TestWindowsServiceWaitsForDaemonReadiness(t *testing.T) {
	requests := make(chan svc.ChangeRequest)
	started := make(chan struct{})
	allowReady := make(chan struct{})
	service := windowsClientService{
		parent: context.Background(),
		role:   "client",
		runDaemon: func(ctx context.Context, _ string, _ []string, _ Environment, ready func()) error {
			close(started)
			select {
			case <-allowReady:
				ready()
			case <-ctx.Done():
				return nil
			}
			<-ctx.Done()
			return nil
		},
		startPendingInterval: 5 * time.Millisecond,
	}
	statuses, result := runWindowsServiceForTest(t, service, requests)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("service daemon did not start")
	}
	time.Sleep(25 * time.Millisecond)
	checkpoint := receiveWindowsServiceStatus(t, statuses)
	if checkpoint.State != svc.StartPending || checkpoint.CheckPoint < 2 {
		t.Fatalf("startup progress status = %+v, want increasing StartPending checkpoint", checkpoint)
	}
	for len(statuses) > 0 {
		if status := <-statuses; status.State == svc.Running {
			t.Fatal("service reported Running before daemon readiness")
		}
	}
	close(allowReady)
	for {
		status := receiveWindowsServiceStatus(t, statuses)
		if status.State == svc.Running {
			break
		}
		if status.State != svc.StartPending {
			t.Fatalf("state before Running = %v, want StartPending", status.State)
		}
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.StopPending {
		t.Fatalf("stop state = %v, want StopPending", status.State)
	}
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Stopped {
		t.Fatalf("final service state = %v, want Stopped", status.State)
	}
	if value := receiveWindowsServiceResult(t, result); value.exitCode != 0 {
		t.Fatalf("Execute exit code = %d, want 0", value.exitCode)
	}
}

func TestWindowsServiceDaemonFailureAndLifecycleEvents(t *testing.T) {
	requests := make(chan svc.ChangeRequest)
	started := make(chan struct{})
	resumeCalls := make(chan struct{}, 1)
	sessionCalls := make(chan struct{}, 1)
	service := windowsClientService{
		parent: context.Background(),
		role:   "client",
		runDaemon: func(context.Context, string, []string, Environment, func()) error {
			close(started)
			return errors.New("simulated daemon startup failure")
		},
		notifyConsoleSessionChange: func() { sessionCalls <- struct{}{} },
		notifyPowerResume:          func() { resumeCalls <- struct{}{} },
	}
	statuses, result := runWindowsServiceForTest(t, service, requests)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("service daemon did not start")
	}
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Stopped {
		t.Fatalf("failed daemon final service state = %v, want Stopped", status.State)
	}
	if value := receiveWindowsServiceResult(t, result); value.accepted || value.exitCode != 1 {
		t.Fatalf("Execute result = (%v, %d), want (false, 1)", value.accepted, value.exitCode)
	}

	// A separate long-running daemon verifies SCM event routing without relying
	// on global network-monitor state.
	requests = make(chan svc.ChangeRequest)
	started = make(chan struct{})
	resumeCalls = make(chan struct{}, 1)
	sessionCalls = make(chan struct{}, 1)
	service.runDaemon = func(ctx context.Context, _ string, _ []string, _ Environment, ready func()) error {
		signalWindowsServiceReady(ready)
		close(started)
		<-ctx.Done()
		return nil
	}
	service.notifyConsoleSessionChange = func() { sessionCalls <- struct{}{} }
	service.notifyPowerResume = func() { resumeCalls <- struct{}{} }
	statuses, result = runWindowsServiceForTest(t, service, requests)
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Running {
		t.Fatalf("ready service state = %v, want Running", status.State)
	}
	<-started
	requests <- svc.ChangeRequest{Cmd: svc.SessionChange}
	select {
	case <-sessionCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("session-change control was not forwarded")
	}
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: 0x0004}
	select {
	case <-resumeCalls:
		t.Fatal("suspend event was incorrectly forwarded as resume")
	case <-time.After(50 * time.Millisecond):
	}
	requests <- svc.ChangeRequest{Cmd: svc.PowerEvent, EventType: 0x0007}
	select {
	case <-resumeCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("resume control was not forwarded")
	}
	requests <- svc.ChangeRequest{Cmd: svc.Stop}
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.StopPending {
		t.Fatalf("stop state = %v, want StopPending", status.State)
	}
	if status := receiveWindowsServiceStatus(t, statuses); status.State != svc.Stopped {
		t.Fatalf("final service state = %v, want Stopped", status.State)
	}
	if value := receiveWindowsServiceResult(t, result); value.accepted || value.exitCode != 0 {
		t.Fatalf("Execute result = (%v, %d), want (false, 0)", value.accepted, value.exitCode)
	}
}

func TestWindowsPowerResumeEvent(t *testing.T) {
	for _, test := range []struct {
		event uint32
		want  bool
	}{
		{event: 0x0004},             // suspend
		{event: 0x0006, want: true}, // critical resume
		{event: 0x0007, want: true}, // resume from suspend
		{event: 0x0008, want: true}, // resume from standby
		{event: 0x0012, want: true}, // automatic resume
		{event: 0x0018},             // power setting change
	} {
		if got := windowsPowerResumeEvent(test.event); got != test.want {
			t.Errorf("windowsPowerResumeEvent(%#x) = %v, want %v", test.event, got, test.want)
		}
	}
}
