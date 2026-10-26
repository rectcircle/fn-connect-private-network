package main

import (
	"fyne.io/fyne/v2/test"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"testing"
)

func TestLoginReturnRestoresMenuSource(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		state                                                    model.ClientState
		fromMenu, wasEditing, wasExpanded, wantEditing, wantMenu bool
	}{
		{"menu from connection", model.ClientPaused, true, false, true, false, true},
		{"menu from root login", model.ClientUnconfigured, true, true, true, true, true},
		{"rail from connection", model.ClientPaused, true, false, false, false, false},
		{"rail from root login", model.ClientUnconfigured, true, true, false, true, false},
		{"ordinary return", model.ClientPaused, false, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := test.NewApp()
			defer a.Quit()
			v := newClientView(nil)
			defer v.close()
			v.window = a.NewWindow("test")
			v.window.SetContent(v.content)
			v.prepareMenu()
			drawer := v.drawer
			v.current.State = tc.state
			v.loginFromMenu = tc.fromMenu
			v.loginReturnEditing = tc.wasEditing
			v.loginReturnMenu = tc.wasExpanded
			v.showLogin(true)
			v.returnFromLogin()
			if v.editing != tc.wantEditing || v.drawer.visible != tc.wantMenu {
				t.Fatalf("editing=%v menu=%v", v.editing, v.drawer.visible)
			}
			if v.drawer != drawer {
				t.Fatal("return rebuilt the menu")
			}
			if v.loginFromMenu || v.loginReturnEditing || v.loginReturnMenu {
				t.Fatal("return retained stale login origin")
			}
		})
	}
}
