package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"math"
	"time"
)

// Gesture arbitration belongs to the shared Fyne UI, not the Android adapter.
// Lock a direction after a small threshold so vertical menu scrolling remains usable.
func (d *navigationDrawer) drag(e *fyne.DragEvent) {
	if !d.dragging {
		d.dragging = true
		d.dragX = 0
		d.dragY = 0
		d.dragDirection = 0
		d.dragStartProgress = d.layout.progress
		d.dragLastTime = time.Now()
		d.dragVelocity = 0
	}
	now := time.Now()
	if elapsed := now.Sub(d.dragLastTime).Seconds(); elapsed >= 0.002 {
		speed := e.Dragged.DX / float32(elapsed)
		d.dragVelocity = 0.3*d.dragVelocity + 0.7*speed
	}
	d.dragLastTime = now
	d.dragX += e.Dragged.DX
	d.dragY += e.Dragged.DY
	if d.dragDirection == 0 {
		if max(math.Abs(float64(d.dragX)), math.Abs(float64(d.dragY))) < 8 {
			return
		}
		if math.Abs(float64(d.dragX)) > math.Abs(float64(d.dragY))*1.3 {
			d.dragDirection = 1
		} else {
			d.dragDirection = 2
			return
		}
		if d.animation != nil {
			d.animation.Stop()
		}
		d.generation++
		d.attach()
		d.position(d.layout.progress + d.dragX/drawerWidth(d.canvas.Size().Width))
		return
	}
	if d.dragDirection == 1 {
		d.position(d.layout.progress + e.Dragged.DX/drawerWidth(d.canvas.Size().Width))
	}
}
func (d *navigationDrawer) endDrag() {
	if !d.dragging {
		return
	}
	horizontal := d.dragDirection == 1
	d.dragging = false
	if !horizontal {
		return
	}
	if drawerSettleOpen(d.dragStartProgress, d.layout.progress, drawerWidth(d.canvas.Size().Width), d.dragVelocity, time.Since(d.dragLastTime)) {
		d.animate(1, nil)
	} else {
		d.Hide()
	}
}
func (s *drawerScrim) Dragged(e *fyne.DragEvent) { s.drawer.drag(e) }
func (s *drawerScrim) DragEnd()                  { s.drawer.endDrag() }

type drawerScroll struct {
	container.Scroll
	drawer *navigationDrawer
}

func newDrawerScroll(content fyne.CanvasObject, d *navigationDrawer) fyne.CanvasObject {
	s := &drawerScroll{drawer: d}
	d.scroll = s
	s.Content = content
	s.Direction = container.ScrollVerticalOnly
	s.ExtendBaseWidget(s)
	return quietScrollView(&s.Scroll, s)
}
func (s *drawerScroll) Dragged(e *fyne.DragEvent) {
	s.drawer.drag(e)
	if s.drawer.dragDirection == 2 {
		if sc, ok := any(&s.Scroll).(fyne.Draggable); ok {
			sc.Dragged(e)
		}
	}
}
func (s *drawerScroll) DragEnd() {
	s.drawer.endDrag()
	if sc, ok := any(&s.Scroll).(fyne.Draggable); ok {
		sc.DragEnd()
	}
}

type drawerButton struct {
	touchButton
	view *clientView
}

func (b *drawerButton) Dragged(e *fyne.DragEvent) {
	b.TouchCancel(nil)
	if b.view.drawer == nil {
		return
	}
	if s := b.view.drawer.scroll; s != nil {
		s.Dragged(e)
	}
}
func (b *drawerButton) DragEnd() {
	if b.view.drawer != nil {
		b.view.drawer.endDrag()
	}
}

// Leave the outer 32dp to system back. A wider 80dp inner strip is drag-only,
// so taps still reach buttons and vertical gestures can scroll the page.
const drawerGestureInset float32 = 32
const drawerGestureWidth float32 = 80

type drawerEdge struct {
	widget.BaseWidget
	view *clientView
}

func (e *drawerEdge) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}
func (e *drawerEdge) Dragged(event *fyne.DragEvent) {
	v := e.view
	if v.editing || !v.statusReceived {
		return
	}
	v.prepareMenu()
	if v.drawer != nil {
		v.drawer.drag(event)
		if v.drawer.dragDirection == 2 && v.mainScroll != nil {
			if scroll, ok := any(v.mainScroll).(fyne.Draggable); ok {
				scroll.Dragged(event)
			}
		}
	}
}
func (e *drawerEdge) DragEnd() {
	if e.view.drawer != nil {
		e.view.drawer.endDrag()
	}
}

type drawerEdgeLayout struct{}

func (drawerEdgeLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.Size{} }
func (drawerEdgeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Resize(size)
	objects[1].Move(fyne.NewPos(drawerGestureInset, 0))
	objects[1].Resize(fyne.NewSize(min(drawerGestureWidth, max(float32(0), size.Width-drawerGestureInset)), size.Height))
}

// Track the finger 1:1 while dragging. On release, a short intentional swipe
// or a recent flick is enough; do not require dragging through half the panel.
func drawerSettleOpen(start, progress, width, velocity float32, age time.Duration) bool {
	if age <= 100*time.Millisecond && float32(math.Abs(float64(velocity))) >= 450 {
		return velocity > 0
	}
	distance := (progress - start) * width
	if distance >= 48 {
		return true
	}
	if distance <= -48 {
		return false
	}
	return progress >= 0.5
}
