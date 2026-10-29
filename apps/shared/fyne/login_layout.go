package main

import "fyne.io/fyne/v2"

// The full canvas determines orientation, so opening a keyboard does not turn a
// portrait form into a landscape form when the usable viewport becomes short.
type loginLayout struct {
	landscape   func() bool
	changed     func()
	wide        bool
	formContent fyne.CanvasObject
}

func (l *loginLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	hero, form := objects[0].MinSize(), objects[1].MinSize()
	if l.formContent != nil {
		form = l.formContent.MinSize()
	}
	if l.wide {
		return fyne.NewSize(0, 0)
	}
	return fyne.NewSize(0, hero.Height+16+form.Height)
}
func (l *loginLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	wide := l.landscape != nil && l.landscape()
	changed := wide != l.wide
	l.wide = wide
	hero, form := objects[0], objects[1]
	if wide {
		left := min(float32(300), size.Width*0.36)
		hero.Move(fyne.NewPos(0, 0))
		hero.Resize(fyne.NewSize(left, hero.MinSize().Height))
		form.Move(fyne.NewPos(left+24, 0))
		form.Resize(fyne.NewSize(max(float32(0), size.Width-left-24), size.Height))
	} else {
		hero.Move(fyne.NewPos(0, 0))
		hero.Resize(fyne.NewSize(size.Width, hero.MinSize().Height))
		form.Move(fyne.NewPos(0, hero.MinSize().Height+16))
		height := form.MinSize().Height
		if l.formContent != nil {
			height = l.formContent.MinSize().Height
		}
		form.Resize(fyne.NewSize(size.Width, height))
	}
	if changed && l.changed != nil {
		l.changed()
	}
}
