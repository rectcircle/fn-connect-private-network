package main

import (
	"bytes"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"image"
	"strings"
	"testing"
)

func TestNativeSettingsUsesWindowBackStack(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(clientTheme{theme.DefaultTheme()})
	view := newClientView(nil)
	defer view.cancel()
	view.showStatus(model.ClientStatus{State: model.ClientPaused})
	defer view.settings.dispose()
	view.openSettings()
	if view.settings.page != "menu" || !view.settings.panel.Visible() {
		t.Fatal("mobile settings must open menu in native window")
	}
	if view.headerTitle.Text != tr("设置") || view.headerIcon.Visible() || view.headerVersion.Visible() {
		t.Fatal("settings must replace the product header")
	}
	view.settings.open("about")
	if view.headerTitle.Text != tr("关于") {
		t.Fatal("detail title must follow navigation")
	}
	view.back()
	if view.settings.page != "menu" || !view.settings.panel.Visible() {
		t.Fatal("system back must return to settings menu")
	}
	view.back()
	if view.settings.panel.Visible() {
		t.Fatal("second back must return to main window")
	}
	if view.headerTitle.Text != "FnCPN" || !view.headerIcon.Visible() || !view.headerVersion.Visible() {
		t.Fatal("main header was not restored")
	}
}

func TestSettingsInitialPreferencesDoNotTriggerSave(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newClientView(nil)
	defer view.cancel()
	defer view.settings.dispose()
	s := view.settings
	s.rows = container.NewVBox()
	s.status = widget.NewLabel("")
	s.preferences = client.LinkSettings{FNID: "saved-nas", Links: []client.LinkPreference{{Link: "ipv6", Enabled: true}, {Link: "fn-connect", Enabled: true}}}
	// No backend is attached: rendering initial enabled values must not invoke
	// persistence. Previously SetChecked re-entered save/renderRows indefinitely.
	s.renderRows()
	if len(s.rows.Objects) != 3 {
		t.Fatal("expected LAN and two remote links")
	}
	for _, object := range s.rows.Objects {
		row := object.(*fyne.Container)
		found := false
		for _, child := range row.Objects {
			if check, ok := child.(*linkSwitch); ok {
				found = true
				if !check.Checked {
					t.Fatal("saved enabled value not rendered")
				}
			}
		}
		if !found {
			t.Fatal("missing link switch")
		}
	}
}

func TestDiagramRendererRecreationDoesNotAccumulateLabels(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	diagram := newTestDiagram()
	first := diagram.CreateRenderer().(*diagramRenderer)
	defer first.Destroy()
	count := len(first.Objects())
	second := diagram.CreateRenderer().(*diagramRenderer)
	defer second.Destroy()
	second.Layout(fyne.NewSize(330, 90))
	if len(second.Objects()) != count {
		t.Fatal("renderer recreation retained stale graphics")
	}
	for i, label := range second.labels {
		if label == first.labels[i] || label.Position().Y != 58 {
			t.Fatal("device label was reused or left at the origin")
		}
	}
}

func TestCompletedTestHidesLiveVisualsAndKeepsResults(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	s := &nativeSettings{status: widget.NewLabel(""), speed: widget.NewLabel(""), visual: newTestDiagram(), graph: newTestSparkline(), results: container.NewVBox()}
	s.statusView = container.NewThemeOverride(s.status, theme.DefaultTheme())
	s.speedView = container.NewThemeOverride(s.speed, theme.DefaultTheme())
	s.scroll = container.NewVScroll(container.NewVBox(s.statusView, s.visual, s.speedView, s.graph, s.results))
	defer s.hideLiveTest()
	status := client.SpeedTestStatus{Active: true, Stage: "upload", Link: "fn-connect", Mbps: 3.7, SampleElapsedMS: 1000, TargetSampleMS: 8000, Results: []client.SpeedTestResult{{Link: "fn-connect"}}}
	s.renderTest(status)
	if !s.visual.Visible() || !s.graph.Visible() || !s.speedView.Visible() {
		t.Fatal("missing active measurement presentation")
	}
	status.Active = false
	status.Stage = "completed"
	s.renderTest(status)
	if s.visual.Visible() || s.graph.Visible() || s.speedView.Visible() || s.visual.animation != nil {
		t.Fatal("completed test retained live animation or graph")
	}
	if len(s.results.Objects) == 0 {
		t.Fatal("completion discarded result presentation")
	}
}

func TestNASIconRemainsHollowAfterThemeColorization(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	icon, err := oksvg.ReadIconStream(bytes.NewReader(nasDeviceIcon.Content()))
	if err != nil {
		t.Fatal(err)
	}
	pixels := image.NewRGBA(image.Rect(0, 0, 64, 64))
	icon.SetTarget(0, 0, 64, 64)
	icon.Draw(rasterx.NewDasher(64, 64, rasterx.NewScannerGV(64, 64, pixels, pixels.Bounds())), 1)
	if pixels.RGBAAt(32, 24).A != 0 {
		t.Fatal("NAS body became a solid block after theming")
	}
	if pixels.RGBAAt(32, 10).A == 0 || pixels.RGBAAt(32, 38).A == 0 {
		t.Fatal("NAS outline or front divider missing")
	}
}

func TestSettingsPopupsFitContentAtFinalWidth(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	window := app.NewWindow("test")
	window.Resize(fyne.NewSize(390, 780))
	anchor := widget.NewButton("?", nil)
	window.SetContent(anchor)
	text := widget.NewLabel("Short explanation.")
	text.Wrapping = fyne.TextWrapWord
	hint := showSettingsHint(window, anchor, text)
	if hint.Size().Height >= 110 {
		t.Fatal("short hint retained fixed empty space", hint.Size())
	}
	hint.Hide()
	confirmation := showTestConfirmation(window, text, func() {})
	if confirmation.Size().Height >= 300 {
		t.Fatal("short confirmation retained fixed empty space", confirmation.Size())
	}
	confirmation.Hide()
	longText := widget.NewLabel(strings.Repeat("A longer explanation needs wrapping. ", 100))
	longText.Wrapping = fyne.TextWrapWord
	longHint := showSettingsHint(window, anchor, longText)
	defer longHint.Hide()
	if longHint.Size().Height > window.Canvas().Size().Height-24 {
		t.Fatal("long hint exceeds available viewport")
	}
}

func TestSparklineUsesDaemonTimeInsteadOfEventCount(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	graph := newTestSparkline()
	for _, elapsed := range []int64{250, 1000, 4000, 8000} {
		graph.update(client.SpeedTestStatus{Active: true, Stage: "download", Link: "ipv6", Mbps: 4, SampleElapsedMS: elapsed, TargetSampleMS: 8000})
	}
	renderer := graph.CreateRenderer().(*sparklineRenderer)
	defer renderer.Destroy()
	renderer.Layout(fyne.NewSize(320, 52))
	if len(renderer.lines) != 3 || renderer.lines[2].Position2.X != 320 || renderer.lines[0].Position2.X != 40 {
		t.Fatal("curve positions depend on event count instead of measurement time")
	}
	graph.update(client.SpeedTestStatus{Active: true, Stage: "download", Link: "ipv6", Mbps: 4, SampleElapsedMS: 10000, TargetSampleMS: 8000})
	renderer.Layout(fyne.NewSize(320, 52))
	if renderer.lines[len(graph.samples)-2].Position2.X != 320 {
		t.Fatal("elapsed window did not scroll")
	}
	count := len(graph.samples)
	graph.update(client.SpeedTestStatus{Active: true, Stage: "download", Link: "ipv6", Mbps: 4, SampleElapsedMS: 10000, TargetSampleMS: 8000})
	if len(graph.samples) != count {
		t.Fatal("duplicate heartbeat became a throughput sample")
	}
}
