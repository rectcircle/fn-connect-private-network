//go:build !android && !ios

package main

import "fyne.io/fyne/v2"

func platformFont(fyne.TextStyle) fyne.Resource { return nil }
