import { connection, vpnExtension } from '@kit.NetworkKit';
import { asset } from '@kit.AssetStoreKit';
import { util } from '@kit.ArkTS';
import { fileIo } from '@kit.CoreFileKit';
import { BusinessError, deviceInfo, settings } from '@kit.BasicServicesKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { common } from '@kit.AbilityKit';
import bridge from 'libfncpnbridge.so';

interface Params { fd?: number; address?: string; routes?: string[]; mtu?: number; dns?: string; host?: string; key?: string; value?: string; }
interface SystemRequest { version: number; method: string; params: Params; }
interface SecretReply { found: boolean; value: string; }
interface Snapshot { name: string; index: number; identity: string; prefixes: string[]; addresses: string[]; }
interface DNSReply { servers: string[]; tlsName: string; }
export class SystemAdapter {
  private vpn: vpnExtension.VpnConnection | undefined;
  private tun: number = -1;
  private created: boolean = false;
  private changing: Promise<void> = Promise.resolve();
  private monitor: connection.NetConnection = connection.createNetConnection();
  private context: common.Context;
  private directory: string;
  constructor(context: common.Context, directory: string, vpn?: vpnExtension.VpnConnection) { this.context = context; this.directory = directory; this.vpn = vpn; }
  private record(event: string): void {
    try {
      const folder = this.directory + '/logs'; if (!fileIo.accessSync(folder)) { fileIo.mkdirSync(folder, true); }
      const path = folder + '/privileged.log';
      if (fileIo.accessSync(path) && fileIo.statSync(path).size > 2 * 1024 * 1024) {
        if (fileIo.accessSync(path + '.2')) { fileIo.unlinkSync(path + '.2'); }
        if (fileIo.accessSync(path + '.1')) { fileIo.renameSync(path + '.1', path + '.2'); }
        fileIo.renameSync(path, path + '.1');
      }
      const file = fileIo.openSync(path, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.APPEND);
      try { fileIo.writeSync(file.fd, JSON.stringify({ time: new Date().toISOString(), level: 'INFO', platform: 'harmonyos', msg: event }) + '\n'); }
      finally { fileIo.closeSync(file); }
    } catch (_) {}
  }
  private async deviceName(): Promise<string> {
    try { const name = await settings.getValue(this.context, settings.general.DEVICE_NAME); if (name?.trim()) { return name.trim(); } } catch (_) {}
    return deviceInfo.marketName || deviceInfo.productModel || 'HarmonyOS device';
  }
  register(): void {
    this.record('extension started');
    bridge.setSystem((id: string, raw: string) => { this.dispatch(id, raw); });
    this.monitor.on('netAvailable', () => { bridge.invoke(3, '').catch(() => {}); });
    this.monitor.on('netLost', () => { bridge.invoke(3, '').catch(() => {}); });
    this.monitor.on('netConnectionPropertiesChange', () => { bridge.invoke(3, '').catch(() => {}); });
    this.monitor.register((_error: BusinessError) => {});
  }
  async close(): Promise<void> { await this.remove(); await new Promise<void>((resolve) => { this.monitor.unregister((_error: BusinessError) => { resolve(); }); }); }
  private async physical(): Promise<connection.NetHandle> {
    const nets = await connection.getAllNets();
    const preferred = await connection.getDefaultNet();
    for (const net of [preferred, ...nets]) {
      if (net.netId === 0) { continue; }
      const caps = await connection.getNetCapabilities(net);
      if (caps.networkCap?.includes(connection.NetCap.NET_CAPABILITY_NOT_VPN)) { return net; }
    }
    throw new Error('No physical network');
  }
  private async dispatch(id: string, raw: string): Promise<void> {
    let method = 'decode';
    const started = Date.now();
    try {
      const request = JSON.parse(raw) as SystemRequest;
      method = request.method;
      if (method === 'network.resolve') { hilog.info(0, 'FnCPNNetwork', 'DNS resolution started'); }
      if (request.version !== 1) { throw new Error('System bridge version mismatch'); }
      const result = await this.call(request.method, request.params);
      if (method === 'network.resolve' || method === 'network.protect' || method === 'vpn.establish') {
        const event = method + ' completed in ' + (Date.now() - started) + 'ms';
        this.record(event); hilog.info(0, 'FnCPNNetwork', '%{public}s', event);
      }
      bridge.reply(id, JSON.stringify({ version: 1, ok: true, result: result }));
    } catch (error) {
      const failure = error as BusinessError;
      const detail = 'code=' + String(failure.code ?? 'unknown') + '; ' + (failure.message || String(error));
      // Log only operation/code/timing: credential values, URLs and server text
      // must not be exposed through public HiLog fields.
      const event = method + ' failed in ' + (Date.now() - started) + 'ms; code=' + String(failure.code ?? 'unknown');
      this.record(event); hilog.error(0, 'FnCPNNetwork', '%{public}s', event);
      bridge.reply(id, JSON.stringify({ version: 1, ok: false, error: method + ': ' + detail }));
    }
  }
  private async remove(): Promise<void> {
    if (this.created) { this.record('extension stopping'); }
    // Destroy even if closing our descriptor fails. A failed create can also
    // have left system state behind, so created tracks attempted ownership.
    try {
      if (this.tun >= 0) { const fd = this.tun; this.tun = -1; fileIo.closeSync(fd); }
    } finally {
      if (this.created) { await this.vpn?.destroy(); this.created = false; }
    }
  }
  private async establish(p: Params): Promise<number> {
    if (!this.vpn) { throw new Error('System VPN unavailable in application tunnel mode'); }
    await this.remove();
    this.record('network settings applying');
    const address = (p.address ?? '').split('/');
    if (address.length !== 2 || !p.mtu || p.mtu < 576 || p.mtu > 1500) { throw new Error('Invalid network plan'); }
    const routes: connection.RouteInfo[] = (p.routes ?? []).map((cidr: string): connection.RouteInfo => {
      const parts = cidr.split('/');
      if (parts.length !== 2 || Number(parts[1]) < 0 || Number(parts[1]) > 32 || parts[0].includes(':')) { throw new Error('Invalid IPv4 route'); }
      return { interface: '', destination: { address: { address: parts[0], family: 1 }, prefixLength: Number(parts[1]) }, gateway: { address: '', family: 1 }, hasGateway: false, isDefaultRoute: false };
    });
    this.created = true;
    try {
    this.tun = await this.vpn.create({ addresses: [{ address: { address: address[0], family: 1 }, prefixLength: Number(address[1]) }], routes: routes, mtu: p.mtu, dnsAddresses: p.dns ? [p.dns] : [], isIPv4Accepted: true });
    } catch (error) {
      const failure = error as BusinessError;
      const original = 'VPN create code=' + String(failure.code ?? 'unknown') + '; ' + (failure.message || String(error));
      try { await this.remove(); }
      catch (cleanup) {
        const cause = cleanup as BusinessError;
        throw new Error(original + '; cleanup code=' + String(cause.code ?? 'unknown') + '; ' + (cause.message || String(cleanup)));
      }
      throw new Error(original);
    }
    this.record('network settings applied');
    return this.tun;
  }
  private async secrets(method: string, p: Params): Promise<Object> {
    if (!p.key || !/^[0-9a-f]{64}$/.test(p.key)) { throw new Error('Invalid credential alias'); }
    const alias = new util.TextEncoder().encodeInto(p.key);
    const query: asset.AssetMap = new Map(); query.set(asset.Tag.ALIAS, alias);
    const base64 = new util.Base64Helper();
    if (method === 'secret.get') {
      query.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ALL);
      try {
        const rows = await asset.query(query);
        if (rows.length === 0) { return { found: false, value: '' } as SecretReply; }
        return { found: true, value: base64.encodeToStringSync(rows[0].get(asset.Tag.SECRET) as Uint8Array) } as SecretReply;
      } catch (error) {
        if ((error as BusinessError).code === asset.ErrorCode.NOT_FOUND) { return { found: false, value: '' } as SecretReply; }
        throw new Error(String(error));
      }
    }
    if (method === 'secret.delete') {
      try { await asset.remove(query); } catch (error) { if ((error as BusinessError).code !== asset.ErrorCode.NOT_FOUND) { throw new Error(String(error)); } }
      return {} as Record<string, Object>;
    }
    if (method !== 'secret.put' || !p.value || p.value.length > 90000) { throw new Error('Invalid credential value'); }
    const update: asset.AssetMap = new Map(); update.set(asset.Tag.SECRET, base64.decodeSync(p.value));
    try { await asset.update(query, update); }
    catch (error) {
      if ((error as BusinessError).code !== asset.ErrorCode.NOT_FOUND) { throw new Error(String(error)); }
      query.set(asset.Tag.SECRET, base64.decodeSync(p.value));
      query.set(asset.Tag.ACCESSIBILITY, asset.Accessibility.DEVICE_FIRST_UNLOCKED);
      query.set(asset.Tag.SYNC_TYPE, asset.SyncType.NEVER);
      await asset.add(query);
    }
    return {} as Record<string, Object>;
  }
  private async call(method: string, p: Params): Promise<Object> {
    if (method.startsWith('secret.')) { return this.secrets(method, p); }
    switch (method) {
      case 'device.name': return this.deviceName();
      case 'network.snapshot': {
        const net = await this.physical(); const props = await connection.getConnectionProperties(net);
        return { name: props.interfaceName, index: net.netId, identity: String(net.netId), prefixes: props.linkAddresses.map((v: connection.LinkAddress) => v.address.address + '/' + v.prefixLength), addresses: props.linkAddresses.map((v: connection.LinkAddress) => v.address.address) } as Snapshot;
      }
      case 'network.resolve': return (await (await this.physical()).getAddressesByName(p.host ?? '')).map((v: connection.NetAddress) => v.address);
      case 'network.dns': {
        const props = await connection.getConnectionProperties(await this.physical());
        return { servers: props.dnses.map((v: connection.NetAddress) => v.address), tlsName: '' } as DNSReply;
      }
      case 'network.protect':
        if (p.fd === undefined || p.fd < 0) { throw new Error('Invalid socket'); }
        if (this.vpn) { await this.vpn.protect(p.fd); } return {} as Record<string, Object>;
      case 'vpn.establish': {
        let fd = -1;
        const operation = this.changing.then(async () => { fd = await this.establish(p); });
        this.changing = operation.catch(() => {}); await operation; return fd;
      }
      case 'vpn.remove': {
        const operation = this.changing.then(async () => { await this.remove(); });
        this.changing = operation.catch(() => {}); await operation; return {} as Record<string, Object>;
      }
      default: throw new Error('Unsupported system method');
    }
  }
}
