import { pasteboard } from '@kit.BasicServicesKit';
import { Client, LinkPreference, LinkSettings } from '../platform/Client';
import { BuildInfo, TestStatus, TestResult, SettingsTest, emptyTest, ipv6Availability } from '../controller/SettingsTest';
import { isDesktop } from '../platform/DeviceServices';
import { tr } from '../platform/Messages';

interface ThroughputPoint { elapsed: number; rate: number; }

@Component
export struct NativeSettings {
  client: Client = new Client('', false);
  @Prop @Watch('pageChanged') page: string = 'menu';
  @Prop forward: boolean = true;
  navigate: (page: string) => void = () => {};
  openLink: (url: string) => void = () => {};
  @State preferences: LinkSettings = { fnId: '', links: [] };
  @State info: BuildInfo = { version: '', commit: '' };
  @State saving: boolean = false;
  @State notice: string = '';
  @State chooseIPv6: boolean = true;
  @State chooseRelay: boolean = true;
  @State selectorOpen: boolean = false;
  @State test: TestStatus = emptyTest();
  @State samples: ThroughputPoint[] = [];
 @State policyHint: boolean = false;
 private sampleLink: string = '';
 private sampleWindow: number = 8000;
  private scroll: Scroller = new Scroller();
  private stream: SettingsTest | undefined;
  private generation: number = 0;
  private disposed: boolean = false;
  private visiblePage: string = '';
  private dragFrom: number = -1;
  @State draggingLink: string = '';
  @State dragOffset: number = 0;
  @State dragSettling: boolean = false;
  private dragEpoch: number = 0;
  private dragStartY: number = 0;
  private readonly rowStep: number = 64.5;
  private diagram: CanvasRenderingContext2D = new CanvasRenderingContext2D(new RenderingContextSettings(true));
  private graph: CanvasRenderingContext2D = new CanvasRenderingContext2D(new RenderingContextSettings(true));
  private diagramWidth: number = 0;
  private graphWidth: number = 0;
  private animationTimer: number = -1;
  private sampleStage: string = '';
  private pingAt: number = 0;
  private pongAt: number = 0;
  aboutToAppear(): void { this.disposed = false; this.visiblePage = this.page; this.load(); }
  aboutToDisappear(): void { this.disposed = true; this.stop(); }
  private pageChanged(): void { if (this.visiblePage === this.page) { return; } this.visiblePage = this.page; this.stop(); this.selectorOpen = false; this.scroll.scrollEdge(Edge.Top); }
  private stop(): void {
    this.dragEpoch++; this.draggingLink = ''; this.dragOffset = 0; this.dragSettling = false; this.dragFrom = -1;
    this.generation++; this.stream?.cancel(); this.stream = undefined;
    if (this.animationTimer >= 0) { clearInterval(this.animationTimer); this.animationTimer = -1; }
    if (this.test.active) { this.test = { active: false, stage: 'canceled', results: this.test.results }; }
  }
  private async load(): Promise<void> {
    try {
      const preferences = await this.client.call<LinkSettings>('link-settings');
      const info = await this.client.call<BuildInfo>('build-info');
      if (!this.disposed) { this.preferences = preferences; this.info = info; }
    } catch (error) { if (!this.disposed) { this.notice = (error as Error).message; } }
  }
  private async save(links: LinkPreference[]): Promise<void> {
    if (this.saving || this.test.active || !this.preferences.fnId) { return; }
    this.saving = true; this.notice = '';
    try { this.preferences = await this.client.call<LinkSettings>('set-link-settings', { fnId: this.preferences.fnId, links: links }); }
    catch (error) { this.notice = (error as Error).message; await this.load(); }
    finally { this.saving = false; }
  }
  private toggle(link: string, enabled: boolean): void {
    this.save(this.preferences.links.map((item: LinkPreference): LinkPreference => ({ link: item.link, enabled: item.link === link ? enabled : item.enabled })));
  }
  private beginDrag(link: string, index: number): void {
    if (index < 0 || this.saving || this.test.active || !this.preferences.fnId || this.dragSettling) { return; }
    this.dragEpoch++; this.dragFrom = index; this.draggingLink = link; this.dragOffset = 0;
  }
  private drag(offset: number): void {
    if (!this.draggingLink || this.dragSettling) { return; }
    this.dragOffset = this.dragFrom === 0 ? Math.max(0, Math.min(this.rowStep, offset)) : Math.min(0, Math.max(-this.rowStep, offset));
  }
  private rowOffset(link: string, index: number): number {
    if (index < 0 || !this.draggingLink) { return 0; }
    if (link === this.draggingLink) { return this.dragOffset; }
    return Math.abs(this.dragOffset) > this.rowStep / 2 ? (this.dragFrom === 0 ? -this.rowStep : this.rowStep) : 0;
  }
  private finishDrag(canceled: boolean = false): void {
    if (!this.draggingLink || this.dragSettling) { return; }
    const epoch = this.dragEpoch;
    const swap = !canceled && Math.abs(this.dragOffset) > this.rowStep / 2;
    this.dragSettling = true;
    this.dragOffset = swap ? (this.dragFrom === 0 ? this.rowStep : -this.rowStep) : 0;
    setTimeout(() => {
      if (epoch !== this.dragEpoch || this.disposed) { return; }
      const links = swap ? [this.preferences.links[1], this.preferences.links[0]] : this.preferences.links;
      this.preferences = { fnId: this.preferences.fnId, links: links };
      this.draggingLink = ''; this.dragOffset = 0; this.dragSettling = false; this.dragFrom = -1;
      if (swap) { this.save(links); }
    }, 180);
  }
  private linkName(link: string): string { return tr(link === 'ipv6' ? 'IPv6 直连' : link === 'fn-connect' ? 'FN Connect 中继' : '局域网'); }
  private selectionTitle(): string { return tr(this.chooseIPv6 && this.chooseRelay ? 'IPv6 与 FN Connect' : this.chooseIPv6 ? 'IPv6 直连' : this.chooseRelay ? 'FN Connect 中继' : '选择测试链路'); }
  private confirm(): void {
    if (!this.chooseIPv6 && !this.chooseRelay) { return; }
    this.selectorOpen = false;
    AlertDialog.show({ title: tr('开始网络链路测试？'),
      message: tr('测试期间会重新建立连接，当前网络访问可能短暂中断。') + '\n\n' + tr('测试结果仅供参考，受当前设备与 NAS 的网络状况、可用带宽及 FN Connect 中转服务器带宽限制。'),
      primaryButton: { value: tr('取消'), action: () => {} }, secondaryButton: { value: tr('开始测试'), action: () => { this.startTest(); } } });
  }
  private follow(): void { setTimeout(() => { if (!this.disposed && this.page === 'links') { this.scroll.scrollEdge(Edge.Bottom); } }, 0); }
  private async startTest(): Promise<void> {
    this.stream?.cancel(); const generation = ++this.generation;
    const stream = new SettingsTest(); this.stream = stream; this.samples = []; this.notice = '';
    this.test = { active: true, stage: 'preparing', results: [] }; this.follow();
    try {
      await stream.run(this.client, this.chooseIPv6 && this.chooseRelay ? 'all' : this.chooseIPv6 ? 'ipv6' : 'fn-connect', (status: TestStatus) => {
        if (generation !== this.generation || this.disposed) { return; }
        const now = Date.now();
        if (status.link !== this.test.link || (status.stage === 'latency' && this.test.stage !== 'latency')) {
          this.pingAt = 0; this.pongAt = 0;
        }
        // Real replies can arrive before the outgoing animation finishes.
        // Keep one visual round trip at a time, followed by a quiet interval.
        if ((status.echoSent ?? 0) > (this.test.echoSent ?? 0) && now >= this.pongAt + 600) { this.pingAt = now; }
        if ((status.echoReceived ?? 0) > (this.test.echoReceived ?? 0)) { this.pongAt = Math.max(now, this.pingAt + 250); }
        const family = status.stage.startsWith('upload') ? 'upload' : status.stage;
        if (status.link !== this.sampleLink || ((family === 'upload' || family === 'download') && this.sampleStage !== family)) { this.samples = []; }
        this.sampleLink = status.link ?? '';
        if (family === 'upload' || family === 'download') {
          this.sampleStage = family; this.sampleWindow = status.targetSampleMs || 8000;
          const elapsed = status.sampleElapsedMs ?? 0;
          if (elapsed > 0 && (!this.samples.length || elapsed > this.samples[this.samples.length - 1].elapsed)) {
            const points = this.samples.concat([{ elapsed: elapsed, rate: status.mbps ?? 0 } as ThroughputPoint]);
            const cutoff = Math.max(0, elapsed - this.sampleWindow);
            while (points.length > 1 && points[1].elapsed <= cutoff) { points.shift(); }
            this.samples = points;
          }
        }
        this.test = status; this.draw(); this.drawGraph(); this.follow();
        if (!status.active && this.animationTimer >= 0) { clearInterval(this.animationTimer); this.animationTimer = -1; }
      });
    } catch (error) {
      if (generation === this.generation && !this.disposed) { this.test = { active: false, stage: 'failed', results: this.test.results, error: (error as Error).message }; this.follow(); }
    } finally {
      if (generation === this.generation) {
        this.stream = undefined;
        if (this.animationTimer >= 0) { clearInterval(this.animationTimer); this.animationTimer = -1; }
        this.draw();
      }
    }
  }
  private cancel(): void { this.stream?.cancel(); this.test = { active: false, stage: 'canceled', results: this.test.results }; this.draw(); this.follow(); }
  private hasTestError(): boolean { return !!this.test.error || !!this.test.restoreError || this.test.results.some((result: TestResult) => !!result.error && !ipv6Availability(result)); }
  private dismissTestErrors(): void {
    this.test = { active: false, stage: this.test.stage, link: this.test.link, results: this.test.results.map((result: TestResult): TestResult => ({ link: result.link, latencyMs: result.latencyMs, timeoutPercent: result.timeoutPercent, downloadMbps: result.downloadMbps, uploadMbps: result.uploadMbps, downloadLimited: result.downloadLimited, uploadLimited: result.uploadLimited, error: '' })) };
  }
  private phase(): string {
    if (!this.test.active && this.test.results.length === 1 && ipv6Availability(this.test.results[0])) { return this.linkName('ipv6') + ' · ' + ipv6Availability(this.test.results[0]); }
    const names: Record<string, string> = { 'idle': '选择链路开始测速', 'preparing': '准备测试', 'checking': '检查链路条件', 'connecting': '正在建立真实链路', 'verifying': '正在验证 NAS', 'latency': '正在测试延迟', 'download': '正在测试下载', 'upload': '正在测试上传', 'upload-warmup': '正在预热上传', 'upload-waiting': '等待 NAS 接收数据', 'upload-confirming': '等待 NAS 完成确认', 'upload-complete': '上传完成', 'restoring': '正在恢复正常选路', 'completed': '测速完成', 'failed': '测速失败', 'canceled': '测速已取消' };
    return (this.test.link ? this.linkName(this.test.link) + ' · ' : '') + tr(names[this.test.stage] ?? this.test.stage);
  }
  private metric(): string {
    if (this.test.stage === 'latency') { return (this.test.rttMs ?? 0).toFixed(1) + ' ms'; }
    if (['download', 'upload', 'upload-complete'].includes(this.test.stage)) { return (this.test.mbps ?? 0).toFixed(1) + ' Mbps'; }
    if (this.test.stage === 'upload-warmup') { return tr('预热中…'); }
    if (this.test.stage === 'upload-waiting') { return tr('等待接收…'); }
    if (this.test.stage === 'upload-confirming') { return tr('等待确认…'); }
    return '';
  }
  private value(value: number | undefined, unit: string): string { return value === undefined ? '—' : value.toFixed(1) + ' ' + unit; }
  private color(name: string): string {
    const value = getContext(this).resourceManager.getColorByNameSync(name);
    return 'rgba(' + ((value >>> 16) & 255) + ',' + ((value >>> 8) & 255) + ',' + (value & 255) + ',' + ((value >>> 24) / 255) + ')';
  }
  private rounded(context: CanvasRenderingContext2D, x: number, y: number, width: number, height: number, radius: number): void {
    context.beginPath(); context.moveTo(x + radius, y); context.lineTo(x + width - radius, y); context.arcTo(x + width, y, x + width, y + radius, radius); context.lineTo(x + width, y + height - radius); context.arcTo(x + width, y + height, x + width - radius, y + height, radius); context.lineTo(x + radius, y + height); context.arcTo(x, y + height, x, y + height - radius, radius); context.lineTo(x, y + radius); context.arcTo(x, y, x + radius, y, radius); context.closePath(); context.stroke();
  }
  private device(context: CanvasRenderingContext2D, x: number, kind: string): void {
    this.rounded(context, x - 28, 18, 56, 54, 11);
    if (kind === 'phone') { this.rounded(context, x - 8, 29, 16, 30, 3); context.beginPath(); context.moveTo(x - 3, 54); context.lineTo(x + 3, 54); context.stroke(); }
    else if (kind === 'computer') { this.rounded(context, x - 14, 29, 28, 22, 2); context.beginPath(); context.moveTo(x, 51); context.lineTo(x, 58); context.moveTo(x - 16, 58); context.lineTo(x + 16, 58); context.stroke(); }
    else if (kind === 'relay') { this.rounded(context, x - 12, 28, 24, 33, 3); for (let index = 0; index < 3; index++) { context.beginPath(); context.arc(x + 6, 34 + index * 10, 1.5, 0, Math.PI * 2); context.fill(); if (index < 2) { context.beginPath(); context.moveTo(x - 12, 39 + index * 10); context.lineTo(x + 12, 39 + index * 10); context.stroke(); } } }
    else { context.beginPath(); context.moveTo(x - 10, 31); context.lineTo(x + 10, 31); context.lineTo(x + 16, 53); context.lineTo(x - 16, 53); context.closePath(); context.stroke(); context.beginPath(); context.moveTo(x - 14, 46); context.lineTo(x + 14, 46); context.stroke(); }
  }
  private draw(): void {
    const c = this.diagram, width = this.diagramWidth; if (width <= 0) { return; }
    c.clearRect(0, 0, width, 145);
    const accent = this.color('primary'), muted = this.color('secondary'), line = this.color('divider');
    const relay = this.test.link === 'fn-connect', centers = relay ? [32, width / 2, width - 32] : [32, width - 32];
    const moving = this.test.active && ['download', 'upload', 'latency'].includes(this.test.stage);
    const left = centers[0] + 30, right = centers[centers.length - 1] - 30;
    for (let lane = 0; lane < 2; lane++) {
      const y = lane === 0 ? 38 : 54;
      const highlight = moving && (this.test.stage === 'latency' || this.test.stage === (lane === 0 ? 'upload' : 'download'));
      c.strokeStyle = highlight ? accent : line; c.fillStyle = accent; c.lineWidth = 1.6;
      for (let index = 0; index < centers.length - 1; index++) {
        const a = centers[index] + 30, b = centers[index + 1] - 30;
        c.beginPath(); c.moveTo(a, y); c.lineTo(b, y); c.stroke();
        const tip = lane === 0 ? b : a, d = lane === 0 ? -4 : 4;
        c.beginPath(); c.moveTo(tip + d, y - 4); c.lineTo(tip, y); c.lineTo(tip + d, y + 4); c.stroke();
        if (highlight) {
          const age = (Date.now() - (lane === 0 ? this.pingAt : this.pongAt)) / 1000;
          for (let dot = 0; dot < (this.test.stage === 'latency' ? 1 : 3); dot++) {
            const t = age / 0.25;
            const progress = this.test.stage === 'latency' ? t * t * (3 - 2 * t) : (Date.now() / 1000 * 0.65 + dot / 3) % 1;
            if (this.test.stage === 'latency' && (age < 0 || age >= 0.25)) { continue; }
            const x = lane === 0 ? left + (right - left) * progress : right - (right - left) * progress;
            if (x >= a && x <= b) { c.beginPath(); c.arc(x, y, 3, 0, Math.PI * 2); c.fill(); }
          }
        }
      }
    }
    c.strokeStyle = moving ? accent : muted; c.fillStyle = muted; c.lineWidth = 1.6;
    c.textAlign = 'center'; c.textBaseline = 'middle'; c.font = vp2px(12) + 'px sans-serif';
    for (let index = 0; index < centers.length; index++) {
      const kind = index === 0 ? isDesktop() ? 'computer' : 'phone' : index === centers.length - 1 ? 'nas' : 'relay';
      this.device(c, centers[index], kind);
      if (kind === 'relay') { c.fillText('FN Connect', centers[index], 91); c.fillText(tr('中转'), centers[index], 108); }
      else { c.fillText(index === 0 ? tr('当前设备') : 'NAS', centers[index], 96, 75); }
    }
    if (moving && this.animationTimer < 0) { this.animationTimer = setInterval(() => { this.draw(); }, 33); }
  }
  private drawGraph(): void {
    const c = this.graph; c.clearRect(0, 0, this.graphWidth, 52); if (!this.samples.length) { return; }
    const maximum = Math.max(1, ...this.samples.map((point: ThroughputPoint): number => point.rate));
    const origin = Math.max(0, this.samples[this.samples.length - 1].elapsed - this.sampleWindow);
    c.strokeStyle = this.color('primary'); c.lineWidth = 1.7; c.beginPath();
    this.samples.forEach((point: ThroughputPoint, index: number) => {
      let elapsed = point.elapsed, rate = point.rate;
      if (elapsed < origin && index + 1 < this.samples.length) { const next = this.samples[index + 1]; rate += (next.rate - rate) * (origin - elapsed) / (next.elapsed - elapsed); elapsed = origin; }
      const x = (elapsed - origin) / this.sampleWindow * this.graphWidth, y = 42 - rate / maximum * 36;
      if (index === 0) { c.moveTo(x, y); } else { c.lineTo(x, y); }
    }); c.stroke(); c.strokeStyle = this.color('divider'); c.lineWidth = 1; c.beginPath(); c.moveTo(0, 50); c.lineTo(this.graphWidth, 50); c.stroke();
  }
  private async copyCommit(): Promise<void> {
    try { await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, this.info.commit)); this.getUIContext().getPromptAction().showToast({ message: tr('已复制 Commit') }); }
    catch (error) { this.notice = (error as Error).message; }
  }
  @Builder private policyHelp() {
    Text(tr('打开或关闭开关可启用或禁用对应链路；拖动远程链路可调整优先级。连接时按从上到下的顺序尝试已启用的链路。局域网始终优先，且不可关闭或调整顺序。')).fontSize(13).width(280).padding(14)
  }
  @Builder private menuRow(title: string, page: string) {
    Button({ type: ButtonType.Normal }) {
      Row() {
        Text(tr(title)).layoutWeight(1).textAlign(TextAlign.Start).fontColor($r('app.color.foreground')).fontSize(16)
        Image($r('app.media.action_chevron_down')).width(18).height(18).rotate({ angle: -90 }).fillColor($r('app.color.secondary'))
      }.width('100%').padding({ left: 20, right: 16 })
    }.width('100%').height(60).borderRadius(12).backgroundColor($r('app.color.surface')).onClick(() => { this.navigate(page); })
  }
  @Builder private menu() {
    Column({ space: 8 }) { this.menuRow('网络链路', 'links'); this.menuRow('关于', 'about'); }.width('100%')
  }
  @Builder private externalRow(title: string, url: string) {
    Button({ type: ButtonType.Normal }) {
      Row() { Text(tr(title)).layoutWeight(1).textAlign(TextAlign.Start).fontSize(15); Image($r('app.media.action_external')).width(18).height(18).fillColor($r('app.color.secondary')); }.width('100%').padding({ left: 18, right: 18 })
    }.width('100%').height(56).backgroundColor($r('app.color.surface')).fontColor($r('app.color.foreground')).borderRadius(12).onClick(() => { this.openLink(url); })
  }
  @Builder private about() {
    Column({ space: 18 }) {
      if (isDesktop()) { Text(tr('通用')).fontSize(26).fontWeight(FontWeight.Bold).alignSelf(ItemAlign.Start) }
      Image($r('app.media.icon')).width(72).height(72).margin({ top: 16 })
      Text('FnCPN').fontSize(26).fontWeight(FontWeight.Bold)
      Column({ space: 16 }) {
        Row() { Text(tr('版本')).fontColor($r('app.color.secondary')); Blank(); Text(this.info.version); }.width('100%')
        Row({ space: 8 }) {
          Text('Commit').fontColor($r('app.color.secondary'))
          Text(this.info.commit).fontSize(13).fontColor($r('app.color.secondary')).copyOption(CopyOptions.LocalDevice).layoutWeight(1).maxLines(1).textOverflow({ overflow: TextOverflow.Ellipsis })
          Button({ type: ButtonType.Normal }) { Image($r('app.media.action_copy')).width(18).height(18).fillColor($r('app.color.secondary')) }.padding(10).backgroundColor(Color.Transparent).onClick(() => { this.copyCommit(); })
        }.width('100%')
      }.width('100%').padding(20).backgroundColor($r('app.color.surface')).borderRadius(12)
      Column({ space: 8 }) {
        this.externalRow('GitHub 项目', 'https://github.com/rectcircle/fn-connect-private-network')
        this.externalRow('官方网站', 'https://fncpn.rectcircle.cn/')
        this.externalRow('下载服务端与其他客户端', 'https://github.com/rectcircle/fn-connect-private-network/releases')
      }.width('100%')
    }.width('100%')
  }
  @Builder private policyRow(link: string, enabled: boolean, index: number) {
    Row({ space: 12 }) {
      Text('⠿').fontSize(22).fontColor($r('app.color.secondary')).padding({ top: 14, bottom: 14, right: 6 })
        .onTouch((event: TouchEvent) => {
          if (index < 0) { return; }
          event.stopPropagation();
          if (event.type === TouchType.Down && event.touches.length) {
            this.beginDrag(link, index); this.dragStartY = event.touches[0].windowY;
          } else if (event.type === TouchType.Move && event.touches.length) {
            this.drag(event.touches[0].windowY - this.dragStartY);
          } else if (event.type === TouchType.Up) { this.finishDrag(); }
          else if (event.type === TouchType.Cancel) { this.finishDrag(true); }
        })
      Text(this.linkName(link) + (index < 0 ? tr(' · 始终优先') : '')).layoutWeight(1).fontSize(15).fontColor(index < 0 ? $r('app.color.secondary') : $r('app.color.foreground'))
      Toggle({ type: ToggleType.Switch, isOn: enabled }).enabled(index >= 0 && !!this.preferences.fnId && !this.saving && !this.test.active && !this.draggingLink)
        .onChange((on: boolean) => { if (index >= 0) { this.toggle(link, on); } }).accessibilityText(this.linkName(link))
    }.height(64).width('100%')
      .backgroundColor(this.draggingLink === link ? $r('app.color.control_background') : Color.Transparent)
      .borderRadius(8).zIndex(this.draggingLink === link ? 2 : 0)
      .shadow({ radius: this.draggingLink === link ? 5 : 0, color: this.draggingLink === link ? '#18000000' : '#00000000', offsetY: 2 })
      .translate({ y: this.rowOffset(link, index) })
      .animation({ duration: this.draggingLink === link && !this.dragSettling ? 0 : 180, curve: Curve.EaseOut })
  }
  @Builder private testChoices() {
    Column({ space: 12 }) {
      Row({ space: 10 }) { Checkbox().select(this.chooseIPv6).onChange((on: boolean) => { this.chooseIPv6 = on; }); Text(tr('IPv6 直连')); }
      Row({ space: 10 }) { Checkbox().select(this.chooseRelay).onChange((on: boolean) => { this.chooseRelay = on; }); Text(tr('FN Connect 中继')); }
      Button(tr('完成')).onClick(() => { this.selectorOpen = false; })
    }.alignItems(HorizontalAlign.Start).padding(18)
  }
  @Builder private resultRow(result: TestResult, table: boolean) {
    Column({ space: 6 }) {
      if (table) {
        Row({ space: 8 }) {
          Text(this.linkName(result.link)).layoutWeight(1).fontSize(12)
          Text(this.value(result.latencyMs, 'ms')).layoutWeight(1).fontSize(12)
          Text(this.value(result.downloadMbps, 'Mbps')).layoutWeight(1).fontSize(12)
          Text(this.value(result.uploadMbps, 'Mbps')).layoutWeight(1).fontSize(12)
        }.alignItems(VerticalAlign.Top).width('100%')
      } else {
        Text(this.linkName(result.link)).fontWeight(FontWeight.Medium).fontSize(14)
        Row({ space: 8 }) {
          Text(tr('延迟') + ' ' + this.value(result.latencyMs, 'ms')).layoutWeight(1).fontSize(12)
          Text(tr('下载') + ' ' + this.value(result.downloadMbps, 'Mbps')).layoutWeight(1).fontSize(12)
          Text(tr('上传') + ' ' + this.value(result.uploadMbps, 'Mbps')).layoutWeight(1).fontSize(12)
        }.alignItems(VerticalAlign.Top).width('100%')
      }
      if (result.message || result.error) { Text(ipv6Availability(result) || result.error || result.message || '').fontColor(ipv6Availability(result) ? $r('app.color.secondary') : $r('app.color.danger')).fontSize(12) }
    }.alignItems(HorizontalAlign.Start).width('100%').padding({ top: 12, bottom: 12 }).border({ width: { bottom: table ? 0.5 : 0 }, color: $r('app.color.divider') })
  }
  @Builder private links() {
    Column({ space: 16 }) {
      if (isDesktop()) { Text(tr('网络链路')).fontSize(26).fontWeight(FontWeight.Bold) }
      Text('NAS · ' + (this.preferences.fnId || '—')).fontColor($r('app.color.secondary'))
      Row() {
        Text(tr('开关与优先级')).layoutWeight(1).fontSize(18).fontWeight(FontWeight.Bold)
        Button('?').type(ButtonType.Circle).width(28).height(28).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.secondary'))
          .onClick(() => { this.policyHint = !this.policyHint; }).onHover((hover: boolean) => { this.policyHint = hover; })
          .bindPopup(this.policyHint, { builder: this.policyHelp, placement: Placement.Bottom, onStateChange: (event) => { this.policyHint = event.isVisible; } })
      }.width('100%')
      Column() {
        this.policyRow('local', true, -1)
        ForEach(this.preferences.links, (link: LinkPreference, index: number) => {
          Divider().color($r('app.color.divider')).strokeWidth(0.5)
          this.policyRow(link.link, link.enabled, index)
        }, (link: LinkPreference, index: number) => link.link + ':' + link.enabled + ':' + index)
        Button(tr('恢复默认设置')).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).enabled(!!this.preferences.fnId && !this.saving && !this.test.active).margin({ top: 12 }).onClick(() => { this.save([{ link: 'ipv6', enabled: true }, { link: 'fn-connect', enabled: true }]); })
      }.width('100%').padding(16).backgroundColor($r('app.color.surface')).borderRadius(12)
      Text(tr('网络链路测试')).fontSize(18).fontWeight(FontWeight.Bold).margin({ top: 10 })
      Column({ space: 16 }) {
        Row({ space: 8 }) {
          Text(tr('测试链路')).fontSize(12)
          Button({ type: ButtonType.Normal }) {
          Stack({ alignContent: Alignment.End }) {
            Text(this.selectionTitle()).width('100%').textAlign(TextAlign.Center).padding({ left: 20, right: 20 }).fontSize(12)
            Image($r('app.media.action_chevron_down')).width(18).height(18).fillColor($r('app.color.foreground')).margin({ right: 10 })
          }.width('100%').height(44)
          }.layoutWeight(1).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).borderRadius(8).onClick(() => { this.selectorOpen = !this.selectorOpen; })
          .bindPopup(this.selectorOpen, { builder: this.testChoices, placement: Placement.Bottom, onStateChange: (event) => { this.selectorOpen = event.isVisible; } })
        Row({ space: 12 }) {
          if (!this.test.active) { Button(tr(this.test.stage === 'idle' ? '开始测试' : '重新测试')).fontSize(12).padding({ left: 10, right: 10 }).enabled(!!this.preferences.fnId && (this.chooseIPv6 || this.chooseRelay)).onClick(() => { this.confirm(); }) }
          if (this.test.active) { Button(tr('取消')).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).onClick(() => { this.cancel(); }) }
        }
        }.width('100%')
        if (this.test.stage !== 'idle') {
          Text(this.phase()).fontSize(13).fontColor($r('app.color.secondary')).textAlign(TextAlign.Center).width('100%')
          if (this.test.active) {
          Canvas(this.diagram).width('100%').height(130).onReady(() => { this.draw(); }).onAreaChange((_, area) => { this.diagramWidth = Number(area.width); this.draw(); })
          if (this.metric()) { Text(this.metric()).fontSize(28).fontWeight(FontWeight.Bold).fontColor($r('app.color.primary')).width('100%').textAlign(TextAlign.Center) }
          if (this.samples.length) { Canvas(this.graph).width('100%').height(52).onReady(() => { this.drawGraph(); }).onAreaChange((_, area) => { this.graphWidth = Number(area.width); this.drawGraph(); }) }
          }
          if (this.test.results.length) {
            if (!this.test.active) {
              Text(tr('测试结果')).fontWeight(FontWeight.Medium).alignSelf(ItemAlign.Start)
              Row({ space: 8 }) {
                Text(tr('网络链路')).layoutWeight(1).fontSize(12).fontWeight(FontWeight.Medium)
                Text(tr('延迟')).layoutWeight(1).fontSize(12).fontWeight(FontWeight.Medium)
                Text(tr('下载')).layoutWeight(1).fontSize(12).fontWeight(FontWeight.Medium)
                Text(tr('上传')).layoutWeight(1).fontSize(12).fontWeight(FontWeight.Medium)
              }.width('100%')
            }
            ForEach(this.test.results, (result: TestResult) => { this.resultRow(result, !this.test.active) }, (result: TestResult) => result.link + ':' + this.test.active + ':' + result.latencyMs + ':' + result.downloadMbps + ':' + result.uploadMbps + ':' + result.error + ':' + result.outcome + ':' + result.message)
          }
          if (this.test.error || this.test.restoreError) { Text((this.test.error ?? '') + '\n' + (this.test.restoreError ?? '')).fontColor($r('app.color.danger')).fontSize(13) }
          if (!this.test.active && this.hasTestError()) { Button(tr('关闭')).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).onClick(() => { this.dismissTestErrors(); }) }
        }
      }.width('100%').padding(16).backgroundColor($r('app.color.surface')).borderRadius(12)
        .onAreaChange((oldArea, area) => { if (this.test.stage !== 'idle' && Number(oldArea.height) !== Number(area.height)) { this.follow(); } })
    }.alignItems(HorizontalAlign.Start).width('100%')
  }
  build() {
    Row() {
      if (isDesktop()) {
        Column({ space: 12 }) {
          Button(tr('通用')).backgroundColor(this.page === 'about' ? $r('app.color.control_background') : Color.Transparent).fontColor($r('app.color.foreground')).onClick(() => { this.navigate('about'); })
          Button(tr('网络链路')).backgroundColor(this.page === 'links' ? $r('app.color.control_background') : Color.Transparent).fontColor($r('app.color.foreground')).onClick(() => { this.navigate('links'); })
        }.width(150).height('100%').padding(16).backgroundColor($r('app.color.surface'))
      }
      Scroll(this.scroll) {
        Column({ space: 16 }) {
          if (this.page === 'menu') { Column() { this.menu(); }.width('100%').transition(TransitionEffect.asymmetric(TransitionEffect.move(this.forward ? TransitionEdge.END : TransitionEdge.START), TransitionEffect.move(this.forward ? TransitionEdge.START : TransitionEdge.END))) }
          else if (this.page === 'about') { Column() { this.about(); }.width('100%').transition(TransitionEffect.asymmetric(TransitionEffect.move(this.forward ? TransitionEdge.END : TransitionEdge.START), TransitionEffect.move(this.forward ? TransitionEdge.START : TransitionEdge.END))) }
          else { Column() { this.links(); }.width('100%').transition(TransitionEffect.asymmetric(TransitionEffect.move(this.forward ? TransitionEdge.END : TransitionEdge.START), TransitionEffect.move(this.forward ? TransitionEdge.START : TransitionEdge.END))) }
          if (this.notice) { Text(this.notice).fontColor($r('app.color.danger')); Button(tr('关闭')).onClick(() => { this.notice = ''; }) }
        }.padding(20).width('100%').constraintSize({ maxWidth: 920 }).alignItems(HorizontalAlign.Start)
      }.layoutWeight(1).height('100%').scrollBar(BarState.Auto).scrollable(this.draggingLink ? ScrollDirection.None : ScrollDirection.Vertical).align(Alignment.Top)
    }.width('100%').height('100%').backgroundColor($r('app.color.page_background')).clip(true)
  }
}
