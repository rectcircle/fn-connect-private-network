//go:build ios

package iosui

/*
void fncpn_ui_install_back_gesture(void);
void fncpn_ui_enable_back_gesture(int enabled);
void fncpn_set_keyboard_action(int action);
int fncpn_ui_duo(void);
void fncpn_ui_navigation_state(int back, int enabled);
void fncpn_ui_rail_menu(const char *json);
void fncpn_ui_rail_admin(int enabled);
void fncpn_ui_rail_expanded(int expanded);
#include <stdlib.h>
*/
import "C"
import (
	"bytes"
	"encoding/json"
	"fyne.io/fyne/v2"
	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
	"image"
	"image/png"
	"sync"
	"unsafe"
)

func SetKeyboardAction(action int) { C.fncpn_set_keyboard_action(C.int(action)) }

var backGesture struct {
	sync.Mutex
	handler func()
}

func SetBackHandler(handler func()) {
	backGesture.Lock()
	backGesture.handler = handler
	backGesture.Unlock()
	C.fncpn_ui_install_back_gesture()
}

func SetBackGestureEnabled(enabled bool) {
	var flag C.int
	if enabled {
		flag = 1
	}
	C.fncpn_ui_enable_back_gesture(flag)
}

//export fncpn_ui_back_gesture
func fncpn_ui_back_gesture() {
	backGesture.Lock()
	handler := backGesture.handler
	backGesture.Unlock()
	if handler != nil {
		handler()
	}
}

func NativeNavigation() bool { return C.fncpn_ui_duo() != 0 }

var navigationHandler struct {
	sync.Mutex
	handler func()
}

func SetNavigationHandler(handler func()) {
	navigationHandler.Lock()
	navigationHandler.handler = handler
	navigationHandler.Unlock()
}
func SetNavigationState(back, enabled bool) {
	var b, e C.int
	if back {
		b = 1
	}
	if enabled {
		e = 1
	}
	C.fncpn_ui_navigation_state(b, e)
}

//export fncpn_ui_navigation_tap
func fncpn_ui_navigation_tap() {
	navigationHandler.Lock()
	handler := navigationHandler.handler
	navigationHandler.Unlock()
	if handler != nil {
		handler()
	}
}

var railHandler struct {
	sync.Mutex
	handler func(int)
}

func SetRailMenu(labels []string, icons []fyne.Resource, handler func(int)) {
	railHandler.Lock()
	railHandler.handler = handler
	railHandler.Unlock()
	images := make([][]byte, len(icons))
	for i, res := range icons {
		svg, err := oksvg.ReadIconStream(bytes.NewReader(res.Content()))
		if err != nil {
			continue
		}
		img := image.NewRGBA(image.Rect(0, 0, 72, 72))
		svg.SetTarget(0, 0, 72, 72)
		svg.Draw(rasterx.NewDasher(72, 72, rasterx.NewScannerGV(72, 72, img, img.Bounds())), 1)
		var out bytes.Buffer
		_ = png.Encode(&out, img)
		images[i] = out.Bytes()
	}
	data, _ := json.Marshal(struct {
		Labels []string `json:"labels"`
		Icons  [][]byte `json:"icons"`
	}{labels, images})
	value := C.CString(string(data))
	defer C.free(unsafe.Pointer(value))
	C.fncpn_ui_rail_menu(value)
}
func SetRailAdmin(enabled bool) {
	var flag C.int
	if enabled {
		flag = 1
	}
	C.fncpn_ui_rail_admin(flag)
}

//export fncpn_ui_rail_tap
func fncpn_ui_rail_tap(index C.int) {
	railHandler.Lock()
	handler := railHandler.handler
	railHandler.Unlock()
	if handler != nil {
		handler(int(index))
	}
}

func SetRailExpanded(expanded bool) {
	var flag C.int
	if expanded {
		flag = 1
	}
	C.fncpn_ui_rail_expanded(flag)
}
