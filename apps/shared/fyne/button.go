package main

import (
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/mobile"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/uiperf"
)

// Keep Fyne's button layout, accessibility and keyboard handling. Mobile touch
// adds immediate feedback instead of waiting for the tap to finish.
type touchButton struct {
	deferUnfocus bool
	traceMenu    bool
	widget.Button
	shade   *canvas.Rectangle
	fade    *fyne.Animation
	pressed bool
}

func newTouchButton(text string, tapped func()) *touchButton {
	return newTouchButtonWithIcon(text, nil, tapped)
}
func newTouchButtonWithIcon(text string, icon fyne.Resource, tapped func()) *touchButton {
	b := &touchButton{}
	b.Text = text
	b.Icon = icon
	b.OnTapped = tapped
	b.ExtendBaseWidget(b)
	return b
}
func (b *touchButton) CreateRenderer() fyne.WidgetRenderer {
	base := b.Button.CreateRenderer()
	b.shade = canvas.NewRectangle(color.Transparent)
	return &touchButtonRenderer{WidgetRenderer: base, button: b, objects: append(append([]fyne.CanvasObject{}, base.Objects()...), b.shade)}
}
func (b *touchButton) feedback(alpha uint8) {
	if b.shade == nil {
		return
	}
	ink := color.NRGBAModel.Convert(b.Theme().Color(theme.ColorNamePressed, fyne.CurrentApp().Settings().ThemeVariant())).(color.NRGBA)
	ink.A = alpha
	b.shade.FillColor = ink
	b.shade.CornerRadius = b.Theme().Size(theme.SizeNameButtonRadius)
	b.shade.Refresh()
}
func (b *touchButton) stopFade() {
	if b.fade != nil {
		b.fade.Stop()
		b.fade = nil
	}
}
func (b *touchButton) TouchDown(*mobile.TouchEvent) {
	if b.traceMenu {
		uiperf.Mark("menu.touchdown", 0, 0)
	}
	if b.Disabled() {
		return
	}
	b.pressed = true
	b.stopFade()
	b.feedback(55)
}
func (b *touchButton) TouchUp(*mobile.TouchEvent) {
	if b.pressed {
		b.pressed = false
		b.release()
	}
}
func (b *touchButton) TouchCancel(*mobile.TouchEvent) { b.pressed = false; b.stopFade(); b.feedback(0) }
func (b *touchButton) release() {
	b.stopFade()
	if !fyne.CurrentApp().Settings().ShowAnimations() {
		b.feedback(0)
		return
	}
	b.fade = fyne.NewAnimation(160*time.Millisecond, func(p float32) { b.feedback(uint8(55 * (1 - p))) })
	b.fade.Start()
}

// Keep keyboard geometry stable between touch-down and touch-up. The existing
// Tapped path unfocuses only after the gesture has been accepted as a click.
func (b *touchButton) DeferUnfocusOnTap() bool { return b.deferUnfocus && !b.Disabled() }

func (b *touchButton) Tapped(*fyne.PointEvent) {
	if b.traceMenu {
		uiperf.Mark("menu.tapped", 0, 0)
	}
	if b.Disabled() {
		return
	}
	if c := fyne.CurrentApp().Driver().CanvasForObject(b); c != nil {
		c.Focus(nil)
	}
	b.feedback(55)
	b.release()
	if b.OnTapped != nil {
		b.OnTapped()
	}
}

type touchButtonRenderer struct {
	fyne.WidgetRenderer
	button  *touchButton
	objects []fyne.CanvasObject
}

func (r *touchButtonRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *touchButtonRenderer) Layout(size fyne.Size) {
	r.WidgetRenderer.Layout(size)
	r.button.shade.Resize(size)
}
func (r *touchButtonRenderer) Destroy() { r.button.stopFade(); r.WidgetRenderer.Destroy() }
