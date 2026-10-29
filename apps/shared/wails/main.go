package main

import (
	"embed"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"log"
	"os"
	"path/filepath"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	if len(os.Args) > 1 && os.Args[1] == "--smoke-test" {
		if uiPlatform() != "linux" || len(os.Args) != 3 || !filepath.IsAbs(os.Args[2]) {
			log.Fatal("usage: fncpn-ui --smoke-test /absolute/result.json (Linux only)")
		}
		app.smokeResult = os.Args[2]
	}
	var appMenu *menu.Menu
	if uiPlatform() == "darwin" {
		appMenu = menu.NewMenu()
		appMenu.Append(menu.AppMenu())
		appMenu.Append(menu.EditMenu())
	}
	err := wails.Run(&options.App{
		Title: "FnCPN", Width: 494, Height: 668, MinWidth: 494, MinHeight: 390,
		HideWindowOnClose: true,
		StartHidden:       nativeLoginLaunch(),
		AssetServer:       &assetserver.Options{Assets: assets},
		BackgroundColour:  options.NewRGB(240, 242, 243),
		Menu:              appMenu, OnStartup: app.startup, OnDomReady: app.ready, OnShutdown: app.shutdown,
		Mac: &mac.Options{TitleBar: mac.TitleBarHidden(), OnUrlOpen: app.handleURL},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "cn.rectcircle.fncpn.wails", OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
			app.showMainWindow()
			for _, arg := range data.Args {
				app.handleURL(arg)
			}
		}},
		Bind: []interface{}{app},
	})
	if err != nil {
		log.Fatal(err)
	}
}
