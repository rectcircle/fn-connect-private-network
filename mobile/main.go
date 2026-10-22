package main

import (
	"embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/lang"
)

//go:embed translations/*.json
var translationsFS embed.FS

func main() {
	a := app.NewWithID("com.rectcircle.fncpn")
	w := a.NewWindow("FnCPN")

	if err := lang.AddTranslationsFS(translationsFS, "translations"); err != nil {
		fyne.LogError("failed to load translations", err)
	}

	view := newClientView(platformClient())
	view.window = w
	w.SetContent(view.content)
	w.Resize(fyne.NewSize(420, 700))
	w.SetOnClosed(view.close)
	view.refresh()
	a.Lifecycle().SetOnEnteredForeground(view.watch)
	a.Lifecycle().SetOnExitedForeground(view.stopWatch)
	w.ShowAndRun()
}
