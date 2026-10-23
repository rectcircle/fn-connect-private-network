package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"image/color"
)

// Keep Fyne's accessible metrics and fonts, with a quieter branded palette.
type clientTheme struct{ fyne.Theme }

func (t clientTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	// Headless Fyne drivers may report an unspecified preference (2).
	// Resolve it consistently for both our palette and the delegated colors.
	if variant != theme.VariantDark && variant != theme.VariantLight {
		variant = theme.VariantLight
	}
	dark := variant == theme.VariantDark
	switch name {
	case theme.ColorNameForeground:
		if dark {
			return color.NRGBA{R: 239, G: 241, B: 245, A: 255}
		}
		return color.NRGBA{R: 23, G: 27, B: 33, A: 255}
	case theme.ColorNamePlaceHolder:
		if dark {
			return color.NRGBA{R: 160, G: 168, B: 180, A: 255}
		}
		return color.NRGBA{R: 111, G: 121, B: 133, A: 255}
	case theme.ColorNameSuccess:
		if dark {
			return color.NRGBA{R: 48, G: 209, B: 88, A: 255}
		}
		return color.NRGBA{R: 36, G: 138, B: 61, A: 255}
	case theme.ColorNameDisabled:
		if dark {
			return color.NRGBA{R: 170, G: 170, B: 170, A: 255}
		}
		return color.NRGBA{R: 133, G: 133, B: 133, A: 255}
	case theme.ColorNameForegroundOnPrimary:
		if dark {
			return color.NRGBA{R: 19, G: 23, B: 32, A: 255}
		}
		return color.White
	case theme.ColorNamePrimary, theme.ColorNameHyperlink:
		if dark {
			return color.NRGBA{R: 157, G: 171, B: 255, A: 255}
		}
		return color.NRGBA{R: 0, G: 102, B: 255, A: 255}
	case theme.ColorNameBackground:
		if dark {
			return color.NRGBA{R: 19, G: 23, B: 32, A: 255}
		}
		return color.NRGBA{R: 244, G: 246, B: 250, A: 255}
	case theme.ColorNameInputBackground, theme.ColorNameOverlayBackground:
		if dark {
			return color.NRGBA{R: 31, G: 37, B: 49, A: 255}
		}
		return color.White
	}
	return t.Theme.Color(name, variant)
}

func (t clientTheme) Font(style fyne.TextStyle) fyne.Resource {
	if font := platformFont(style); font != nil {
		return font
	}
	return t.Theme.Font(style)
}

// Low importance is flat, but menu actions retain the normal text contrast.
type menuTheme struct{ fyne.Theme }

func (t menuTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if n == theme.ColorNameDisabledButton {
		return color.Transparent
	}
	if n == theme.ColorNamePlaceHolder {
		return t.Theme.Color(theme.ColorNameForeground, v)
	}
	return t.Theme.Color(n, v)
}
func (t menuTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameInlineIcon {
		return 24
	}
	if n == theme.SizeNamePadding {
		return 24
	}
	if n == theme.SizeNameInnerPadding {
		return 12
	}
	return t.Theme.Size(n)
}
func (t clientTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameText {
		return 16
	}
	return t.Theme.Size(n)
}

type textSizeTheme struct {
	fyne.Theme
	size float32
}

func (t textSizeTheme) Size(n fyne.ThemeSizeName) float32 {
	if n == theme.SizeNameText {
		return t.size
	}
	return t.Theme.Size(n)
}
