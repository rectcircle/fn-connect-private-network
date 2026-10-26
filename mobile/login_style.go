package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"strings"
	"time"
)

func heroCopy() fyne.CanvasObject {
	lines := []fyne.CanvasObject{}
	for i, text := range []string{tr("无论在哪。"), tr("你的网络，"), tr("就在身旁。")} {
		ink := color.NRGBA{R: 23, G: 47, B: 88, A: 255}
		if i == 2 {
			ink = color.NRGBA{R: 52, G: 125, B: 235, A: 255}
		}
		line := canvas.NewText(text, ink)
		line.TextSize = 32
		line.TextStyle = fyne.TextStyle{Bold: true}
		lines = append(lines, line)
	}
	return container.NewVBox(lines...)
}
func identityText(id, user string) string {
	parts := []string{}
	for _, part := range []string{id, user} {
		if strings.TrimSpace(part) != "" {
			parts = append(parts, strings.TrimSpace(part))
		}
	}
	return strings.Join(parts, " · ")
}

type loginEntry struct {
	widget.Entry
	onFocus    func(fyne.CanvasObject)
	onKeyboard func(int)
	submitKey  bool
}

func newLoginEntry(password bool, onFocus func(fyne.CanvasObject)) *loginEntry {
	entry := &loginEntry{onFocus: onFocus}
	entry.Password = password
	entry.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	entry.ExtendBaseWidget(entry)
	return entry
}
func (e *loginEntry) FocusGained() {
	e.Entry.FocusGained()
	if e.onKeyboard != nil {
		action := 1
		if e.submitKey {
			action = 2
		}
		e.onKeyboard(action)
	}
	time.AfterFunc(350*time.Millisecond, func() {
		fyne.Do(func() {
			if fyne.CurrentApp().Driver().CanvasForObject(e) != nil && fyne.CurrentApp().Driver().CanvasForObject(e).Focused() == e && e.onFocus != nil {
				e.onFocus(e)
			}
		})
	})
}
func (e *loginEntry) FocusLost() {
	e.Entry.FocusLost()
	if e.onKeyboard != nil {
		e.onKeyboard(0)
	}
}

// Run after the keyboard has resized the viewport, and reveal only the focused
// field: scrolling to the bottom can move the first fields above the viewport.
func (v *clientView) revealLoginEntry(entry fyne.CanvasObject) {
	if v.ctx.Err() != nil || v.loginScroll == nil {
		return
	}
	driver := fyne.CurrentApp().Driver()
	y := driver.AbsolutePositionForObject(entry).Y - driver.AbsolutePositionForObject(v.loginScroll).Y
	offset := v.loginScroll.Offset
	bottom := y + entry.Size().Height + 12
	if (entry == v.password || entry == v.otp) && v.loginSubmit != nil {
		buttonBottom := driver.AbsolutePositionForObject(v.loginSubmit).Y - driver.AbsolutePositionForObject(v.loginScroll).Y + v.loginSubmit.Size().Height + 12
		// Reveal the action only if the field and action fit together.
		if buttonBottom-y <= v.loginScroll.Size().Height-24 {
			bottom = max(bottom, buttonBottom)
		}
	}
	if bottom > v.loginScroll.Size().Height {
		offset.Y += bottom - v.loginScroll.Size().Height
	} else if y < 12 {
		offset.Y += y - 12
	}
	v.loginScroll.ScrollToOffset(offset)
}

func externalLinkIcon() fyne.Resource {
	return theme.NewThemedResource(fyne.NewStaticResource("open-browser.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M14 3h7v7h-2V6.4l-9.3 9.3-1.4-1.4L17.6 5H14V3zM5 5h6v2H5v12h12v-6h2v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z"/></svg>`)))
}
