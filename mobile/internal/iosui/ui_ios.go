//go:build ios

package iosui

/*
#include <stdlib.h>
char *fncpn_ui_call(const char *request);
int fncpn_ui_simulator(void);
void fncpn_ui_open(const char *url, int embedded);
void fncpn_ui_share(const char *text);
*/
import "C"
import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"unsafe"

	"github.com/rectcircle/fn-connect-private-network/internal/ipc"
)

func Simulator() bool { return C.fncpn_ui_simulator() != 0 }

func Call(ctx context.Context, method string, params, result any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if method == "watch-client-status" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if Simulator() {
		return simulatorCall(ctx, method, params, result)
	}

	request, err := ipc.NewRequest("ios-ui", method, params)
	if err != nil {
		return err
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data) > ipc.MaxFrameSize {
		return errors.New("message too large")
	}
	input := C.CString(string(data))
	defer C.free(unsafe.Pointer(input))
	output := C.fncpn_ui_call(input)
	if output == nil {
		return errors.New("VPN provider unavailable")
	}
	defer C.free(unsafe.Pointer(output))
	if err = ctx.Err(); err != nil {
		return err
	}
	var response ipc.Response
	if err = json.Unmarshal([]byte(C.GoString(output)), &response); err != nil {
		return err
	}
	if response.Version != ipc.ProtocolVersion || response.ID != request.ID {
		return errors.New("iOS local protocol mismatch")
	}
	if !response.OK {
		if response.Error != nil {
			return response.Error
		}
		return errors.New("VPN request failed")
	}
	if result != nil {
		if err := json.Unmarshal(response.Result, result); err != nil {
			return err
		}
	}
	if method == "status" || method == "watch-client-status" {
		scheduleLogSync()
	}
	return nil
}
func Open(url string, embedded bool) error {
	if Simulator() && embedded {
		if err := simulatorBrowserAllowed(url); err != nil {
			return err
		}
	}
	c := C.CString(url)
	defer C.free(unsafe.Pointer(c))
	var flag C.int
	if embedded {
		flag = 1
	}
	C.fncpn_ui_open(c, flag)
	return nil
}
func Share(text string) error {
	c := C.CString(text)
	defer C.free(unsafe.Pointer(c))
	C.fncpn_ui_share(c)
	return nil
}
