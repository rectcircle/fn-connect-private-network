package main

import (
	"embed"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"log"
	"os"
	"path/filepath"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()
	app.settingsWindow = uiPlatform() == "linux" && len(os.Args) > 1 && os.Args[1] == "--settings"
	title, width, height, minWidth, minHeight, instanceID := "FnCPN", 494, 668, 494, 390, "cn.rectcircle.fncpn.wails"
	if app.settingsWindow {
		title = "Settings"
		if nativeLanguage() == "zh" {
			title = "设置"
		}
		width = 960
		height = 760
		minWidth = 850
		minHeight = 580
		instanceID += ".settings"
	}
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
		Title: title, Width: width, Height: height, MinWidth: minWidth, MinHeight: minHeight,
		HideWindowOnClose: !app.settingsWindow,
		StartHidden:       !app.settingsWindow && nativeLoginLaunch(),
		AssetServer:       &assetserver.Options{Assets: assets, Middleware: settingsAssets(app.settingsWindow)},
		BackgroundColour:  options.NewRGB(240, 242, 243),
		Menu:              appMenu, OnStartup: app.startup, OnDomReady: app.ready, OnShutdown: app.shutdown,
		Mac: &mac.Options{TitleBar: mac.TitleBarHidden(), OnUrlOpen: app.handleURL},
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: instanceID, OnSecondInstanceLaunch: func(data options.SecondInstanceData) {
			if app.settingsWindow {
				runtime.WindowUnminimise(app.ctx)
				runtime.WindowShow(app.ctx)
				return
			}
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
