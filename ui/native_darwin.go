//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -mmacosx-version-min=13.0 -framework Cocoa -framework WebKit -framework ServiceManagement -framework UserNotifications
#include <stdlib.h>
void fn_init(void);
void fn_notify_root(void);
void fn_dispose(void);
void fn_present_main(void);
void fn_tray(char*,char*,char*,int,int);
void fn_admin(char*);
int fn_autostart(void);
int fn_login_launch(void);
char* fn_set_autostart(int);
char* fn_language(void);
char* fn_symbols(void);
*/
import "C"
import "unsafe"
import "encoding/json"

//export fnGoAction
func fnGoAction(value *C.char) { action := C.GoString(value); go nativeAction(action) }
func nativeInit()              { C.fn_init() }
func nativeDispose()           { C.fn_dispose() }
func nativeTray(state, title, action string, needsLogin, busy bool) {
	s, t, a := C.CString(state), C.CString(title), C.CString(action)
	defer C.free(unsafe.Pointer(s))
	defer C.free(unsafe.Pointer(t))
	defer C.free(unsafe.Pointer(a))
	var n, b C.int
	if needsLogin {
		n = 1
	}
	if busy {
		b = 1
	}
	C.fn_tray(s, t, a, n, b)
}
func nativeAdmin(address string) {
	v := C.CString(address)
	defer C.free(unsafe.Pointer(v))
	C.fn_admin(v)
}
func nativeAutoStart() bool { return C.fn_autostart() != 0 }
func nativeSetAutoStart(enabled bool) string {
	var e C.int
	if enabled {
		e = 1
	}
	value := C.fn_set_autostart(e)
	defer C.free(unsafe.Pointer(value))
	return C.GoString(value)
}
func nativeLanguage() string {
	v := C.fn_language()
	defer C.free(unsafe.Pointer(v))
	return C.GoString(v)
}

func nativeNotifyRoot() { C.fn_notify_root() }

func nativeLoginLaunch() bool { return C.fn_login_launch() != 0 }

func nativeSymbols() map[string]string {
	value := C.fn_symbols()
	defer C.free(unsafe.Pointer(value))
	var symbols map[string]string
	_ = json.Unmarshal([]byte(C.GoString(value)), &symbols)
	return symbols
}

func nativePresentMain() { C.fn_present_main() }
