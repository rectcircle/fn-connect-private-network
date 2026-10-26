"""Pinned mobile renderer overlay: pre-upload menu text/images without publishing it."""
from pathlib import Path


def prepare(source: Path, local: Path):
    canvas_path = local / 'internal/driver/mobile/canvas.go'
    text = canvas_path.read_text()
    marker = 'type canvas struct {'
    if text.count(marker) != 1:
        raise SystemExit('Review Fyne mobile canvas overlay')
    text = text.replace(marker, marker + '\n prewarmObjects []fyne.CanvasObject\n perfObserver func(string,time.Duration,int)\n', 1)
    focus_marker='\t\tif wid, ok := co.(fyne.Focusable); !ok || wid != c.Focused() {\n\t\t\tc.Unfocus()\n\t\t}'
    if text.count(focus_marker)!=1:raise SystemExit('Review keyboard tap focus hook')
    text=text.replace(focus_marker,'''
        if wid, ok := co.(fyne.Focusable); !ok || wid != c.Focused() {
            policy,deferFocus:=co.(interface{DeferUnfocusOnTap() bool})
            if !deferFocus || !policy.DeferUnfocusOnTap() {c.Unfocus()}
        }''',1)

    text += '''
// Prewarm queues detached menu primitives for the next real GL frame.
// Call on the Fyne event goroutine. No temporary overlay or focus change.
func (c *canvas) SetPerfObserver(observer func(string,time.Duration,int)) {c.perfObserver=observer;textpaint.SetFontPerfObserver(observer)}
func (c *canvas) Prewarm(root fyne.CanvasObject) {
 for _,queued:=range c.prewarmObjects {if queued==root{return}}
 c.prewarmObjects=append(c.prewarmObjects,root)
 c.SetDirty()
}
func (c *canvas) warmPrimitives() {
 roots:=c.prewarmObjects
 if len(roots)==0{return}
 started:=time.Now();primitives:=0
 c.prewarmObjects=nil
 for _,root:=range roots {
  intdriver.WalkVisibleObjectTree(root,func(obj fyne.CanvasObject,pos,clipPos fyne.Position,clipSize fyne.Size) bool {
   switch obj.(type) {
   case *canvaspkg.Text,*canvaspkg.Image:
    primitives++
    c.Painter().Paint(obj,fyne.NewPos(0,0),c.Size(),nil)
   }
   return false
  },nil)
 }
 if c.perfObserver!=nil {c.perfObserver("warm.completed",time.Since(started),primitives)}
 slog.Info("mobile menu textures warmed","primitives",primitives,"elapsed_ms",time.Since(started).Milliseconds())
}
'''
    text = text.replace('"fyne.io/fyne/v2"', '"fyne.io/fyne/v2"\n "log/slog"\n canvaspkg "fyne.io/fyne/v2/canvas"\n textpaint "fyne.io/fyne/v2/internal/painter"', 1)
    canvas_path.chmod(0o644)
    canvas_path.write_text(text)
    path = local / 'internal/driver/mobile/driver.go'
    text = path.read_text()
    marker = '\td.paintWindow(w, newSize)'
    if text.count(marker) != 1:
        raise SystemExit('Review Fyne mobile paint overlay')
    text = text.replace(marker, '''
 // Upload detached menu text/icons on the active GL context, then clear and
 // render the normal page before Publish. Warm-up pixels never reach display.
 c.Painter().SetOutputSize(d.currentSize.WidthPx,d.currentSize.HeightPx)
 c.warmPrimitives()
''' + marker, 1)
    if text.count('\n\tapp.BeginPaint()')!=1 or text.count('\n\td.app.Publish()')!=1:
        raise SystemExit('Review Fyne paint diagnostic boundaries')
    text=text.replace('\n\tapp.BeginPaint()', '\n\tpaintStarted:=time.Now()\n\tapp.BeginPaint()',1)
    text=text.replace('\n\td.app.Publish()', '\n\td.app.Publish()\n if c.perfObserver!=nil {c.perfObserver("frame.published",time.Since(paintStarted),0)}',1)
    # Foreground re-entry needs a repaint, not invalidating all warmed glyphs.
    text = text.replace('c.Content().Refresh()', 'c.Refresh(c.Content())', 1)
    path.chmod(0o644)
    path.write_text(text)

    path=local/'internal/painter/font.go'
    text=path.read_text()
    text=text.replace('"bytes"','"bytes"\n "fmt"\n "time"',1)
    marker='func loadMeasureFont(data fyne.Resource) *font.Face {'
    if text.count(marker)!=1:raise SystemExit('Review font diagnostic hook')
    original='''func loadMeasureFont(data fyne.Resource) *font.Face {
	loaded, err := font.ParseTTF(bytes.NewReader(data.Content()))
	if err != nil {
		fyne.LogError("font load error", err)
		return nil
	}

	return loaded
}'''
    if text.count(original)!=1:raise SystemExit('Review parsed font cache hook')
    text=text.replace(original,'func loadMeasureFont(data fyne.Resource) *font.Face {return fncpnLoadMeasureFont(data)}',1)
    # The source no longer uses bytes after parsing moves into the shared cache.
    text=text.replace('\t"bytes"\n','',1)
    text=text.replace('func ClearFontCache() {','func ClearFontCache() {\n fncpnClearParsedFonts()',1)
    marker='val, ok := fontCache.Load(cacheID{style: style, scope: scope})\n\tif !ok {'
    if text.count(marker)!=1:raise SystemExit('Review font scope diagnostic hook')
    text=text.replace(marker,marker+'\n if observer:=fontPerfObserver();observer!=nil {observer(fmt.Sprintf("font.miss:scope=%s:style=%+v",scope,style),0,0)}\n',1)
    text+='''
var fontPerf struct {sync.RWMutex; observer func(string,time.Duration,int)}
func SetFontPerfObserver(observer func(string,time.Duration,int)) {fontPerf.Lock();fontPerf.observer=observer;fontPerf.Unlock()}
func fontPerfObserver() func(string,time.Duration,int) {fontPerf.RLock();observer:=fontPerf.observer;fontPerf.RUnlock();return observer}
'''
    path.chmod(0o644);path.write_text(text)

    cache_path=local/'internal/painter/fncpn_parsed_font_cache.go'
    cache_path.parent.chmod(0o755)
    if cache_path.exists():cache_path.chmod(0o644)
    cache_path.write_text((Path(__file__).parent/'fyne/parsed_font_cache.go.in').read_text())
