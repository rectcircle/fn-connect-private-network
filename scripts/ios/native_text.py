"""Add a narrow system text hook to the pinned, local Fyne build copy."""
from pathlib import Path

API = '''package fyne

import (
    "image/color"
    "image/draw"
    "sync/atomic"
)

// SystemTextRenderer replaces normal text measurement and rasterization together.
// Install before creating widgets. Symbol-font rendering remains with Fyne.
type SystemTextRenderer interface {
    Measure(string,float32,TextStyle)(Size,float32)
    Draw(draw.Image,string,color.Color,float32,float32,TextStyle,int)
}
type systemTextHolder struct { renderer SystemTextRenderer }
var systemText atomic.Pointer[systemTextHolder]
func SetSystemTextRenderer(r SystemTextRenderer) {
    if r==nil {systemText.Store(nil)} else {systemText.Store(&systemTextHolder{r})}
}
func CurrentSystemTextRenderer() SystemTextRenderer {
    if h:=systemText.Load();h!=nil{return h.renderer};return nil
}
'''

def prepare(source: Path, local: Path):
    local.chmod(0o755)
    path = local / 'system_text.go'
    if path.exists():
        path.chmod(0o644)
    path.write_text(API)
    font = (source / 'internal/painter/font.go').read_text()
    hooks = {
        'func DrawStringOffset(dst draw.Image, s string, color color.Color, f shaping.Fontmap, fontSize, scale float32, style fyne.TextStyle, offset int) {':
            '\n\tif renderer:=fyne.CurrentSystemTextRenderer(); renderer!=nil && !style.Symbol {renderer.Draw(dst,s,color,fontSize,scale,style,offset);return}\n',
        'func MeasureString(f shaping.Fontmap, s string, textSize float32, style fyne.TextStyle) (size fyne.Size, advance float32) {':
            '\n\tif renderer:=fyne.CurrentSystemTextRenderer(); renderer!=nil && !style.Symbol {return renderer.Measure(s,textSize,style)}\n',
        'func measureText(text string, fontSize float32, style fyne.TextStyle, source fyne.Resource) (fyne.Size, float32) {':
            '\n\tif renderer:=fyne.CurrentSystemTextRenderer(); renderer!=nil && !style.Symbol {return renderer.Measure(text,fontSize,style)}\n',
    }
    for marker, hook in hooks.items():
        if font.count(marker) != 1:
            raise SystemExit('Review native text overlay after Fyne API drift: ' + marker)
        font = font.replace(marker, marker + hook, 1)
    path = local / 'internal/painter/font.go'
    path.chmod(0o644)
    path.write_text(font)
