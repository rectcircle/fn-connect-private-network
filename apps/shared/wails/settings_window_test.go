package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSettingsEntryPointDoesNotReplaceMainWindow(t *testing.T) {
	for _, tc := range []struct {
		settings   bool
		path, want string
	}{{true, "/", "/settings-window.html"}, {true, "/index.html", "/settings-window.html"}, {true, "/assets/app.js", "/assets/app.js"}, {false, "/", "/"}} {
		request := httptest.NewRequest("GET", tc.path, nil)
		settingsAssets(tc.settings)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			if r.URL.Path != tc.want {
				t.Errorf("got %s, want %s", r.URL.Path, tc.want)
			}
		})).ServeHTTP(httptest.NewRecorder(), request)
		if request.URL.Path != tc.path {
			t.Fatal("middleware mutated original request")
		}
	}
}
func TestSettingsWindowDoesNotOwnDaemonLifecycle(t *testing.T) {
	app := NewApp()
	app.settingsWindow = true
	app.startup(t.Context())
	select {
	case <-app.startupDone:
	default:
		t.Fatal("settings tried to start daemon")
	}
	if app.started {
		t.Fatal("settings must not own daemon")
	}
	app.shutdown(t.Context())
	if app.ctx.Err() == nil {
		t.Fatal("settings shutdown did not cancel active operations")
	}
}
