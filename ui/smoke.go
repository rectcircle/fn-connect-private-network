package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/rectcircle/fn-connect-private-network/internal/version"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Ready is reached through the real JS -> Go bridge, after frontend render.
// A separate CLI mode uses the same packaged assets and WebKit sandbox, but
// deliberately does not start daemons, invoke polkit, or modify user settings.
func (a *App) finishSmoke() {
	result := map[string]any{"version": version.Current, "commit": version.Revision, "frontendReady": true, "platform": uiPlatform()}
	data, err := json.Marshal(result)
	if err == nil {
		err = os.WriteFile(a.smokeResult, data, 0600)
	}
	if err != nil {
		log.Printf("UI smoke result: %v", err)
	}
	runtime.Quit(a.ctx)
}
