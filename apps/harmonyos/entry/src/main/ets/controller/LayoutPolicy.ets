export interface LayoutState {
  embedded: boolean;
  sidebarWidth: number;
  twoColumns: boolean;
  compactHeight: boolean;
  contentWidth: number;
}
// Pure window policy. No lifecycle or VPN actions belong in layout decisions.
export function resolveLayout(width: number, height: number, fontScale: number, tv: boolean, collapsed: boolean): LayoutState {
  const scale = Math.max(1, fontScale);
  const sidebar = tv ? 256 : 256;
  const embedded = tv || (!collapsed && width >= Math.max(840, sidebar + 520 * scale));
  const content = Math.max(0, width - (embedded ? sidebar : 0));
  return { embedded: embedded, sidebarWidth: sidebar, twoColumns: content >= 840 * scale,
    compactHeight: height < 520, contentWidth: content };
}
export function vpnAvailability(type: string, api: number, capability: boolean, simulator: boolean): string {
  if (simulator) { return ''; }
  if (type === 'tv' && api < 26) { return '智慧屏系统 VPN 需要 HarmonyOS 7 / API 26 或以上'; }
  if (!capability) { return '当前设备不支持系统 VPN'; }
  return '';
}

// Login needs only two narrow blocks, unlike status/diagnostic panels.
// Reserve 40vp page padding and 32vp gap before deciding whether both fit.
export function loginColumns(contentWidth: number, fontScale: number): boolean {
  return contentWidth >= 640 * Math.max(1, fontScale);
}
