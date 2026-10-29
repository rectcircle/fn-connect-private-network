// Package uifeedback forwards native operation failures to the shared App UI.
package uifeedback

import "sync"

var bridge struct {
	sync.Mutex
	handler func(string)
	pending []string
}

func SetHandler(handler func(string)) {
	bridge.Lock()
	bridge.handler = handler
	pending := bridge.pending
	bridge.pending = nil
	bridge.Unlock()
	for _, text := range pending {
		handler(text)
	}
}
func Publish(text string) {
	bridge.Lock()
	handler := bridge.handler
	if handler == nil {
		if len(bridge.pending) < 8 {
			bridge.pending = append(bridge.pending, text)
		}
	}
	bridge.Unlock()
	if handler != nil {
		handler(text)
	}
}
