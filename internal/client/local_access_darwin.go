//go:build darwin && cgo

package client

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network
#include <Network/Network.h>
#include <dispatch/dispatch.h>
#include <stdatomic.h>
#include <stdlib.h>

typedef struct {
 nw_connection_t connection;
 dispatch_queue_t queue;
 dispatch_semaphore_t stopped;
 atomic_int state;
} fncpn_access;

static fncpn_access *fncpn_access_start(const char *host, const char *port) {
 fncpn_access *probe = calloc(1, sizeof(*probe));
 if (!probe) return NULL;
 atomic_init(&probe->state, 0);
 probe->queue = dispatch_queue_create("fncpn.local-access", DISPATCH_QUEUE_SERIAL);
 probe->stopped = dispatch_semaphore_create(0);
 nw_endpoint_t endpoint = nw_endpoint_create_host(host, port);
 nw_parameters_t parameters = nw_parameters_create_secure_tcp(NW_PARAMETERS_DISABLE_PROTOCOL, NW_PARAMETERS_DEFAULT_CONFIGURATION);
 probe->connection = nw_connection_create(endpoint, parameters);
 nw_release(endpoint);
 nw_release(parameters);
 nw_connection_set_queue(probe->connection, probe->queue);
 nw_connection_set_state_changed_handler(probe->connection, ^(nw_connection_state_t state, nw_error_t error) {
  if (state == nw_connection_state_cancelled) { dispatch_semaphore_signal(probe->stopped); return; }
  if (state == nw_connection_state_ready) { atomic_store(&probe->state, 1); return; }
  if (state == nw_connection_state_failed) { atomic_store(&probe->state, 3); return; }
  if (state == nw_connection_state_waiting) {
   nw_path_t path = nw_connection_copy_current_path(probe->connection);
   int denied = path && nw_path_get_unsatisfied_reason(path) == nw_path_unsatisfied_reason_local_network_denied;
   if (path) nw_release(path);
   atomic_store(&probe->state, denied ? 2 : 3);
  }
 });
 nw_connection_start(probe->connection);
 return probe;
}
static int fncpn_access_state(fncpn_access *probe) { return atomic_load(&probe->state); }
static void fncpn_access_stop(fncpn_access *probe) {
 nw_connection_cancel(probe->connection);
 dispatch_semaphore_wait(probe->stopped, DISPATCH_TIME_FOREVER);
 dispatch_sync(probe->queue, ^{});
 nw_connection_set_state_changed_handler(probe->connection, NULL);
 nw_release(probe->connection);
 dispatch_release(probe->stopped);
 dispatch_release(probe->queue);
 free(probe);
}
*/
import "C"

import (
	"context"
	"net"
	"time"
	"unsafe"
)

// Observe the daemon's real NAS endpoint. Ready only proves TCP access; LOCAL
// still requires the authenticated HTTP/HMAC probe on the physical interface.
func (SystemLocalProbe) WatchLocalAccess(ctx context.Context, endpoint string) (<-chan localAccessState, func()) {
	events := make(chan localAccessState, 1)
	ctx, cancel := context.WithCancel(ctx)
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil {
		close(events)
		return events, cancel
	}
	chost, cport := C.CString(host), C.CString(port)
	probe := C.fncpn_access_start(chost, cport)
	C.free(unsafe.Pointer(chost))
	C.free(unsafe.Pointer(cport))
	if probe == nil {
		close(events)
		return events, cancel
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		defer C.fncpn_access_stop(probe)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		previous := localAccessState(0)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				state := localAccessState(C.fncpn_access_state(probe))
				if state == 0 || state == previous {
					continue
				}
				previous = state
				select {
				case events <- state:
				case <-ctx.Done():
					return
				}
				if state == localAccessAllowed || state == localAccessUnavailable {
					return
				}
			}
		}
	}()
	return events, func() { cancel(); <-done }
}
