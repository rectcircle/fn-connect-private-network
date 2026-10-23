package main

import (
	"context"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"image/color"
	"net/url"
	"time"
)

func fixedGap(height float32) fyne.CanvasObject {
	r := canvas.NewRectangle(color.Transparent)
	r.SetMinSize(fyne.NewSize(1, height))
	return r
}

func loginField(title string, entry fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(widget.NewLabel(title), entry)
}
func (v *clientView) toast(text string) {
	if platform, ok := v.backend.(interface{ Toast(string) error }); ok {
		go func() { _ = platform.Toast(text) }()
		return
	}
	popup := widget.NewPopUp(widget.NewLabel(text), v.window.Canvas())
	popup.Show()
	time.AfterFunc(2*time.Second, func() { fyne.Do(popup.Hide) })
}
func (v *clientView) promptAdminLogin() {
	if v.window == nil {
		return
	}
	dialog.ShowConfirm("登录管理后台", "管理后台需要具有管理员权限的 fnOS 账号。是否前往登录？", func(yes bool) {
		if yes {
			v.execute(func(ctx context.Context) error {
				if err := v.authorization.cancel(ctx, v.backend); err != nil {
					return err
				}
				fyne.Do(func() { v.showLogin(false) })
				return nil
			})
		}
	}, v.window)
}

func (v *clientView) openNAS() {
	address := v.report.NASAddress
	v.openExternal(func(ctx context.Context) error {
		target, err := resolveNASWebURL(ctx, address)
		if err != nil {
			return err
		}
		if platform, ok := v.backend.(interface{ OpenNAS(string) error }); ok {
			return platform.OpenNAS(target)
		}
		parsed, err := url.Parse(target)
		if err != nil {
			return err
		}
		return fyne.CurrentApp().OpenURL(parsed)
	})
}

// External navigation does not change connection state. Leave page controls and
// diagnostics untouched while resolving an address or launching another app.
func (v *clientView) openExternal(operation func(context.Context) error) {
	go func() {
		ctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
		defer cancel()
		if err := operation(ctx); err != nil && v.ctx.Err() == nil {
			fyne.Do(func() {
				if v.ctx.Err() == nil {
					v.toast(err.Error())
				}
			})
		}
	}()
}
