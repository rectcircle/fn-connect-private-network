package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"image/color"
	"testing"
)

func TestTouchFeedbackDoesNotTriggerActionUntilTap(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(clientTheme{theme.DefaultTheme()})
	calls := 0
	b := newTouchButton("test", func() { calls++ })
	test.WidgetRenderer(b)
	b.TouchDown(nil)
	_, _, _, alpha := b.shade.FillColor.RGBA()
	if alpha == 0 || calls != 0 {
		t.Fatal("press must highlight without performing action")
	}
	b.TouchCancel(nil)
	_, _, _, alpha = b.shade.FillColor.RGBA()
	if alpha != 0 || calls != 0 {
		t.Fatal("cancel must clear highlight without an action")
	}
	b.Tapped(nil)
	if calls != 1 {
		t.Fatal("tap must perform exactly one action")
	}
	b.stopFade()
	b.feedback(0)
	b.Disable()
	b.TouchDown(nil)
	b.Tapped(nil)
	_, _, _, alpha = b.shade.FillColor.RGBA()
	if calls != 1 || alpha != 0 {
		t.Fatal("disabled button responded to touch")
	}
}
func TestPreparedDrawerRefreshesAfterAppearanceChange(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	palette := &appearanceTheme{Theme: clientTheme{theme.DefaultTheme()}, variant: theme.VariantDark}
	app.Settings().SetTheme(palette)
	v := newClientView(nil)
	defer v.cancel()
	v.window = app.NewWindow("test")
	v.window.SetContent(v.content)
	v.window.Resize(fyne.NewSize(420, 700))
	v.prepareMenu()
	panel := v.drawer.root.Objects[1].(*fyne.Container)
	surface := panel.Objects[0].(*themeSurface)
	renderer := test.WidgetRenderer(surface).(*surfaceRenderer)
	palette.variant = theme.VariantLight
	v.showMenu()
	defer v.drawer.Dispose()
	r, g, b, _ := renderer.rectangle.FillColor.RGBA()
	if r < 0xeeee || g < 0xeeee || b < 0xeeee {
		t.Fatal("cached dark drawer survived attachment", renderer.rectangle.FillColor)
	}
}

type appearanceTheme struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (t *appearanceTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return t.Theme.Color(name, t.variant)
}
