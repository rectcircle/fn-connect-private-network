import { deviceInfo } from '@kit.BasicServicesKit';
import { common } from '@kit.AbilityKit';
import { vpnAvailability } from '../controller/LayoutPolicy';

export function deviceType(): string { return deviceInfo.deviceType; }
export function isDesktop(): boolean { return ['2in1', 'pc'].includes(deviceType()); }
export function vpnUnavailable(simulator: boolean): string {
  if (!simulator && !canIUse('SystemCapability.Security.Asset')) { return '当前设备不支持安全凭据存储'; }
  return vpnAvailability(deviceType(), deviceInfo.sdkApiVersion, canIUse('SystemCapability.Communication.NetManager.Vpn'), simulator);
}
export async function openLogDirectory(context: common.UIAbilityContext, uri: string): Promise<void> {
  // A picker is not a standalone file manager. Unsupported devices keep the export.
  await context.startAbility({ bundleName: 'com.huawei.hmos.filemanager', abilityName: 'MainAbility', parameters: { fileUri: uri } });
}
