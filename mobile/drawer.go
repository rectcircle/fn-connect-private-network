package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"math"
	"time"
)

type drawerScrim struct {
	widget.BaseWidget
	drawer  *navigationDrawer
	dismiss func()
	shade   *canvas.Rectangle
}

func (s *drawerScrim) CreateRenderer() fyne.WidgetRenderer { return widget.NewSimpleRenderer(s.shade) }
func (s *drawerScrim) Tapped(*fyne.PointEvent)             { s.dismiss() }

type drawerLayout struct{ progress float32 }

func (l *drawerLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 0) }
func (l *drawerLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	objects[1].Resize(fyne.NewSize(drawerWidth(size.Width), size.Height))
	objects[1].Move(fyne.NewPos((l.progress-1)*drawerWidth(size.Width), 0))
}

type navigationDrawer struct {
	canvas            fyne.Canvas
	root              *fyne.Container
	layout            *drawerLayout
	animation         *fyne.Animation
	visible           bool
	generation        uint64
	shade             *canvas.Rectangle
	scroll            *drawerScroll
	dragging          bool
	dragX, dragY      float32
	dragDirection     int
	dragStartProgress float32
	dragLastTime      time.Time
	dragVelocity      float32
}

func newNavigationDrawer(c fyne.Canvas, content fyne.CanvasObject) *navigationDrawer {
	d := &navigationDrawer{canvas: c, layout: &drawerLayout{}, shade: canvas.NewRectangle(color.Transparent)}
	scrim := &drawerScrim{drawer: d, dismiss: func() { d.Hide() }, shade: d.shade}
	scrim.ExtendBaseWidget(scrim)
	panel := container.NewStack(newThemeSurface(theme.ColorNameOverlayBackground), newDrawerScroll(content, d))
	d.root = container.New(d.layout, scrim, panel)
	return d
}
func (d *navigationDrawer) animate(target float32, done func()) {
	if d.animation != nil {
		d.animation.Stop()
	}
	d.generation++
	generation := d.generation
	start := d.layout.progress
	// Settle only the remaining distance, with a fast start and gentle finish.
	duration := time.Duration(max(float32(0.08), float32(0.2)*float32(math.Abs(float64(target-start)))) * float32(time.Second))
	d.animation = fyne.NewAnimation(duration, func(p float32) {
		if generation != d.generation {
			return
		}
		position := start + (target-start)*p
		d.position(position)
		if p >= 1 && done != nil {
			done()
		}
	})
	d.animation.Curve = fyne.AnimationEaseOut
	d.animation.Start()
}
func (d *navigationDrawer) position(position float32) {
	d.layout.progress = max(float32(0), min(float32(1), position))
	position = d.layout.progress
	// Only translate the already laid out panel; never relayout its text per frame.
	panel := d.root.Objects[1]
	panel.Move(fyne.NewPos((position-1)*panel.Size().Width, 0))
	d.shade.FillColor = color.NRGBA{A: uint8(72 * position)}
	d.shade.Refresh()
}

func (d *navigationDrawer) Show() {
	d.attach()
	d.animate(1, nil)
}
func (d *navigationDrawer) attach() {
	if d.visible {
		return
	}
	d.visible = true
	d.canvas.Overlays().Add(d.root)
	// A prepared, detached overlay can retain the startup appearance. Refresh
	// its surfaces and themed children once after attachment, before animation.
	d.root.Refresh()
}
func (d *navigationDrawer) Hide() { d.CloseThen(nil) }
func (d *navigationDrawer) CloseThen(action func()) {
	if !d.visible {
		return
	}
	d.animate(0, func() {
		d.Dispose()
		if action != nil {
			action()
		}
	})
}
func (d *navigationDrawer) Dispose() {
	d.generation++
	if d.animation != nil {
		d.animation.Stop()
	}
	d.visible = false
	d.canvas.Overlays().Remove(d.root)
}

func drawerWidth(width float32) float32 { return min(float32(320), width*0.78) }
