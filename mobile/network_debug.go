package main

import (
	"context"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"time"
)

func (v *clientView) networkDebug() {
	platform, ok := v.backend.(interface {
		NetworkProbe(context.Context, string) (string, error)
	})
	if !ok || v.window == nil {
		return
	}
	entry := widget.NewEntry()
	entry.SetPlaceHolder(tr("http://私网IP:端口/ 或 dns:完整私网域名"))
	if v.report.NASAddress != "" {
		entry.SetText("http://" + v.report.NASAddress + "/")
	}
	dialog.ShowForm(tr("应用内网络调试"), tr("请求"), tr("取消"), []*widget.FormItem{widget.NewFormItem(tr("目标"), entry)}, func(confirmed bool) {
		if !confirmed {
			return
		}
		target := entry.Text
		go func() {
			ctx, cancel := context.WithTimeout(v.ctx, 10*time.Second)
			defer cancel()
			report, err := platform.NetworkProbe(ctx, target)
			if err != nil {
				report = err.Error()
			}
			if v.ctx.Err() != nil {
				return
			}
			fyne.Do(func() {
				body := widget.NewLabel(report)
				body.Wrapping = fyne.TextWrapWord
				d := dialog.NewCustom(tr("应用内网络结果"), tr("关闭"), container.NewVScroll(body), v.window)
				d.Resize(fyne.NewSize(420, 300))
				d.Show()
			})
		}()
	}, v.window)
}
