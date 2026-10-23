package main

import (
	"embed"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"
)

//go:embed translations/*.json
var translationsFS embed.FS

//go:embed assets/appicon.svg
var appIcon []byte

func main() {
	a := app.NewWithID("cn.rectcircle.fncpn")
	a.SetIcon(fyne.NewStaticResource("appicon.svg", appIcon))
	a.Settings().SetTheme(clientTheme{theme.DefaultTheme()})
	w := a.NewWindow("FnCPN")
	w.SetPadded(false)

	if err := lang.AddTranslationsFS(translationsFS, "translations"); err != nil {
		fyne.LogError("failed to load translations", err)
	}

	view := newClientView(platformClient())
	view.window = w
	if platform, ok := view.backend.(interface{ SetBackHandler(func()) }); ok {
		platform.SetBackHandler(func() { fyne.Do(view.back) })
	}
	w.SetContent(view.content)
	w.Resize(fyne.NewSize(420, 700))
	view.prepareMenu()
	w.SetOnClosed(view.close)
	view.refresh()
	a.Lifecycle().SetOnEnteredForeground(func() { fyne.Do(func() { go func() { _ = view.readStatus(view.ctx) }(); view.watch() }) })
	a.Lifecycle().SetOnExitedForeground(func() { fyne.Do(view.stopWatch) })
	w.ShowAndRun()
}
