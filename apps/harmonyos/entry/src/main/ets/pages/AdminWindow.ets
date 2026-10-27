import { webview } from '@kit.ArkWeb';
import { common } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { Client, ClientError, Diagnostics } from '../platform/Client';
import { tr } from '../platform/Messages';

@Entry({ useSharedStorage: true })
@Component
struct AdminWindow {
  @LocalStorageLink('url') @Watch('reload') url: string = '';
  @LocalStorageLink('directory') directory: string = '';
  @LocalStorageLink('identity') identity: string = '';
  @State error: string = '';
  private controller: webview.WebviewController = new webview.WebviewController();
  private timer: number = -1;
  private checking: boolean = false;
  private ready: boolean = false;
  aboutToAppear(): void { this.checkSession(); this.timer = setInterval(() => { this.checkSession(); }, 2000); }
  aboutToDisappear(): void { if (this.timer >= 0) { clearInterval(this.timer); this.timer = -1; } }
  private reload(): void { this.error = ''; if (this.ready) { this.controller.loadUrl(this.url); } }
  private async checkSession(): Promise<void> {
    if (this.checking) { return; } this.checking = true;
    try {
      // Passive IPC only: this window never owns or starts another Manager/VPN.
      const report = await new Client(this.directory, false).call<Diagnostics>('diagnose');
      if ([report.fnId ?? '', report.username ?? ''].join('|') !== this.identity || !report.administrator || ['UNCONFIGURED', 'AUTH_REQUIRED'].includes(report.status.state)) {
        try { if (this.ready) { this.controller.loadUrl('about:blank'); } } catch (_) {}
        await (getContext(this) as common.UIAbilityContext).terminateSelf(); return;
      }
      this.error = ['ERROR', 'RECONNECTING', 'SERVER_UNAVAILABLE'].includes(report.status.state) ? tr('连接暂不可用，请在主窗口检查连接状态') : '';
    } catch (error) {
      const failure = error as ClientError;
      hilog.warn(0, 'FnCPNAdmin', 'Session check failed: code=%{public}s; operation=%{public}s', failure.failure?.code ?? 'UNKNOWN', failure.failure?.operation ?? 'diagnose');
      this.error = tr('连接服务暂不可用，请在主窗口重新连接');
    }
    finally { this.checking = false; }
  }
  onBackPress(): boolean { if (this.ready && this.controller.accessBackward()) { this.controller.backward(); return true; } return false; }
  build() {
    Column() {
      if (this.error) { Text(this.error).fontColor($r('app.color.danger')).padding(12).width('100%') }
      if (this.url) {
      Web({ src: this.url, controller: this.controller }).javaScriptAccess(true).fileAccess(false).domStorageAccess(true)
        .onControllerAttached(() => { this.ready = true; }).layoutWeight(1)
      }
    }.width('100%').height('100%').backgroundColor($r('app.color.page_background'))
  }
}
