// Dynamic values must remain reactive after the first connection render.
@Component
export struct StatusRow {
  @Prop label: string = '';
  @Prop value: string = '';
  @Prop tv: boolean = false;
  build() {
    Row() {
      Text(this.label).fontSize(this.tv ? 22 : 16).fontColor($r('app.color.secondary')).width('36%')
      Text(this.value || '—').fontSize(this.tv ? 22 : 16).fontColor($r('app.color.foreground')).layoutWeight(1).copyOption(CopyOptions.InApp)
    }.width('100%').padding({ top: 8, bottom: 8 })
  }
}
