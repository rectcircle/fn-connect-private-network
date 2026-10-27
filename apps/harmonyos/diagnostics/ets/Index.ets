import { vpnExtension } from '@kit.NetworkKit';
import bridge from 'libharmonybridge.so';
import { hilog } from '@kit.PerformanceAnalysisKit';
@Entry
@Component
struct Index {
  @State result: string = 'Not run';
  @State url: string = '';
  @State busy: boolean = false;
  aboutToAppear(): void {
    this.url = AppStorage.get<string>('probeUrl') ?? '';
    this.runProbe();
    if (AppStorage.get<string>('vpnAction') === 'start') {
      setTimeout(() => { this.startVPN(); }, 1000);
    }
  }
  startVPN(): void {
    hilog.info(0x0000, 'HarmonyVPN', 'request VPN extension');
    vpnExtension.startVpnExtensionAbility({ bundleName: 'cn.rectcircle.fncpn.harmonysmoke', abilityName: 'VpnProbeAbility' })
      .catch((error: Error) => { hilog.error(0x0000, 'HarmonyVPN', 'start failed %{public}s', String(error)); });
  }
  async runProbe(): Promise<void> {
    this.busy = true;
    try {
      this.result = await bridge.run(this.url);
      hilog.info(0x0000, 'HarmonySmoke', '%{public}s', this.result);
    } catch (error) {
      this.result = 'Probe failed: ' + String(error);
      hilog.error(0x0000, 'HarmonySmoke', '%{public}s', this.result);
    } finally {
      this.busy = false;
    }
  }
  build() {
    Column({ space: 16 }) {
      Text('Go runtime verification').fontSize(24)
      TextInput({ placeholder: 'Optional HTTPS / WSS URL', text: this.url })
        .onChange((value: string) => { this.url = value; })
      Button(this.busy ? 'Running' : 'Run verification')
        .enabled(!this.busy).onClick(() => { this.runProbe(); })
      Button('Run VPN diagnostic').onClick(() => {
        this.startVPN();
      })
      Button('Stop VPN diagnostic').onClick(() => {
        vpnExtension.stopVpnExtensionAbility({ bundleName: 'cn.rectcircle.fncpn.harmonysmoke', abilityName: 'VpnProbeAbility' });
      })
      Text(this.result).fontSize(16)
    }.padding(24).width('100%')
  }
}
