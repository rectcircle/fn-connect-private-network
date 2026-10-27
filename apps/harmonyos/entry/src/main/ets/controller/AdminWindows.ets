import { common } from '@kit.AbilityKit';

export class AdminWindows {
  private static closeWindow: (() => void) | undefined;
  static register(close: () => void): void { AdminWindows.closeWindow = close; }
  static unregister(): void { AdminWindows.closeWindow = undefined; }
  static invalidate(): void { const close = AdminWindows.closeWindow; AdminWindows.closeWindow = undefined; close?.(); }
  static async open(context: common.UIAbilityContext, url: string, directory: string, identity: string): Promise<void> {
    if (!/^http:\/\/127\.0\.0\.1:\d+\//.test(url)) { throw new Error('Invalid local administration URL'); }
    await context.startAbility({ bundleName: 'cn.rectcircle.fncpn', abilityName: 'AdminAbility',
      parameters: { adminURL: url, directory: directory, identity: identity } }, { withAnimation: true });
  }
}
