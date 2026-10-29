const identity=(key,...args)=>key.replace(/\{(\d+)\}/g,(m,i)=>args[i]??m);
export function normalizeFNID(input) {
 let candidate=input.trim().toLowerCase();
 try { const u=new URL(candidate); if(u.hostname==='fnos.net')candidate=u.pathname.split('/').filter(Boolean)[0]||''; else if(u.hostname.endsWith('.fnos.net'))candidate=u.hostname.slice(0,-9); else return null; }
 catch { if(candidate.endsWith('.fnos.net'))candidate=candidate.slice(0,-9); }
 return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(candidate)?candidate:null;
}
export const factorMessages={ACCESS_CODE_REQUIRED:'该 NAS 开启了访问码，请输入访问码',ACCESS_CODE_INVALID:'访问码不正确，请重试',ACCESS_CODE_RATE_LIMITED:'访问码尝试次数过多，请稍后重试',TWO_FACTOR_REQUIRED:'该账号需要两步验证，请输入 OTP',TWO_FACTOR_CODE_INVALID:'OTP 不正确，请重试',TWO_FACTOR_SETUP_REQUIRED:'该账号要求两步验证，请先在 fnOS 网页端完成绑定'};
export function presentation(status={}, L=identity) {
 const state=status.state||'UNCONFIGURED',error=status.lastError||{},op=error.operation||'';
 const needsLogin=state==='AUTH_REQUIRED'||!!factorMessages[error.remoteCode]||['AUTH_REQUIRED','DEVICE_REVOKED'].includes(error.code)||(error.code==='PERMISSION_DENIED'&&!op.startsWith('credentials.')&&!op.startsWith('privileged.')&&(error.httpStatus===403||op.startsWith('authorization.')));
 const connected=['LOCAL','DIRECT','RELAY'].includes(state);
 const titles={UNCONFIGURED:'尚未连接',AUTHORIZING:'正在登录',PROBING:'正在检测网络',LOCAL:'已在同一局域网',DIRECT:'IPv6 直连',RELAY:'FN Connect 中继',RECONNECTING:'正在重新连接',PAUSED:'已断开',SERVER_UNAVAILABLE:'未找到 FnCPN 服务',ERROR:'连接失败',DAEMON_UNAVAILABLE:'客户端服务不可用'};
 return {state,needsLogin,connected,title:L(needsLogin?'需要重新登录':titles[state]||state),action:L(needsLogin?'重新登录':connected?'断开连接':'连接')};
}
export function describeFailure(error,L=identity) {
 if(!error)return '';
 let summary=factorMessages[error.remoteCode];
 if(error.code==='VERSION_INCOMPATIBLE') summary=L(error.upgradeTarget==='client'?'版本不兼容，请升级客户端。':'版本不兼容，请升级服务端。')+` ${L('客户端 {0}，服务端 {1}',error.clientVersion||'?',error.serverVersion||'?')}`;
 else if(['UNAVAILABLE','TIMEOUT'].includes(error.code)&&error.operation==='native_session.connect') summary=L('服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。');
 else summary=L(summary||error.message||error.code||'操作失败，请重试或查看诊断详情。');
 const details=[error.code,error.message,error.operation,error.httpStatus&&`HTTP ${error.httpStatus}`,error.remoteCode,error.detail,error.requestId].filter(Boolean).join(': ');
 return `${summary}\n${L('详细信息')}: ${details}`+(error.releaseURL?`\n${L('版本下载：{0}',error.releaseURL)}`:'');
}
export const directReasons={connected:'已建立 UDP 直连',local_network:'同一局域网，无需隧道',no_local_ipv6:'本机网络不支持 IPv6',no_server_ipv6:'NAS 未提供可用的公网 IPv6',server_disabled:'NAS 已禁用公网 IPv6 访问',handshake_failed:'UDP 握手未通过，已回退中继',discovery_failed:'地址发现失败，已使用中继',cooldown:'等待下一次直连检测',probing:'正在检测',checking:'正在检测'};
