//go:build android

package main

import (
	"fmt"
	"fyne.io/fyne/v2"
	"github.com/rectcircle/fn-connect-private-network/mobile/internal/systemfont"
	"os"
	"path/filepath"
	"sync"
)

var fonts struct {
	sync.Once
	regular, bold fyne.Resource
}

func platformFont(style fyne.TextStyle) fyne.Resource {
	fonts.Do(func() {
		data, err := os.ReadFile("/system/etc/fonts.xml")
		if err != nil {
			return
		}
		loaded := map[string]fyne.Resource{}
		for _, bold := range []bool{false, true} {
			font, err := systemfont.Select(data, bold)
			if err != nil {
				continue
			}
			for _, directory := range []string{"/system/fonts", "/product/fonts", "/system_ext/fonts"} {
				path := filepath.Join(directory, font.File)
				key := fmt.Sprintf("%s#%d", path, font.Index)
				if resource := loaded[key]; resource != nil {
					if bold {
						fonts.bold = resource
					} else {
						fonts.regular = resource
					}
					break
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				single, err := systemfont.Face(raw, font.Index)
				if err != nil {
					continue
				}
				resource := fyne.NewStaticResource(fmt.Sprintf("android-system-%s-%d-%t.ttf", filepath.Base(font.File), font.Index, bold), single)
				loaded[key] = resource
				if bold {
					fonts.bold = resource
				} else {
					fonts.regular = resource
				}
				break
			}
		}
	})
	if style.Bold && fonts.bold != nil {
		return fonts.bold
	}
	return fonts.regular
}
