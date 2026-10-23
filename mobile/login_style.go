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
	for i, text := range []string{"无论在哪。", "你的网络，", "就在身旁。"} {
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

type passwordEntry struct {
	widget.Entry
	onFocus func()
}

func newPasswordEntry(onFocus func()) *passwordEntry {
	entry := &passwordEntry{onFocus: onFocus}
	entry.Password = true
	entry.Wrapping = fyne.TextWrap(fyne.TextTruncateClip)
	entry.ExtendBaseWidget(entry)
	return entry
}
func (e *passwordEntry) FocusGained() {
	e.Entry.FocusGained()
	time.AfterFunc(350*time.Millisecond, func() {
		fyne.Do(func() {
			if fyne.CurrentApp().Driver().CanvasForObject(e) != nil && fyne.CurrentApp().Driver().CanvasForObject(e).Focused() == e && e.onFocus != nil {
				e.onFocus()
			}
		})
	})
}

func externalLinkIcon() fyne.Resource {
	return theme.NewThemedResource(fyne.NewStaticResource("open-browser.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path d="M14 3h7v7h-2V6.4l-9.3 9.3-1.4-1.4L17.6 5H14V3zM5 5h6v2H5v12h12v-6h2v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2z"/></svg>`)))
}
