import './style.css';
import {fallbackSymbols} from './icons.js';
import translations from './en.json';
import {Ready,Call,Info,SetAutoStart,UpdateTray,OpenAdmin,OpenNAS,OpenLogs,Copy,Quit} from '../wailsjs/go/main/App';
import {EventsOn,WindowShow,WindowGetSize,WindowSetSize} from '../wailsjs/runtime/runtime';
import {normalizeFNID,presentation,describeFailure,directReasons,factorMessages} from './presentation.js';

const $=id=>document.getElementById(id);
let language='zh', diagnostics={}, busy=false, editing=false, challenge=false, accessRequired=false, requestId='', operationError=null, autoStart=false, refreshInFlight=false, probeGeneration=0, currentPane='', symbols={};
const L=(key,...args)=>(language==='en'?(translations[key]||key):key).replace(/\{(\d+)\}/g,(m,i)=>args[i]??m);
const set=(id,text)=>{const element=$(id);(element.querySelector('.button-label')||element).textContent=text;};
function applyIcons(){document.querySelectorAll('[data-symbol]').forEach(image=>{const source=symbols[image.dataset.symbol];if(source&&image.getAttribute('src')!==source)image.src=source;});}
const show=(id,visible)=>{$(id).hidden=!visible;};
const value=id=>$(id).value;
const connected=()=>presentation(diagnostics.status,L);
function render() {
 const status=diagnostics.status||{}, p=connected(),setup=editing||!diagnostics.fnId;
 const pane=setup?(challenge?'otp':accessRequired?'access':'login'):'details';
 if(pane!==currentPane){currentPane=pane;WindowGetSize().then(size=>WindowSetSize(Math.max(size.w,494),setup?(challenge?418:accessRequired?594:548):668));}
 $('login-form').noValidate=!!status.localNetworkWaiting;
 show('login',setup);show('details',!setup);show('change',!setup);show('admin',!setup&&diagnostics.administrator===true);
 document.querySelectorAll('.credentials').forEach(row=>{row.hidden=challenge;row.querySelector('input').required=!challenge;});
 show('access-row',accessRequired&&!challenge);$('access').required=accessRequired&&!challenge;
 show('otp-row',challenge);$('otp').required=challenge;
 show('back',challenge);show('return',!!diagnostics.fnId&&!challenge);show('spinner',busy||status.localNetworkWaiting);
 document.querySelectorAll('button,input').forEach(el=>{el.disabled=busy;});$('quit').disabled=false;
 if(status.localNetworkWaiting){$('primary').disabled=false;$('login-form').querySelector('button[type=submit]').disabled=false;}
 set('submit-login',L(status.localNetworkWaiting?'继续远程连接':'登录并连接'));
 const title=status.localNetworkWaiting?L('正在等待局域网访问权限'):p.title;
 set('identity',[diagnostics.fnId,diagnostics.username].filter(Boolean).join(' · '));set('status',title);$('status').className=p.needsLogin?'attention':p.connected?'connected':'';
 set('primary',status.localNetworkWaiting?L('继续远程连接'):p.action);$('primary').querySelector('img').dataset.symbol=p.needsLogin?'person.badge.key':p.connected?'stop.fill':'bolt.fill';applyIcons();show('relogin',!p.needsLogin);show('retry',!p.needsLogin&&!!diagnostics.fnId&&!status.localNetworkWaiting);
 set('path',L({local:'局域网',ipv6:'IPv6 / UDP','fn-connect':'FN Connect / WSS'}[status.path]||'-'));
 set('address',diagnostics.clientAddress||'-');set('interface',(diagnostics.networkInterface||'-')+(diagnostics.mtu?` · MTU ${diagnostics.mtu}`:''));set('handshake',diagnostics.handshakeAge||'-');
 const dns=status.dns||{};
 set('dns',L({ready:'就绪',preparing:'正在读取',degraded:'降级',inactive:'未连接'}[dns.state]||'未连接')+' · '+(dns.suffix||''));$('copy-dns').disabled=busy||!dns.suffix;
 set('root',L(!diagnostics.privilegedAvailable?'不可用':diagnostics.privilegedDegraded?'降级':diagnostics.privilegedActive?'活动':'就绪'));
 set('direct',L(directReasons[diagnostics.direct?.reason]||'尚未检测'));set('nas',diagnostics.nasAddress||'-');show('open-nas',!!diagnostics.nasAddress);
 const direct=diagnostics.direct||{};
 const messages=[describeFailure(operationError||status.lastError,L),describeFailure(dns.lastError,L),direct.lastError&&('IPv6: '+describeFailure(direct.lastError,L)),direct.endpoint&&`UDP ${direct.endpoint}`,direct.candidateCount>0&&L('IPv6 候选 {0}，已尝试 {1}',direct.candidateCount,direct.attemptCount||0),diagnostics.lanOverlap&&L('本地与 NAS 网段重叠，仅保留隧道内 NAS 路由')].filter(Boolean);
 set('diagnostics',messages.join('\n')||L('无异常'));
 UpdateTray(busy?'PROBING':p.state,'FnCPN: '+title,p.action,p.needsLogin,busy).catch(console.error);
}
async function call(method,params={}) { const reply=await Call(method,params);if(reply.error)throw reply.error;return reply.result||{}; }
async function refresh() {
 if(refreshInFlight)return;refreshInFlight=true;
 try { diagnostics=await call('diagnose'); if(connected().connected&&!diagnostics.status.lastError)operationError=null; }
 catch(error){diagnostics={...diagnostics,status:{state:'DAEMON_UNAVAILABLE',lastError:error}};}
 finally{refreshInFlight=false;render();}
}
async function perform(task) {
 if(busy)return;busy=true;operationError=null;set('notice','');render();
 try{await task();}catch(error){operationError=typeof error==='string'?{code:'UNAVAILABLE',message:error}:error;set('notice',describeFailure(operationError,L));}
 finally{busy=false;render();await refresh();}
}
async function cancelAuthorization(){ const previous=requestId;requestId='';if(previous){try{await call('authorization-cancel',{requestId:previous});}catch(error){set('notice',describeFailure(error,L));}} }
function clearSecrets(){for(const id of ['password','access','otp'])$(id).value='';$('trust').checked=false;}
async function login(change=false){await cancelAuthorization();editing=true;challenge=false;accessRequired=false;clearSecrets();$('fnid').value=change?'':diagnostics.fnId||'';$('username').value=change?'':diagnostics.username||'';set('login-error','');render();WindowShow();$(change?'fnid':'password').focus();}
async function primary(){if(busy)return;if(diagnostics.status?.localNetworkWaiting)return perform(()=>call('continue-remote'));if(connected().needsLogin||!diagnostics.fnId)return login();return perform(()=>call(connected().connected?'disconnect':'connect'));}
$('login-form').addEventListener('submit',async event=>{
 event.preventDefault();if(busy)return;
 if(diagnostics.status?.localNetworkWaiting){await perform(()=>call('continue-remote'));return;}
 const fnId=normalizeFNID(value('fnid'));if(!fnId){set('login-error',L('FN Connect ID 格式不正确'));return;}
 busy=true;operationError=null;set('login-error','');render();
 try {
  if(!requestId){const result=await call('authorization-begin',{fnId});if(!result.requestId)throw {code:'PROTOCOL_ERROR',message:L('授权请求无效')};requestId=result.requestId;}
  await call('authorize-native',{requestId,fnId,username:value('username').trim(),password:value('password'),...(value('access')?{accessCode:value('access')}:{}),...(value('otp')?{twofaCode:value('otp').trim()}:{}),...($('trust').checked?{trustDevice:true}:{})});
  requestId='';editing=false;challenge=false;clearSecrets();
 } catch(error) {
  if(error.remoteCode?.startsWith('ACCESS_CODE_')){accessRequired=true;challenge=false;}
  if(['TWO_FACTOR_REQUIRED','TWO_FACTOR_CODE_INVALID'].includes(error.remoteCode)){challenge=true;accessRequired=false;$('access').value='';$('otp').value='';}
  if(error.remoteCode==='TWO_FACTOR_SETUP_REQUIRED'){challenge=false;accessRequired=false;}
  editing=true;operationError=error;set('login-error',describeFailure(error,L));
 } finally {busy=false;render();await refresh();if(challenge)$('otp').focus();}
});
$('fnid').addEventListener('blur',async()=>{
 const generation=++probeGeneration,fnId=normalizeFNID(value('fnid'));if(!fnId||busy)return;
 try{const reply=await call('detect-access-code',{fnId});if(generation===probeGeneration&&normalizeFNID(value('fnid'))===fnId){accessRequired=reply.required===true;render();}}catch{/* Credential-free probe is optional; login reports actionable errors. */}
});
$('primary').onclick=primary;$('retry').onclick=()=>perform(()=>call('retry'));
$('change').onclick=()=>login(true);$('relogin').onclick=()=>login();
$('back').onclick=()=>{challenge=false;$('otp').value='';$('trust').checked=false;render();$('password').focus();};
$('return').onclick=async()=>{await cancelAuthorization();editing=false;challenge=false;accessRequired=false;clearSecrets();set('login-error','');set('notice','');operationError=null;render();};
$('admin').onclick=()=>perform(async()=>{const reply=await OpenAdmin();if(reply.error){if(reply.error.code==='AUTH_REQUIRED'||factorMessages[reply.error.remoteCode]){await login();throw {...reply.error,message:L('管理后台登录已过期')+' — '+L('请重新登录 FnCPN。当前隧道连接不受影响。')};}throw reply.error;}});
$('open-nas').onclick=()=>perform(()=>OpenNAS(diagnostics.nasAddress));
$('copy-dns').onclick=()=>Copy(diagnostics.status?.dns?.suffix||'');
$('copy').onclick=()=>Copy(JSON.stringify({...diagnostics,...(operationError?{operationError}:{})},null,2));
$('logs').onclick=()=>perform(()=>OpenLogs());$('refresh').onclick=refresh;$('quit').onclick=()=>Quit();
EventsOn('status-changed',refresh);
EventsOn('daemon-error',message=>{set('notice',message);});
EventsOn('authorize',async data=>{await login();$('fnid').value=data.fnId;requestId=data.requestId;render();$('password').focus();});
EventsOn('tray-action',async action=>{
 if(action==='primary')await primary();else if(action==='login')await login();else if(action==='autostart')await perform(async()=>{const current=await Info();const next=!current.autoStart;const error=await SetAutoStart(next);if(error)throw {message:error};autoStart=next;});
});
const info=await Info();language=info.language;autoStart=info.autoStart;symbols={...fallbackSymbols,...info.symbols};document.documentElement.dataset.platform=info.platform;applyIcons();document.documentElement.lang=language==='zh'?'zh-CN':'en';set('version','v'+info.version);
document.querySelectorAll('[data-i18n]').forEach(el=>{el.textContent=L(el.textContent);});document.querySelectorAll('[title]').forEach(el=>{el.title=L(el.title);el.setAttribute('aria-label',el.title);});
if(info.startupError)set('notice',info.startupError);
await refresh();
await Ready();
// Handshake ages change without status generations; refresh them at a modest rate.
setInterval(refresh,15000);
