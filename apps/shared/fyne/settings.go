package main

import (
	"context"
	"encoding/json"
	"fmt"
	"fyne.io/fyne/v2/canvas"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/version"
)

type streamClient interface {
	Stream(context.Context, string, any, func(json.RawMessage) (bool, error)) error
}

type nativeSettings struct {
	view         *clientView
	panel        *fyne.Container
	menu, detail fyne.CanvasObject
	pages        *fyne.Container
	layout       *pageLayout
	animation    *fyne.Animation
	page         string
	nativeTitle  *widget.Label
	epoch        uint64
	cancel       context.CancelFunc
	preferences  client.LinkSettings
	rows         *fyne.Container
	scroll       *container.Scroll
	contentTheme *container.ThemeOverride
	popup        *widget.PopUp
	selected     map[string]bool
	start, stop  *touchButton
	status       *widget.Label
	statusView   *container.ThemeOverride
	speed        *widget.Label
	speedView    *container.ThemeOverride
	results      *fyne.Container
	visual       *testDiagram
	graph        *testSparkline
}

func newNativeSettings(v *clientView) *nativeSettings {
	s := &nativeSettings{view: v, selected: map[string]bool{"ipv6": true, "fn-connect": true}}
	s.menu = container.NewPadded(container.NewVBox(
		newSettingsMenuRow(tr("网络链路"), func() { s.open("links") }),
		newSettingsMenuRow(tr("关于"), func() { s.open("about") })))
	s.detail = container.NewVBox()
	s.layout = &pageLayout{}
	s.pages = container.New(s.layout, s.menu, s.detail)
	var content fyne.CanvasObject = s.pages
	if native, ok := v.backend.(interface{ NativeNavigation() bool }); ok && native.NativeNavigation() {
		s.nativeTitle = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		content = container.NewBorder(container.NewPadded(s.nativeTitle), nil, nil, nil, s.pages)
	}
	s.panel = container.NewStack(newThemeSurface(theme.ColorNameBackground), content)
	s.panel.Hide()
	return s
}
func (v *clientView) openSettings() {
	if v.drawer != nil {
		v.drawer.Hide()
	}
	v.settings.open("menu")
	v.navigation.SetIcon(theme.NavigateBackIcon())
	v.syncBackGesture()
}
func (s *nativeSettings) back() {
	if s.popup != nil && s.popup.Visible() {
		s.popup.Hide()
		s.popup = nil
		return
	}
	if s.page != "menu" {
		s.open("menu")
		return
	}
	s.dispose()
	s.panel.Hide()
	if s.view.editing {
		s.view.navigation.SetIcon(theme.NavigateBackIcon())
	} else {
		s.view.navigation.SetIcon(theme.MenuIcon())
	}
	s.view.syncBackGesture()
}
func (s *nativeSettings) dispose() {
	if s.popup != nil {
		s.popup.Hide()
		s.popup = nil
	}
	if s.animation != nil {
		s.animation.Stop()
		s.animation = nil
	}
	s.epoch++
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.visual != nil {
		s.visual.stop()
	}
}
func (s *nativeSettings) open(page string) {
	s.dispose()
	s.page = page
	if s.nativeTitle != nil {
		title := tr("设置")
		if page == "links" {
			title = tr("网络链路")
		}
		if page == "about" {
			title = tr("关于")
		}
		s.nativeTitle.SetText(title)
	}
	s.panel.Show()
	if page == "about" {
		s.about()
	} else if page == "links" {
		s.links()
	}
	if s.animation != nil {
		s.animation.Stop()
	}
	start, target := s.layout.progress, float32(0)
	if page != "menu" {
		target = 1
	}
	s.menu.Show()
	s.detail.Show()
	s.animation = fyne.NewAnimation(220*time.Millisecond, func(p float32) {
		s.layout.progress = start + (target-start)*p
		s.layout.position(s.pages.Objects, s.pages.Size())
		if p >= 1 {
			if page == "menu" {
				s.detail.Hide()
			} else {
				s.menu.Hide()
			}
		}
	})
	s.animation.Start()
	s.view.syncBackGesture()
}
func (s *nativeSettings) replace(object fyne.CanvasObject) {
	s.detail = object
	s.pages.Objects[1] = object
	s.pages.Refresh()
}
func (s *nativeSettings) about() {
	icon := canvas.NewImageFromResource(fyne.NewStaticResource("appicon.svg", appIcon))
	icon.FillMode = canvas.ImageFillContain
	title := widget.NewLabelWithStyle("FnCPN", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	hero := container.NewVBox(fixedGap(20), container.NewCenter(container.NewGridWrap(fyne.NewSize(76, 76), icon)), fixedGap(8), container.NewThemeOverride(title, textSizeTheme{theme.Current(), 26}), fixedGap(16))
	versionLabel := widget.NewLabel(version.Current)
	versionLabel.Alignment = fyne.TextAlignTrailing
	commit := version.Revision
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if strings.HasSuffix(version.Revision, "-dirty") && !strings.HasSuffix(commit, "-dirty") {
		commit += "-dirty"
	}
	commitLabel := widget.NewLabel(commit)
	commitLabel.Alignment = fyne.TextAlignTrailing
	commitLabel.TextStyle.Monospace = true
	commitLabel.Importance = widget.LowImportance
	copyCommit := newTouchButtonWithIcon("", theme.ContentCopyIcon(), func() {
		if s.view.window != nil {
			s.view.window.Clipboard().SetContent(version.Revision)
			s.view.toast(tr("已复制 Commit"))
		}
	})
	copyCommit.Importance = widget.LowImportance
	commitValue := container.NewBorder(nil, nil, nil, copyCommit, commitLabel)
	details := settingsCard(container.NewVBox(container.NewBorder(nil, nil, widget.NewLabel(tr("版本")), nil, versionLabel), container.NewBorder(nil, nil, widget.NewLabel("Commit"), nil, commitValue)))
	actions := container.NewVBox()
	for _, link := range []struct{ label, path string }{{"GitHub", "https://github.com/rectcircle/fn-connect-private-network"}, {tr("官网 · 捐赠"), "https://fncpn.rectcircle.cn/"}, {tr("下载服务端与其他客户端"), "https://github.com/rectcircle/fn-connect-private-network/releases"}} {
		link := link
		actions.Add(newSettingsMenuRow(link.label, func() { s.view.openBrowser(link.path) }))
	}
	s.replace(newQuietScroll(container.NewPadded(container.NewVBox(hero, details, fixedGap(12), actions))))
}

func (s *nativeSettings) links() {
	s.rows = container.NewVBox(widget.NewLabel(tr("正在读取")))
	s.status = widget.NewLabel("")
	s.status.Wrapping = fyne.TextWrapWord
	s.status.Importance = widget.LowImportance
	s.statusView = container.NewThemeOverride(s.status, textSizeTheme{theme.Current(), 13})
	s.statusView.Hide()
	s.speed = widget.NewLabelWithStyle("", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	s.speed.Importance = widget.HighImportance
	s.speedView = container.NewThemeOverride(s.speed, textSizeTheme{theme.Current(), 28})
	s.speedView.Hide()
	s.results = container.NewVBox()
	s.visual = newTestDiagram()
	s.graph = newTestSparkline()
	s.graph.Hide()
	s.visual.Hide()
	s.results.Hide()
	selector := newTouchButtonWithIcon(s.selectionTitle(), theme.MenuDropDownIcon(), nil)
	selector.IconPlacement = widget.ButtonIconTrailingText
	selector.OnTapped = func() {
		boxes := container.NewVBox()
		for _, key := range []string{"ipv6", "fn-connect"} {
			key := key
			check := widget.NewCheck(linkTitle(key), func(on bool) { s.selected[key] = on; selector.SetText(s.selectionTitle()) })
			check.SetChecked(s.selected[key])
			boxes.Add(check)
		}
		popup := widget.NewPopUp(container.NewPadded(boxes), s.view.window.Canvas())
		s.popup = popup
		boxes.Add(newTouchButton(tr("完成"), popup.Hide))
		popup.ShowAtPosition(fyne.CurrentApp().Driver().AbsolutePositionForObject(selector).Add(fyne.NewPos(0, selector.Size().Height)))
	}
	s.start = newTouchButton(tr("开始测试"), func() {
		if !s.selected["ipv6"] && !s.selected["fn-connect"] {
			return
		}
		confirm := widget.NewLabel(tr("测试期间会重新建立连接，当前网络访问可能短暂中断。") + "\n\n" + tr("测试结果仅供参考，受当前设备与 NAS 的网络状况、可用带宽及 FN Connect 中转服务器带宽限制。"))
		confirm.Wrapping = fyne.TextWrapWord
		s.popup = showTestConfirmation(s.view.window, confirm, s.run)
	})
	s.start.Importance = widget.HighImportance
	s.stop = newTouchButton(tr("取消"), func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	s.stop.Hide()
	help := widget.NewLabel(tr("打开或关闭开关可启用或禁用对应链路；拖动远程链路可调整优先级。连接时按从上到下的顺序尝试已启用的链路。局域网始终优先，且不可关闭或调整顺序。"))
	help.Wrapping = fyne.TextWrapWord
	info := newTouchButtonWithIcon("", theme.QuestionIcon(), nil)
	info.Importance = widget.LowImportance
	info.OnTapped = func() { s.popup = showSettingsHint(s.view.window, info, help) }
	heading := container.NewBorder(nil, nil, settingsSectionTitle(tr("开关与优先级")), info)
	reset := newTouchButton(tr("恢复默认设置"), func() {
		s.preferences.Links = []client.LinkPreference{{Link: "ipv6", Enabled: true}, {Link: "fn-connect", Enabled: true}}
		s.save()
	})
	preferences := settingsCard(container.NewVBox(heading, s.rows, container.NewHBox(reset)))
	actions := container.NewStack(s.start, s.stop)
	controls := container.New(settingsTestControlsLayout{}, widget.NewLabel(tr("测试链路")), selector, actions)
	compact := container.NewThemeOverride(controls, textSizeTheme{settingsControlsTheme{theme.Current()}, 12})
	test := settingsCard(container.NewVBox(settingsSectionTitle(tr("网络链路测试")), fixedGap(8), compact, s.statusView, s.visual, s.speedView, s.graph, s.results))
	content := container.NewVBox(preferences, fixedGap(12), test)
	contentTheme := container.NewThemeOverride(content, settingsControlsTheme{theme.Current()})
	s.contentTheme = contentTheme
	s.scroll = container.NewVScroll(container.NewPadded(contentTheme))
	s.replace(quietScrollView(s.scroll, s.scroll))
	epoch := s.epoch
	go func() {
		var prefs client.LinkSettings
		err := s.view.backend.Call(s.view.ctx, client.MethodLinkSettings, nil, &prefs)
		fyne.Do(func() {
			if s.epoch != epoch {
				return
			}
			if err != nil {
				s.status.SetText(err.Error())
				s.statusView.Show()
				s.reflow()
				return
			}
			s.preferences = prefs
			s.renderRows()
		})
	}()
}
func (s *nativeSettings) selectionTitle() string {
	if s.selected["ipv6"] && s.selected["fn-connect"] {
		return tr("IPv6 与 FN Connect")
	}
	if s.selected["ipv6"] {
		return linkTitle("ipv6")
	}
	if s.selected["fn-connect"] {
		return linkTitle("fn-connect")
	}
	return tr("选择测试链路")
}
func linkTitle(key string) string {
	if key == "ipv6" {
		return tr("IPv6 直连")
	}
	return tr("FN Connect 中继")
}
func (s *nativeSettings) renderRows() {
	s.rows.RemoveAll()
	local := newLinkSwitch(tr("局域网 · 始终优先"))
	local.SetChecked(true)
	local.Disable()
	localLabel := widget.NewLabel(tr("局域网 · 始终优先"))
	localLabel.Importance = widget.LowImportance
	s.rows.Add(container.NewBorder(nil, nil, disabledGrip(), local, localLabel))
	rows := make([]*fyne.Container, 2)
	for i, pref := range s.preferences.Links {
		i, pref := i, pref
		toggle := newLinkSwitch(linkTitle(pref.Link))
		toggle.SetChecked(pref.Enabled)
		// Initial model rendering must never issue a write or recurse into rendering.
		toggle.OnChanged = func(enabled bool) { s.preferences.Links[i].Enabled = enabled; s.save() }
		var origin, neighbor fyne.Position
		dragging := false
		grip := newPriorityGrip(func(delta float32) {
			if !dragging {
				return
			}
			dragging = false
			height := rows[i].Size().Height + theme.Padding()
			swap := i == 0 && delta > height/2 || i == 1 && delta < -height/2
			start, otherStart := rows[i].Position(), rows[1-i].Position()
			target, otherTarget := origin, neighbor
			if swap {
				target, otherTarget = neighbor, origin
			}
			epoch := s.epoch
			animation := fyne.NewAnimation(180*time.Millisecond, func(p float32) {
				if epoch != s.epoch {
					return
				}
				rows[i].Move(fyne.NewPos(start.X, start.Y+(target.Y-start.Y)*p))
				rows[1-i].Move(fyne.NewPos(otherStart.X, otherStart.Y+(otherTarget.Y-otherStart.Y)*p))
				if p >= 1 && swap {
					s.preferences.Links[0], s.preferences.Links[1] = s.preferences.Links[1], s.preferences.Links[0]
					s.save()
				}
			})
			animation.Start()
		})
		grip.move = func(delta float32) {
			if !dragging {
				origin = rows[i].Position()
				neighbor = rows[1-i].Position()
				dragging = true
			}
			height := rows[i].Size().Height + theme.Padding()
			if delta > height {
				delta = height
			}
			if delta < -height {
				delta = -height
			}
			rows[i].Move(origin.Add(fyne.NewPos(0, delta)))
			other := neighbor.Y
			if i == 0 && delta > height/2 {
				other -= height
			}
			if i == 1 && delta < -height/2 {
				other += height
			}
			rows[1-i].Move(fyne.NewPos(neighbor.X, other))
		}
		rows[i] = container.NewBorder(nil, nil, grip, toggle, widget.NewLabel(linkTitle(pref.Link)))
		s.rows.Add(rows[i])
	}

	s.reflow()
}
func (s *nativeSettings) save() {
	s.renderRows()
	prefs := s.preferences
	epoch := s.epoch
	go func() {
		var saved client.LinkSettings
		err := s.view.backend.Call(s.view.ctx, client.MethodSetLinkSettings, prefs, &saved)
		fyne.Do(func() {
			if epoch != s.epoch {
				return
			}
			if err != nil {
				s.status.SetText(err.Error())
				s.statusView.Show()
				s.reflow()
				return
			}
			s.preferences = saved
			s.renderRows()
		})
	}()
}
func (s *nativeSettings) run() {
	backend, ok := s.view.backend.(streamClient)
	if !ok {
		s.status.SetText(tr("连接服务尚未就绪"))
		return
	}
	s.dispose()
	epoch := s.epoch
	ctx, cancel := context.WithCancel(s.view.ctx)
	s.cancel = cancel
	s.start.Disable()
	s.start.Hide()
	s.visual.Show()
	s.speedView.Show()
	s.results.Show()
	s.stop.Show()
	s.results.RemoveAll()
	s.graph.samples = nil
	s.graph.family = ""
	s.graph.link = ""
	s.graph.Hide()
	s.speed.SetText("")
	s.reflow()
	s.scroll.ScrollToBottom()
	link := "all"
	if !s.selected["ipv6"] {
		link = "fn-connect"
	} else if !s.selected["fn-connect"] {
		link = "ipv6"
	}
	go func() {
		err := backend.Stream(ctx, client.MethodSpeedTest, client.SpeedTestRequest{Link: link}, func(raw json.RawMessage) (bool, error) {
			var status client.SpeedTestStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return false, err
			}
			fyne.Do(func() {
				if epoch == s.epoch {
					s.renderTest(status)
				}
			})
			return !status.Active, nil
		})
		fyne.Do(func() {
			if epoch != s.epoch {
				return
			}
			s.start.Enable()
			s.start.Show()
			s.start.SetText(tr("重新测试"))
			s.stop.Hide()
			s.hideLiveTest()
			if ctx.Err() != nil {
				s.status.SetText(tr("已取消"))
				s.statusView.Show()
			} else if err != nil {
				s.status.SetText(err.Error())
				s.statusView.Show()
			}
			s.reflow()
			s.scroll.ScrollToBottom()
		})
	}()
}
func (s *nativeSettings) renderTest(status client.SpeedTestStatus) {
	s.statusView.Show()
	phases := map[string]string{"preparing": tr("准备测试"), "upload-warmup": tr("正在预热上传"), "upload-waiting": tr("等待 NAS 接收数据"), "upload-confirming": tr("等待 NAS 完成确认"), "upload-complete": tr("上传完成"), "checking": tr("正在读取"), "connecting": tr("正在建立真实链路"), "verifying": tr("正在验证 NAS"), "latency": tr("正在测试延迟"), "download": tr("正在测试下载"), "upload": tr("正在测试上传"), "restoring": tr("正在恢复连接"), "completed": tr("测试完成"), "canceled": tr("已取消")}
	s.status.SetText(linkTitle(status.Link) + " · " + phases[status.Stage])
	s.speed.SetText(fmt.Sprintf("%.1f Mbps", status.Mbps))
	if status.Stage == "latency" {
		s.speed.SetText(fmt.Sprintf("%.1f ms", status.RTTMS))
	}
	if status.Stage == "upload-waiting" || status.Stage == "upload-confirming" || status.Stage == "upload-warmup" {
		s.speed.SetText(phases[status.Stage])
	}
	if status.Stage == "latency" || status.Stage == "download" || status.Stage == "upload" || status.Stage == "upload-complete" {
		s.speedView.Show()
	} else {
		s.speedView.Hide()
	}

	s.visual.update(status)
	s.graph.update(status)
	if status.Active {
		s.visual.Show()
	} else {
		s.hideLiveTest()
	}
	s.results.RemoveAll()
	if !status.Active {
		s.results.Add(widget.NewLabelWithStyle(tr("测试结果"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))
	}
	for _, r := range status.Results {
		title := widget.NewLabelWithStyle(linkTitle(r.Link), fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		s.results.Add(container.NewThemeOverride(title, textSizeTheme{theme.Current(), 14}))
		metrics := container.NewGridWithColumns(3)
		for _, field := range []struct {
			label string
			value *float64
			unit  string
		}{{tr("延迟"), r.LatencyMS, "ms"}, {tr("下载"), r.DownloadMbps, "Mbps"}, {tr("上传"), r.UploadMbps, "Mbps"}} {
			caption := widget.NewLabel(field.label)
			caption.Importance = widget.LowImportance
			text := "—"
			if field.value != nil {
				text = fmt.Sprintf("%.1f %s", *field.value, field.unit)
			}
			value := widget.NewLabel(text)
			value.TextStyle.Bold = true
			metrics.Add(container.NewVBox(container.NewThemeOverride(caption, textSizeTheme{theme.Current(), 11}), container.NewThemeOverride(value, textSizeTheme{theme.Current(), 13})))
		}
		s.results.Add(metrics)
		message := r.Message
		if message == "" {
			message = r.Error
		}
		if message != "" {
			label := widget.NewLabel(tr(message))
			label.Wrapping = fyne.TextWrapWord
			if r.Outcome == "failed" {
				label.Importance = widget.DangerImportance
			} else {
				label.Importance = widget.LowImportance
			}
			s.results.Add(container.NewThemeOverride(label, textSizeTheme{theme.Current(), 12}))
		}
	}

	if status.RestoreError != "" {
		s.results.Add(widget.NewLabel(status.RestoreError))
	}
	s.reflow()
	s.scroll.ScrollToBottom()
}

func (s *nativeSettings) reflow() {
	if s.contentTheme != nil {
		s.contentTheme.Refresh()
	}
	if s.scroll != nil {
		s.scroll.Content.Refresh()
		s.scroll.Refresh()
	}
}

func (s *nativeSettings) hideLiveTest() {
	if s.visual != nil {
		s.visual.stop()
		s.visual.Hide()
	}
	if s.graph != nil {
		s.graph.Hide()
	}
	if s.speedView != nil {
		s.speedView.Hide()
	}
}
