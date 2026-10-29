package main

import "fyne.io/fyne/v2/lang"

// Resolve visible text through the same catalog as Fyne's standard controls.
// The application is restarted when its preferred language changes.
func tr(text string) string { return lang.L(text) }
