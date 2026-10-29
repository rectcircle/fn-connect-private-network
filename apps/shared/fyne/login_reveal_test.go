package main

import (
	"context"
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

func TestFinalLoginFieldRevealsSubmitWithoutHidingField(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	field := newLoginEntry(true, nil)
	field.Move(fyne.NewPos(0, 80))
	field.Resize(fyne.NewSize(280, 40))
	button := canvas.NewRectangle(color.White)
	button.Move(fyne.NewPos(0, 145))
	button.Resize(fyne.NewSize(280, 40))
	extent := canvas.NewRectangle(color.Transparent)
	extent.SetMinSize(fyne.NewSize(1, 401))
	extent.Resize(fyne.NewSize(1, 401))
	content := container.NewWithoutLayout(field, button, extent)
	scroll := container.NewVScroll(content)
	w := a.NewWindow("test")
	w.SetPadded(false)
	w.SetContent(scroll)
	w.Resize(fyne.NewSize(300, 200))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	v := &clientView{ctx: ctx, password: field, loginSubmit: button, loginScroll: scroll, window: w}
	scroll.Resize(fyne.NewSize(300, 180))
	v.revealLoginEntry(field)
	driver := a.Driver()
	viewportY := driver.AbsolutePositionForObject(scroll).Y
	fieldY := driver.AbsolutePositionForObject(field).Y - viewportY
	buttonBottom := driver.AbsolutePositionForObject(button).Y - viewportY + button.Size().Height
	if fieldY < 0 || buttonBottom > scroll.Size().Height {
		t.Fatalf("field=%v buttonBottom=%v viewport=%v", fieldY, buttonBottom, scroll.Size())
	}
	// A small viewport must keep the field visible instead of forcing the action.
	scroll.ScrollToTop()
	scroll.Resize(fyne.NewSize(300, 75))
	v.revealLoginEntry(field)
	fieldY = driver.AbsolutePositionForObject(field).Y - driver.AbsolutePositionForObject(scroll).Y
	if fieldY < 0 || fieldY+field.Size().Height > scroll.Size().Height {
		t.Fatalf("field hidden in short viewport: %v", fieldY)
	}
}
