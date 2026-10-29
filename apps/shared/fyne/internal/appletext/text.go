//go:build darwin || ios

// Package appletext draws system fonts through public Core Text APIs. No font
// files or private system font tables are read or exported.
package appletext

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework CoreText -framework CoreGraphics
#include <stdlib.h>
#include <stdint.h>
int fncpn_text_measure(const char *text, size_t length, double size, int flags, int tabs, double *width, double *height, double *baseline);
int fncpn_text_draw(const char *text, size_t length, double size, int flags, int tabs, double scale, int offset, uint8_t *pixels, int width, int height, int stride, double red, double green, double blue, double alpha);
int fncpn_text_missing(const char *text,size_t length,double size,int flags,int tabs);
*/
import "C"
import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"unsafe"

	"fyne.io/fyne/v2"
)

type Renderer struct{}

func flags(style fyne.TextStyle) C.int {
	var f C.int
	if style.Bold {
		f |= 1
	}
	if style.Italic {
		f |= 2
	}
	if style.Monospace {
		f |= 4
	}
	return f
}
func normalized(text string) string {
	text = strings.ReplaceAll(text, "\r", "")
	return strings.ToValidUTF8(strings.ReplaceAll(text, "\n", "\uFFFD"), "\uFFFD")
}
func (Renderer) Measure(text string, size float32, style fyne.TextStyle) (fyne.Size, float32) {
	if size <= 0 || math.IsNaN(float64(size)) || math.IsInf(float64(size), 0) {
		return fyne.Size{}, 0
	}
	text = normalized(text)
	p := C.CString(text)
	defer C.free(unsafe.Pointer(p))
	var w, h, b C.double
	if C.fncpn_text_measure(p, C.size_t(len(text)), C.double(size), flags(style), C.int(style.TabWidth), &w, &h, &b) == 0 {
		return fyne.Size{}, 0
	}
	return fyne.NewSize(float32(w), float32(h)), float32(b)
}
func (Renderer) Draw(dst draw.Image, text string, ink color.Color, size, scale float32, style fyne.TextStyle, offset int) {
	if size <= 0 || scale <= 0 || dst.Bounds().Empty() {
		return
	}
	// Fyne's GL and software painters already provide premultiplied RGBA.
	img, ok := dst.(*image.RGBA)
	if !ok || img.Stride != img.Bounds().Dx()*4 {
		ok = false
		img = image.NewRGBA(dst.Bounds())
		draw.Draw(img, img.Bounds(), dst, dst.Bounds().Min, draw.Src)
	}
	if len(img.Pix) == 0 {
		return
	}
	if ink == nil {
		ink = color.Black
	}
	text = normalized(text)
	p := C.CString(text)
	defer C.free(unsafe.Pointer(p))
	r, g, b, a := color.NRGBAModel.Convert(ink).(color.NRGBA).RGBA()
	// Core Graphics needs unpremultiplied color components, unlike Color.RGBA.
	red, green, blue := 0., 0., 0.
	if a != 0 {
		red = float64(r) / float64(a)
		green = float64(g) / float64(a)
		blue = float64(b) / float64(a)
	}
	C.fncpn_text_draw(p, C.size_t(len(text)), C.double(size), flags(style), C.int(style.TabWidth), C.double(scale), C.int(offset), (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(img.Bounds().Dx()), C.int(img.Bounds().Dy()), C.int(img.Stride), C.double(red), C.double(green), C.double(blue), C.double(float64(a)/65535))
	if !ok {
		draw.Draw(dst, dst.Bounds(), img, img.Bounds().Min, draw.Src)
	}
}

func missingGlyphs(text string, size float32, style fyne.TextStyle) int {
	text = normalized(text)
	p := C.CString(text)
	defer C.free(unsafe.Pointer(p))
	return int(C.fncpn_text_missing(p, C.size_t(len(text)), C.double(size), flags(style), C.int(style.TabWidth)))
}
