import { failureMessage, dnsStateName } from '../platform/FailureMessages';
import { StatusRow } from '../components/StatusRow';
import { initialLoginPage } from '../controller/LoginPolicy';
import { resolveLayout, loginColumns } from '../controller/LayoutPolicy';
import { AdminWindows } from '../controller/AdminWindows';
import { deviceType, isDesktop, vpnUnavailable, openLogDirectory } from '../platform/DeviceServices';
import { LoginHero } from '../components/LoginHero';
import { BrandHeader } from '../components/BrandHeader';
import { i18n } from '@kit.LocalizationKit';
import { KeyCode } from '@kit.InputKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { tr } from '../platform/Messages';
import { Client, ClientError, Status, Diagnostics, Admin, Failure, SupportArchive } from '../platform/Client';
import { webview } from '@kit.ArkWeb';
import { pasteboard } from '@kit.BasicServicesKit';
import { fileIo, picker, fileUri } from '@kit.CoreFileKit';
import { util } from '@kit.ArkTS';
import { common } from '@kit.AbilityKit';
import { LengthMetrics, ColorMetrics, KeyboardAvoidMode } from '@kit.ArkUI';

const BUTTON_RADIUS: number = 8;

@Entry
@Component
struct Index {
  @State state: string = 'UNCONFIGURED';
  @State path: string = '';
  @State identity: string = '';
  @State dnsSuffix: string = '';
  @State loginFromMenu: boolean = false;
  private loginReturnEditing: boolean = false;
  private adminGeneration: number = 0;
  @State adminLoading: boolean = false;
  @State clientAddress: string = '';
  @State nasAddress: string = '';
  @State network: string = '';
  @State handshake: string = '';
  @State dns: string = '';
  @State dnsDegraded: boolean = false;
  @State direct: string = '';
  @State privileged: string = '';
  @State message: string = '';
  @State diagnostics: string = tr('无异常');
  @State loginPage: boolean = true;
  @State menu: boolean = false;
  @State drawerProgress: number = 0;
  private gestureX: number = -1;
  private gestureProgress: number = 0;
  private drawerDragging: boolean = false;
  @State busy: boolean = false;
  @State challenge: boolean = false;
  @State fnId: string = '';
  @State username: string = '';
  @State password: string = '';
  @State accessCode: string = '';
  @State otp: string = '';
  @State trust: boolean = false;
  @State privacyAccepted: boolean = false;
  @State adminURL: string = '';
  @State privacyURL: string = '';
  private privacyController: webview.WebviewController = new webview.WebviewController();
  @State viewportWidth: number = 360;
  @State viewportHeight: number = 800;
  @State contentWidth: number = 360;
  @State sidebarCollapsed: boolean = false;
  @StorageLink('foldRight') foldRight: number = 0;
  @StorageLink('foldBottom') foldBottom: number = 0;
  @StorageLink('fontScale') fontScale: number = 1;
  private readonly tv: boolean = deviceType() === 'tv';
  private embedded(): boolean { return resolveLayout(this.viewportWidth, this.viewportHeight, this.fontScale, this.tv, this.sidebarCollapsed).embedded; }
  private columns(): boolean { return this.contentWidth >= 840 * Math.max(1, this.fontScale); }
  private controlHeight(): number { return this.tv ? 64 : 44; }
  @State logBody: string = '';
  @State logsOpen: boolean = false;
  @State logFile: string = 'client.log';
  @State logOptions: SelectOption[] = [];
  @State browsingLogs: boolean = false;
  @StorageLink('resumeEpoch') @Watch('resumeConnection') resumeEpoch: number = 0;
  @State networkDebug: boolean = false;
  @State probeTarget: string = '';
  @State probeResult: string = '';
  private client: Client = new Client('', false);
  @State uiDebug: boolean = AppStorage.get<boolean>('uiDebug') === true;
  @State showDebugUI: boolean = AppStorage.get<boolean>('showDebugUI') === true;
  private formScroll: Scroller = new Scroller();
  private logScroll: Scroller = new Scroller();
  private logScrollPending: boolean = false;
  private timer: number = -1;
  private reading: boolean = false;
  private initialized: boolean = false;
  private sessionGeneration: number = 0;
  @State localWaiting: boolean = false;
  private adminController: webview.WebviewController = new webview.WebviewController();
  private report: Diagnostics | undefined = undefined;
  aboutToAppear(): void {
    this.getUIContext().setKeyboardAvoidMode(KeyboardAvoidMode.RESIZE);
    this.uiDebug = AppStorage.get<boolean>('uiDebug') === true;
    this.client = new Client(AppStorage.get<string>('filesDir') ?? '', this.uiDebug);
    this.client.resume().then(() => this.refresh(true)).catch((error: Error) => { this.message = error.message || String(error); }).then(() => { hilog.info(0, 'FnCPNUI', 'UI ready; native transport call completed'); });
    this.timer = setInterval(() => { if (!this.busy) { this.refresh(false); } }, 2000);
  }
  private resumeConnection(): void {
    if (!this.client.directory || this.busy) { return; }
    this.client.resume().then(() => this.refresh(true)).catch((error: Error) => { this.message = error.message || String(error); });
  }
  aboutToDisappear(): void { if (this.timer >= 0) { clearInterval(this.timer); this.timer = -1; } }
  onBackPress(): boolean {
    if (this.adminLoading) { this.cancelAdmin(); return true; }
    if (this.busy) { return true; }
    if (this.networkDebug) { this.networkDebug = false; return true; }
    if (this.privacyURL) { this.closePrivacy(); return true; }
    if (this.adminURL) { this.closeAdmin(); return true; }
    if (this.logsOpen) { this.logsOpen = false; return true; }
    if (this.menu) { this.closeMenu(); return true; }
    if (this.loginPage && this.loginFromMenu) {
      this.cancelLogin(); return true;
    }
    return false;
  }
  private setMenu(open: boolean): void {
    if (open) { this.menu = true; }
    this.getUIContext().animateTo({ duration: 240, curve: Curve.EaseOut, onFinish: () => {
      if (this.drawerProgress === 0) { this.menu = false; }
    } }, () => { this.drawerProgress = open ? 1 : 0; });
  }
  private closeMenu(): void { this.setMenu(false); }
  private clearLoginSecrets(): void {
    this.password = ''; this.accessCode = ''; this.otp = ''; this.trust = false; this.challenge = false;
  }
  private async enterLogin(change: boolean): Promise<void> {
    if (this.busy) { return; }
    this.busy = true;
    try {
      AdminWindows.invalidate();
      await this.client.cancelAuthorization();
      this.loginReturnEditing = this.loginPage;
      this.closeMenu(); this.message = ''; this.clearLoginSecrets(); this.privacyAccepted = false;
      this.fnId = change ? '' : this.report?.fnId ?? this.fnId;
      this.username = change ? '' : this.report?.username ?? this.username;
      this.loginFromMenu = true;
      this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => { this.loginPage = true; });
    } catch (error) { this.message = (error as Error).message || String(error); }
    finally { this.busy = false; }
  }
  private async cancelLogin(): Promise<void> {
    if (this.busy) { return; }
    this.busy = true;
    try {
      await this.client.cancelAuthorization();
      this.clearLoginSecrets(); this.message = '';
      const editing = this.loginReturnEditing || ['UNCONFIGURED','AUTH_REQUIRED'].includes(this.state);
      if (editing) { this.fnId = this.report?.fnId ?? ''; this.username = this.report?.username ?? ''; }
      this.loginFromMenu = false;
      this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => { this.loginPage = editing; });
      this.setMenu(true);
    } catch (error) { this.message = (error as Error).message || String(error); }
    finally { this.busy = false; }
  }
  private async previousLogin(): Promise<void> {
    if (this.busy) { return; } this.busy = true;
    try { await this.client.cancelAuthorization(); this.clearLoginSecrets(); this.message = ''; }
    catch (error) { this.message = (error as Error).message || String(error); }
    finally { this.busy = false; }
  }
  private navigate(): void {
    if (this.privacyURL) { this.closePrivacy(); }
    else if (this.adminURL) { this.closeAdmin(); }
    else if (this.logsOpen) { this.logsOpen = false; }
    else if (this.loginPage && this.loginFromMenu) { this.cancelLogin(); }
    else if (this.tv) { this.getUIContext().getFocusController().requestFocus('menu-重新登录'); }
    else if (this.embedded() && !this.tv) { this.getUIContext().animateTo({ duration: 240, curve: Curve.EaseOut }, () => { this.sidebarCollapsed = true; }); }
    else if (this.sidebarCollapsed && this.viewportWidth >= 840) { this.getUIContext().animateTo({ duration: 240, curve: Curve.EaseOut }, () => { this.sidebarCollapsed = false; }); }
    else { this.setMenu(!this.menu); }
  }
  private healthy(): boolean { return ['LOCAL','DIRECT','RELAY'].includes(this.state); }
  private directReason(value: string): string {
    const labels: Record<string, string> = { "no_local_ipv6": '当前网络无可用 IPv6 路由', "no_server_ipv6": 'NAS 无公网 IPv6', "server_disabled": 'NAS 关闭 IPv6 直连', "handshake_failed": '直连握手失败，使用中继', "cooldown": '稍后自动重试直连', "connected": 'IPv6 直连已建立', "local_network": '局域网直达', "probing": '正在检测 IPv6', "discovery_failed": '地址发现失败' };
    return tr(labels[value] ?? value) || '—';
  }
  private failureText(error: Failure): string {
    let text = error.code ? error.code + ': ' + failureMessage(error) : failureMessage(error);
    if (error.message && error.message !== failureMessage(error)) { text += ': ' + error.message; }
    if (error.clientVersion || error.serverVersion) { text += ' · client ' + (error.clientVersion ?? '—') + ' / server ' + (error.serverVersion ?? '—'); }
    if (error.upgradeTarget) { text += ' · upgrade ' + error.upgradeTarget; }
    if (error.operation) { text += ' [' + error.operation + ']'; }
    if (error.httpStatus) { text += ' (HTTP ' + error.httpStatus + ')'; }
    if (error.remoteCode) { text += ' (remote ' + error.remoteCode + ')'; }
    if (error.detail) { text += ': ' + error.detail; }
    if (error.requestId) { text += ' [request ' + error.requestId + ']'; }
    return text;
  }
  private stateName(): string {
    if (this.healthy() && this.dnsDegraded) { return tr('已连接 · DNS 异常'); }
    switch (this.state) {
      case 'UNCONFIGURED': return tr('尚未设置'); case 'AUTH_REQUIRED': return tr('需要登录');
      case 'AUTHORIZING': return tr('正在授权'); case 'PROBING': return tr('正在检测');
      case 'PAUSED': return tr('已断开连接'); case 'LOCAL': return tr('已连接 · 局域网');
      case 'DIRECT': return tr('已连接 · IPv6 直连'); case 'RELAY': return tr('已连接 · FN Connect 中继');
      case 'RECONNECTING': return tr('正在恢复连接'); case 'SERVER_UNAVAILABLE': return tr('服务端不可用');
      default: return tr('连接异常');
    }
  }
  private connected(): boolean { return ['LOCAL','DIRECT','RELAY','RECONNECTING','PROBING'].includes(this.state); }
  private async refresh(showError: boolean): Promise<void> {
    if (this.reading) { return; } this.reading = true;
    const generation = this.sessionGeneration;
    try {
      let status = await this.client.inspectStatus();
      if (generation !== this.sessionGeneration) { return; }
      if (!status) { this.initialized = true; this.loginPage = true; return; }
      this.state = status.state;
      this.localWaiting = status.localNetworkWaiting ?? false;
      this.path = status.path === 'local' ? tr('局域网直达') : status.path === 'ipv6' ? tr('IPv6 直连') : status.path === 'fn-connect' ? tr('FN Connect 中继') : '—';
      const report = await this.client.call<Diagnostics>('diagnose');
      if (generation !== this.sessionGeneration) { return; }
      this.report = report;
      if (report.status?.state) { status = report.status; this.state = status.state; this.localWaiting = status.localNetworkWaiting ?? false; }
      if (!this.initialized) {
        this.loginPage = initialLoginPage(status.state, report.fnId ?? '');
        this.fnId = report.fnId || this.fnId; this.username = report.username || this.username;
        this.initialized = true;
      }
      this.identity = [report.fnId || this.fnId, report.username || this.username].filter((v: string) => v.length > 0).join(' · ');
      this.clientAddress = report.clientAddress ?? '—'; this.nasAddress = report.nasAddress ?? '—';
      this.path = status.path === 'local' ? tr('局域网直达') : status.path === 'ipv6' ? tr('IPv6 直连') : status.path === 'fn-connect' ? tr('FN Connect 中继') : status.path || '—';
      this.network = (status.interface || report.networkInterface || '—') + (status.mtu ? ' · MTU ' + status.mtu : '');
      this.handshake = status.lastHandshake ? new Date(status.lastHandshake).toLocaleTimeString() : '—';
      this.dnsDegraded = status.dns?.state === 'degraded';
      this.dns = [dnsStateName(status.dns?.state ?? '—'), status.dns?.suffix ?? ''].filter((v: string) => v.length > 0).join(' · '); this.dnsSuffix = status.dns?.suffix ?? ''; this.direct = this.directReason(report.direct?.reason ?? '');
      if (!this.uiDebug && ['DIRECT','RELAY'].includes(this.state) && !report.privilegedActive) { this.state = 'ERROR'; }
      if (!report.administrator || ['UNCONFIGURED','AUTH_REQUIRED'].includes(status.state)) { AdminWindows.invalidate(); }
      this.privileged = report.privilegedActive ? tr('运行中') : report.privilegedAvailable ? tr('可用') : tr('不可用');
      const issues: Failure[] = [...(report.errors ?? [])];
      if (status.lastError) { issues.push(status.lastError); }
      if (status.dns?.lastError) { issues.push(status.dns.lastError); }
      if (report.direct?.lastError) { issues.push(report.direct.lastError); }
      this.diagnostics = Array.from(new Set(issues.map((error: Failure) => this.failureText(error)))).join('\n\n') || tr('无异常');
      if (['UNCONFIGURED','AUTH_REQUIRED'].includes(status.state)) { this.loginPage = true; }
    } catch (error) { if (showError && generation === this.sessionGeneration) { this.message = (error as Error).message || String(error); } }
    finally { this.reading = false; }
  }
  private async command(method: string): Promise<void> {
    if (this.busy) { return; } this.busy = true; this.message = '';
    try { await this.client.ensure(); await this.client.call<Object>(method); await this.refresh(true); }
    catch (error) { this.message = (error as Error).message || String(error); } finally { this.busy = false; }
  }
  private closePrivacy(): void {
    this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => { this.privacyURL = ''; });
  }
  private async login(): Promise<void> {
    if (this.busy || !this.fnId.trim() || (!this.challenge && (!this.username || !this.password))) { return; }
    if (!this.privacyAccepted) { this.message = tr('请先阅读并同意隐私政策。'); return; }
    this.getUIContext().getFocusController().clearFocus();
    this.busy = true; this.message = ''; this.sessionGeneration++;
    const password = this.password; const accessCode = this.accessCode; const otp = this.otp;
    try {
      this.password = ''; this.accessCode = ''; this.otp = '';
      await this.client.login(this.fnId.trim(), this.username, password, accessCode, otp, this.trust);
      this.clearLoginSecrets(); this.loginFromMenu = false; this.loginPage = false; this.initialized = true;
      await this.refresh(true);
    } catch (error) {
      this.message = (error as Error).message || String(error);
      const failure = error as ClientError;
      this.challenge = ['ACCESS_CODE_INVALID','ACCESS_CODE_REQUIRED','ACCESS_CODE_RATE_LIMITED','TWO_FACTOR_REQUIRED','TWO_FACTOR_SETUP_REQUIRED','TWO_FACTOR_CODE_INVALID'].includes(failure.failure?.remoteCode ?? '');
      this.otp = '';
      this.loginPage = true; this.initialized = true;
      if (!this.challenge) { this.password = password; this.accessCode = accessCode; }
    } finally { this.busy = false; }
  }
  private closeAdmin(): void {
    this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => { this.adminURL = ''; });
  }
  private cancelAdmin(): void { this.adminGeneration++; this.adminLoading = false; this.busy = false; }
  private async openAdmin(): Promise<void> {
    if (this.busy) { return; }
    const generation = ++this.adminGeneration;
    this.closeMenu(); this.busy = true; this.adminLoading = true; this.message = '';
    try {
      const result = await Promise.race<Admin>([
        this.client.call<Admin>('admin-proxy'),
        new Promise<Admin>((_, reject) => { setTimeout(() => reject(new Error(tr('管理后台加载失败，请重试。'))), 30000); })
      ]);
      if (generation !== this.adminGeneration) { return; }
      if (!/^http:\/\/127\.0\.0\.1:\d+\//.test(result.url)) { throw new Error(tr('无效的管理后台地址')); }
      if (isDesktop()) {
        const owner = await this.client.call<Diagnostics>('diagnose');
        if (generation !== this.adminGeneration) { return; }
        await AdminWindows.open(getContext(this) as common.UIAbilityContext, result.url, this.client.directory, [owner.fnId ?? '', owner.username ?? ''].join('|'));
      } else { this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => { this.adminURL = result.url; }); }
    } catch (error) {
      if (generation !== this.adminGeneration) { return; }
      const failure = error as ClientError;
      this.message = failure.message || String(error);
      if (['AUTH_REQUIRED', 'PERMISSION_DENIED'].includes(failure.failure?.code ?? '')) {
        this.busy = false; await this.enterLogin(false);
        this.message = tr('请使用有管理权限的账号登录后重试。');
      }
    } finally { if (generation === this.adminGeneration) { this.busy = false; this.adminLoading = false; } }
  }
  private async copyText(text: string, notice: string): Promise<void> {
    try {
      if (!canIUse('SystemCapability.MiscServices.Pasteboard')) { throw new Error(tr('当前设备不支持剪贴板，请导出日志')); }
      await pasteboard.getSystemPasteboard().setData(pasteboard.createData(pasteboard.MIMETYPE_TEXT_PLAIN, text));
      this.getUIContext().getPromptAction().showToast({ message: tr(notice), duration: 2000 });
    } catch (error) { this.message = (error as Error).message || String(error); }
  }
  private async copyReport(): Promise<void> {
    await this.copyText(JSON.stringify(this.report ?? {}, null, 2), '诊断信息已复制');
  }
  private async openBrowser(url: string): Promise<void> {
    if (!/^https?:\/\//.test(url)) { throw new Error(tr('无效的网页地址')); }
    await (getContext(this) as common.UIAbilityContext).openLink(url, { appLinkingOnly: false });
  }
  private async openNAS(): Promise<void> {
    if (this.busy) { return; }
    if (!/^\d+\.\d+\.\d+\.\d+$/.test(this.nasAddress)) {
      this.getUIContext().getPromptAction().showToast({ message: tr('NAS IP 暂不可用') }); return;
    }
    this.busy = true;
    this.getUIContext().getPromptAction().showToast({ message: tr('正在打开 NAS 网页…'), duration: 2000 });
    try {
      let url = 'http://' + this.nasAddress + '/';
      try { url = await this.client.call<string>('nas-web-url', { address: this.nasAddress }); }
      catch (_) { this.getUIContext().getPromptAction().showToast({ message: tr('未能探测网页端口，尝试打开 NAS 默认地址'), duration: 3000 }); }
      await this.openBrowser(url);
    } catch (error) {
      this.message = tr('打开 NAS 网页失败') + ': ' + ((error as Error).message || String(error));
      this.getUIContext().getPromptAction().showToast({ message: this.message, duration: 3000 });
    } finally { this.busy = false; }
  }
  private async exportReport(): Promise<void> {
    if (this.busy) { return; }
    this.closeMenu();
    const confirmation = await this.getUIContext().getPromptAction().showDialog({ title: tr('导出诊断与日志'), message: tr('报告仅包含经过筛选的连接状态与系统事件，不含身份、地址和凭据。'), buttons: [{ text: tr('取消'), color: '#6F7985' }, { text: tr('导出'), color: '#0066FF' }] });
    if (confirmation.index !== 1) { return; }
    this.busy = true;
    let source = '';
    try {
      const document = new picker.DocumentViewPicker(getContext(this) as common.UIAbilityContext);
      const archive = await this.client.call<SupportArchive>('export-support');
      if (!/^fncpn-support-\d+\.zip$/.test(archive.name) || archive.size < 1) { throw new Error(tr('支持报告无效')); }
      source = this.client.directory + '/exports/' + archive.name;
      const result = await document.save({ newFileNames: [archive.name] });
      if (result.length === 0) { return; }
      const input = fileIo.openSync(source, fileIo.OpenMode.READ_ONLY);
      try {
        const output = fileIo.openSync(result[0], fileIo.OpenMode.WRITE_ONLY | fileIo.OpenMode.TRUNC);
        try { fileIo.copyFileSync(input.fd, output.fd); } finally { fileIo.closeSync(output); }
      } finally { fileIo.closeSync(input); }
      this.getUIContext().getPromptAction().showToast({ message: tr('导出完成') });
    } catch (error) { this.message = (error as Error).message || String(error); }
    finally { if (source) { try { fileIo.unlinkSync(source); } catch (_) {} } this.busy = false; }
  }
  private async probe(): Promise<void> {
    if (this.busy) { return; } this.busy = true; this.probeResult = '';
    try { this.probeResult = await this.client.call<string>('application-probe', { target: this.probeTarget }); }
    catch (error) { this.probeResult = (error as Error).message || String(error); }
    finally { this.busy = false; }
  }
  private async exit(): Promise<void> {
    if (this.busy) { return; } this.busy = true;
    try { AdminWindows.invalidate(); await this.client.exit(); await (getContext(this) as common.UIAbilityContext).terminateSelf(); }
    catch (error) { this.message = (error as Error).message || String(error); this.busy = false; }
  }
  private async openLogs(): Promise<void> {
    this.logsOpen = true;
    await this.refreshLogFiles();
  }
  private async refreshLogFiles(): Promise<void> {
    const options: SelectOption[] = [];
    for (const name of ['client.log', 'client.log.1', 'client.log.2', 'privileged.log', 'privileged.log.1', 'privileged.log.2']) {
      const path = this.client.directory + '/logs/' + name;
      try { if (await fileIo.access(path) && (await fileIo.stat(path)).size > 0) { options.push({ value: name }); } }
      catch (_) { /* A log may rotate while listing. */ }
    }
    this.logOptions = options;
    if (options.length === 0) { this.logBody = tr('暂无日志'); return; }
    if (!options.some((option: SelectOption) => option.value === this.logFile)) { this.logFile = options[0].value as string; }
    await this.readLog(this.logFile);
  }
  private async browseLogs(): Promise<void> {
    if (this.browsingLogs) { return; }
    this.browsingLogs = true;
    try {
      const document = new picker.DocumentViewPicker(getContext(this) as common.UIAbilityContext);
      const selected = await document.save({ pickerMode: picker.DocumentPickerMode.DOWNLOAD });
      if (selected.length === 0) { return; }
      const destination = selected[0] + '/fncpn-logs-' + Date.now();
      await fileIo.mkdir(new fileUri.FileUri(destination).path);
      let count = 0;
      for (const name of ['client.log', 'client.log.1', 'client.log.2', 'privileged.log', 'privileged.log.1', 'privileged.log.2']) {
        const source = this.client.directory + '/logs/' + name;
        if (!await fileIo.access(source)) { continue; }
        const input = await fileIo.open(source, fileIo.OpenMode.READ_ONLY);
        try {
          if ((await fileIo.stat(input.fd)).size === 0) { continue; }
          const output = await fileIo.open(new fileUri.FileUri(destination + '/' + name).path, fileIo.OpenMode.CREATE | fileIo.OpenMode.WRITE_ONLY);
          try { await fileIo.copyFile(input.fd, output.fd); count++; }
          finally { await fileIo.close(output); }
        } finally { await fileIo.close(input); }
      }
      this.getUIContext().getPromptAction().showToast({ message: tr('日志副本已导出') + ': ' + count, duration: 3000 });
      // Launch the standalone Huawei File Manager instead of a FilePicker.
      // fileUri is a navigation hint; supported system versions may ignore it.
      try {
        await openLogDirectory(getContext(this) as common.UIAbilityContext, destination);
      } catch (error) {
        this.getUIContext().getPromptAction().showToast({
          message: tr('日志已导出，打开文件管理失败') + ': ' + ((error as Error).message || String(error)) + '\n' + tr('请在下载/cn.rectcircle.fncpn 中查看'), duration: 4000
        });
      }
    } catch (error) {
      this.getUIContext().getPromptAction().showToast({ message: tr('导出日志目录失败') + ': ' + ((error as Error).message || String(error)), duration: 3000 });
    } finally { this.browsingLogs = false; }
  }
  private logReadGeneration: number = 0;
  @State copyingLog: boolean = false;
  private async copyLog(): Promise<void> {
    if (this.copyingLog) { return; }
    this.copyingLog = true;
    try { await this.copyText(this.logBody, '当前日志已复制'); }
    finally { this.copyingLog = false; }
  }
  private async readLog(name: string): Promise<void> {
    if (!['client.log', 'client.log.1', 'client.log.2', 'privileged.log', 'privileged.log.1', 'privileged.log.2'].includes(name)) { return; }
    this.logFile = name;
    const generation = ++this.logReadGeneration;
    this.logBody = tr('正在读取日志…');
    try {
      const path = this.client.directory + '/logs/' + name;
      if (!await fileIo.access(path)) { if (generation === this.logReadGeneration) { this.logBody = tr('暂无日志'); } return; }
      const file = await fileIo.open(path, fileIo.OpenMode.READ_ONLY);
      let text = '';
      try {
        const total = (await fileIo.stat(file.fd)).size;
        const size = Math.min(total, 64 * 1024);
        const data = new ArrayBuffer(size);
        const count = await fileIo.read(file.fd, data, { offset: Math.max(0, total - size) });
        text = new util.TextDecoder().decodeWithStream(new Uint8Array(data, 0, count));
        // Tail reads may start in the middle of a UTF-8 character or JSON line.
        if (total > size) {
          const newline = text.indexOf('\n');
          text = tr('仅显示最近 64 KiB；完整日志请使用导出诊断与日志。') + '\n\n' + (newline >= 0 ? text.substring(newline + 1) : text);
        }
      } finally { await fileIo.close(file); }
      if (generation === this.logReadGeneration) {
        this.logScrollPending = true; this.logBody = text || tr('暂无日志');
        setTimeout(() => { if (this.logsOpen && generation === this.logReadGeneration) { this.logScroll.scrollEdge(Edge.Bottom); } }, 0);
      }
    } catch (error) {
      if (generation === this.logReadGeneration) { this.logBody = tr('读取日志失败') + ': ' + ((error as Error).message || String(error)); }
    }
  }

  @Builder secondaryAction(label: string, icon: Resource | undefined, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 6 }) {
        if (icon) { Image(icon).width(18).height(18).fillColor($r('app.color.foreground')) }
        if (label) { Text(tr(label)).fontSize(this.tv ? 20 : 13).fontColor($r('app.color.foreground')) }
      }.justifyContent(FlexAlign.Center).width('100%')
    }.height(this.controlHeight()).width('100%').focusBox({ margin: LengthMetrics.vp(3), strokeWidth: LengthMetrics.vp(2), strokeColor: ColorMetrics.resourceColor($r('app.color.primary')) }).hoverEffect(HoverEffect.Highlight).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.control_background'))
      .border({ width: 1, color: $r('app.color.divider'), radius: BUTTON_RADIUS }).borderRadius(BUTTON_RADIUS).enabled(!this.busy).onClick(action)
  }
  @Builder inlineAction(icon: Resource, label: string, enabled: boolean, action: () => void) {
    Button({ type: ButtonType.Normal }) { Image(icon).width(20).height(20).fillColor($r('app.color.foreground')) }
      .width(40).height(40).borderRadius(BUTTON_RADIUS).backgroundColor(Color.Transparent).accessibilityText(tr(label)).enabled(enabled && !this.busy).onClick(action)
  }
  private revealSubmit(): void {
    setTimeout(() => { this.formScroll.scrollEdge(Edge.Bottom); }, 350);
  }
  @Builder hero() {
    LoginHero({ compact: this.viewportHeight < 520, tv: this.tv })
  }
  @Builder loginForm() {
    Column({ space: 12 }) {
      if (!this.challenge) {
        Text('FN Connect ID').fontSize(this.tv ? 22 : 16).alignSelf(ItemAlign.Start)
        TextInput({ text: this.fnId, placeholder: tr('例如 home-nas') }).id('fnid').defaultFocus(this.tv).height(this.controlHeight()).fontSize(this.tv ? 22 : 16).type(InputType.Email).inputFilter('.*').enablePreviewText(false).enterKeyType(EnterKeyType.Next).onSubmit(() => { this.getUIContext().getFocusController().requestFocus('username'); }).enabled(!this.busy).onChange((v: string) => { this.fnId = v; })
        Text(tr('用户名')).fontSize(this.tv ? 22 : 16).alignSelf(ItemAlign.Start)
        TextInput({ text: this.username, placeholder: tr('fnOS 用户名') }).id('username').height(this.controlHeight()).fontSize(this.tv ? 22 : 16).type(InputType.Email).inputFilter('.*').enablePreviewText(false).enterKeyType(EnterKeyType.Next).onSubmit(() => { this.getUIContext().getFocusController().requestFocus('password'); }).enabled(!this.busy).onChange((v: string) => { this.username = v; })
        Text(tr('密码')).fontSize(this.tv ? 22 : 16).alignSelf(ItemAlign.Start)
        TextInput({ text: this.password, placeholder: tr('fnOS 密码') }).id('password').height(this.controlHeight()).fontSize(this.tv ? 22 : 16).type(InputType.Password).inputFilter('.*').enablePreviewText(false).enterKeyType(EnterKeyType.Go).onFocus(() => { this.revealSubmit(); }).onSubmit(() => { this.login(); }).enabled(!this.busy).onChange((v: string) => { this.password = v; })
      } else {
        Text(tr('根据提示输入访问码或验证码')).alignSelf(ItemAlign.Start)
        TextInput({ text: this.accessCode, placeholder: tr('fnOS 访问码') }).height(this.controlHeight()).fontSize(this.tv ? 22 : 16).type(InputType.Password).enterKeyType(EnterKeyType.Next).onSubmit(() => { this.getUIContext().getFocusController().requestFocus('otp'); }).enabled(!this.busy).onChange((v: string) => { this.accessCode = v; })
        TextInput({ text: this.otp, placeholder: tr('6 位 OTP') }).id('otp').height(this.controlHeight()).fontSize(this.tv ? 22 : 16).type(InputType.Number).maxLength(6).enterKeyType(EnterKeyType.Go).onFocus(() => { this.revealSubmit(); }).onSubmit(() => { this.login(); }).enabled(!this.busy).onChange((v: string) => { this.otp = v; })
        Row() { Checkbox().select(this.trust).enabled(!this.busy).onChange((v: boolean) => { this.trust = v; }); Text(tr('信任此设备')); }
        this.secondaryAction('上一步', $r('app.media.menu_login'), () => { this.previousLogin(); })
      }
      Row({ space: 4 }) {
        Checkbox({ name: 'privacyConsent' }).id('privacy-consent').select(this.privacyAccepted)
          .enabled(!this.busy).accessibilityText(tr('我已阅读并同意隐私政策'))
          .onChange((accepted: boolean) => {
            this.privacyAccepted = accepted;
            if (accepted && this.message === tr('请先阅读并同意隐私政策。')) { this.message = ''; }
          })
        Text() {
          Span(tr('我已阅读并同意'))
          Span(tr('《隐私政策》')).fontColor($r('app.color.primary')).decoration({ type: TextDecorationType.Underline })
            .onClick(() => {
              const language = i18n.System.getSystemLanguage().startsWith('zh') ? 'zh' : 'en';
              this.getUIContext().getFocusController().clearFocus();
              this.getUIContext().animateTo({ duration: 220, curve: Curve.EaseOut }, () => {
                this.privacyURL = language;
              });
            })
        }.fontSize(14).layoutWeight(1).textAlign(TextAlign.Start)
      }.width('100%')
      if (vpnUnavailable(this.uiDebug)) { Text(tr(vpnUnavailable(this.uiDebug))).fontColor($r('app.color.danger')).width('100%') }
      if (this.message) { Text(this.message).fontSize(13).fontColor($r('app.color.danger')).width('100%').textAlign(TextAlign.Start) }
      Button(this.busy ? tr('正在登录…') : tr('登录并连接')).type(ButtonType.Normal).height(this.controlHeight()).focusBox({ margin: LengthMetrics.vp(3), strokeWidth: LengthMetrics.vp(2), strokeColor: ColorMetrics.resourceColor($r('app.color.primary')) }).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.primary')).fontColor($r('app.color.on_primary')).width('100%')
        .enabled(!this.busy && this.privacyAccepted && !vpnUnavailable(this.uiDebug) && this.fnId.trim().length > 0 && (this.challenge || (this.username.length > 0 && this.password.length > 0))).onClick(() => { this.login(); })
    }.width('100%').constraintSize({ maxWidth: this.tv ? 640 : 480 })
  }
  @Builder menuItem(label: string, icon: Resource, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 16 }) {
        Image(icon).width(22).height(22).fillColor($r('app.color.foreground'))
        Text(tr(label)).fontSize(this.tv ? 22 : 16).fontColor($r('app.color.foreground')).layoutWeight(1).textAlign(TextAlign.Start)
      }.width('100%').padding({ left: 16, right: 16 })
    }.id('menu-' + label).width('100%').height(this.tv ? 64 : 52).focusBox({ margin: LengthMetrics.vp(3), strokeWidth: LengthMetrics.vp(2), strokeColor: ColorMetrics.resourceColor($r('app.color.primary')) }).hoverEffect(HoverEffect.Highlight).borderRadius(BUTTON_RADIUS).backgroundColor(Color.Transparent).enabled(!this.busy).onClick(action)
  }
  @Builder menuPanel() {
    Column({ space: 4 }) {
      Row({ space: 12 }) {
        Image($r('app.media.icon')).width(36).height(36)
        Text('FnCPN').fontSize(20).fontWeight(FontWeight.Bold).layoutWeight(1)
      }.width('100%').height(88).padding({ left: 16, right: 8 })
      Divider().color($r('app.color.divider')).strokeWidth(1).width('100%').margin({ bottom: 8 })
      this.menuItem('重新登录', $r('app.media.menu_login'), () => { this.enterLogin(false); })
      this.menuItem('更换 NAS', $r('app.media.menu_storage'), () => { this.enterLogin(true); })
      this.menuItem('管理后台', $r('app.media.menu_computer'), () => { if (!this.busy) { this.openAdmin(); } })
      this.menuItem('官网 · 捐赠', $r('app.media.menu_help'), () => { this.closeMenu(); this.openBrowser('https://fncpn.rectcircle.cn/#donate').catch((error: Error) => { this.message = (error as Error).message || String(error); }); })
      this.menuItem('导出诊断与日志', $r('app.media.menu_download'), () => { this.exportReport(); })
      if (this.showDebugUI) { this.menuItem('应用内网络调试', $r('app.media.menu_computer'), () => { this.closeMenu(); this.probeTarget = this.nasAddress !== '—' ? 'http://' + this.nasAddress + '/' : ''; this.probeResult = ''; this.networkDebug = true; }) }
      Divider().color($r('app.color.divider')).strokeWidth(1).width('100%').margin({ top: 8, bottom: 8 })
      this.menuItem('退出', $r('app.media.menu_logout'), () => { this.exit(); })
      if (this.showDebugUI) { Text(tr('应用内隧道 · 真实登录与通信')).fontSize(12).fontColor($r('app.color.secondary')).padding(16) }
    }.alignItems(HorizontalAlign.Start).padding({ left: 12, right: 12 }).backgroundColor($r('app.color.surface')).width('100%').constraintSize({ minHeight: '100%' })
  }
  @Builder connectionSummary() {
    Column({ space: 16 }) {
                Text(this.identity).fontSize(this.tv ? 24 : 16).fontWeight(FontWeight.Bold).width('100%')
                Text(this.stateName()).fontSize(this.tv ? 32 : 20).fontWeight(FontWeight.Bold).fontColor(this.healthy() ? (this.dnsDegraded ? $r('app.color.danger') : $r('app.color.success')) : $r('app.color.foreground')).width('100%')
                Column() {
                  StatusRow({ label: tr('连接路径'), value: this.path, tv: this.tv }); StatusRow({ label: tr('隧道地址'), value: this.clientAddress, tv: this.tv }); StatusRow({ label: tr('网络接口'), value: this.network, tv: this.tv });
                  StatusRow({ label: tr('最近握手'), value: this.handshake, tv: this.tv });
                  Row() {
                    Text('DNS').fontColor($r('app.color.secondary')).width('36%')
                    Text(this.dns).fontColor($r('app.color.foreground')).layoutWeight(1)
                    if (this.dnsSuffix) { this.inlineAction($r('app.media.action_copy'), '复制 DNS 后缀', true, () => { this.copyText(this.dnsSuffix, 'DNS 后缀已复制'); }) }
                  }.width('100%').padding({ top: 4, bottom: 4 })
                  StatusRow({ label: tr('网络服务'), value: this.privileged, tv: this.tv });
                  StatusRow({ label: tr('IPv6 检测'), value: this.direct, tv: this.tv });
                  Row() {
                    Text('NAS IP').fontColor($r('app.color.secondary')).width('36%')
                    Text(this.nasAddress).fontColor($r('app.color.foreground')).layoutWeight(1).copyOption(CopyOptions.InApp)
                    this.inlineAction($r('app.media.action_external'), '打开 NAS', true, () => { this.openNAS(); })
                  }.width('100%').padding({ top: 4, bottom: 4 })
                }
                Row({ space: 12 }) {
                  Button(this.localWaiting ? tr('继续远程连接') : this.connected() ? tr('断开连接') : tr('连接')).type(ButtonType.Normal).height(this.controlHeight()).focusBox({ margin: LengthMetrics.vp(3), strokeWidth: LengthMetrics.vp(2), strokeColor: ColorMetrics.resourceColor($r('app.color.primary')) }).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.primary')).fontColor($r('app.color.on_primary')).layoutWeight(1).defaultFocus(this.tv).enabled(!this.busy && !vpnUnavailable(this.uiDebug))
                    .onClick(() => { this.command(this.localWaiting ? 'continue-remote' : this.connected() ? 'disconnect' : 'connect'); })
                  Column() { this.secondaryAction('重新检测', undefined, () => { this.command('retry'); }) }.layoutWeight(1)
                }
    }.width('100%')
  }
  @Builder diagnosticPanel() {
    Column({ space: 16 }) {
                Text(tr('诊断详情')).width('100%')
                Text(this.diagnostics).width('100%').padding(16).backgroundColor($r('app.color.surface')).borderRadius(BUTTON_RADIUS).border({ width: 1, color: $r('app.color.divider'), radius: BUTTON_RADIUS }).borderRadius(BUTTON_RADIUS).fontColor(this.diagnostics === tr('无异常') ? $r('app.color.foreground') : $r('app.color.danger')).fontSize(this.tv ? 20 : 13)
                Row({ space: 8 }) {
                  Column() { this.secondaryAction('复制', $r('app.media.action_copy'), () => { this.copyReport(); }) }.layoutWeight(1)
                  Column() { this.secondaryAction('打开日志', $r('app.media.action_folder'), () => { this.openLogs(); }) }.layoutWeight(1)
                  Column() { this.secondaryAction('刷新', $r('app.media.action_refresh'), () => { this.refresh(true); }) }.layoutWeight(1)
                }
    }.width('100%')
  }
  build() {
    Stack({ alignContent: Alignment.TopStart }) {
      Row() {
        Scroll() { Column() { this.menuPanel(); }.width(256) }
          .width(this.embedded() ? 256 : 0).height('100%').clip(true)
          .opacity(this.embedded() ? 1 : 0)
          .hitTestBehavior(this.embedded() ? HitTestMode.Default : HitTestMode.None)
          .accessibilityLevel(this.embedded() ? 'auto' : 'no-hide-descendants')
          .border({ width: { right: this.embedded() ? 1 : 0 }, color: $r('app.color.divider') })
      Column() {
        BrandHeader({ back: !!this.privacyURL || !!this.adminURL || this.logsOpen || (this.loginPage && this.loginFromMenu), debug: this.showDebugUI, busy: this.busy, tv: this.tv, navigate: () => { this.navigate(); } })
        if (this.privacyURL) {
          Web({ src: $rawfile('privacy/index.html'), controller: this.privacyController }).layoutWeight(1)
            .javaScriptAccess(true).fileAccess(true).domStorageAccess(false)
            .onPageEnd(() => {
              this.privacyController.runJavaScript(this.privacyURL === 'en' ? "showLanguage('en')" : "showLanguage('zh')").catch(() => {});
            })
            .transition(TransitionEffect.move(TransitionEdge.END).combine(TransitionEffect.OPACITY))
        } else if (this.adminURL) {
          Web({ src: this.adminURL, controller: this.adminController }).layoutWeight(1).javaScriptAccess(true).fileAccess(false).domStorageAccess(true).transition(TransitionEffect.move(TransitionEdge.END).combine(TransitionEffect.OPACITY))
        } else if (this.logsOpen) {
          Column({ space: 8 }) {
            Select(this.logOptions).value(this.logFile).enabled(this.logOptions.length > 0).onSelect((_, value: string) => { this.readLog(value); })
            Row({ space: 8 }) {
              Column() { this.secondaryAction(this.copyingLog ? '正在复制…' : '复制', $r('app.media.action_copy'), () => { this.copyLog(); }) }.layoutWeight(1)
              Column() { this.secondaryAction('打开目录', $r('app.media.action_folder'), () => { this.browseLogs(); }) }.layoutWeight(1)
              Column() { this.secondaryAction('刷新', $r('app.media.action_refresh'), () => { this.refreshLogFiles(); }) }.layoutWeight(1)
            }.padding({ left: 16, right: 16 }).width('100%')
            Scroll(this.logScroll) {
              Text(this.logBody).fontFamily('monospace').fontSize(this.tv ? 20 : 12).copyOption(CopyOptions.None).padding(16).width('100%')
                .onAreaChange(() => {
                  if (this.logScrollPending) { this.logScrollPending = false; this.logScroll.scrollEdge(Edge.Bottom); }
                });
            }.focusable(this.tv).layoutWeight(1)
          }.layoutWeight(1).width('100%')
        } else {
          Scroll(this.formScroll) {
            Column({ space: 16 }) {
              if (this.message && !this.loginPage) { Text(this.message).fontColor($r('app.color.danger')).width('100%'); }
              if (this.loginPage) {
                if (loginColumns(this.contentWidth, this.fontScale)) {
                  Row({ space: 24 }) {
                    Column() { this.hero(); }.width((this.tv ? 280 : this.viewportHeight < 520 ? 160 : 200) * Math.max(1, this.fontScale))
                    Column() { this.loginForm(); }.layoutWeight(1).constraintSize({ maxWidth: (this.tv ? 640 : 400) * Math.max(1, this.fontScale) })
                  }.width('100%').constraintSize({ maxWidth: (this.tv ? 944 : this.viewportHeight < 520 ? 584 : 624) * Math.max(1, this.fontScale) }).alignItems(VerticalAlign.Top)
                } else {
                  Column({ space: 16 }) { this.hero(); this.loginForm(); }.width('100%').constraintSize({ maxWidth: this.tv ? 640 : 480 })
                }
              } else {
                if (this.columns()) {
                  Row({ space: 24 }) {
                    Column() { this.connectionSummary(); }.layoutWeight(1)
                    Column() { this.diagnosticPanel(); }.layoutWeight(1)
                  }.alignItems(VerticalAlign.Top).width('100%')
                } else { this.connectionSummary(); this.diagnosticPanel(); }
              }
              if (this.showDebugUI) { Text(tr('模拟器应用内隧道调试：真实登录与通信，仅本 App 请求进入隧道；系统 VPN 需真机验证。')).fontSize(12).fontColor($r('app.color.secondary')).width('100%') }
            }.width('100%').padding({ left: 20, right: 20, top: 16, bottom: 32 }).constraintSize({ maxWidth: this.tv ? 1600 : 1200 })
          }.layoutWeight(1).width('100%').align(Alignment.Top)
        }
      }.layoutWeight(1).height('100%').backgroundColor($r('app.color.page_background')).onAreaChange((_, area) => { this.contentWidth = Number(area.width); })
      }.width('100%').height('100%').onAreaChange((_, area) => {
        this.viewportWidth = Number(area.width); this.viewportHeight = Number(area.height);
        if (this.embedded()) { this.menu = false; this.drawerProgress = 0; }
      })
      if (this.networkDebug) {
        Stack() {
          Column().width('100%').height('100%').backgroundColor('#66000000')
          Column({ space: 16 }) {
            Text(tr('应用内网络调试')).fontSize(18).fontWeight(FontWeight.Bold)
            TextInput({ text: this.probeTarget, placeholder: tr('http://私网IP:端口/ 或 dns:完整私网域名') }).onChange((value: string) => { this.probeTarget = value; }).enterKeyType(EnterKeyType.Go).onSubmit(() => { this.probe(); })
            if (this.probeResult) { Text(this.probeResult).fontSize(13).copyOption(CopyOptions.InApp).width('100%') }
            Row({ space: 12 }) {
              Button(tr('请求')).type(ButtonType.Normal).height(this.controlHeight()).focusBox({ margin: LengthMetrics.vp(3), strokeWidth: LengthMetrics.vp(2), strokeColor: ColorMetrics.resourceColor($r('app.color.primary')) }).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.primary')).fontColor($r('app.color.on_primary')).enabled(!this.busy).onClick(() => { this.probe(); })
              Button(tr('关闭')).type(ButtonType.Normal).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).enabled(!this.busy).onClick(() => { this.networkDebug = false; })
            }
          }.width('90%').constraintSize({ maxWidth: 480 }).padding(24).backgroundColor($r('app.color.surface')).borderRadius(12)
        }.width('100%').height('100%')
      }
      if (this.adminLoading) {
        Stack() {
          Column().width('100%').height('100%').backgroundColor('#66000000')
          Column({ space: 16 }) {
            LoadingProgress().width(40).height(40).color($r('app.color.primary'))
            Text(tr('正在打开管理后台…')).fontColor($r('app.color.foreground'))
            Button(tr('取消')).type(ButtonType.Normal).borderRadius(BUTTON_RADIUS).backgroundColor($r('app.color.control_background')).fontColor($r('app.color.foreground')).onClick(() => { this.cancelAdmin(); })
          }.padding(24).backgroundColor($r('app.color.surface')).borderRadius(12)
        }.width('100%').height('100%')
      }
      Stack({ alignContent: Alignment.TopStart }) {
        Column().width('100%').height('100%').backgroundColor('#66000000').opacity(this.drawerProgress).onClick(() => { this.closeMenu(); })
        Column() { this.menuPanel(); }.width(288).height('100%').translate({ x: -288 * (1 - this.drawerProgress) })
      }.width('100%').height('100%').visibility(this.menu && !this.embedded() ? Visibility.Visible : Visibility.Hidden)
        .hitTestBehavior(this.menu ? HitTestMode.Default : HitTestMode.None)
    }.width('100%').height('100%').padding({ right: this.foldRight + (this.tv ? 32 : 0), bottom: this.foldBottom + (this.tv ? 24 : 0), left: this.tv ? 32 : 0, top: this.tv ? 24 : 0 })
      .onKeyEvent((event: KeyEvent) => { if (event.type === KeyType.Down && event.keyCode === KeyCode.KEYCODE_ESCAPE) { if (this.onBackPress()) { event.stopPropagation(); } } })
      .onTouch((event: TouchEvent) => {
        if (event.type === TouchType.Down && event.touches.length > 0) {
          this.gestureX = event.touches[0].x;
          this.gestureProgress = this.drawerProgress;
        }
      })
      .parallelGesture(PanGesture({ direction: PanDirection.Horizontal, distance: 16 })
        .onActionStart((event: GestureEvent) => {
          // Reserve the first 24 vp for system back; the 24–96 vp strip opens the drawer.
          this.drawerDragging = !this.embedded() && !this.tv && !this.privacyURL && !this.adminURL && !this.logsOpen && !this.networkDebug && !this.adminLoading && (this.menu || (this.gestureX >= 24 && this.gestureX <= 96 && event.offsetX > 0));
          if (this.drawerDragging) { this.menu = true; }
        })
        .onActionUpdate((event: GestureEvent) => {
          if (this.drawerDragging) { this.drawerProgress = Math.max(0, Math.min(1, this.gestureProgress + event.offsetX / 288)); }
        })
        .onActionEnd((event: GestureEvent) => {
          if (this.drawerDragging) {
            const open = Math.abs(event.velocityX) > 500 ? event.velocityX > 0 : this.drawerProgress > 0.35;
            this.drawerDragging = false; this.setMenu(open);
          }
        })
        .onActionCancel(() => { if (this.drawerDragging) { this.drawerDragging = false; this.setMenu(this.gestureProgress > 0.5); } }))
  }
}
