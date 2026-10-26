package main

import "fyne.io/fyne/v2"
import "time"
import "github.com/rectcircle/fn-connect-private-network/mobile/internal/uiperf"

// The mobile driver warms the actual GL text/icon textures, without presenting
// the detached menu. Avoid measuring large synthetic strings at startup.
func (v *clientView) prewarm() {
	if v.ctx.Err() != nil || v.drawer == nil {
		return
	}
	if uiperf.Enabled() {
		if observer, ok := v.window.Canvas().(interface {
			SetPerfObserver(func(string, time.Duration, int))
		}); ok {
			observer.SetPerfObserver(uiperf.Mark)
		}
	}
	uiperf.Mark("warm.request", 0, 0)
	if renderer, ok := v.window.Canvas().(interface{ Prewarm(fyne.CanvasObject) }); ok {
		renderer.Prewarm(v.drawer.root.Objects[1])
	}
}
