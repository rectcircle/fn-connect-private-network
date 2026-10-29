package main

import (
	"fyne.io/fyne/v2"
	"time"
)

// Two persistent pages form the navigation stack; changing pages never rebuilds
// the connection screen or loses its scroll position.
type pageLayout struct{ progress float32 }

func (l *pageLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(0, 0) }
func (l *pageLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Resize(size)
	}
	l.position(objects, size)
}
func (l *pageLayout) position(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.NewPos(-size.Width*l.progress/4, 0))
	objects[1].Move(fyne.NewPos(size.Width*(1-l.progress), 0))
}
func (v *clientView) transition(login bool) {
	if !v.statusReceived {
		v.settlePage(login)
		return
	}
	if v.pageAnimation != nil {
		v.pageAnimation.Stop()
	}
	v.pageGeneration++
	generation := v.pageGeneration
	target := float32(0)
	if login {
		target = 1
	}
	start := v.pages.progress
	v.mainPanel.Show()
	v.accountPanel.Show()
	v.pageAnimation = fyne.NewAnimation(220*time.Millisecond, func(p float32) {
		if generation != v.pageGeneration || v.ctx.Err() != nil {
			return
		}
		v.pages.progress = start + (target-start)*p
		v.pages.position(v.body.Objects, v.body.Size())
		if p >= 1 {
			v.settlePage(login)
		}
	})
	v.pageAnimation.Start()
}
func (v *clientView) settlePage(login bool) {
	if v.pageAnimation != nil {
		v.pageAnimation.Stop()
	}
	v.pageGeneration++
	if login {
		v.accountPanel.Show()
		v.mainPanel.Hide()
	} else {
		v.mainPanel.Show()
		v.accountPanel.Hide()
	}
	v.pages.progress = 0
	if login {
		v.pages.progress = 1
	}
	v.pages.position(v.body.Objects, v.body.Size())
}
func (v *clientView) back() {
	if !v.statusReceived {
		if v.background != nil {
			go v.background()
		}
		return
	}
	if v.drawer != nil && v.drawer.visible {
		v.drawer.Hide()
		return
	}
	if v.editing {
		if v.requiresLogin() && !v.loginFromMenu {
			if v.canExit() {
				v.quit()
			}
			return
		}
		v.navigate()
		return
	}
	if v.background != nil {
		go v.background()
	}
}

// Restore the page beneath the menu and reopen the same persistent drawer.
// A successful sign-in or an automatic status change never takes this path.
func (v *clientView) returnFromLogin() {
	fromMenu, editing, menu := v.loginFromMenu, v.loginReturnEditing, v.loginReturnMenu
	v.loginFromMenu = false
	v.loginReturnEditing = false
	v.loginReturnMenu = false
	if fromMenu && editing || v.requiresLogin() {
		v.showLogin(false)
	} else {
		v.showMain()
	}
	if menu {
		v.showMenu()
	}
}

func (v *clientView) syncBackGesture() {
	if platform, ok := v.backend.(interface{ SetRailAdmin(bool) }); ok {
		platform.SetRailAdmin(v.admin != nil && !v.admin.Disabled())
	}
	if platform, ok := v.backend.(interface{ SetNavigationState(bool, bool) }); ok {
		platform.SetNavigationState(v.editing && (v.loginFromMenu || !v.requiresLogin()), v.statusReceived && !v.navigation.Disabled())
	}
	if platform, ok := v.backend.(interface{ SetBackGestureEnabled(bool) }); ok {
		platform.SetBackGestureEnabled(v.statusReceived && v.editing && (v.loginFromMenu || !v.requiresLogin()) && !v.navigation.Disabled())
	}
}

// Keep the divider adjacent to both app bar and viewport, without Border's
// default theme padding on either side of the separator.
type appFrameLayout struct{}

func (appFrameLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	h, line, body := objects[0].MinSize(), objects[1].MinSize(), objects[2].MinSize()
	return fyne.NewSize(max(h.Width, body.Width), h.Height+line.Height+body.Height)
}
func (appFrameLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	headerHeight := objects[0].MinSize().Height
	lineHeight := objects[1].MinSize().Height
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(size.Width, headerHeight))
	objects[1].Move(fyne.NewPos(0, headerHeight))
	objects[1].Resize(fyne.NewSize(size.Width, lineHeight))
	objects[2].Move(fyne.NewPos(0, headerHeight+lineHeight))
	objects[2].Resize(fyne.NewSize(size.Width, max(float32(0), size.Height-headerHeight-lineHeight)))
}
