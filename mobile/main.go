package main

import (
	"embed"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/lang"
	"fyne.io/fyne/v2/theme"

	"github.com/rectcircle/fn-connect-private-network/mobile/internal/uifeedback"
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
	if platform, ok := view.backend.(interface{ SetNavigationHandler(func()) }); ok {
		platform.SetNavigationHandler(func() { fyne.Do(view.navigate) })
	}
	if platform, ok := view.backend.(interface{ SetBackHandler(func()) }); ok {
		platform.SetBackHandler(func() { fyne.Do(view.back) })
	}
	if platform, ok := view.backend.(interface{ SetKeyboardSubmitHandler(func()) }); ok {
		platform.SetKeyboardSubmitHandler(func() {
			fyne.Do(func() {
				if view.ctx.Err() != nil {
					return
				}
				if input, ok := w.Canvas().Focused().(interface{ TypedKey(*fyne.KeyEvent) }); ok {
					input.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
				}
			})
		})
	}
	uifeedback.SetHandler(func(text string) {
		fyne.Do(func() {
			if view.ctx.Err() == nil {
				dialog.ShowInformation(tr("提示"), tr(text), w)
			}
		})
	})
	w.SetContent(view.content)
	w.Resize(fyne.NewSize(420, 700))
	view.prepareMenu()
	w.SetOnClosed(view.close)
	// Start asynchronous UI updates only after Show has finished initializing widgets.
	a.Lifecycle().SetOnStarted(func() {
		fyne.Do(view.refresh)
		time.AfterFunc(120*time.Millisecond, func() { fyne.Do(view.prewarm) })
	})
	a.Lifecycle().SetOnEnteredForeground(func() { fyne.Do(func() { view.refresh(); view.watch() }) })
	a.Lifecycle().SetOnExitedForeground(func() { fyne.Do(view.stopWatch) })
	w.ShowAndRun()
}
