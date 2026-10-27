import { VpnExtensionAbility, vpnExtension } from '@kit.NetworkKit';
import { Want } from '@kit.AbilityKit';
import { fileIo } from '@kit.CoreFileKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import bridge from 'libharmonybridge.so';

interface ProbeReply { error?: string; fd?: number; echo?: boolean; }
export default class VpnProbeAbility extends VpnExtensionAbility {
  private connection: vpnExtension.VpnConnection | undefined = undefined;
  private tun: number = -1;
  private created: boolean = false;
  private stopping: boolean = false;
  private work: Promise<void> = Promise.resolve();
  onCreate(want: Want): void {
    this.work = this.startProbe();
  }
  onDestroy(): void {
    this.stopping = true;
    this.work = this.work.then(async () => { await this.cleanup(); });
  }
  private async native(action: number, fd: number = -1): Promise<ProbeReply> {
    const reply: ProbeReply = JSON.parse(await bridge.vpn(action, fd)) as ProbeReply;
    if (reply.error) { throw new Error(reply.error); }
    return reply;
  }
  private async exchange(protect: boolean): Promise<boolean> {
    const reply = await this.native(3);
    const fd = reply.fd ?? -1;
    try {
      if (protect) { await this.connection?.protect(fd); }
      return (await this.native(5, fd)).echo ?? false;
    } finally {
      await this.native(4, fd);
    }
  }
  private async cleanup(): Promise<void> {
    try { await this.native(2); }
    finally {
      if (this.tun >= 0) { fileIo.closeSync(this.tun); this.tun = -1; }
      if (this.created) {
        await this.connection?.destroy();
        this.created = false;
      }
    }
  }
  private async startProbe(): Promise<void> {
    try {
      this.connection = vpnExtension.createVpnConnection(this.context);
      this.tun = await this.connection.create({
        addresses: [{ address: { address: '198.18.0.2', family: 1 }, prefixLength: 32 }],
        routes: [{ interface: '', destination: { address: { address: '198.18.0.1', family: 1 }, prefixLength: 32 },
          gateway: { address: '', family: 1 }, hasGateway: false, isDefaultRoute: false }],
        mtu: 1400, isIPv4Accepted: true
      });
      this.created = true;
      if (this.stopping) { return; }
      await this.native(1, this.tun);
      const unprotected = await this.exchange(false);
      const protectedEcho = await this.exchange(true);
      hilog.info(0x0000, 'HarmonyVPN', 'probe unprotected=%{public}s protectedEcho=%{public}s',
        String(unprotected), String(protectedEcho));
      if (!unprotected || protectedEcho) { throw new Error('VPN routing/protect sentinel failed'); }
      hilog.info(0x0000, 'HarmonyVPN', 'PASS TUN echo and protected socket bypass');
    } catch (error) {
      hilog.error(0x0000, 'HarmonyVPN', 'FAIL %{public}s', String(error));
    } finally {
      try { await this.cleanup(); hilog.info(0x0000, 'HarmonyVPN', 'cleanup complete'); }
      catch (error) { hilog.error(0x0000, 'HarmonyVPN', 'cleanup failed %{public}s', String(error)); }
    }
  }
}
