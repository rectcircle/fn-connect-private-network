import { failureMessage } from './FailureMessages';
import { tr } from './Messages';
import bridge from 'libfncpnbridge.so';
import { vpnUnavailable } from './DeviceServices';
import { vpnExtension } from '@kit.NetworkKit';
export interface Failure { code: string; message: string; remoteCode?: string; clientVersion?: string; serverVersion?: string; upgradeTarget?: string; operation?: string; httpStatus?: number; detail?: string; requestId?: string; }
interface Response<T> { version: number; ok: boolean; result: T; error?: Failure; }
export interface Status { state: string; path?: string; lastError?: Failure; interface?: string; mtu?: number; lastHandshake?: string; localNetworkWaiting?: boolean; dns?: DNSStatus; }
export interface DNSStatus { state?: string; message?: string; suffix?: string; lastError?: Failure; }
export interface Direct { reason?: string; lastError?: Failure; }
export interface Diagnostics { administrator?: boolean; status: Status; fnId?: string; username?: string; clientAddress?: string; nasAddress?: string; networkInterface?: string; handshakeAge?: string; direct?: Direct; privilegedAvailable?: boolean; privilegedActive?: boolean; errors?: Failure[]; }
export interface Authorization { requestId: string; state: string; }
export interface Admin { url: string; }
export interface SupportArchive { name: string; size: number; }
interface ConnectionIntent { configured: boolean; autoConnect: boolean; }
interface CommandParams { target?: string; address?: string; requestId?: string; fnId?: string; username?: string; password?: string; accessCode?: string; twofaCode?: string; trustDevice?: boolean; }
interface CommandRequest { version: number; directory: string; method: string; params: CommandParams; }
export class ClientError extends Error {
  failure: Failure;
  constructor(failure: Failure) { super(failureMessage(failure)); this.failure = failure; }
}
export class Client {
  directory: string;
  readonly uiDebug: boolean;
  private startup: Promise<void> | undefined = undefined;
  private authorization: string = '';
  private authorizationFNID: string = '';
  private credentials: CommandParams = {};
  constructor(directory: string, applicationOnly: boolean) { this.directory = directory; this.uiDebug = applicationOnly; }
  async call<T>(method: string, params: CommandParams = {}): Promise<T> {
    if (this.uiDebug && method !== 'export-support') { await this.startApplication(); }
    const response = JSON.parse(await bridge.invoke(1, JSON.stringify({ version: 1, directory: this.directory, method: method, params: params } as CommandRequest))) as Response<T>;
    if (response.version !== 1 || !response.ok) {
      throw new ClientError(response.error ?? { code: 'UNAVAILABLE', message: tr('连接服务未启动，请允许 VPN 后重试') });
    }
    return response.result;
  }
  private async startApplication(): Promise<void> {
    if (!this.startup) {
      this.startup = bridge.invoke(4, this.directory).then((raw: string) => {
        const response = JSON.parse(raw) as Response<Object>;
        if (response.version !== 1 || !response.ok) { throw new ClientError(response.error ?? { code: 'UNAVAILABLE', message: tr('应用内客户端启动失败') }); }
      });
      this.startup.catch(() => { this.startup = undefined; });
    }
    await this.startup;
  }
  // Passive UI polling must not require VPN authorization on a fresh install.
  // Keep status itself strict: ensure() uses it to check that the service is alive.
  async inspectStatus(): Promise<Status | undefined> {
    try { return await this.call<Status>('status'); }
    catch (error) {
      if (this.uiDebug) { throw error as Error; }
      const failure = error as ClientError;
      if (failure.failure?.code !== 'UNAVAILABLE') { throw error as Error; }
      const intent = JSON.parse(await bridge.invoke(5, this.directory)) as Response<ConnectionIntent>;
      if (intent.version !== 1 || !intent.ok) { throw new ClientError(intent.error ?? { code: 'UNAVAILABLE', message: tr('读取连接意图失败') }); }
      if (!intent.result.configured) { return undefined; }
      throw error as Error;
    }
  }
  async resume(): Promise<void> {
    if (this.uiDebug) { await this.startApplication(); return; }
    try { await this.call<Status>('status'); return; } catch (_) {}
    const reply = JSON.parse(await bridge.invoke(5, this.directory)) as Response<ConnectionIntent>;
    if (reply.version !== 1 || !reply.ok) { throw new ClientError(reply.error ?? { code: 'UNAVAILABLE', message: tr('读取连接意图失败') }); }
    if (reply.result.autoConnect) { await this.ensure(); }
  }
  async exit(): Promise<void> {
    await this.cancelAuthorization();
    // Ordinary app close preserves Manager's AutoConnect intent. Explicit
    // Disconnect remains the only UI action that pauses future connection.
    if (this.uiDebug) { await bridge.invoke(2, ''); this.startup = undefined; }
  }
  async ensure(): Promise<void> {
    if (this.uiDebug) { await this.startApplication(); return; }
    const unavailable = vpnUnavailable(false);
    if (unavailable) { throw new Error(tr(unavailable)); }
    try { await this.call<Status>('status'); return; } catch (_) {}
    await Promise.race<void>([
      vpnExtension.startVpnExtensionAbility({ bundleName: 'cn.rectcircle.fncpn', abilityName: 'VpnAbility' }),
      new Promise<void>((_, reject) => { setTimeout(() => reject(new Error(tr('等待 VPN 授权超时，请确认系统授权后重试'))), 15000); })
    ]);
    for (let count = 0; count < 30; count++) {
      try { await this.call<Status>('status'); return; } catch (_) {}
      await new Promise<void>((resolve) => { setTimeout(resolve, 300); });
    }
    throw new Error(tr('连接服务启动失败，请检查 VPN 授权'));
  }
  async cancelAuthorization(): Promise<void> {
    const id = this.authorization;
    this.authorization = ''; this.authorizationFNID = ''; this.credentials = {};
    if (id) { await this.call<Object>('authorization-cancel', { requestId: id }); }
  }
  async login(fnId: string, username: string, password: string, accessCode: string, twofaCode: string, trustDevice: boolean): Promise<void> {
    await this.ensure();
    if (this.authorization && this.authorizationFNID !== fnId) { await this.cancelAuthorization(); }
    if (!this.authorization) {
      const pending = await this.call<Authorization>('authorization-begin', { fnId: fnId });
      this.authorization = pending.requestId; this.authorizationFNID = fnId;
    }
    const input: CommandParams = { requestId: this.authorization, fnId: fnId, username: username || this.credentials.username, password: password || this.credentials.password, accessCode: accessCode || this.credentials.accessCode, twofaCode: twofaCode, trustDevice: trustDevice };
    try {
      await this.call<Object>('authorize-native', input);
      this.authorization = ''; this.authorizationFNID = ''; this.credentials = {};
      // The shared Manager schedules connection after successful authorization.
    } catch (error) {
      const failure = error as ClientError;
      if (!failure.failure?.remoteCode || !['ACCESS_CODE_INVALID','ACCESS_CODE_REQUIRED','ACCESS_CODE_RATE_LIMITED','TWO_FACTOR_REQUIRED','TWO_FACTOR_SETUP_REQUIRED','TWO_FACTOR_CODE_INVALID'].includes(failure.failure.remoteCode)) {
        this.authorization = ''; this.authorizationFNID = ''; this.credentials = {};
      } else { input.twofaCode = ''; this.credentials = input; }
      throw new ClientError(failure.failure ?? { code: 'UNAVAILABLE', message: String(error) });
    }
  }
}
