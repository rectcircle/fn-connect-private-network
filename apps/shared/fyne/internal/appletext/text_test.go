//go:build darwin || ios

package appletext

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
)

func TestSystemTextHasRealPixelsAndConsistentMetrics(t *testing.T) {
	report := Probe()
	if !report.Passed {
		t.Fatalf("native probe failed: %+v", report)
	}
	r := Renderer{}
	for _, bold := range []bool{false, true} {
		for _, char := range "连接直连中继断开重新检测隧道地址网络接口最近握手特权服务可用诊断详情无异常复制打开日志刷新用户名密码访问码验证码官网捐赠授权失败版本状态设备配置正在恢复信任此设备：，。·—FnCPN0123456789" {
			style := fyne.TextStyle{Bold: bold}
			size, base := r.Measure(string(char), 24, style)
			img := image.NewRGBA(image.Rect(0, 0, int(size.Width+4)*2, int(size.Height+4)*2))
			r.Draw(img, string(char), color.Black, 24, 2, style, 0)
			ink := false
			for i := 3; i < len(img.Pix); i += 4 {
				ink = ink || img.Pix[i] != 0
			}
			if !ink || base <= 0 || size.Width <= 0 {
				t.Fatalf("blank glyph %q bold=%v", char, bold)
			}
		}
	}
	base, _ := r.Measure("M", 16, fyne.TextStyle{})
	cjk, _ := r.Measure("中文", 16, fyne.TextStyle{})
	empty, _ := r.Measure("", 16, fyne.TextStyle{})
	if base.Height != cjk.Height || base.Height != empty.Height {
		t.Fatalf("entry line heights differ: %v %v %v", base, cjk, empty)
	}
	plain, _ := r.Measure("FN", 16, fyne.TextStyle{})
	spaced, _ := r.Measure("FN ", 16, fyne.TextStyle{})
	tabbed, _ := r.Measure("F\tN", 16, fyne.TextStyle{})
	if spaced.Width <= plain.Width || tabbed.Width <= plain.Width {
		t.Fatal("spaces/tabs do not advance the caret")
	}
}

func TestClippedRetinaTextMatchesFullRaster(t *testing.T) {
	r := Renderer{}
	text := "FnCPN 中文用户名 0123456789 连接日志"
	for _, scale := range []float32{1, 2, 3} {
		size, base := r.Measure(text, 18, fyne.TextStyle{Bold: true})
		height := int(math.Ceil(float64((size.Height + 4) * scale)))
		full := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(float64((size.Width+4)*scale))), height))
		r.Draw(full, text, color.NRGBA{R: 20, G: 80, B: 240, A: 180}, 18, scale, fyne.TextStyle{Bold: true}, 0)
		offset, width := 37, 120
		clip := image.NewRGBA(image.Rect(0, 0, width, height))
		r.Draw(clip, text, color.NRGBA{R: 20, G: 80, B: 240, A: 180}, 18, scale, fyne.TextStyle{Bold: true}, offset)
		for y := 0; y < height; y++ {
			if !bytes.Equal(clip.Pix[y*clip.Stride:y*clip.Stride+width*4], full.Pix[y*full.Stride+offset*4:y*full.Stride+(offset+width)*4]) {
				t.Fatalf("clipping differs scale=%v row=%d", scale, y)
			}
		}
		// A capital has no descender: its bottom must not lie below the measured baseline.
		capital := image.NewRGBA(image.Rect(0, 0, 80, height))
		r.Draw(capital, "F", color.Black, 18, scale, fyne.TextStyle{Bold: true}, 0)
		maxY := -1
		for y := 0; y < height; y++ {
			for x := 0; x < 80; x++ {
				if capital.RGBAAt(x, y).A != 0 {
					maxY = y
				}
			}
		}
		if maxY < 0 || maxY > int(math.Ceil(float64(base*scale)))+1 {
			t.Fatalf("flipped or misplaced text scale=%v bottom=%d baseline=%v", scale, maxY, base*scale)
		}
	}
}

func TestWriteNativeTextPreview(t *testing.T) {
	dir := os.Getenv("FNCPN_TEXT_PREVIEW")
	if dir == "" {
		t.Skip("preview not requested")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	r := Renderer{}
	text := "FnCPN 系统字体 · 已连接 IPv6 · 用户名 密码 𠮷"
	for _, bold := range []bool{false, true} {
		style := fyne.TextStyle{Bold: bold}
		size, _ := r.Measure(text, 24, style)
		img := image.NewRGBA(image.Rect(0, 0, int(size.Width+4)*2, int(size.Height+4)*2))
		r.Draw(img, text, color.NRGBA{R: 0, G: 102, B: 255, A: 255}, 24, 2, style, 0)
		name := "regular.png"
		if bold {
			name = "bold.png"
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, img)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}
