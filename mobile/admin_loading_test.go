package main

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
)

// The stock headless driver executes fyne.Do inline on worker goroutines.
// Queue UI work as the real mobile driver does, so the assertion observes a
// completed transition rather than racing with the loading-dialog callback.
type queuedAdminDriver struct {
	fyne.Driver
	jobs chan func()
	done <-chan struct{}
}

func (d *queuedAdminDriver) DoFromGoroutine(fn func(), wait bool) {
	complete := make(chan struct{})
	select {
	case d.jobs <- func() { fn(); close(complete) }:
	case <-d.done:
		return
	}
	if wait {
		select {
		case <-complete:
		case <-d.done:
		}
	}
}

type queuedAdminApp struct {
	fyne.App
	driver fyne.Driver
}

func (a *queuedAdminApp) Driver() fyne.Driver { return a.driver }

type adminExpiredBackend struct{}

func (adminExpiredBackend) Call(context.Context, string, any, any) error {
	return model.NewError(model.ErrorAuthRequired, "expired", false)
}

func TestAdminLoadingPreservesReauthenticationPrompt(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver := &queuedAdminDriver{Driver: a.Driver(), jobs: make(chan func(), 256), done: ctx.Done()}
	fyne.SetCurrentApp(&queuedAdminApp{App: a, driver: driver})
	v := newClientView(adminExpiredBackend{})
	defer v.cancel()
	v.window = a.NewWindow("test")
	v.window.SetContent(v.content)
	v.current.State = model.ClientPaused
	v.report.Administrator = true
	v.openAdmin()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case work := <-driver.jobs:
			work()
			if !v.navigation.Disabled() {
				if len(v.window.Canvas().Overlays().List()) != 1 {
					t.Fatal("reauthentication dialog disappeared with loading overlay")
				}
				return
			}
		case <-deadline.C:
			t.Fatal("admin request did not complete")
		}
	}
}
