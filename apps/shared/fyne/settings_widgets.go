package main

import (
	"fyne.io/fyne/v2/layout"
	"image/color"
	"math"
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/rectcircle/fn-connect-private-network/internal/client"
)

func showTestConfirmation(window fyne.Window, text fyne.CanvasObject, start func()) *widget.PopUp {
	if window == nil {
		return nil
	}
	title := settingsSectionTitle(tr("开始网络链路测试？"))
	body := container.NewThemeOverride(text, textSizeTheme{theme.Current(), 14})
	popup := widget.NewModalPopUp(container.NewVBox(), window.Canvas())
	cancel := newTouchButton(tr("取消"), popup.Hide)
	confirm := newTouchButton(tr("开始测试"), func() { popup.Hide(); start() })
	confirm.Importance = widget.HighImportance
	buttons := container.NewHBox(layout.NewSpacer(), cancel, confirm)
	content := container.New(layout.NewCustomPaddedLayout(20, 20, 20, 20), container.NewVBox(title, fixedGap(12), body, fixedGap(16), buttons))
	popup.Content = content
	fitSettingsPopup(popup, min(float32(360), window.Canvas().Size().Width-40), window.Canvas().Size().Height-40)
	popup.Show()
	return popup
}

func (v *clientView) openBrowser(address string) {
	go func() {
		var err error
		if platform, ok := v.backend.(interface{ OpenBrowser(string) error }); ok {
			err = platform.OpenBrowser(address)
		} else {
			var parsed *url.URL
			parsed, err = url.Parse(address)
			if err == nil {
				err = fyne.CurrentApp().OpenURL(parsed)
			}
		}
		if err != nil {
			fyne.Do(func() {
				if v.ctx.Err() == nil && v.window != nil {
					v.toast(err.Error())
				}
			})
		}
	}()
}

type priorityGrip struct {
	widget.BaseWidget
	offset float32
	done   func(float32)
	move   func(float32)
}

func newPriorityGrip(done func(float32)) *priorityGrip {
	g := &priorityGrip{done: done}
	g.ExtendBaseWidget(g)
	return g
}
func (g *priorityGrip) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewCenter(widget.NewLabel("⠿")))
}
func (g *priorityGrip) Dragged(event *fyne.DragEvent) {
	g.offset += event.Dragged.DY
	if g.move != nil {
		g.move(g.offset)
	}
}
func (g *priorityGrip) DragEnd() {
	offset := g.offset
	g.offset = 0
	if g.done != nil {
		g.done(offset)
	}
}
func disabledGrip() fyne.CanvasObject {
	label := widget.NewLabel("⠿")
	label.Importance = widget.LowImportance
	return container.NewCenter(label)
}

// The diagram consumes daemon measurements; its clock only interpolates pixels.
type testDiagram struct {
	widget.BaseWidget
	status           client.SpeedTestStatus
	animation        *fyne.Animation
	phase            float32
	travel           float32
	animationStarted time.Time
}

// Inner contour runs in the opposite direction so the body stays hollow even
// after Fyne recolors fills. No inherited fill="none" is lost in SVG theming.
var nasDeviceIcon = theme.NewThemedResource(fyne.NewStaticResource("nas-device.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 32 32"><path fill="#000" d="M8 4H24L30 20V23A5 5 0 0 1 25 28H7A5 5 0 0 1 2 23V20ZM9.4 6L4 20.4V23A3 3 0 0 0 7 26H25A3 3 0 0 0 28 23V20.4L22.6 6Z"/><rect fill="#000" x="4" y="18" width="24" height="2"/><circle fill="#000" cx="24" cy="23" r="1.3"/></svg>`)))

var phoneIcon = fyne.NewStaticResource("phone.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 48 48"><rect x="13" y="3" width="22" height="42" rx="4" fill="none" stroke="#287ad7" stroke-width="3"/><path d="M21 8h6M22 39h4" stroke="#287ad7" stroke-width="3"/></svg>`))

func newTestDiagram() *testDiagram        { d := &testDiagram{}; d.ExtendBaseWidget(d); return d }
func (d *testDiagram) MinSize() fyne.Size { return fyne.NewSize(260, 90) }
func (d *testDiagram) CreateRenderer() fyne.WidgetRenderer {
	r := &diagramRenderer{diagram: d}
	for i := 0; i < 4; i++ {
		line := canvas.NewLine(theme.DisabledColor())
		line.StrokeWidth = 2
		r.paths = append(r.paths, line)
		r.objects = append(r.objects, line)
	}
	for i := 0; i < 8; i++ {
		arrow := canvas.NewLine(theme.DisabledColor())
		arrow.StrokeWidth = 2
		r.arrows = append(r.arrows, arrow)
		r.objects = append(r.objects, arrow)
	}
	for _, resource := range []fyne.Resource{phoneIcon, theme.StorageIcon(), nasDeviceIcon} {
		icon := canvas.NewImageFromResource(resource)
		icon.FillMode = canvas.ImageFillContain
		r.icons = append(r.icons, icon)
		r.objects = append(r.objects, icon)
	}
	for _, text := range []string{tr("当前设备"), tr("FN Connect 中转"), "NAS"} {
		label := canvas.NewText(text, theme.Color(theme.ColorNamePlaceHolder))
		label.TextSize = 12
		label.Alignment = fyne.TextAlignCenter
		r.labels = append(r.labels, label)
		r.objects = append(r.objects, label)
	}
	for i := 0; i < 3; i++ {
		dot := canvas.NewCircle(theme.PrimaryColor())
		r.dots = append(r.dots, dot)
		r.objects = append(r.objects, dot)
	}
	return r
}

func (d *testDiagram) stop() {
	if d.animation != nil {
		d.animation.Stop()
		d.animation = nil
	}
}
func (d *testDiagram) update(status client.SpeedTestStatus) {
	if d.status.Stage != status.Stage {
		d.animationStarted = time.Now()
	}
	d.status = status
	if status.Active && d.animation == nil {
		d.animationStarted = time.Now()
		d.animation = fyne.NewAnimation(time.Second, func(_ float32) {
			elapsed := time.Since(d.animationStarted).Seconds()
			d.phase = float32(math.Mod(elapsed, 1))
			d.travel = float32(math.Mod(elapsed*.65, 1))
			d.Refresh()
		})
		d.animation.RepeatCount = fyne.AnimationRepeatForever
		d.animation.Start()
	} else if !status.Active {
		d.stop()
	}
	d.Refresh()
}

type diagramRenderer struct {
	diagram *testDiagram
	objects []fyne.CanvasObject
	paths   []*canvas.Line
	arrows  []*canvas.Line
	dots    []*canvas.Circle
	icons   []*canvas.Image
	labels  []*canvas.Text
}

func (r *diagramRenderer) MinSize() fyne.Size           { return r.diagram.MinSize() }
func (r *diagramRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *diagramRenderer) Destroy()                     { r.diagram.stop() }
func (r *diagramRenderer) Refresh()                     { r.Layout(r.diagram.Size()); canvas.Refresh(r.diagram) }
func (r *diagramRenderer) Layout(size fyne.Size) {
	d := r.diagram
	if len(r.icons) == 0 {
		return
	}
	w := size.Width
	left, right := float32(40), w-40
	mid := w / 2
	relay := d.status.Link == "fn-connect"
	for i, x := range []float32{left, mid, right} {
		r.icons[i].Move(fyne.NewPos(x-22, 8))
		r.icons[i].Resize(fyne.NewSize(44, 44))
		r.labels[i].Move(fyne.NewPos(x-40, 58))
		r.labels[i].Resize(fyne.NewSize(80, 22))
		if i == 1 {
			r.labels[i].Move(fyne.NewPos(x-56, 58))
			r.labels[i].Resize(fyne.NewSize(112, 22))
		}
		if i == 1 && !relay {
			r.icons[i].Hide()
			r.labels[i].Hide()
		} else {
			r.icons[i].Show()
			r.labels[i].Show()
		}
	}
	segments := [][2]float32{{left + 28, right - 28}}
	if relay {
		segments = [][2]float32{{left + 28, mid - 28}, {mid + 28, right - 28}}
	}
	for _, line := range r.paths {
		line.Hide()
	}
	for _, arrow := range r.arrows {
		arrow.Hide()
	}
	upload, download := d.status.Stage == "upload", d.status.Stage == "download"
	latency := d.status.Stage == "latency"
	for i, segment := range segments {
		for direction := 0; direction < 2; direction++ {
			line := r.paths[i*2+direction]
			y := float32(26 + direction*12)
			line.Position1 = fyne.NewPos(segment[0], y)
			line.Position2 = fyne.NewPos(segment[1], y)
			line.StrokeColor = theme.DisabledColor()
			if upload && direction == 0 || download && direction == 1 || latency {
				line.StrokeColor = theme.PrimaryColor()
			}
			tip, tail := segment[1], float32(-4)
			if direction == 1 {
				tip = segment[0]
				tail = 4
			}
			for arm := 0; arm < 2; arm++ {
				arrow := r.arrows[(i*2+direction)*2+arm]
				dy := float32(-4)
				if arm == 1 {
					dy = 4
				}
				arrow.Position1 = fyne.NewPos(tip+tail, y+dy)
				arrow.Position2 = fyne.NewPos(tip, y)
				arrow.StrokeColor = line.StrokeColor
				arrow.Show()
				arrow.Refresh()
			}
			line.Show()
			line.Refresh()
		}
	}
	for i, dot := range r.dots {
		dot.Hide()
		if !d.status.Active || (!upload && !download && !latency) {
			continue
		}
		phase := float32(math.Mod(float64(d.travel)+float64(i)/3, 1))
		back := download
		if latency {
			if i > 0 {
				continue
			}
			cycle := float64(d.phase)
			if cycle > .5 {
				continue
			}
			back = cycle >= .25
			t := math.Mod(cycle, .25) / .25
			phase = float32((1 - math.Cos(t*math.Pi)) / 2)
		}
		if back {
			phase = 1 - phase
		}
		x := left + 28 + (right-left-56)*phase
		if relay && x > mid-28 && x < mid+28 {
			continue
		}
		y := float32(26)
		if back {
			y = 38
		}
		dot.Move(fyne.NewPos(x-3, y-3))
		dot.Resize(fyne.NewSize(6, 6))
		dot.Show()
	}
}

// A full-width navigation row: leading label and an independent trailing chevron.
// Button supplies keyboard, focus and tap behavior; layout is not a centered action button.
type settingsMenuRow struct{ widget.Button }

func newSettingsMenuRow(title string, tapped func()) *settingsMenuRow {
	row := &settingsMenuRow{}
	row.Text = title
	row.OnTapped = tapped
	row.ExtendBaseWidget(row)
	return row
}
func (row *settingsMenuRow) CreateRenderer() fyne.WidgetRenderer {
	label := widget.NewLabel(row.Text)
	label.Alignment = fyne.TextAlignLeading
	chevron := canvas.NewImageFromResource(theme.NewThemedResource(fyne.NewStaticResource("chevron-right.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16"><path fill="#000" d="m5.5 3 1-1 6 6-6 6-1-1 5-5z"/></svg>`))))
	chevron.FillMode = canvas.ImageFillContain
	content := container.New(&settingsMenuRowLayout{}, label, chevron)
	return widget.NewSimpleRenderer(container.NewStack(settingsSurface(), content))
}

type settingsMenuRowLayout struct{}

func (settingsMenuRowLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(objects[0].MinSize().Width+64, 56)
}
func (settingsMenuRowLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	height := objects[0].MinSize().Height
	objects[0].Move(fyne.NewPos(12, (size.Height-height)/2))
	objects[0].Resize(fyne.NewSize(max(float32(0), size.Width-56), height))
	objects[1].Move(fyne.NewPos(size.Width-28, (size.Height-16)/2))
	objects[1].Resize(fyne.NewSize(16, 16))
}

// Cards follow system appearance; light mode uses a white surface on the page background.
func settingsSurface() fyne.CanvasObject {
	surface := newThemeSurface(theme.ColorNameInputBackground)
	surface.radius = 14
	return surface
}
func settingsCard(content fyne.CanvasObject) fyne.CanvasObject {
	return container.NewStack(settingsSurface(), container.New(layout.NewCustomPaddedLayout(14, 14, 14, 14), content))
}

type settingsTestControlsLayout struct{}

func (settingsTestControlsLayout) MinSize([]fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(280, 44)
}
func (settingsTestControlsLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	labelWidth := objects[0].MinSize().Width
	actionWidth := max(float32(70), objects[2].MinSize().Width)
	gap := float32(8)
	selectX := labelWidth + gap
	selectWidth := max(float32(0), size.Width-selectX-gap-actionWidth)
	objects[0].Move(fyne.NewPos(0, (size.Height-objects[0].MinSize().Height)/2))
	objects[0].Resize(fyne.NewSize(labelWidth, objects[0].MinSize().Height))
	objects[1].Move(fyne.NewPos(selectX, 0))
	objects[1].Resize(fyne.NewSize(selectWidth, size.Height))
	objects[2].Move(fyne.NewPos(size.Width-actionWidth, 0))
	objects[2].Resize(fyne.NewSize(actionWidth, size.Height))
}
func showSettingsHint(window fyne.Window, anchor fyne.CanvasObject, text *widget.Label) *widget.PopUp {
	if window == nil {
		return nil
	}
	popup := widget.NewPopUp(container.NewThemeOverride(container.NewPadded(text), textSizeTheme{theme.Current(), 13}), window.Canvas())
	width := min(float32(280), window.Canvas().Size().Width-24)
	fitSettingsPopup(popup, width, window.Canvas().Size().Height-24)
	point := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor)
	point.X = max(float32(12), min(point.X+anchor.Size().Width-width, window.Canvas().Size().Width-width-12))
	anchorY := point.Y
	point.Y += anchor.Size().Height + 6
	if point.Y+popup.Size().Height > window.Canvas().Size().Height-12 {
		point.Y = max(float32(12), anchorY-popup.Size().Height-6)
	}
	popup.ShowAtPosition(point)
	return popup
}

type settingsControlsTheme struct{ fyne.Theme }

func (t settingsControlsTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNameButton {
		if variant == theme.VariantDark {
			return color.NRGBA{R: 54, G: 67, B: 90, A: 255}
		}
		return color.NRGBA{R: 225, G: 234, B: 250, A: 255}
	}
	return t.Theme.Color(name, variant)
}

// A fixed-height sparkline is independent of topology and live typography.
// Renderer geometry belongs to the renderer, so rebuilding it cannot leave stale text.
type throughputSample struct {
	elapsedMS int64
	mbps      float64
}
type testSparkline struct {
	widget.BaseWidget
	samples      []throughputSample
	family, link string
	windowMS     int64
}

func newTestSparkline() *testSparkline {
	g := &testSparkline{windowMS: 8000}
	g.ExtendBaseWidget(g)
	return g
}
func (g *testSparkline) MinSize() fyne.Size { return fyne.NewSize(260, 52) }
func (g *testSparkline) update(status client.SpeedTestStatus) {
	if !status.Active {
		g.Hide()
		return
	}
	family := status.Stage
	if strings.HasPrefix(family, "upload") {
		family = "upload"
	}
	if status.Link != g.link || (family == "download" || family == "upload") && family != g.family {
		g.samples = nil
	}
	g.link = status.Link
	if family == "download" || family == "upload" {
		g.family = family
		if status.TargetSampleMS > 0 {
			g.windowMS = status.TargetSampleMS
		}
		if status.SampleElapsedMS > 0 && (len(g.samples) == 0 || status.SampleElapsedMS > g.samples[len(g.samples)-1].elapsedMS) {
			g.samples = append(g.samples, throughputSample{elapsedMS: status.SampleElapsedMS, mbps: max(0, status.Mbps)})
			cutoff := max(int64(0), status.SampleElapsedMS-g.windowMS)
			for len(g.samples) > 1 && g.samples[1].elapsedMS <= cutoff {
				g.samples = g.samples[1:]
			}
			// Bound renderer resources independently of timestamp-based placement.
			if len(g.samples) > 512 {
				g.samples = g.samples[len(g.samples)-512:]
			}
		}
	}
	if len(g.samples) > 0 {
		g.Show()
	} else {
		g.Hide()
	}
	g.Refresh()
}

type sparklineRenderer struct {
	owner    *testSparkline
	lines    []*canvas.Line
	baseline *canvas.Line
	objects  []fyne.CanvasObject
}

func (g *testSparkline) CreateRenderer() fyne.WidgetRenderer {
	r := &sparklineRenderer{owner: g}
	r.baseline = canvas.NewLine(theme.Color(theme.ColorNameInputBorder))
	r.baseline.StrokeWidth = 1
	r.objects = append(r.objects, r.baseline)
	return r
}
func (r *sparklineRenderer) MinSize() fyne.Size           { return r.owner.MinSize() }
func (r *sparklineRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *sparklineRenderer) Destroy()                     {}
func (r *sparklineRenderer) Refresh()                     { r.Layout(r.owner.Size()); canvas.Refresh(r.owner) }
func (r *sparklineRenderer) Layout(size fyne.Size) {
	g := r.owner
	maximum := 1.0
	for _, sample := range g.samples {
		maximum = math.Max(maximum, sample.mbps)
	}
	for len(r.lines) < len(g.samples)-1 {
		line := canvas.NewLine(theme.PrimaryColor())
		line.StrokeWidth = 1.5
		r.lines = append(r.lines, line)
		r.objects = append(r.objects, line)
	}
	window := max(int64(1), g.windowMS)
	origin := int64(0)
	if len(g.samples) > 0 {
		origin = max(int64(0), g.samples[len(g.samples)-1].elapsedMS-window)
	}
	for i, line := range r.lines {
		line.Hide()
		if i+1 >= len(g.samples) {
			continue
		}
		a, b := g.samples[i], g.samples[i+1]
		if a.elapsedMS < origin {
			fraction := float64(origin-a.elapsedMS) / float64(b.elapsedMS-a.elapsedMS)
			a.mbps += (b.mbps - a.mbps) * fraction
			a.elapsedMS = origin
		}
		line.Position1 = fyne.NewPos(float32(a.elapsedMS-origin)/float32(window)*size.Width, 42-float32(a.mbps/maximum)*36)
		line.Position2 = fyne.NewPos(float32(b.elapsedMS-origin)/float32(window)*size.Width, 42-float32(b.mbps/maximum)*36)
		line.Show()
		line.Refresh()
	}
	r.baseline.Position1 = fyne.NewPos(0, 50)
	r.baseline.Position2 = fyne.NewPos(size.Width, 50)
	r.baseline.Refresh()
}

// Wrapped text height depends on its final width. Lay out at that width before
// measuring; do not reserve an arbitrary height below short content.
func fitSettingsPopup(popup *widget.PopUp, width, maxHeight float32) {
	content := popup.Content
	content.Resize(fyne.NewSize(width, 0))
	content.Refresh()
	height := content.MinSize().Height
	for pass := 0; pass < 4; pass++ {
		content.Resize(fyne.NewSize(width, height))
		content.Refresh()
		next := content.MinSize().Height
		if math.Abs(float64(next-height)) < .5 {
			height = next
			break
		}
		height = next
	}
	if height > maxHeight {
		popup.Content = container.NewVScroll(content)
		height = maxHeight
	}
	popup.Resize(fyne.NewSize(width, height))
}

func settingsSectionTitle(text string) fyne.CanvasObject {
	return container.NewThemeOverride(widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), textSizeTheme{theme.Current(), 18})
}

// linkSwitch retains keyboard, focus and disabled behavior from the toolkit,
// while rendering the link preference as a switch rather than a checkbox.
type linkSwitch struct{ widget.Check }

func newLinkSwitch(label string) *linkSwitch {
	s := &linkSwitch{}
	s.Text = label
	s.ExtendBaseWidget(s)
	return s
}
func (s *linkSwitch) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(theme.Color(theme.ColorNameDisabled))
	track.CornerRadius = 12
	knob := canvas.NewCircle(color.White)
	r := &linkSwitchRenderer{owner: s, track: track, knob: knob}
	r.Refresh()
	return r
}

type linkSwitchRenderer struct {
	owner *linkSwitch
	track *canvas.Rectangle
	knob  *canvas.Circle
}

func (r *linkSwitchRenderer) Layout(size fyne.Size) {
	y := (size.Height - 24) / 2
	r.track.Move(fyne.NewPos(4, y))
	r.track.Resize(fyne.NewSize(40, 24))
	x := float32(7)
	if r.owner.Checked {
		x = 23
	}
	r.knob.Move(fyne.NewPos(x, y+3))
	r.knob.Resize(fyne.NewSize(18, 18))
}
func (r *linkSwitchRenderer) MinSize() fyne.Size { return fyne.NewSize(48, 44) }
func (r *linkSwitchRenderer) Refresh() {
	c := theme.Color(theme.ColorNameDisabled)
	if r.owner.Checked {
		c = theme.Color(theme.ColorNamePrimary)
	}
	if r.owner.Disabled() {
		cr, cg, cb, _ := c.RGBA()
		c = color.NRGBA{uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), 95}
	}
	r.track.FillColor = c
	r.Layout(r.owner.Size())
	r.track.Refresh()
	r.knob.Refresh()
}
func (r *linkSwitchRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.track, r.knob}
}
func (r *linkSwitchRenderer) Destroy() {}
