import { UIAbility, Want } from '@kit.AbilityKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { window } from '@kit.ArkUI';
export default class EntryAbility extends UIAbility {
  onCreate(want: Want): void {
    hilog.info(0x0000, 'HarmonyVPN', 'launch action %{public}s', String(want.parameters?.['vpnAction'] ?? 'none'));
    AppStorage.setOrCreate<string>('vpnAction', String(want.parameters?.['vpnAction'] ?? ''));
    AppStorage.setOrCreate<string>('probeUrl', String(want.parameters?.['probeUrl'] ?? ''));
  }
  onWindowStageCreate(stage: window.WindowStage): void {
    stage.loadContent('pages/Index');
  }
}
