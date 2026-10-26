//go:build android

package androidruntime

import (
	"sync"
)

var keyboardBridge struct {
	sync.Mutex
	handler func()
}

func SetKeyboardSubmitHandler(handler func()) {
	keyboardBridge.Lock()
	keyboardBridge.handler = handler
	keyboardBridge.Unlock()
}
func dispatchKeyboardSubmit() {
	keyboardBridge.Lock()
	handler := keyboardBridge.handler
	keyboardBridge.Unlock()
	if handler != nil {
		handler()
	}
}
func SetKeyboardAction(action int) {
	_ = (system{}).Call("ui.keyboard", map[string]int{"action": action}, nil)
}

// The platform forwards system back events; navigation belongs to the Go UI.
var backBridge struct {
	sync.Mutex
	handler func()
}

func SetBackHandler(handler func()) {
	backBridge.Lock()
	backBridge.handler = handler
	backBridge.Unlock()
}
func dispatchBack() {
	backBridge.Lock()
	handler := backBridge.handler
	backBridge.Unlock()
	if handler != nil {
		handler()
	}
}
