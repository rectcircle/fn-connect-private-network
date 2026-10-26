package main

import (
	"context"
	"errors"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
	"github.com/rectcircle/fn-connect-private-network/internal/model"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionAndLoginNavigation(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(clientTheme{theme.DefaultTheme()})
	for _, state := range []model.ClientState{model.ClientUnconfigured, model.ClientDirect, model.ClientPaused, model.ClientAuthRequired} {
		view := newClientView(nil)
		defer view.cancel()
		view.showStatus(model.ClientStatus{State: state, Path: "ipv6"})
		login := needsLogin(state)
		view.settlePage(view.editing)
		if view.accountPanel.Visible() != login || view.mainPanel.Visible() == login {
			t.Fatalf("incorrect page for %s", state)
		}
		if state == model.ClientDirect && view.primary.Text != "断开连接" {
			t.Fatal("connected screen must offer disconnect")
		}
		if state == model.ClientPaused && view.primary.Text != "连接" {
			t.Fatal("paused screen must offer connect")
		}
		if directory := os.Getenv("FNCPN_SCREENSHOT_DIR"); directory != "" {
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			c := software.NewCanvas()
			c.SetContent(view.content)
			c.Resize(fyne.NewSize(390, 820))
			file, err := os.Create(filepath.Join(directory, string(state)+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, c.Capture())
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestEditingLoginSurvivesBackgroundUpdatesAndRetainsIdentity(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.showStatus(model.ClientStatus{State: model.ClientDirect})
	v.showReport(model.ClientDiagnostics{FNID: "home", Username: "owner"})
	v.showLogin(false)
	v.settlePage(true)
	if v.fnID.Text != "home" || v.username.Text != "owner" {
		t.Fatal("relogin must prefill existing identity")
	}
	v.password.SetText("temporary-secret")
	v.showStatus(model.ClientStatus{State: model.ClientDirect})
	if !v.accountPanel.Visible() {
		t.Fatal("background status replaced login page")
	}
	v.showMain()
	v.settlePage(false)
	if v.password.Text != "" {
		t.Fatal("return retained password")
	}
	v.showLogin(true)
	v.settlePage(true)
	v.showStatus(model.ClientStatus{State: model.ClientDirect})
	if v.fnID.Text != "" || v.username.Text != "" || !v.accountPanel.Visible() {
		t.Fatal("change NAS must keep a blank login page")
	}
	if v.report.FNID != "home" {
		t.Fatal("entering login must not erase current NAS")
	}
}
func TestDrawerWidthAndOffscreenPosition(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	l := drawerLayout{progress: 1}
	left, right := widgetStub(), widgetStub()
	l.Layout([]fyne.CanvasObject{left, right}, fyne.NewSize(400, 800))
	if right.Size().Width != 312 || right.Position().X != 0 || right.Size().Height != 800 {
		t.Fatal("drawer must occupy 78 percent on phones")
	}
	l.progress = 0
	l.Layout([]fyne.CanvasObject{left, right}, fyne.NewSize(400, 800))
	if right.Position().X != -312 {
		t.Fatal("closed drawer must be offscreen")
	}
}
func widgetStub() fyne.CanvasObject { return widget.NewLabel("") }

type exitBackend struct {
	calls   []string
	failure error
}

func (b *exitBackend) Call(_ context.Context, method string, _, _ any) error {
	b.calls = append(b.calls, method)
	return b.failure
}
func (b *exitBackend) Exit() error { b.calls = append(b.calls, "exit"); return nil }
func TestQuitRequiresSuccessfulPauseBeforeFinishingActivity(t *testing.T) {
	b := &exitBackend{}
	v := &clientView{backend: b}
	if err := v.pauseAndExit(context.Background(), true); err != nil || strings.Join(b.calls, ",") != client.MethodDisconnect+",exit" {
		t.Fatal("must pause before exit", b.calls, err)
	}
	b.calls = nil
	b.failure = errors.New("cannot stop forwarding")
	if err := v.pauseAndExit(context.Background(), true); err == nil || strings.Join(b.calls, ",") != client.MethodDisconnect {
		t.Fatal("failed disconnect must keep app open", b.calls, err)
	}
}

func TestBackClosesDrawerBeforeLeavingPage(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.window = app.NewWindow("test")
	v.window.SetContent(v.content)
	v.showStatus(model.ClientStatus{State: model.ClientDirect})
	v.settlePage(false)
	v.showMenu()
	v.back()
	if v.editing {
		t.Fatal("dismissal changed page")
	}
	v.drawer.Dispose()
}
func TestThemeHasPrimaryAndSecondaryContrast(t *testing.T) {
	palette := clientTheme{theme.DefaultTheme()}
	for _, variant := range []fyne.ThemeVariant{theme.VariantLight, theme.VariantDark} {
		if palette.Color(theme.ColorNameForeground, variant) == palette.Color(theme.ColorNameDisabled, variant) {
			t.Fatal("text hierarchy missing")
		}
	}
}
func TestPageTransitionPreservesPageSize(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	l := pageLayout{progress: 0.5}
	main, login := widgetStub(), widgetStub()
	l.Layout([]fyne.CanvasObject{main, login}, fyne.NewSize(400, 800))
	if main.Position().X != -50 || login.Position().X != 200 || login.Size().Width != 400 {
		t.Fatal("incorrect page translation")
	}
}

func TestSurfaceFollowsAppearanceChanges(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	app.Settings().SetTheme(clientTheme{theme.DefaultTheme()})
	surface := newThemeSurface(theme.ColorNameBackground)
	renderer := surface.CreateRenderer().(*surfaceRenderer)
	app.Settings().SetTheme(theme.LightTheme())
	renderer.Refresh()
	light := renderer.rectangle.FillColor
	app.Settings().SetTheme(theme.DarkTheme())
	renderer.Refresh()
	if light == renderer.rectangle.FillColor {
		t.Fatal("surface retained startup color")
	}
}

type backgroundBackend struct {
	exitBackend
	background bool
}

func (b *backgroundBackend) Background() error { b.background = true; return nil }

type systemLifetimeBackend struct{ exitBackend }

func (*systemLifetimeBackend) CanExit() bool { return false }

func TestRootLoginMenuOnExitCapablePlatform(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	b := &exitBackend{}
	v := newClientView(b)
	defer v.cancel()
	v.window = app.NewWindow("navigation")
	v.window.SetContent(v.content)
	for _, state := range []model.ClientState{model.ClientUnconfigured, model.ClientAuthRequired} {
		v.showStatus(model.ClientStatus{State: state})
		v.navigate()
		if !v.navigation.Visible() || v.navigation.Icon != theme.MenuIcon() || v.drawer == nil || !v.drawer.visible || len(b.calls) != 0 {
			t.Fatal("unconfigured and expired authorization must allow menu access without exiting")
		}
		v.back()
		if v.drawer.visible || len(b.calls) != 0 {
			t.Fatal("system back must dismiss the open menu first")
		}
	}
}

func TestLoginEntriesRevealWithinReducedViewport(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	w := app.NewWindow("keyboard")
	w.SetContent(v.accountPanel)
	w.Resize(fyne.NewSize(420, 280))
	for _, challenge := range []bool{false, true} {
		v.account.Show()
		v.challenge.Hide()
		entries := []*loginEntry{v.fnID, v.username, v.password}
		if challenge {
			v.account.Hide()
			v.challenge.Show()
			entries = []*loginEntry{v.accessCode, v.otp}
		}
		// Move between fields in both directions with the keyboard already open.
		entries = append(entries, entries[0])
		for _, entry := range entries {
			if entry.onFocus == nil {
				t.Fatal("every login field must reveal itself on focus")
			}
			v.revealLoginEntry(entry)
			y := app.Driver().AbsolutePositionForObject(entry).Y - app.Driver().AbsolutePositionForObject(v.loginScroll).Y
			if y < 0 || y+entry.Size().Height > v.loginScroll.Size().Height {
				t.Fatalf("focused field outside reduced viewport: y=%v height=%v viewport=%v", y, entry.Size().Height, v.loginScroll.Size().Height)
			}
		}
	}
}

func TestSystemLifetimeRootNavigationKeepsWindowAlive(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	b := &systemLifetimeBackend{}
	v := newClientView(b)
	defer v.cancel()
	v.window = app.NewWindow("navigation")
	v.window.SetContent(v.content)
	v.showStatus(model.ClientStatus{State: model.ClientUnconfigured})
	v.navigate()
	if !v.navigation.Visible() || v.navigation.Icon != theme.MenuIcon() || v.drawer == nil || !v.drawer.visible || v.ctx.Err() != nil || len(b.calls) != 0 {
		t.Fatal("root login must open the menu without quitting or issuing commands")
	}
	v.back()
	if v.drawer.visible || v.ctx.Err() != nil {
		t.Fatal("back must dismiss the menu and keep the root login page alive")
	}
	v.showStatus(model.ClientStatus{State: model.ClientDirect})
	v.showLogin(false)
	if !v.navigation.Visible() {
		t.Fatal("login entered from an existing connection must retain back navigation")
	}
	if err := v.pauseAndExit(context.Background(), false); err != nil || len(b.calls) != 0 {
		t.Fatal("system-owned lifetime must never call Exit")
	}
}
func TestMainBackDoesNotPauseConnection(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	b := &backgroundBackend{}
	v := newClientView(b)
	defer v.cancel()
	v.current.State = model.ClientDirect
	v.editing = false
	// The platform invocation runs outside the UI loop.
	done := make(chan struct{})
	v.background = func() { b.Background(); close(done) }
	v.back()
	<-done
	if !b.background || len(b.calls) != 0 {
		t.Fatal("back must only background the task")
	}
}
func TestIdentitySeparatorAndDiagnosticErrorStyle(t *testing.T) {
	if identityText("home", "owner") != "home · owner" || identityText("home", "") != "home" {
		t.Fatal("identity separators")
	}
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.showReport(model.ClientDiagnostics{})
	if v.diagnosticBody.Importance != widget.MediumImportance {
		t.Fatal("healthy diagnostics must use normal text")
	}
	v.showReport(model.ClientDiagnostics{Errors: []*model.Error{model.NewError(model.ErrorUnavailable, "unavailable", true)}})
	if v.diagnosticBody.Importance != widget.DangerImportance {
		t.Fatal("errors must use danger text")
	}
}

func TestColdStartLongDNSDoesNotOverlapActions(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	view := newClientView(nil)
	defer view.cancel()
	window := app.NewWindow("layout")
	view.window = window
	window.SetContent(view.content)
	window.Resize(fyne.NewSize(390, 820))
	window.Show()
	for _, suffix := range []string{"", strings.Repeat("long-device-name", 5) + ".fncpn", "home.fncpn"} {
		view.applyStatus(model.ClientStatus{State: model.ClientLocal, DNS: model.DNSStatus{State: "ready", Suffix: suffix}})
		view.applyReport(model.ClientDiagnostics{NASAddress: "192.168.71.2"}, "{}")
		view.mainPanel.Refresh()
		view.settlePage(false)
		ip := view.values["NAS IP"]
		top := app.Driver().AbsolutePositionForObject(ip)
		actions := app.Driver().AbsolutePositionForObject(view.primary)
		if top.Y+ip.Size().Height > actions.Y {
			t.Fatalf("NAS row overlaps actions after DNS %q: bottom=%v action=%v", suffix, top.Y+ip.Size().Height, actions.Y)
		}
	}
}

func TestFirstSnapshotDoesNotAnimateAndUnchangedReportDoesNotRelayout(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.applyStatus(model.ClientStatus{State: model.ClientDirect})
	report := model.ClientDiagnostics{FNID: "home", Username: "owner", NASAddress: "192.168.71.2"}
	v.applyReport(report, "first")
	v.layoutDirty = false
	if v.pageAnimation != nil || v.loading.Visible() || !v.mainPanel.Visible() {
		t.Fatal("first snapshot must settle without a page animation")
	}
	v.applyStatus(model.ClientStatus{State: model.ClientDirect})
	v.applyReport(report, "updated diagnostic timestamp")
	if v.layoutDirty {
		t.Fatal("unchanged displayed values must not trigger a full layout")
	}
	if v.diagnosticText != "updated diagnostic timestamp" {
		t.Fatal("clipboard diagnostics must remain current")
	}
}

type waitingStatusBackend struct {
	started  chan struct{}
	finished chan struct{}
}

func (b *waitingStatusBackend) Call(ctx context.Context, method string, _, _ any) error {
	close(b.started)
	<-ctx.Done()
	close(b.finished)
	return ctx.Err()
}
func TestReadOnlyRefreshDoesNotDisableControls(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	backend := &waitingStatusBackend{started: make(chan struct{}), finished: make(chan struct{})}
	v := newClientView(backend)
	v.refresh()
	<-backend.started
	for _, button := range v.actions {
		if button.Disabled() {
			t.Fatal("read-only refresh must not dim action buttons")
		}
	}
	if v.navigation.Disabled() {
		t.Fatal("read-only refresh must not disable navigation")
	}
	v.cancel()
	<-backend.finished
}

func TestMenuReusesPreparedPanel(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.window = app.NewWindow("test")
	v.window.SetContent(v.content)
	v.prepareMenu()
	prepared := v.drawer
	if prepared == nil {
		t.Fatal("menu must be prepared before interaction")
	}
	for i := 0; i < 2; i++ {
		v.showMenu()
		if v.drawer != prepared || !v.drawer.visible {
			t.Fatal("opening rebuilt the menu")
		}
		v.drawer.Dispose()
	}
}

func TestExternalNavigationLeavesPageIntact(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	v := newClientView(nil)
	defer v.cancel()
	v.message.SetText("keep current page")
	v.message.Show()
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	v.openExternal(func(context.Context) error {
		close(started)
		<-release
		close(finished)
		return nil
	})
	<-started
	for _, button := range v.actions {
		if button.Disabled() {
			t.Error("external navigation dimmed page controls")
		}
	}
	if v.navigation.Disabled() || !v.message.Visible() {
		t.Error("external navigation changed page")
	}
	close(release)
	<-finished
}
