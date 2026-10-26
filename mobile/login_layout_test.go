package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"image/color"
	"testing"
)

func TestLoginOrientationLayoutPreservesFormAndFitsColumns(t *testing.T) {
	hero := canvas.NewRectangle(color.White)
	hero.SetMinSize(fyne.NewSize(230, 180))
	form := canvas.NewRectangle(color.White)
	form.SetMinSize(fyne.NewSize(280, 400))
	objects := []fyne.CanvasObject{hero, form}
	landscape := false
	l := &loginLayout{landscape: func() bool { return landscape }}
	for _, size := range []fyne.Size{fyne.NewSize(380, 700), fyne.NewSize(800, 380), fyne.NewSize(1100, 700), fyne.NewSize(380, 700)} {
		landscape = size.Width > size.Height
		l.Layout(objects, size)
		if landscape {
			if form.Position().X <= hero.Position().X || form.Position().Y != 0 || form.Position().X+form.Size().Width > size.Width {
				t.Fatal("landscape columns overlap or exceed viewport")
			}
			if l.MinSize(objects).Height != 0 {
				t.Fatal("landscape page must not scroll with the form")
			}
			// A keyboard shrinking the viewport must preserve the full-window layout.
			l.Layout(objects, fyne.NewSize(size.Width, 120))
			if form.Position().X == 0 {
				t.Fatal("keyboard changed orientation layout")
			}
		} else if form.Position().X != 0 || form.Position().Y < hero.Size().Height {
			t.Fatal("portrait must stack form below hero")
		}
	}
}
