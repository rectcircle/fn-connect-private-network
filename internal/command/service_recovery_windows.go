//go:build windows

package command

import (
	"time"

	"golang.org/x/sys/windows/svc/mgr"
)

// configureWindowsServiceRecovery installs the service's crash/failed-exit
// policy. SCM stores this setting, so repeating it on every service start is
// idempotent and also repairs a missing policy after an MSI repair.
func configureWindowsServiceRecovery() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(windowsClientServiceName)
	if err != nil {
		return err
	}
	defer service.Close()

	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}
	if err := service.SetRecoveryActions(actions, 24*60*60); err != nil {
		return err
	}
	return service.SetRecoveryActionsOnNonCrashFailures(true)
}
