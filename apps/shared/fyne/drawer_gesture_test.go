package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"testing"
	"time"
)

func TestDrawerDragTracksFingerAndClamps(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("gesture")
	w.Resize(fyne.NewSize(400, 700))
	d := newNavigationDrawer(w.Canvas(), widget.NewLabel("menu"))
	defer d.Dispose()
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: 100}})
	if !d.visible || d.layout.progress <= 0 || d.layout.progress >= 1 {
		t.Fatal("edge drag must partially reveal drawer", d.layout.progress)
	}
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: 1000}})
	if d.layout.progress != 1 {
		t.Fatal("opening must clamp at full width")
	}
	d.endDrag()
	d.animation.Stop()
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: -1000}})
	if d.layout.progress != 0 {
		t.Fatal("closing must track left drag")
	}
	d.endDrag()
	d.animation.Stop()
}
func TestDrawerVerticalDragKeepsMenuOpen(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("gesture")
	w.Resize(fyne.NewSize(400, 700))
	d := newNavigationDrawer(w.Canvas(), widget.NewLabel("menu"))
	defer d.Dispose()
	d.attach()
	d.position(1)
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: -2, DY: 40}})
	if d.dragDirection != 2 || d.layout.progress != 1 {
		t.Fatal("vertical scroll must not close drawer")
	}
	d.endDrag()
	if !d.visible {
		t.Fatal("vertical scroll dismissed menu")
	}
}

func TestDrawerReleaseAcceptsShortSwipeAndFlick(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		start, progress, velocity float32
		age                       time.Duration
		want                      bool
	}{
		{"short opening swipe", 0, 0.2, 0, 200 * time.Millisecond, true},
		{"small accidental movement", 0, 0.05, 0, 200 * time.Millisecond, false},
		{"short closing swipe", 1, 0.8, 0, 200 * time.Millisecond, false},
		{"opening flick", 0, 0.1, 700, 10 * time.Millisecond, true},
		{"closing flick", 1, 0.9, -700, 10 * time.Millisecond, false},
		{"paused flick loses momentum", 0, 0.1, 700, 200 * time.Millisecond, false},
		{"reversed before release", 0, 0.6, -700, 10 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := drawerSettleOpen(tc.start, tc.progress, 320, tc.velocity, tc.age); got != tc.want {
				t.Fatalf("settle open = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRightDrawerOpensLeftAndClosesRight(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("Duo")
	w.Resize(fyne.NewSize(400, 700))
	d := newNavigationDrawer(w.Canvas(), widget.NewLabel("menu"))
	d.layout.right = true
	defer d.Dispose()
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: -100}})
	if !d.visible || d.layout.progress <= 0 {
		t.Fatal("left swipe must open right drawer")
	}
	if x := d.root.Objects[1].Position().X; x < 0 {
		t.Fatal("right drawer moved to left edge", x)
	}
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: -1000}})
	if d.layout.progress != 1 {
		t.Fatal("left swipe must fully open")
	}
	d.dragging = false
	d.drag(&fyne.DragEvent{Dragged: fyne.Delta{DX: 1000}})
	if d.layout.progress != 0 {
		t.Fatal("right swipe must close")
	}
}

func TestExternalDrawerSharesVisibilityAndRunsActions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	w := app.NewWindow("Duo")
	d := newNavigationDrawer(w.Canvas(), widget.NewLabel("menu"))
	var expanded bool
	d.externalVisibility = func(value bool) { expanded = value }
	d.Show()
	if !d.visible || !expanded {
		t.Fatal("native menu did not open")
	}
	if len(w.Canvas().Overlays().List()) != 0 {
		t.Fatal("native menu must not add a second Fyne panel")
	}
	ran := false
	d.CloseThen(func() { ran = true })
	if d.visible || expanded || !ran {
		t.Fatal("native menu must close and run selected action")
	}
	d.Show()
	d.Dispose()
	if expanded || d.visible {
		t.Fatal("dispose must hide native panel")
	}
}
