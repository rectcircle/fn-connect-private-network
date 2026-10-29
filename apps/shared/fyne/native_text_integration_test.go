//go:build darwin && fncpn_native_text_test

package main

import (
	"math"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/appletext"
)

// Run using the local iOS Fyne modfile: the public hook only exists in that
// reviewed build overlay, never in the global upstream module cache.
func TestNativeTextEntryCaretSelectionAndWrap(t *testing.T) {
	fyne.SetSystemTextRenderer(appletext.Renderer{})
	defer fyne.SetSystemTextRenderer(nil)
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("native text test")
	e := widget.NewEntry()
	e.SetText("AN中B文")
	w.SetContent(e)
	w.Resize(fyne.NewSize(400, 200))
	w.Show()
	w.Canvas().Focus(e)
	r := appletext.Renderer{}
	size := e.Theme().Size(theme.SizeNameText)
	border := e.Theme().Size(theme.SizeNameInputBorder)
	innerPad := e.Theme().Size(theme.SizeNameInnerPadding)
	for i := 0; i <= len([]rune(e.Text)); i++ {
		e.CursorRow = 0
		e.CursorColumn = i
		want, _ := r.Measure(string([]rune(e.Text)[:i]), size, fyne.TextStyle{})
		if math.Abs(float64(e.CursorPosition().X+border/2-innerPad-want.Width)) > 0.1 {
			t.Fatalf("caret %d: got %v want %v", i, e.CursorPosition(), want)
		}
	}
	e.CursorColumn = 3
	e.TypedRune('测')
	if e.Text != "AN中测B文" {
		t.Fatalf("Chinese insertion changed rune offsets: %q", e.Text)
	}
	e.TypedShortcut(&fyne.ShortcutSelectAll{})
	e.TypedRune('文')
	if e.Text != "文" {
		t.Fatalf("selection replacement failed: %q", e.Text)
	}
	p := widget.NewPasswordEntry()
	p.SetText("AN中B文")
	w.SetContent(p)
	w.Canvas().Focus(p)
	p.CursorColumn = 5
	want, _ := r.Measure(strings.Repeat("•", 5), size, fyne.TextStyle{})
	if math.Abs(float64(p.CursorPosition().X+border/2-innerPad-want.Width)) > 0.1 {
		t.Fatal("password caret does not follow the displayed bullets")
	}
	text := widget.NewRichTextWithText("FnCPN 中文连接日志 wrapping text for native layout.")
	text.Wrapping = fyne.TextWrapWord
	w.SetContent(text)
	text.Resize(fyne.NewSize(600, 200))
	wide := text.MinSize().Height
	text.Resize(fyne.NewSize(110, 200))
	narrow := text.MinSize().Height
	if narrow <= wide {
		t.Fatalf("native font widths did not wrap text: wide=%v narrow=%v", wide, narrow)
	}
}
