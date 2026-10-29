package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// Resolve colors when refreshed, after the mobile driver has reported the
// system appearance. Never freeze an early startup theme color in a rectangle.
type themeSurface struct {
	widget.BaseWidget
	name fyne.ThemeColorName
}

func newThemeSurface(name fyne.ThemeColorName) *themeSurface {
	s := &themeSurface{name: name}
	s.ExtendBaseWidget(s)
	return s
}
func (s *themeSurface) CreateRenderer() fyne.WidgetRenderer {
	return &surfaceRenderer{rectangle: canvas.NewRectangle(s.Theme().Color(s.name, fyne.CurrentApp().Settings().ThemeVariant())), owner: s}
}

type surfaceRenderer struct {
	rectangle *canvas.Rectangle
	owner     *themeSurface
}

func (r *surfaceRenderer) Layout(s fyne.Size)           { r.rectangle.Resize(s) }
func (r *surfaceRenderer) MinSize() fyne.Size           { return fyne.Size{} }
func (r *surfaceRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.rectangle} }
func (r *surfaceRenderer) Destroy()                     {}
func (r *surfaceRenderer) Refresh() {
	r.rectangle.FillColor = r.owner.Theme().Color(r.owner.name, fyne.CurrentApp().Settings().ThemeVariant())
	r.rectangle.Refresh()
}
