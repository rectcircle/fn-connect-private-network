//go:build windows

package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rectcircle/fn-connect-private-network/internal/platform"
	"golang.org/x/sys/windows/svc"
)

const windowsClientServiceName = "FnCPNClient"

const (
	windowsServiceStartWaitHint       = 30 * time.Second
	windowsServiceStartCheckpointTick = 5 * time.Second
)

func runWindowsService(
	parent context.Context,
	role string,
	arguments []string,
	environment Environment,
) error {
	if role != "client" {
		return errors.New("Windows only supports the FnCPN client service")
	}
	if err := configureWindowsServiceRecovery(); err != nil {
		// Recovery policy is best-effort so a configuration ACL or SCM issue does
		// not prevent the actual network service from starting. Keep it visible
		// for service diagnostics and setup logs.
		_, _ = fmt.Fprintf(environment.Stderr, "fncpn: configure service recovery: %v\n", err)
	}
	return svc.Run(windowsClientServiceName, windowsClientService{
		parent: parent, role: role, arguments: arguments, environment: environment,
	})
}

type windowsClientService struct {
	parent                     context.Context
	role                       string
	arguments                  []string
	environment                Environment
	runDaemon                  func(context.Context, string, []string, Environment, func()) error
	notifyConsoleSessionChange func()
	notifyPowerResume          func()
	startPendingInterval       time.Duration
}

func (s windowsClientService) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending, CheckPoint: 1, WaitHint: uint32(windowsServiceStartWaitHint / time.Millisecond)}
	checkpointInterval := s.startPendingInterval
	if checkpointInterval <= 0 {
		checkpointInterval = windowsServiceStartCheckpointTick
	}
	checkpointTicker := time.NewTicker(checkpointInterval)
	defer checkpointTicker.Stop()
	checkpoint := uint32(1)
	readyReported := false
	ctx, cancel := context.WithCancel(s.parent)
	done := make(chan error, 1)
	runDaemon := s.runDaemon
	if runDaemon == nil {
		runDaemon = runPrivilegedDaemonWithReady
	}
	ready := make(chan struct{}, 1)
	go func() {
		done <- runDaemon(ctx, s.role, s.arguments, s.environment, func() {
			select {
			case ready <- struct{}{}:
			default:
			}
		})
	}()
	for {
		select {
		case <-ready:
			// The privileged daemon reports ready only after recovery succeeds and
			// its protected IPC endpoint is listening.
			if readyReported {
				continue
			}
			readyReported = true
			checkpointTicker.Stop()
			statuses <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange | svc.AcceptPowerEvent}
		case <-checkpointTicker.C:
			if !readyReported {
				checkpoint++
				statuses <- svc.Status{State: svc.StartPending, CheckPoint: checkpoint, WaitHint: uint32(windowsServiceStartWaitHint / time.Millisecond)}
			}
		case err := <-done:
			cancel()
			statuses <- svc.Status{State: svc.Stopped}
			if err != nil {
				return false, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				checkpointTicker.Stop()
				statuses <- svc.Status{State: svc.StopPending, WaitHint: 15000}
				cancel()
				select {
				case err := <-done:
					statuses <- svc.Status{State: svc.Stopped}
					if err != nil {
						return false, 1
					}
					return false, 0
				case <-time.After(15 * time.Second):
					return false, 1
				}
			case svc.SessionChange:
				if s.notifyConsoleSessionChange != nil {
					s.notifyConsoleSessionChange()
				} else {
					platform.NotifyConsoleSessionChange()
				}
			case svc.PowerEvent:
				if windowsPowerResumeEvent(request.EventType) {
					if s.notifyPowerResume != nil {
						s.notifyPowerResume()
					} else {
						platform.NotifyPowerResume()
					}
				}
			}
		}
	}
}

func windowsPowerResumeEvent(eventType uint32) bool {
	// WinUser.h power broadcast events delivered through SERVICE_CONTROL_POWEREVENT.
	switch eventType {
	case 0x0006, // PBT_APMRESUMECRITICAL
		0x0007, // PBT_APMRESUMESUSPEND
		0x0008, // PBT_APMRESUMESTANDBY
		0x0012: // PBT_APMRESUMEAUTOMATIC
		return true
	default:
		return false
	}
}
