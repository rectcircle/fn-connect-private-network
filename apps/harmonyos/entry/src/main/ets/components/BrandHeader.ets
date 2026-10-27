import { tr } from '../platform/Messages';
@Component
export struct BrandHeader {
  @Prop back: boolean = false;
  @Prop debug: boolean = false;
  @Prop busy: boolean = false;
  @Prop tv: boolean = false;
  navigate: () => void = () => {};
  build() {
    Row({ space: 12 }) {
      Button(this.back ? '‹' : '☰').type(ButtonType.Normal).borderRadius(8).fontSize(this.tv ? 30 : 22)
        .width(this.tv ? 64 : 48).height(this.tv ? 64 : 44).accessibilityText(tr(this.back ? '返回' : '菜单'))
        .fontColor($r('app.color.foreground')).backgroundColor(Color.Transparent).hoverEffect(HoverEffect.Highlight)
        .enabled(!this.busy).onClick(() => { this.navigate(); })
      Image($r('app.media.icon')).width(28).height(28)
      Text('FnCPN').fontWeight(FontWeight.Bold).fontSize(this.tv ? 26 : 20)
      if (this.debug) { Text(tr('应用内调试')).fontSize(11).fontColor($r('app.color.primary')) }
      Text($r('app.string.product_version')).fontColor($r('app.color.secondary')).fontSize(this.tv ? 18 : 12)
    }.height(this.tv ? 80 : 56).width('100%').padding({ left: 4, right: 16 })
      .border({ width: { bottom: 0.5 }, color: '#227F8896' })
  }
}
