//go:build darwin || ios

package appletext

import (
	"image"
	"image/color"
	"math"

	"fyne.io/fyne/v2"
)

type ProbeCase struct {
	Text          string  `json:"text"`
	Bold          bool    `json:"bold"`
	Width         float32 `json:"width"`
	Height        float32 `json:"height"`
	Baseline      float32 `json:"baseline"`
	InkPixels     int     `json:"inkPixels"`
	MissingGlyphs int     `json:"missingGlyphs"`
}
type ProbeReport struct {
	Passed bool        `json:"passed"`
	Cases  []ProbeCase `json:"cases"`
}

// Probe verifies actual system-font pixels, not exported tables or cmap alone.
func Probe() ProbeReport {
	report := ProbeReport{Passed: true}
	r := Renderer{}
	for _, bold := range []bool{false, true} {
		for _, text := range []string{"FnCPN 1.1 DNS IPv6", "连接", "用户名", "日志", "特权服务", "𠮷", "密码 ••••"} {
			style := fyne.TextStyle{Bold: bold}
			size, base := r.Measure(text, 24, style)
			img := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(float64(size.Width+4)*2)), int(math.Ceil(float64(size.Height+4)*2))))
			r.Draw(img, text, color.Black, 24, 2, style, 0)
			pixels := 0
			for i := 3; i < len(img.Pix); i += 4 {
				if img.Pix[i] != 0 {
					pixels++
				}
			}
			missing := missingGlyphs(text, 24, style)
			report.Cases = append(report.Cases, ProbeCase{Text: text, Bold: bold, Width: size.Width, Height: size.Height, Baseline: base, InkPixels: pixels, MissingGlyphs: missing})
			if size.Width <= 0 || size.Height <= 0 || base <= 0 || base > size.Height || pixels == 0 || missing != 0 {
				report.Passed = false
			}
		}
	}
	return report
}
