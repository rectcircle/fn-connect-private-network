//go:build android

package androidruntime

import "sync"

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
