//go:build darwin && cgo

package darwin

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>

extern void fncpnConsoleUserChanged(void);
extern int fncpnConsoleMonitorStopped(void);

static void fncpnConsoleCallback(
	SCDynamicStoreRef store,
	CFArrayRef changedKeys,
	void *info
) {
	(void)store;
	(void)changedKeys;
	(void)info;
	fncpnConsoleUserChanged();
}

static int fncpnRunConsoleMonitor(void) {
	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
	SCDynamicStoreRef store = SCDynamicStoreCreate(
		NULL,
		CFSTR("cn.rectcircle.fncpn.console-monitor"),
		fncpnConsoleCallback,
		&context
	);
	if (store == NULL) return -1;
	const void *keys[] = { CFSTR("State:/Users/ConsoleUser") };
	CFArrayRef keyArray = CFArrayCreate(
		NULL, keys, 1, &kCFTypeArrayCallBacks
	);
	if (keyArray == NULL ||
		!SCDynamicStoreSetNotificationKeys(store, keyArray, NULL)) {
		if (keyArray != NULL) CFRelease(keyArray);
		CFRelease(store);
		return -1;
	}
	CFRunLoopSourceRef source = SCDynamicStoreCreateRunLoopSource(NULL, store, 0);
	if (source == NULL) {
		CFRelease(keyArray);
		CFRelease(store);
		return -1;
	}
	CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
	fncpnConsoleUserChanged();
	while (!fncpnConsoleMonitorStopped()) {
		CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.5, true);
	}
	CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
	CFRelease(source);
	CFRelease(keyArray);
	CFRelease(store);
	return 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

type consoleMonitor struct {
	events  chan consoleUserEvent
	stopped atomic.Bool
}

var activeConsoleMonitor atomic.Pointer[consoleMonitor]

func consoleUserEvents(ctx context.Context) <-chan consoleUserEvent {
	monitor := &consoleMonitor{events: make(chan consoleUserEvent, 1)}
	activeConsoleMonitor.Store(monitor)
	go func() {
		<-ctx.Done()
		monitor.stopped.Store(true)
	}()
	go func() {
		defer activeConsoleMonitor.CompareAndSwap(monitor, nil)
		retryDelay := time.Second
		for {
			result := C.fncpnRunConsoleMonitor()
			if ctx.Err() != nil {
				return
			}
			monitor.notify(consoleUserEvent{
				Err: fmt.Errorf(
					"ConsoleUser change monitor stopped unexpectedly (%d)",
					int(result),
				),
			})
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			if retryDelay < 30*time.Second {
				retryDelay = min(2*retryDelay, 30*time.Second)
			}
		}
	}()
	return monitor.events
}

func (m *consoleMonitor) notify(event consoleUserEvent) {
	select {
	case m.events <- event:
	default:
	}
}

//export fncpnConsoleUserChanged
func fncpnConsoleUserChanged() {
	monitor := activeConsoleMonitor.Load()
	if monitor == nil {
		return
	}
	monitor.notify(consoleUserEvent{})
}

//export fncpnConsoleMonitorStopped
func fncpnConsoleMonitorStopped() C.int {
	monitor := activeConsoleMonitor.Load()
	if monitor == nil || monitor.stopped.Load() {
		return 1
	}
	return 0
}
