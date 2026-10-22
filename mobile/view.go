package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type clientView struct {
	backend       daemonClient
	ctx           context.Context
	cancel        context.CancelFunc
	content       fyne.CanvasObject
	status        *widget.Label
	message       *widget.Label
	actions       []*widget.Button
	authorization authorizationFlow
	watchCancel   context.CancelFunc
	window        fyne.Window
	details       *widget.Label
	admin         *widget.Button
}

func newClientView(backend daemonClient) *clientView {
	ctx, cancel := context.WithCancel(context.Background())
	v := &clientView{backend: backend, ctx: ctx, cancel: cancel,
		status: widget.NewLabel("正在读取连接状态"), message: widget.NewLabel("")}
	v.status.Wrapping = fyne.TextWrapWord
	v.message.Wrapping = fyne.TextWrapWord
	v.details = widget.NewLabel("")
	v.details.Wrapping = fyne.TextWrapWord
	fnID, username := widget.NewEntry(), widget.NewEntry()
	password, accessCode, otp := widget.NewPasswordEntry(), widget.NewPasswordEntry(), widget.NewEntry()
	trust := widget.NewCheck("信任此设备", nil)
	var challenge *fyne.Container
	var account *widget.Form
	login := widget.NewButton("登录并连接", func() {
		input := client.NativeAuthorization{FNID: fnID.Text, Username: username.Text,
			Password: password.Text, AccessCode: accessCode.Text, TwofaCode: otp.Text, TrustDevice: trust.Checked}
		password.SetText("")
		accessCode.SetText("")
		otp.SetText("")
		v.execute(func(ctx context.Context) error {
			if err := v.authorization.submit(ctx, backend, input); err != nil {
				if client.RequiresAdditionalCredential(err) {
					fyne.Do(func() { account.Hide(); challenge.Show() })
				} else {
					fyne.Do(func() { account.Show(); challenge.Hide() })
				}
				return err
			}
			fyne.Do(func() { account.Show(); challenge.Hide() })
			return v.readStatus(ctx)
		})
	})
	connect := widget.NewButton("连接", func() { v.command(client.MethodConnect) })
	pause := widget.NewButton("暂停", func() { v.command(client.MethodDisconnect) })
	refresh := widget.NewButton("刷新状态", v.refresh)
	back := widget.NewButton("返回账号登录", func() {
		v.execute(func(ctx context.Context) error {
			err := v.authorization.cancel(ctx, backend)
			fyne.Do(func() { account.Show(); challenge.Hide() })
			return err
		})
	})
	retry := widget.NewButton("立即重试", func() { v.command(client.MethodRetry) })
	admin := widget.NewButton("管理后台", v.openAdmin)
	v.admin = admin
	admin.Hide()
	diagnose := widget.NewButton("诊断信息", v.diagnose)
	logout := widget.NewButton("退出登录", func() {
		if v.window != nil {
			dialog.ShowConfirm("退出登录", "断开连接并退出当前账号？", func(ok bool) {
				if ok {
					v.command(client.MethodLogout)
				}
			}, v.window)
		}
	})
	forget := widget.NewButton("重置本机设置", func() {
		if v.window != nil {
			dialog.ShowConfirm("重置本机设置", "删除本机身份和凭据，之后需重新登录。不会删除 NAS 设备记录。", func(ok bool) {
				if ok {
					v.command(client.MethodForget)
				}
			}, v.window)
		}
	})
	v.actions = []*widget.Button{login, connect, pause, refresh, back, retry, admin, diagnose, logout, forget}
	account = widget.NewForm(
		widget.NewFormItem("FN ID", fnID), widget.NewFormItem("用户名", username),
		widget.NewFormItem("密码", password),
	)
	challenge = container.NewVBox(widget.NewLabel("根据提示输入访问码或两步验证码"), widget.NewForm(
		widget.NewFormItem("访问码", accessCode), widget.NewFormItem("验证码", otp)), trust, back)
	challenge.Hide()
	v.content = container.NewVScroll(container.NewVBox(
		widget.NewLabelWithStyle("FnCPN", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		v.status, v.details, container.NewGridWithColumns(3, connect, pause, refresh), v.message,
		container.NewGridWithColumns(2, retry, admin), container.NewGridWithColumns(2, diagnose, logout),
		forget, widget.NewLabel("版本 "+version.Current),
		widget.NewSeparator(), account, challenge, login,
	))
	return v
}

// close cancels UI work only. It never stops the daemon or changes user intent.
func (v *clientView) close() {
	v.cancel()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = v.authorization.cancel(ctx, v.backend)
	}()
}

func (v *clientView) execute(operation func(context.Context) error) {
	for _, button := range v.actions {
		button.Disable()
	}
	v.message.SetText("")
	go func() {
		ctx, cancel := context.WithTimeout(v.ctx, 90*time.Second)
		defer cancel()
		err := operation(ctx)
		if v.ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if v.ctx.Err() != nil {
				return
			}
			for _, button := range v.actions {
				button.Enable()
			}
			if err != nil {
				v.message.SetText(err.Error())
			}
		})
	}()
}

func (v *clientView) command(method string) {
	v.execute(func(ctx context.Context) error {
		if method == client.MethodForget || method == client.MethodLogout {
			_ = v.authorization.cancel(ctx, v.backend)
		}
		if err := v.backend.Call(ctx, method, nil, nil); err != nil {
			return err
		}
		if method == client.MethodForget {
			v.showStatus(model.ClientStatus{State: model.ClientUnconfigured})
			fyne.Do(func() { v.details.SetText(""); v.admin.Hide() })
			return nil
		}
		return v.readStatus(ctx)
	})
}

func (v *clientView) refresh() { v.execute(v.readStatus) }

func (v *clientView) readStatus(ctx context.Context) error {
	var status model.ClientStatus
	if err := v.backend.Call(ctx, client.MethodStatus, nil, &status); err != nil {
		return err
	}
	v.showStatus(status)
	var details model.ClientDiagnostics
	if err := v.backend.Call(ctx, client.MethodDiagnose, nil, &details); err == nil {
		text := fmt.Sprintf("服务端：%s\nNAS：%s\n设备地址：%s", details.FNID, details.NASAddress, details.ClientAddress)
		fyne.Do(func() {
			if v.ctx.Err() != nil {
				return
			}
			v.details.SetText(text)
			if details.Administrator {
				v.admin.Show()
			} else {
				v.admin.Hide()
			}
		})
	}
	return nil
}

func (v *clientView) showStatus(status model.ClientStatus) {
	names := map[model.ClientState]string{model.ClientUnconfigured: "尚未设置", model.ClientPaused: "已暂停", model.ClientLocal: "局域网直达", model.ClientDirect: "IPv6 直连", model.ClientRelay: "FN Connect 中继", model.ClientReconnecting: "正在恢复连接"}
	name := names[status.State]
	if name == "" {
		name = string(status.State)
	}
	text := fmt.Sprintf("状态：%s\n路径：%s\nDNS：%s", name, status.Path, status.DNS.State)
	if status.Interface != "" {
		text += fmt.Sprintf("\n接口：%s · MTU %d", status.Interface, status.MTU)
	}
	if status.LastHandshake != nil {
		text += "\n最近握手：" + status.LastHandshake.Local().Format("15:04:05")
	}
	if status.LastError != nil {
		text += "\n" + status.LastError.Message
	}
	fyne.Do(func() {
		if v.ctx.Err() == nil {
			v.status.SetText(text)
		}
	})
}

func (v *clientView) watch() {
	if v.watchCancel != nil {
		v.watchCancel()
	}
	ctx, cancel := context.WithCancel(v.ctx)
	v.watchCancel = cancel
	go func() {
		var generation uint64
		for {
			var result client.StatusWatchResult
			err := v.backend.Call(ctx, client.MethodWatchStatus, client.StatusWatchRequest{After: generation}, &result)
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				generation = result.Generation
				v.showStatus(result.Status)
				_ = v.readStatus(ctx)
			}
			if err != nil {
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
}
func (v *clientView) stopWatch() {
	if v.watchCancel != nil {
		v.watchCancel()
		v.watchCancel = nil
	}
}
func (v *clientView) openAdmin() {
	v.execute(func(ctx context.Context) error {
		var result struct {
			URL string `json:"url"`
		}
		if err := v.backend.Call(ctx, client.MethodAdminProxy, nil, &result); err != nil {
			return err
		}
		parsed, err := url.Parse(result.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil {
			return fmt.Errorf("无效的管理后台地址")
		}
		if actions, ok := v.backend.(interface{ OpenAdmin(string) error }); ok {
			return actions.OpenAdmin(result.URL)
		}
		return fyne.CurrentApp().OpenURL(parsed)
	})
}
func (v *clientView) diagnose() {
	v.execute(func(ctx context.Context) error {
		var report model.ClientDiagnostics
		if err := v.backend.Call(ctx, client.MethodDiagnose, nil, &report); err != nil {
			return err
		}
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		if actions, ok := v.backend.(interface{ Share(string) error }); ok {
			return actions.Share(string(encoded))
		}
		fyne.Do(func() {
			if v.window == nil {
				return
			}
			text := widget.NewMultiLineEntry()
			text.SetText(string(encoded))
			dialog.ShowCustom("诊断信息（已脱敏）", "关闭", container.NewVScroll(text), v.window)
		})
		return nil
	})
}
