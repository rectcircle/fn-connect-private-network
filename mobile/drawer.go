package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/uiperf"
	"image/color"
	"log/slog"
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

type drawerLayout struct {
	progress float32
	right    bool
}

func (l *drawerLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 0) }
func (l *drawerLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	objects[1].Resize(fyne.NewSize(drawerWidth(size.Width), size.Height))
	objects[1].Move(fyne.NewPos(l.panelX(size.Width, drawerWidth(size.Width)), 0))
}

func (l *drawerLayout) panelX(width, panel float32) float32 {
	if l.right {
		return width - l.progress*panel
	}
	return (l.progress - 1) * panel
}

type navigationDrawer struct {
	canvas             fyne.Canvas
	root               *fyne.Container
	layout             *drawerLayout
	animation          *fyne.Animation
	visible            bool
	onVisibility       func(bool)
	externalVisibility func(bool)
	prepared           bool
	preparedVariant    fyne.ThemeVariant
	preparedColor      color.NRGBA
	generation         uint64
	shade              *canvas.Rectangle
	scroll             *drawerScroll
	dragging           bool
	dragX, dragY       float32
	dragDirection      int
	dragStartProgress  float32
	dragLastTime       time.Time
	dragVelocity       float32
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
	traceStart := time.Now()
	firstFrame := true
	// Settle only the remaining distance, with a fast start and gentle finish.
	duration := time.Duration(max(float32(0.08), float32(0.2)*float32(math.Abs(float64(target-start)))) * float32(time.Second))
	d.animation = fyne.NewAnimation(duration, func(p float32) {
		if generation != d.generation {
			return
		}
		if firstFrame && target == 1 {
			uiperf.Mark("menu.animation.first", time.Since(traceStart), 0)
			firstFrame = false
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
	panel.Move(fyne.NewPos(d.layout.panelX(d.root.Size().Width, panel.Size().Width), 0))
	d.shade.FillColor = color.NRGBA{A: uint8(72 * position)}
	d.shade.Refresh()
}

func (d *navigationDrawer) Show() {
	if d.externalVisibility != nil {
		d.visible = true
		d.externalVisibility(true)
		return
	}
	uiperf.Mark("menu.show.begin", 0, 0)
	started := time.Now()
	d.attach()
	d.animate(1, nil)
	slog.Info("mobile menu attached", "elapsed_ms", time.Since(started).Milliseconds())
	uiperf.Mark("menu.show.end", time.Since(started), 0)
}
func (d *navigationDrawer) attach() {
	if d.visible {
		return
	}
	d.visible = true
	if d.onVisibility != nil {
		d.onVisibility(true)
	}
	started := time.Now()
	d.canvas.Overlays().Add(d.root)
	uiperf.Mark("menu.overlay.add", time.Since(started), 0)
	// Keep warmed renderers/text caches between openings. Only a theme change
	// requires refreshing the detached tree before its first frame.
	if !d.prepared || d.preparedVariant != fyne.CurrentApp().Settings().ThemeVariant() || d.preparedColor != drawerSurfaceColor() {
		d.prepare()
	}
}
func (d *navigationDrawer) Hide() { d.CloseThen(nil) }
func (d *navigationDrawer) CloseThen(action func()) {
	if d.externalVisibility != nil {
		d.visible = false
		d.externalVisibility(false)
		if action != nil {
			action()
		}
		return
	}
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
	if d.externalVisibility != nil {
		d.externalVisibility(false)
	}
	d.generation++
	if d.animation != nil {
		d.animation.Stop()
	}
	d.visible = false
	if d.onVisibility != nil {
		d.onVisibility(false)
	}
	d.canvas.Overlays().Remove(d.root)
}

func drawerWidth(width float32) float32 { return min(float32(320), width*0.78) }

// Prepare while the startup placeholder is visible, outside the first tap.
func (d *navigationDrawer) prepare() {
	size := d.canvas.Size()
	d.root.Refresh()
	d.root.Resize(fyne.NewSize(size.Height, size.Width))
	d.root.Objects[1].MinSize()
	d.root.Resize(size)
	d.root.Objects[1].MinSize()
	d.preparedVariant = fyne.CurrentApp().Settings().ThemeVariant()
	d.preparedColor = drawerSurfaceColor()
	d.prepared = true
}

func drawerSurfaceColor() color.NRGBA {
	settings := fyne.CurrentApp().Settings()
	return color.NRGBAModel.Convert(settings.Theme().Color(theme.ColorNameOverlayBackground, settings.ThemeVariant())).(color.NRGBA)
}
