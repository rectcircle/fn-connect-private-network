package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"image/color"
	"time"
)

// Scope invisible stock scrollbars to this scroll only. The overlay thumb is
// non-interactive and appears while the content moves.
type quietScrollTheme struct{ fyne.Theme }

// The fixed app bar already owns the page divider. Scroll edge shadows would
// draw a second, detached line inside the viewport (including at the bottom).
func (t quietScrollTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameShadow {
		return color.Transparent
	}
	return t.Theme.Color(n, v)
}

func (t quietScrollTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameScrollBar || n == theme.SizeNameScrollBarSmall {
		return 0
	}
	return t.Theme.Size(n)
}

type scrollThumbLayout struct {
	scroll *container.Scroll
	thumb  *canvas.Rectangle
}

func (l *scrollThumbLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.scroll.MinSize() }
func (l *scrollThumbLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	l.updateThumb(size)
}
func (l *scrollThumbLayout) updateThumb(size fyne.Size) {
	height := l.scroll.Content.MinSize().Height
	if height <= size.Height {
		l.thumb.Hide()
		return
	}
	h := max(float32(24), size.Height*size.Height/height)
	y := (size.Height - h) * l.scroll.Offset.Y / (height - size.Height)
	l.thumb.Resize(fyne.NewSize(3, h))
	l.thumb.Move(fyne.NewPos(size.Width-4, y))
}
func newQuietScroll(content fyne.CanvasObject) *fyne.Container {
	scroll := container.NewVScroll(content)
	return quietScrollView(scroll, scroll)
}
func quietScrollView(scroll *container.Scroll, view fyne.CanvasObject) *fyne.Container {
	thumb := canvas.NewRectangle(color.NRGBA{R: 120, G: 128, B: 140, A: 140})
	thumb.CornerRadius = 1.5
	thumb.Hide()
	scope := container.NewThemeOverride(view, quietScrollTheme{theme.Current()})
	l := &scrollThumbLayout{scroll: scroll, thumb: thumb}
	root := container.New(l, scope, thumb)
	var lastScroll time.Time
	timer := time.AfterFunc(time.Hour, func() {
		fyne.Do(func() {
			if time.Since(lastScroll) >= 700*time.Millisecond {
				thumb.Hide()
			}
		})
	})
	timer.Stop()
	scroll.OnScrolled = func(fyne.Position) {
		lastScroll = time.Now()
		thumb.Show()
		l.updateThumb(root.Size())
		timer.Reset(700 * time.Millisecond)
	}

	return root
}
