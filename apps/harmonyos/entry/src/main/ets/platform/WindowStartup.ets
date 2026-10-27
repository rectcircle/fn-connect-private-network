import { window } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { BusinessError } from '@kit.BasicServicesKit';

// Window decorations and size constraints are optional across device/window modes.
// They must never prevent the ability from loading its content.
async function optionalSetting(name: string, action: () => Promise<void>): Promise<void> {
  try { await action(); }
  catch (error) {
    const failure = error as BusinessError;
    hilog.warn(0, 'FnCPNStartup', '%{public}s: code=%{public}d; %{public}s', name, failure.code, failure.message);
  }
}
export async function configureWindow(main: window.Window, desktop: boolean, title: string, minWidth: number): Promise<void> {
  await optionalSetting('layout', async () => { await main.setWindowLayoutFullScreen(false); });
  if (desktop) {
    await optionalSetting('decorations', async () => { main.setWindowDecorVisible(true); });
    await optionalSetting('title', async () => { await main.setWindowTitle(title); });
    await optionalSetting('limits', async () => { await main.setWindowLimits({ minWidth: minWidth, minHeight: 360 }); });
  }
}
