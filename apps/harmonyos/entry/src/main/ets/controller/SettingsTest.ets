import { http } from '@kit.NetworkKit';
import { util } from '@kit.ArkTS';
import { Client } from '../platform/Client';
import { tr } from '../platform/Messages';

interface NativeInput { version: number; id: string; method: string; params: NativeParams; }
interface NativeParams { link: string; }
interface NativeEndpoint { version: number; url: string; token: string; }
interface NativeResponse { version: number; id: string; ok: boolean; result: TestStatus; error?: NativeFailure; }
interface NativeFailure { message: string; }
export interface BuildInfo { version: string; commit: string; }
export interface TestResult {
  link: string; outcome?: string; reason?: string; message?: string; latencyMs?: number; timeoutPercent?: number; downloadMbps?: number; uploadMbps?: number;
  downloadLimited?: boolean; uploadLimited?: boolean; error?: string;
}
export interface TestStatus {
  active: boolean; stage: string; link?: string; mbps?: number; rttMs?: number; progress?: number; sampleElapsedMs?: number; targetSampleMs?: number;
  echoSent?: number; echoReceived?: number; results: TestResult[]; error?: string; restoreError?: string;
}
export function emptyTest(): TestStatus { return { active: false, stage: 'idle', results: [] }; }

// An unavailable IPv6 path is an expected capability state, not a failed measurement.
export function ipv6Availability(result: TestResult): string {
  if (result.outcome !== 'unavailable') { return ''; }
  return tr(result.message ?? result.reason ?? '不可用');
}

// One cancellable stream, owned by the visible native page. No daemon test task.
export class SettingsTest {
  private request: http.HttpRequest | undefined;
  private canceled: boolean = false;
  cancel(): void { this.canceled = true; this.request?.destroy(); this.request = undefined; }
  async run(client: Client, link: string, receive: (status: TestStatus) => void): Promise<void> {
    try { await client.ensure(); } catch (error) { if (this.canceled) { return; } throw error as Error; }
    if (this.canceled) { return; }
    let address: NativeEndpoint;
    try { address = await client.call<NativeEndpoint>('native-transport'); } catch (error) { if (this.canceled) { return; } throw error as Error; }
    if (this.canceled) { return; }
    const match = /^http:\/\/127\.0\.0\.1:(\d+)\/ipc$/.exec(address.url);
    if (!match || !/^[a-f0-9]{64}$/.test(address.token)) { throw new Error(tr('无效的设置页面地址')); }
    const request = http.createHttp(); this.request = request;
    const decoder = new util.TextDecoder('utf-8');
    let pending = '', completed = false;
    let streamError: Error | undefined;
    request.on('dataReceive', (data: ArrayBuffer) => {
      if (this.canceled || streamError) { return; }
      try {
        pending += decoder.decodeWithStream(new Uint8Array(data), { stream: true });
        if (pending.length > 256 * 1024) { throw new Error(tr('响应过大')); }
        let newline = pending.indexOf('\n');
        while (newline >= 0) {
          const line = pending.substring(0, newline); pending = pending.substring(newline + 1);
          if (line.trim()) {
            const envelope = JSON.parse(line) as NativeResponse;
            if (envelope.version !== address.version || envelope.id !== 'native-speedtest') { throw new Error(tr('无效响应')); }
            if (!envelope.ok) { throw new Error(envelope.error?.message ?? tr('测速失败')); }
            const status = envelope.result;
            if (typeof status.active !== 'boolean' || typeof status.stage !== 'string') { throw new Error(tr('无效响应')); }
            status.results = status.results ?? [];
            receive(status); completed = !status.active;
          }
          newline = pending.indexOf('\n');
        }
      } catch (error) { streamError = error as Error; request.destroy(); }
    });
    try {
      const status = await request.requestInStream(address.url, {
        method: http.RequestMethod.POST, header: { 'Authorization': 'Bearer ' + address.token, 'Content-Type': 'application/json' },
        extraData: JSON.stringify({ version: address.version, id: 'native-speedtest', method: 'speedtest', params: { link: link } } as NativeInput), usingCache: false, usingProxy: false, maxRedirects: 0, connectTimeout: 5000, readTimeout: 360000
      });
      if (streamError) { throw streamError; }
      if (!this.canceled && (status !== 200 || !completed)) { throw new Error(tr('测试连接已中断，请重试')); }
    } catch (error) {
      if (!this.canceled) { throw streamError ?? error as Error; }
    } finally { request.destroy(); if (this.request === request) { this.request = undefined; } }
  }
}
