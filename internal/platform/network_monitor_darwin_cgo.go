//go:build darwin && cgo

package platform

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>

extern void fncpnNetworkChanged(int primaryNetworkChanged);
extern int fncpnNetworkMonitorStopped(void);

static void fncpnNetworkCallback(
	SCDynamicStoreRef store,
	CFArrayRef changedKeys,
	void *info
) {
	(void)store;
	(void)info;
	int primaryNetworkChanged = 0;
	CFIndex count = CFArrayGetCount(changedKeys);
	for (CFIndex index = 0; index < count; index++) {
		CFStringRef key = (CFStringRef)CFArrayGetValueAtIndex(changedKeys, index);
		if (CFStringHasPrefix(key, CFSTR("State:/Network/Global/"))) {
			primaryNetworkChanged = 1;
			break;
		}
	}
	fncpnNetworkChanged(primaryNetworkChanged);
}

static int fncpnRunNetworkMonitor(void) {
	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
	SCDynamicStoreRef store = SCDynamicStoreCreate(
		NULL,
		CFSTR("com.rectcircle.fncpn.network-monitor"),
		fncpnNetworkCallback,
		&context
	);
	if (store == NULL) {
		return -1;
	}
	const void *patterns[] = {
		CFSTR("State:/Network/Global/IPv4"),
		CFSTR("State:/Network/Global/IPv6"),
		CFSTR("State:/Network/Interface/.*")
	};
	CFArrayRef patternArray = CFArrayCreate(
		NULL,
		patterns,
		3,
		&kCFTypeArrayCallBacks
	);
	if (patternArray == NULL ||
		!SCDynamicStoreSetNotificationKeys(store, NULL, patternArray)) {
		if (patternArray != NULL) CFRelease(patternArray);
		CFRelease(store);
		return -1;
	}
	CFRunLoopSourceRef source = SCDynamicStoreCreateRunLoopSource(NULL, store, 0);
	if (source == NULL) {
		CFRelease(patternArray);
		CFRelease(store);
		return -1;
	}
	CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
	while (!fncpnNetworkMonitorStopped()) {
		CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.5, true);
	}
	CFRunLoopRemoveSource(CFRunLoopGetCurrent(), source, kCFRunLoopDefaultMode);
	CFRelease(source);
	CFRelease(patternArray);
	CFRelease(store);
	return 0;
}
*/
import "C"

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

type systemNetworkMonitor struct {
	notifications         chan struct{}
	events                chan model.NetworkChange
	started               sync.Once
	stopped               atomic.Bool
	primaryNetworkChanged atomic.Bool
}

var activeNetworkMonitor atomic.Pointer[systemNetworkMonitor]

func NewNetworkMonitor() *systemNetworkMonitor {
	return &systemNetworkMonitor{
		notifications: make(chan struct{}, 1),
		events:        make(chan model.NetworkChange, 1),
	}
}

func (m *systemNetworkMonitor) Events(ctx context.Context) <-chan model.NetworkChange {
	m.started.Do(func() {
		activeNetworkMonitor.Store(m)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-m.notifications:
					change := model.NetworkChange{
						PrimaryNetworkChanged: m.primaryNetworkChanged.Swap(false),
					}
					select {
					case m.events <- change:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
		go func() {
			go func() {
				<-ctx.Done()
				m.stopped.Store(true)
			}()
			C.fncpnRunNetworkMonitor()
			activeNetworkMonitor.CompareAndSwap(m, nil)
		}()
	})
	return m.events
}

//export fncpnNetworkChanged
func fncpnNetworkChanged(primaryNetworkChanged C.int) {
	monitor := activeNetworkMonitor.Load()
	if monitor == nil {
		return
	}
	if primaryNetworkChanged != 0 {
		monitor.primaryNetworkChanged.Store(true)
	}
	select {
	case monitor.notifications <- struct{}{}:
	default:
	}
}

//export fncpnNetworkMonitorStopped
func fncpnNetworkMonitorStopped() C.int {
	monitor := activeNetworkMonitor.Load()
	if monitor == nil || monitor.stopped.Load() {
		return 1
	}
	return 0
}
