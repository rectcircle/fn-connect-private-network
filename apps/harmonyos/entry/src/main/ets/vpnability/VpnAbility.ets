import { VpnExtensionAbility, vpnExtension } from '@kit.NetworkKit';
import { Want } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import bridge from 'libfncpnbridge.so';
import { SystemAdapter } from '../platform/SystemAdapter';
interface InitReply { version: number; ok: boolean; }
export default class VpnAbility extends VpnExtensionAbility {
  private adapter: SystemAdapter | undefined;
  private startup: Promise<string> = Promise.resolve('');
  onCreate(want: Want): void {
    this.adapter = new SystemAdapter(this.context, this.context.filesDir, vpnExtension.createVpnConnection(this.context));
    this.adapter.register();
    this.startup = bridge.invoke(0, this.context.filesDir).then((raw: string): string => {
      const reply = JSON.parse(raw) as InitReply;
      if (reply.version !== 1 || !reply.ok) { throw new Error('Client initialization failed'); }
      return raw;
    });
    this.startup.catch(() => { hilog.error(0, 'FnCPN', 'Client host failed to start'); });
  }
  onDestroy(): void {
    this.startup.then(async () => {
      await bridge.invoke(2, '');
      await this.adapter?.close();
    }).catch(() => { hilog.error(0, 'FnCPN', 'VPN cleanup failed'); });
  }
}
