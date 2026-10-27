import { tr } from '../platform/Messages';
import { configureWindow } from '../platform/WindowStartup';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { BusinessError } from '@kit.BasicServicesKit';
import { UIAbility, Want } from '@kit.AbilityKit';
import { window } from '@kit.ArkUI';
import { AdminWindows } from '../controller/AdminWindows';

export default class AdminAbility extends UIAbility {
  private storage: LocalStorage = new LocalStorage();
  private update(want: Want): void {
    const url = want.parameters?.adminURL as string ?? '';
    if (!/^http:\/\/127\.0\.0\.1:\d+\//.test(url)) { this.context.terminateSelf(); return; }
    this.storage.setOrCreate('url', url);
    this.storage.setOrCreate('directory', want.parameters?.directory as string ?? '');
    this.storage.setOrCreate('identity', want.parameters?.identity as string ?? '');
  }
  onCreate(want: Want): void {
    this.update(want);
    AdminWindows.register(() => { this.context.terminateSelf().catch(() => {}); });
  }
  onNewWant(want: Want): void { this.update(want); }
  async onWindowStageCreate(stage: window.WindowStage): Promise<void> {
    try {
      await stage.loadContent('pages/AdminWindow', this.storage);
      hilog.info(0, 'FnCPNStartup', 'Admin content loaded');
      const main = await stage.getMainWindow();
      await configureWindow(main, true, tr('FnCPN · 管理后台'), 480);
    } catch (error) {
      const failure = error as BusinessError;
      hilog.error(0, 'FnCPNStartup', 'Admin startup failed: code=%{public}d; %{public}s', failure.code, failure.message);
    }
  }

  onDestroy(): void { AdminWindows.unregister(); }
}
