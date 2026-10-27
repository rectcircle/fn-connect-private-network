import { hilog } from '@kit.PerformanceAnalysisKit';
import { BusinessError } from '@kit.BasicServicesKit';
import { configureWindow } from '../platform/WindowStartup';
import { UIAbility } from '@kit.AbilityKit';
import { isDesktop, deviceType } from '../platform/DeviceServices';
import { display } from '@kit.ArkUI';
import { window } from '@kit.ArkUI';
import { SystemAdapter } from '../platform/SystemAdapter';
import bridge from 'libfncpnbridge.so';
import BuildProfile from 'BuildProfile';
export default class EntryAbility extends UIAbility {
  private adapter: SystemAdapter | undefined;
  private main: window.Window | undefined;
  private foldChanged = (): void => { this.syncFoldArea(); };
  private sizeChanged = (): void => { this.syncFoldArea(); };
  private syncFoldArea(): void {
    AppStorage.setOrCreate<number>('fontScale', this.context.config.fontSizeScale ?? 1);
    let right = 0; let bottom = 0;
    try {
      if (this.main && display.isFoldable() && display.getFoldStatus() === display.FoldStatus.FOLD_STATUS_HALF_FOLDED) {
        const bounds = this.main.getWindowProperties().windowRect;
        for (const rect of display.getCurrentFoldCreaseRegion().creaseRects) {
          const x = rect.left - bounds.left; const y = rect.top - bounds.top;
          if (rect.width > bounds.width * 0.5 && y > 0 && y < bounds.height) { bottom = Math.max(bottom, px2vp(bounds.height - y)); }
          if (rect.height > bounds.height * 0.5 && x > 0 && x < bounds.width) { right = Math.max(right, px2vp(bounds.width - x)); }
        }
      }
    } catch (_) { /* Flat/non-fold devices keep the entire window available. */ }
    AppStorage.setOrCreate<number>('foldRight', right); AppStorage.setOrCreate<number>('foldBottom', bottom);
  }
  onCreate(): void {
    AppStorage.setOrCreate<number>('resumeEpoch', 0);
    AppStorage.setOrCreate<number>('fontScale', this.context.config.fontSizeScale ?? 1);
    AppStorage.setOrCreate<number>('foldRight', 0); AppStorage.setOrCreate<number>('foldBottom', 0);
    AppStorage.setOrCreate<boolean>('uiDebug', ['simulator'].includes(BuildProfile.PRODUCT_NAME));
  }
  async onWindowStageCreate(stage: window.WindowStage): Promise<void> {
    const application = AppStorage.get<boolean>('uiDebug') === true;
    AppStorage.setOrCreate<string>('filesDir', this.context.filesDir + (application ? '/application-tunnel' : ''));
    if (application) { this.adapter = new SystemAdapter(this.context, this.context.filesDir + '/application-tunnel'); this.adapter.register(); }
    try {
      await stage.loadContent('pages/Index');
      hilog.info(0, 'FnCPNStartup', 'Index content loaded');
    } catch (error) {
      const failure = error as BusinessError;
      hilog.error(0, 'FnCPNStartup', 'Index load failed: code=%{public}d; %{public}s', failure.code, failure.message);
      return;
    }
    try {
      this.main = await stage.getMainWindow();
      await configureWindow(this.main, isDesktop(), 'FnCPN', 360);
      this.main.on('windowSizeChange', this.sizeChanged);
      try { if (display.isFoldable()) { display.on('foldStatusChange', this.foldChanged); } } catch (_) {}
      this.syncFoldArea();
      this.syncBars();
    } catch (error) {
      const failure = error as BusinessError;
      hilog.warn(0, 'FnCPNStartup', 'Window setup failed: code=%{public}d; %{public}s', failure.code, failure.message);
    }
  }

  private syncBars(): void {
    if (!this.main) { return; }
    try {
      const background = '#' + (this.context.resourceManager.getColorSync($r('app.color.page_background').id) & 0xFFFFFF).toString(16).padStart(6, '0');
      const foreground = '#' + (this.context.resourceManager.getColorSync($r('app.color.foreground').id) & 0xFFFFFF).toString(16).padStart(6, '0');
      this.main.setWindowSystemBarProperties({ statusBarColor: background, navigationBarColor: background, statusBarContentColor: foreground, navigationBarContentColor: foreground }).catch(() => {});
    } catch (_) {}
  }
  onForeground(): void { this.syncBars(); this.syncFoldArea(); AppStorage.setOrCreate<number>('resumeEpoch', (AppStorage.get<number>('resumeEpoch') ?? 0) + 1); }
  onConfigurationUpdate(): void { this.syncBars(); this.syncFoldArea(); }
  onDestroy(): void {
    this.main?.off('windowSizeChange', this.sizeChanged);
    try { display.off('foldStatusChange', this.foldChanged); } catch (_) {}
    if (this.adapter) { bridge.invoke(2, '').then(async () => { await this.adapter?.close(); }).catch(() => {}); }
  }
}
