//go:build ignore

// Render the shared vector artwork on an opaque iOS home-screen background.
// Run from the mobile module so this uses its existing SVG dependencies.
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

func main() {
	if len(os.Args) != 3 {
		panic("usage: icon.go SOURCE.svg OUTPUT_DIRECTORY")
	}
	source, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	for _, size := range []int{120, 180, 152, 167} {
		icon, err := oksvg.ReadIconStream(bytes.NewReader(source))
		if err != nil {
			panic(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		// Match the shared artwork's light blue. Fill the entire square:
		// SpringBoard supplies its own corner mask, with no transparent gutter.
		draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0xE0, 0xED, 0xFF, 0xFF}), image.Point{}, draw.Src)
		icon.SetTarget(0, 0, float64(size), float64(size))
		icon.Draw(rasterx.NewDasher(size, size, rasterx.NewScannerGV(size, size, img, img.Bounds())), 1)
		f, err := os.Create(filepath.Join(os.Args[2], "AppIcon"+strconv.Itoa(size)+".png"))
		if err != nil {
			panic(err)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			panic(err)
		}
		if err := f.Close(); err != nil {
			panic(err)
		}
	}
}
