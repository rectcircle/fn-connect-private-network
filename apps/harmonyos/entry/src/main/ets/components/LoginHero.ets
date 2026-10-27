import { tr } from '../platform/Messages';
@Component
export struct LoginHero {
  @Prop compact: boolean = false;
  @Prop tv: boolean = false;
  build() {
    Column({ space: this.compact ? 4 : 8 }) {
      Text(tr('无论在哪。')).fontSize(this.compact ? 24 : this.tv ? 40 : 32).fontWeight(FontWeight.Bold).fontColor($r('app.color.foreground'))
      Text(tr('你的网络，')).fontSize(this.compact ? 24 : this.tv ? 40 : 32).fontWeight(FontWeight.Bold).fontColor($r('app.color.foreground'))
      Text(tr('就在身旁。')).fontSize(this.compact ? 24 : this.tv ? 40 : 32).fontWeight(FontWeight.Bold).fontColor($r('app.color.primary'))
    }.alignItems(HorizontalAlign.Start).width('100%').margin({ bottom: this.compact ? 8 : 24 })
  }
}
