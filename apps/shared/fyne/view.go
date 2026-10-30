package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"net/url"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type chromeLayout struct{}

func (chromeLayout) MinSize([]fyne.CanvasObject) fyne.Size { return fyne.NewSize(250, 56) }
func (chromeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(fyne.NewSize(52, 56))
	iconX := float32(52)
	objects[1].Move(fyne.NewPos(iconX, 14))
	objects[1].Resize(fyne.NewSize(28, 28))
	x := iconX + 38
	for _, object := range objects[2:] {
		width := object.MinSize().Width
		object.Move(fyne.NewPos(x, (56-object.MinSize().Height)/2))
		object.Resize(object.MinSize())
		x += width + 4
	}
}

type clientView struct {
	readMu                                    sync.Mutex
	backend                                   daemonClient
	background                                func()
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	content                                   fyne.CanvasObject
	window                                    fyne.Window
	actions                                   []*touchButton
	authorization                             authorizationFlow
	watchCancel                               context.CancelFunc
	status, message, identity, diagnosticBody *widget.Label
	values                                    map[string]*widget.Label
	primary, navigation, admin                *touchButton
	loginSubmit                               fyne.CanvasObject
	accountPanel, mainPanel                   fyne.CanvasObject
	body                                      *fyne.Container
	pages                                     *pageLayout
	pageAnimation                             *fyne.Animation
	pageGeneration                            uint64
	manualLogin                               bool
	loginRequired                             bool
	loginFromMenu                             bool
	loginReturnEditing                        bool
	loginReturnMenu                           bool
	statusReceived                            bool
	loading                                   fyne.CanvasObject
	layoutDirty                               bool
	fnID, username, accessCode, otp           *loginEntry
	password                                  *loginEntry
	loginScroll                               *container.Scroll
	mainScroll                                *container.Scroll
	account                                   *fyne.Container
	challenge                                 *fyne.Container
	trust                                     *widget.Check
	current                                   model.ClientStatus
	report                                    model.ClientDiagnostics
	diagnosticText                            string
	editing                                   bool
	drawer                                    *navigationDrawer
}

func newClientView(backend daemonClient) *clientView {
	ctx, cancel := context.WithCancel(context.Background())
	v := &clientView{backend: backend, ctx: ctx, cancel: cancel, values: map[string]*widget.Label{}}
	if platform, ok := backend.(interface{ Background() error }); ok {
		v.background = func() { _ = platform.Background() }
	}
	v.status = widget.NewLabelWithStyle(tr("尚未连接"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.identity = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	v.identity.Wrapping = fyne.TextWrapWord
	v.message = widget.NewLabel("")
	v.message.Wrapping = fyne.TextWrapWord
	v.message.Importance = widget.WarningImportance
	v.message.Hide()
	v.diagnosticBody = widget.NewLabel(tr("无异常"))
	v.diagnosticBody.Wrapping = fyne.TextWrapWord
	v.primary = newTouchButton(tr("连接"), func() {
		if v.current.LocalNetworkWaiting {
			v.command(client.MethodContinueRemote)
			return
		}
		if connected(v.current.State) {
			v.command(client.MethodDisconnect)
		} else {
			v.command(client.MethodConnect)
		}
	})
	v.primary.Importance = widget.HighImportance
	retry := newTouchButton(tr("重新检测"), func() { v.command(client.MethodRetry) })
	rows := []fyne.CanvasObject{}
	for _, name := range []string{tr("连接路径"), tr("隧道地址"), tr("网络接口"), tr("最近握手"), "DNS", tr("网络服务"), tr("IPv6 检测"), "NAS IP"} {
		label := widget.NewLabel(name)
		label.Importance = widget.LowImportance
		value := widget.NewLabel("—")
		value.Truncation = fyne.TextTruncateEllipsis
		v.values[name] = value
		var field fyne.CanvasObject = value
		if name == "DNS" {
			copyDNS := newTouchButtonWithIcon("", theme.ContentCopyIcon(), func() {
				if v.window != nil {
					v.copyDNSSuffix()
				}
			})
			copyDNS.Importance = widget.LowImportance
			field = container.NewBorder(nil, nil, nil, copyDNS, value)
		}
		if name == "NAS IP" {
			open := newTouchButtonWithIcon("", externalLinkIcon(), v.openNAS)
			open.Importance = widget.LowImportance
			field = container.NewBorder(nil, nil, nil, open, value)
		}
		rows = append(rows, label, field)
	}
	copyReport := newTouchButtonWithIcon(tr("复制"), theme.ContentCopyIcon(), func() {
		if v.window != nil {
			v.window.Clipboard().SetContent(v.diagnosticText)
			v.toast(tr("诊断信息已复制"))
		}
	})
	logs := newTouchButtonWithIcon(tr("打开日志"), theme.FolderOpenIcon(), v.openLogs)
	refresh := newTouchButtonWithIcon(tr("刷新"), theme.ViewRefreshIcon(), v.refresh)
	border := canvas.NewRectangle(color.Transparent)
	border.CornerRadius = 8
	border.StrokeColor = theme.Color(theme.ColorNameInputBorder)
	border.StrokeWidth = 1
	diagnosticScroll := container.NewStack(newThemeSurface(theme.ColorNameInputBackground), container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), container.NewThemeOverride(v.diagnosticBody, textSizeTheme{theme.Current(), 13})), border)

	v.mainScroll = container.NewVScroll(container.New(layout.NewCustomPaddedLayout(12, 12, 16, 16), container.NewVBox(
		v.identity, v.status, container.New(layout.NewFormLayout(), rows...),
		container.NewGridWithColumns(2, v.primary, retry), widget.NewLabel(tr("诊断详情")), diagnosticScroll,
		container.NewGridWithColumns(3, copyReport, logs, refresh), fixedGap(32),
	)))
	v.mainPanel = quietScrollView(v.mainScroll, v.mainScroll)
	v.actions = []*touchButton{v.primary, retry, copyReport, logs, refresh}
	v.buildLogin()
	v.navigation = newTouchButtonWithIcon("", theme.MenuIcon(), v.navigate)
	v.navigation.traceMenu = true
	v.navigation.Importance = widget.LowImportance
	icon := canvas.NewImageFromResource(fyne.NewStaticResource("appicon.svg", appIcon))
	icon.FillMode = canvas.ImageFillContain
	title := widget.NewLabelWithStyle("FnCPN", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	versionLabel := widget.NewLabel(version.Current)
	versionLabel.Importance = widget.LowImportance
	header := container.New(chromeLayout{}, v.navigation, icon, container.NewThemeOverride(title, textSizeTheme{theme.Current(), 18}), container.NewThemeOverride(versionLabel, textSizeTheme{theme.Current(), 13}))
	v.mainPanel = container.NewStack(newThemeSurface(theme.ColorNameBackground), v.mainPanel)
	v.accountPanel = container.NewStack(newThemeSurface(theme.ColorNameBackground), v.accountPanel)
	v.pages = &pageLayout{progress: 1}
	v.body = container.New(v.pages, v.mainPanel, v.accountPanel)
	v.mainPanel.Hide()
	v.editing = true
	v.navigation.SetIcon(theme.MenuIcon())
	v.loading = container.NewCenter(widget.NewLabel(tr("正在加载连接状态…")))
	v.accountPanel.Hide()
	var messages fyne.CanvasObject
	if platform, ok := backend.(interface{ EnvironmentNotice() string }); ok {
		if text := platform.EnvironmentNotice(); text != "" {
			notice := widget.NewLabel(text)
			notice.Wrapping = fyne.TextWrapWord
			notice.Importance = widget.WarningImportance
			messages = notice
		}
	}
	v.content = container.New(appFrameLayout{}, header, widget.NewSeparator(), container.NewBorder(messages, nil, nil, nil, container.NewStack(v.body, v.loading)))
	if platform, ok := v.backend.(interface{ NativeNavigation() bool }); ok && platform.NativeNavigation() {
		v.content = container.NewBorder(messages, nil, nil, nil, container.NewStack(v.body, v.loading))
	}
	edge := &drawerEdge{view: v}
	edge.ExtendBaseWidget(edge)
	if platform, ok := v.backend.(interface{ NativeNavigation() bool }); !ok || !platform.NativeNavigation() {
		v.content = container.New(drawerEdgeLayout{}, v.content, edge)
	}
	return v
}

func (v *clientView) buildLogin() {
	v.fnID = newLoginEntry(false, v.revealLoginEntry)
	v.fnID.SetPlaceHolder(tr("例如 home-nas"))
	v.username = newLoginEntry(false, v.revealLoginEntry)
	v.username.SetPlaceHolder(tr("fnOS 用户名"))
	v.password = newLoginEntry(true, v.revealLoginEntry)
	v.password.SetPlaceHolder(tr("fnOS 密码"))
	v.accessCode = newLoginEntry(true, v.revealLoginEntry)
	v.accessCode.SetPlaceHolder(tr("fnOS 访问码"))
	v.otp = newLoginEntry(false, v.revealLoginEntry)
	v.otp.SetPlaceHolder(tr("6 位 OTP"))
	v.trust = widget.NewCheck(tr("信任此设备"), nil)
	v.account = container.NewVBox(loginField("FN Connect ID", v.fnID), fixedGap(12), loginField(tr("用户名"), v.username), fixedGap(12), loginField(tr("密码"), v.password))
	previous := newTouchButton(tr("上一步"), func() {
		v.execute(func(ctx context.Context) error {
			if err := v.authorization.cancel(ctx, v.backend); err != nil {
				return err
			}
			fyne.Do(func() { v.account.Show(); v.challenge.Hide() })
			return nil
		})
	})
	v.challenge = container.NewVBox(widget.NewLabel(tr("根据提示输入访问码或验证码")), widget.NewForm(widget.NewFormItem(tr("访问码"), v.accessCode), widget.NewFormItem(tr("验证码"), v.otp)), v.trust, previous)
	v.challenge.Hide()
	login := newTouchButton(tr("登录并连接"), func() {
		input := client.NativeAuthorization{FNID: v.fnID.Text, Username: v.username.Text, Password: v.password.Text, AccessCode: v.accessCode.Text, TwofaCode: v.otp.Text, TrustDevice: v.trust.Checked}
		v.password.SetText("")
		v.accessCode.SetText("")
		v.otp.SetText("")
		v.execute(func(ctx context.Context) error {
			err := v.authorization.submit(ctx, v.backend, input)
			fyne.Do(func() {
				if client.RequiresAdditionalCredential(err) {
					v.account.Hide()
					v.challenge.Show()
				} else {
					v.account.Show()
					v.challenge.Hide()
				}
				if err == nil {
					v.loginFromMenu = false
					v.loginReturnEditing = false
					v.loginReturnMenu = false
					v.manualLogin = false
					v.loginRequired = false
					v.editing = false
					v.transition(false)
					v.navigation.SetIcon(theme.MenuIcon())
					v.navigation.Show()
				}
			})
			if err != nil {
				return err
			}
			return v.readStatus(ctx)
		})
	})
	login.deferUnfocus = true
	v.loginSubmit = login
	login.Importance = widget.HighImportance
	v.bindLoginKeyboard(login)
	v.actions = append(v.actions, login, previous)
	intro := heroCopy()

	form := container.NewVBox(v.message, v.account, v.challenge, fixedGap(20), container.NewHBox(login), fixedGap(32))
	formView := newQuietScroll(form)
	formScroll := formView.Layout.(*scrollThumbLayout).scroll
	formScroll.Direction = container.ScrollNone
	responsive := &loginLayout{formContent: form, landscape: func() bool {
		canvas := fyne.CurrentApp().Driver().CanvasForObject(v.accountPanel)
		return canvas != nil && canvas.Size().Width > canvas.Size().Height
	}}
	v.accountPanel = newQuietScroll(container.New(layout.NewCustomPaddedLayout(20, 32, 20, 20), container.New(responsive, intro, formView)))
	v.loginScroll = v.accountPanel.(*fyne.Container).Layout.(*scrollThumbLayout).scroll
	pageScroll := v.loginScroll
	responsive.changed = func() {
		if responsive.wide {
			formScroll.Direction = container.ScrollVerticalOnly
			v.loginScroll = formScroll
		} else {
			formScroll.Direction = container.ScrollNone
			formScroll.ScrollToTop()
			v.loginScroll = pageScroll
		}
		pageScroll.ScrollToTop()
		pageScroll.Refresh()
		formScroll.Refresh()
	}

}
func connected(state model.ClientState) bool {
	return state == model.ClientLocal || state == model.ClientDirect || state == model.ClientRelay || state == model.ClientReconnecting || state == model.ClientProbing
}
func needsLogin(state model.ClientState) bool {
	return state == model.ClientUnconfigured || state == model.ClientAuthRequired
}

// A network retry/error state does not prove that authorization completed.
// Keep required login sticky until the form's authorization succeeds.
func (v *clientView) requiresLogin() bool { return v.loginRequired || needsLogin(v.current.State) }

func (v *clientView) showMain() {
	defer v.syncBackGesture()
	v.message.Hide()
	if v.requiresLogin() {
		return
	}
	v.editing = false
	v.loginFromMenu = false
	v.loginReturnEditing = false
	v.loginReturnMenu = false
	v.password.SetText("")
	v.accessCode.SetText("")
	v.otp.SetText("")
	v.trust.SetChecked(false)
	v.manualLogin = false
	v.transition(false)
	v.navigation.SetIcon(theme.MenuIcon())
	v.navigation.Show()
}
func (v *clientView) showLogin(change bool) {
	defer v.syncBackGesture()
	v.editing = true
	v.manualLogin = !v.requiresLogin()
	v.transition(true)
	v.navigation.SetIcon(theme.NavigateBackIcon())
	if v.requiresLogin() && !v.loginFromMenu {
		v.navigation.SetIcon(theme.MenuIcon())
	}
	v.navigation.Show()
	v.password.SetText("")
	v.accessCode.SetText("")
	v.otp.SetText("")
	v.trust.SetChecked(false)
	v.account.Show()
	v.challenge.Hide()
	if change {
		v.fnID.SetText("")
		v.username.SetText("")
	} else {
		v.fnID.SetText(v.report.FNID)
		v.username.SetText(v.report.Username)
	}
}
func (v *clientView) navigate() {
	if !v.statusReceived {
		v.back()
		return
	}
	if v.editing {
		if v.requiresLogin() && !v.loginFromMenu {
			v.showMenu()
			return
		}
		v.execute(func(ctx context.Context) error {
			if err := v.authorization.cancel(ctx, v.backend); err != nil {
				return err
			}
			fyne.Do(v.returnFromLogin)
			return nil
		})
		return
	}
	v.showMenu()
}
func (v *clientView) prepareMenu() {
	if v.window == nil || v.drawer != nil {
		return
	}
	brand := canvas.NewImageFromResource(fyne.NewStaticResource("appicon.svg", appIcon))
	brand.FillMode = canvas.ImageFillContain
	brand.SetMinSize(fyne.NewSize(36, 36))
	var railButtons []*touchButton
	var railLabels []string
	var menuWasExpanded bool
	railIcons := []fyne.Resource{theme.MenuIcon(), theme.NavigateBackIcon()}
	buttons := []fyne.CanvasObject{container.New(layout.NewCustomPaddedLayout(24, 24, 16, 16), container.NewHBox(brand, widget.NewLabelWithStyle("FnCPN", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))), widget.NewSeparator(), fixedGap(8)}
	add := func(text string, icon fyne.Resource, action func()) *touchButton {
		button := &drawerButton{view: v}
		button.Text = text
		button.Icon = icon
		button.OnTapped = func() {
			menuWasExpanded = v.drawer.visible
			if v.drawer.visible {
				v.drawer.CloseThen(action)
			} else {
				action()
			}
		}
		railButtons = append(railButtons, &button.touchButton)
		railLabels = append(railLabels, text)
		railIcons = append(railIcons, icon)
		button.ExtendBaseWidget(button)
		button.Alignment = widget.ButtonAlignLeading
		button.Importance = widget.LowImportance
		buttons = append(buttons, container.New(layout.NewCustomPaddedLayout(2, 2, 12, 12), container.NewThemeOverride(button, menuTheme{theme.Current()})))
		return &button.touchButton
	}
	login := func(change bool) {
		restoreMenu := menuWasExpanded
		v.execute(func(ctx context.Context) error {
			if err := v.authorization.cancel(ctx, v.backend); err != nil {
				return err
			}
			fyne.Do(func() {
				v.loginReturnEditing = v.editing
				v.loginReturnMenu = restoreMenu
				v.loginFromMenu = true
				v.showLogin(change)
			})
			return nil
		})
	}
	add(tr("重新登录"), theme.LoginIcon(), func() { login(false) })
	add(tr("更换 NAS"), theme.StorageIcon(), func() { login(true) })
	v.admin = add(tr("管理后台"), theme.ComputerIcon(), v.openAdmin)

	add(tr("官网 · 捐赠"), theme.HelpIcon(), func() { v.openWebsite() })
	add(tr("导出诊断与日志"), theme.DownloadIcon(), v.exportSupport)
	if platform, ok := v.backend.(interface{ NetworkDebugAvailable() bool }); ok && platform.NetworkDebugAvailable() {
		add(tr("应用内网络调试"), theme.ComputerIcon(), v.networkDebug)
	}
	if v.canExit() {
		buttons = append(buttons, widget.NewSeparator())
		add(tr("退出"), theme.LogoutIcon(), v.quit)
	}
	v.drawer = newNavigationDrawer(v.window.Canvas(), container.NewVBox(buttons...))
	if platform, ok := v.backend.(interface {
		NativeNavigation() bool
		SetRailMenu([]string, []fyne.Resource, func(int))
	}); ok && platform.NativeNavigation() {
		v.drawer.layout.right = true
		if state, ok := v.backend.(interface{ SetRailExpanded(bool) }); ok {
			v.drawer.externalVisibility = state.SetRailExpanded
		}
		platform.SetRailMenu(railLabels, railIcons, func(index int) {
			fyne.Do(func() {
				if v.ctx.Err() != nil || !v.statusReceived || v.navigation.Disabled() {
					return
				}
				if index == -3 {
					v.drawer.Hide()
					return
				}
				if index < 0 {
					if index == -1 && v.drawer.visible {
						v.drawer.Hide()
					} else {
						v.showMenu()
					}
					return
				}
				if index < len(railButtons) && !railButtons[index].Disabled() {
					railButtons[index].Tapped(nil)
				}
			})
		})
	}

	// Create renderers and measure text before the first interaction. Actual
	// animation only translates this persistent panel.
	v.drawer.prepare()
}
func (v *clientView) showMenu() {
	v.prepareMenu()
	if v.drawer != nil && !v.drawer.visible {
		v.drawer.Show()
	}
}
func (v *clientView) close() {
	if v.drawer != nil {
		v.drawer.Dispose()
	}
	if v.pageAnimation != nil {
		v.pageAnimation.Stop()
	}
	v.pageGeneration++
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
	v.navigation.Disable()
	v.syncBackGesture()
	v.message.Hide()
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
			v.navigation.Enable()
			v.syncBackGesture()
			if err != nil {
				if v.editing {
					v.message.SetText(loginErrorMessage(err))
					v.message.Show()
				} else {
					v.diagnosticBody.Importance = widget.DangerImportance
					v.diagnosticBody.SetText(err.Error())
					v.diagnosticBody.Refresh()
				}
			}
		})
	}()
}
func (v *clientView) command(method string) {
	v.execute(func(ctx context.Context) error {
		if err := v.backend.Call(ctx, method, nil, nil); err != nil {
			return err
		}
		return v.readStatus(ctx)
	})
}

// Read-only refresh must not toggle all controls or clear the current page.
func (v *clientView) refresh() {
	go func() {
		ctx, cancel := context.WithTimeout(v.ctx, 30*time.Second)
		defer cancel()
		var err error
		if platform, ok := v.backend.(interface{ Resume(context.Context) error }); ok {
			err = platform.Resume(ctx)
		}
		if err == nil {
			err = v.readStatus(ctx)
		}
		if err != nil && v.ctx.Err() == nil {
			fyne.Do(func() {
				if v.ctx.Err() != nil {
					return
				}
				if !v.statusReceived {
					v.applyStatus(model.ClientStatus{State: model.ClientError})
				}
				v.diagnosticBody.Importance = widget.DangerImportance
				v.setLabel(v.diagnosticBody, err.Error())
				v.diagnosticBody.Refresh()
			})
		}
	}()
}

func (v *clientView) readStatus(ctx context.Context) error {
	if !v.readMu.TryLock() {
		return nil
	}
	defer v.readMu.Unlock()
	var status model.ClientStatus
	if err := v.backend.Call(ctx, client.MethodStatus, nil, &status); err != nil {
		return err
	}
	var report model.ClientDiagnostics
	hasReport := v.backend.Call(ctx, client.MethodDiagnose, nil, &report) == nil
	if hasReport && report.Status.State != "" {
		status = report.Status
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fyne.Do(func() {
		if v.ctx.Err() != nil {
			return
		}
		v.applyStatus(status)
		if hasReport {
			v.applyReport(report, string(encoded))
		}
		if v.layoutDirty {
			v.mainPanel.Refresh()
			v.layoutDirty = false
		}
	})
	return nil
}
func (v *clientView) showReport(report model.ClientDiagnostics) {
	data, _ := json.MarshalIndent(report, "", "  ")
	v.applyReport(report, string(data))
}
func (v *clientView) applyReport(report model.ClientDiagnostics, encoded string) {
	v.report = report
	v.diagnosticText = encoded
	v.setLabel(v.identity, identityText(report.FNID, report.Username))
	value := func(name, text string) {
		if text == "" {
			text = "—"
		}
		v.setLabel(v.values[name], text)
	}
	value(tr("隧道地址"), report.ClientAddress)
	value("NAS IP", report.NASAddress)
	value(tr("IPv6 检测"), directReason(report.Direct.Reason))
	root := tr("不可用")
	if report.PrivilegedAvailable {
		root = tr("可用")
	}
	if report.PrivilegedActive {
		root = tr("运行中")
	}
	value(tr("网络服务"), root)
	messages := []string{}
	for _, err := range append(report.Errors, report.Status.LastError, report.Status.DNS.LastError, report.Direct.LastError) {
		if err != nil {
			messages = append(messages, model.FormatError(err))
		}
	}
	if len(messages) == 0 {
		messages = append(messages, tr("无异常"))
	}
	importance := widget.MediumImportance
	if len(messages) > 0 && messages[0] != tr("无异常") {
		importance = widget.DangerImportance
	}
	if v.diagnosticBody.Importance != importance {
		v.diagnosticBody.Importance = importance
		v.diagnosticBody.Refresh()
	}
	v.setLabel(v.diagnosticBody, strings.Join(messages, "\n\n"))
}
func (v *clientView) showStatus(status model.ClientStatus) {
	fyne.Do(func() { v.applyStatus(status) })
}
func (v *clientView) applyStatus(status model.ClientStatus) {
	defer v.syncBackGesture()
	if v.ctx.Err() != nil {
		return
	}
	v.current = status
	names := map[model.ClientState]string{model.ClientUnconfigured: tr("尚未设置"), model.ClientAuthRequired: tr("需要登录"), model.ClientAuthorizing: tr("正在授权"), model.ClientProbing: tr("正在检测"), model.ClientPaused: tr("已断开连接"), model.ClientLocal: tr("已连接 · 局域网"), model.ClientDirect: tr("已连接 · IPv6 直连"), model.ClientRelay: tr("已连接 · FN Connect 中继"), model.ClientReconnecting: tr("正在恢复连接"), model.ClientServerUnavailable: tr("服务端不可用"), model.ClientError: tr("连接异常")}
	name := names[status.State]
	if name == "" {
		name = string(status.State)
	}
	v.setLabel(v.status, name)
	importance := widget.MediumImportance
	if status.State == model.ClientLocal || status.State == model.ClientDirect || status.State == model.ClientRelay {
		importance = widget.SuccessImportance
	}
	if v.status.Importance != importance {
		v.status.Importance = importance
		v.status.Refresh()
	}
	action := tr("连接")
	if connected(status.State) {
		action = tr("断开连接")
	}
	if status.LocalNetworkWaiting {
		action = tr("继续远程连接")
	}
	if v.primary.Text != action {
		v.primary.SetText(action)
	}
	path := map[string]string{"ipv6": tr("IPv6 直连"), "fn-connect": tr("FN Connect 中继"), "local": tr("局域网直达")}[status.Path]
	if path == "" {
		path = status.Path
	}
	if path == "" {
		path = "—"
	}
	v.setLabel(v.values[tr("连接路径")], path)
	iface := status.Interface
	if iface == "" {
		iface = "—"
	} else if status.MTU > 0 {
		iface += fmt.Sprintf(" · MTU %d", status.MTU)
	}
	v.setLabel(v.values[tr("网络接口")], iface)
	handshake := "—"
	if status.LastHandshake != nil {
		handshake = status.LastHandshake.Local().Format("15:04:05")
	}
	v.setLabel(v.values[tr("最近握手")], handshake)
	dns := status.DNS.State
	if status.DNS.Suffix != "" {
		dns += " · " + status.DNS.Suffix
	}
	if dns == "" {
		dns = "—"
	}
	v.setLabel(v.values["DNS"], dns)
	firstStatus := !v.statusReceived
	if needsLogin(status.State) {
		v.loginRequired = true
		if !v.editing {
			v.showLogin(false)
		}
	} else if v.editing && !v.manualLogin && !v.loginRequired && status.State != model.ClientAuthorizing {
		v.showMain()
	}
	v.statusReceived = true
	if firstStatus {
		v.loading.Hide()
		v.settlePage(v.editing)
	}
}
func (v *clientView) watch() {
	v.stopWatch()
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
				_ = v.readStatus(ctx)
			} else {
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
	if v.requiresLogin() || !v.report.Administrator {
		v.promptAdminLogin()
		return
	}

	progress := widget.NewActivity()
	progress.Start()
	loading := dialog.NewCustom(tr("管理后台"), tr("取消"), container.NewVBox(progress, widget.NewLabel(tr("正在打开管理后台…"))), v.window)
	requestCtx, cancel := context.WithCancel(v.ctx)
	loading.SetOnClosed(func() { progress.Stop(); cancel() })
	loading.Show()
	v.execute(func(_ context.Context) error {
		defer cancel()
		requireLogin := false
		defer func() {
			fyne.Do(func() {
				loading.Hide()
				if requireLogin && v.ctx.Err() == nil {
					v.promptAdminLogin()
				}
			})
		}()
		ctx, done := context.WithTimeout(requestCtx, 30*time.Second)
		defer done()
		var result struct {
			URL string `json:"url"`
		}
		if err := v.backend.Call(ctx, client.MethodAdminProxy, nil, &result); err != nil {
			if ctx.Err() == context.Canceled {
				return nil
			}
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("%s", tr("管理后台加载失败，请重试。"))
			}
			code := model.AsError(err).Code
			if code == model.ErrorAuthRequired || code == model.ErrorPermissionDenied {
				requireLogin = true
				return nil
			}
			return err
		}
		parsed, err := url.Parse(result.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil {
			return fmt.Errorf("%s", tr("无效的管理后台地址"))
		}
		if ctx.Err() == context.Canceled {
			return nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("%s", tr("管理后台加载失败，请重试。"))
		}
		if actions, ok := v.backend.(interface{ OpenAdmin(string) error }); ok {
			return actions.OpenAdmin(result.URL)
		}
		return fyne.CurrentApp().OpenURL(parsed)
	})
}
func (v *clientView) openWebsite() {
	address := "https://fncpn.rectcircle.cn/#donate"
	if actions, ok := v.backend.(interface{ OpenBrowser(string) error }); ok {
		v.execute(func(context.Context) error { return actions.OpenBrowser(address) })
		return
	}
	parsed, _ := url.Parse(address)
	_ = fyne.CurrentApp().OpenURL(parsed)
}
func (v *clientView) openLogs() {
	if actions, ok := v.backend.(interface{ OpenLogs() error }); ok {
		v.execute(func(context.Context) error { return actions.OpenLogs() })
		return
	}
	if v.window != nil {
		dialog.ShowInformation(tr("日志"), tr("当前平台暂不支持查看日志。"), v.window)
	}
}
func (v *clientView) quit() {
	shouldDisconnect := !needsLogin(v.current.State)
	v.execute(func(ctx context.Context) error { return v.pauseAndExit(ctx, shouldDisconnect) })
}
func (v *clientView) canExit() bool {
	if platform, ok := v.backend.(interface{ CanExit() bool }); ok {
		return platform.CanExit()
	}
	return true
}
func (v *clientView) pauseAndExit(ctx context.Context, shouldDisconnect bool) error {
	if shouldDisconnect {
		if err := v.backend.Call(ctx, client.MethodDisconnect, nil, nil); err != nil {
			return err
		}
	}
	_ = v.authorization.cancel(ctx, v.backend)
	if !v.canExit() {
		return nil
	}
	if actions, ok := v.backend.(interface{ Exit() error }); ok {
		return actions.Exit()
	}
	fyne.Do(func() { fyne.CurrentApp().Quit() })
	return nil
}
func directReason(reason string) string {
	names := map[string]string{"no_local_ipv6": tr("当前网络无可用 IPv6 路由"), "no_server_ipv6": tr("NAS 无公网 IPv6"), "server_disabled": tr("NAS 关闭 IPv6 直连"), "handshake_failed": tr("直连握手失败，使用中继"), "cooldown": tr("稍后自动重试直连"), "connected": tr("IPv6 直连已建立"), "local_network": tr("局域网直达"), "probing": tr("正在检测 IPv6"), "discovery_failed": tr("地址发现失败")}
	if text, ok := names[reason]; ok {
		return text
	}
	return reason
}
func (v *clientView) exportSupport() {
	if v.window == nil {
		return
	}
	dialog.ShowConfirm(tr("导出诊断与日志"), tr("报告仅包含经过筛选的连接状态与系统事件，不含身份、地址和凭据。"), func(ok bool) {
		if !ok {
			return
		}
		v.execute(func(ctx context.Context) error {
			var report model.ClientDiagnostics
			_ = v.backend.Call(ctx, client.MethodDiagnose, nil, &report)
			data, err := json.Marshal(report)
			if err != nil {
				return err
			}
			if exporter, ok := v.backend.(interface{ ExportSupport(string) error }); ok {
				return exporter.ExportSupport(string(data))
			}
			return fmt.Errorf("%s", tr("当前平台暂不支持导出日志"))
		})
	}, v.window)
}

// Label.SetText refreshes even unchanged text in Fyne 2.8. Avoid that work;
// only wrapping text can require a parent layout pass.
func (v *clientView) setLabel(label *widget.Label, text string) {
	if label.Text == text {
		return
	}
	label.SetText(text)
	if label == v.identity || label == v.diagnosticBody {
		v.layoutDirty = true
	}
}
