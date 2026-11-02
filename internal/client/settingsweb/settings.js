const $ = id => document.getElementById(id);
const translations = await fetch(new URL('settings/en.json',location.href)).then(r=>r.json());
const english = !navigator.language.toLowerCase().startsWith('zh');
const L = text => english ? (translations[text] || text) : text;
document.documentElement.lang = english ? 'en' : 'zh-CN';
document.querySelector('aside').setAttribute('aria-label',L('设置'));
document.querySelector('.help').setAttribute('aria-label',L('说明'));
document.querySelectorAll('[data-i18n]').forEach(node=>{ node.textContent=L(node.textContent); });
const params = new URLSearchParams(location.search), token=params.get('fncpn_proxy')||sessionStorage.getItem('fncpn-settings-token');
if(token)sessionStorage.setItem('fncpn-settings-token',token);
history.replaceState(null,'',location.pathname);
const base=location.pathname;
let packetStage='',packetFrame=0,packetStarted=0;
let layoutApplied=false,mobile=document.documentElement.dataset.layout==='mobile', dragging=false, state, saving=false, controller, last={}, historyValues=[], historyStage='', selection=[];
const linkName = key => L(key==='ipv6'?'IPv6 直连':key==='fn-connect'?'FN Connect 中继':'局域网');
function notice(text=''){ $('notice').textContent=text;$('notice').hidden=!text;$('dismiss').hidden=!text; }
async function request(path, body, signal){
 const response=await fetch(base+'/api/'+path,{method:body?'POST':'GET',headers:{'Authorization':'Bearer '+token,...(body?{'Content-Type':'application/json'}:{})},body:body?JSON.stringify(body):undefined,signal});
 if(!response.ok){ let text;try{text=(await response.json()).error;}catch{} throw new Error(text||L('请求失败')); }return response;
}
function selectionChanged(){
 const links=[$('choose-ipv6').checked?'ipv6':'',$('choose-relay').checked?'fn-connect':''].filter(Boolean);
 $('selector').textContent=links.length===2?L('IPv6 与 FN Connect'):links.length?linkName(links[0]):L('选择测试链路');
 $('start').disabled=!links.length||!state?.settings.fnId||!!state?.infoOnly;
 return links;
}
function setChoices(open){$('choices').hidden=!open;$('selector').setAttribute('aria-expanded',String(open));}
$('selector').onclick=()=>setChoices($('choices').hidden);
$('choices-done').onclick=()=>{setChoices(false);$('selector').focus();};
$('choose-ipv6').onchange=selectionChanged;$('choose-relay').onchange=selectionChanged;
document.addEventListener('click',e=>{if(!e.target.closest('.select-wrap'))setChoices(false);});
document.addEventListener('keydown',e=>{if(e.key==='Escape')setChoices(false);});
let currentPage,finishPageTransition;
function page(name,push=true){
 finishPageTransition?.();
 const previous=currentPage,old=previous&&$(previous==='menu'?'settings-menu':previous);
 const rect=old?.getBoundingClientRect();
 currentPage=name;
 if(name!=='links'){controller?.abort();animatePacket('');setChoices(false);$('confirm').close();}
 if(push)history.pushState({settingsPage:name},'',base+'#'+name);
 $('settings-menu').hidden=name!=='menu';$('general').hidden=name!=='general';$('links').hidden=name!=='links';
 $('settings-back').hidden=!mobile||params.get('nativeBack')==='1'||name==='menu';
 $('general-tab').classList.toggle('selected',name==='general');$('links-tab').classList.toggle('selected',name==='links');
 window.scrollTo(0,0);
 if(mobile&&previous&&previous!==name&&!matchMedia('(prefers-reduced-motion: reduce)').matches){
  const next=$(name==='menu'?'settings-menu':name),back=name==='menu';
  old.hidden=false;old.inert=true;
  Object.assign(old.style,{position:'fixed',top:rect.top+'px',left:rect.left+'px',width:rect.width+'px',zIndex:'8',pointerEvents:'none'});
  const options={duration:280,easing:'cubic-bezier(.22,.75,.25,1)',fill:'both'};
  const incoming=next.animate([{transform:`translateX(${back?'-':''}100vw)`},{transform:'translateX(0)'}],options);
  const outgoing=old.animate([{transform:'translateX(0)'},{transform:`translateX(${back?'':'-'}100vw)`}],options);
  const finish=()=>{if(finishPageTransition!==finish)return;finishPageTransition=undefined;incoming.cancel();outgoing.cancel();old.hidden=true;old.inert=false;old.removeAttribute('style');};
  finishPageTransition=finish;incoming.onfinish=finish;
 }
}
function applyLayout(next){
 if(layoutApplied)return;layoutApplied=true;
 mobile=next;if(mobile)$('startup').hidden=true;document.documentElement.dataset.layout=mobile?'mobile':'desktop';
 $('general-title').textContent=L(mobile?'关于':'通用');$('about-heading').hidden=mobile;
 history.replaceState({settingsPage:mobile?'menu':'general'},'',base);page(mobile?'menu':'general',false);
}
$('general-tab').onclick=()=>page('general');$('links-tab').onclick=()=>page('links');
$('mobile-links').onclick=()=>page('links');$('mobile-about').onclick=()=>page('general');
$('settings-back').onclick=()=>history.back();
window.addEventListener('popstate',event=>page(event.state?.settingsPage||(mobile?'menu':'general'),false));
function renderPreferences(){
 if(dragging)return;
 $('priorities').replaceChildren();
 for(const [index,link] of state.settings.links.entries()){
  const row=document.createElement('div');row.className='link-row';row.draggable=!saving&&!!state.settings.fnId;row.dataset.index=index;
  const grip=document.createElement('span');grip.textContent='⠿';grip.setAttribute('aria-hidden','true');
  grip.className='grip';grip.tabIndex=0;grip.setAttribute('role','button');grip.setAttribute('aria-label',L('拖动调整优先级')+' '+linkName(link.link));
  grip.onkeydown=e=>{if((e.key==='Enter'||e.key===' ')&&!saving&&state.settings.fnId){e.preventDefault();save([...state.settings.links].reverse());}};
  grip.onpointerdown=e=>{if(e.pointerType!=='touch'||saving||!state.settings.fnId)return;dragging=true;row.classList.add('dragging');grip.setPointerCapture(e.pointerId);};
  grip.onpointerup=e=>{if(e.pointerType!=='touch'||!dragging)return;dragging=false;row.classList.remove('dragging');const target=document.elementFromPoint(e.clientX,e.clientY)?.closest('.link-row');if(target&&target.dataset.index!==undefined&&target.dataset.index!==String(index))save([...state.settings.links].reverse());};
  grip.onpointercancel=()=>{dragging=false;row.classList.remove('dragging');};
  const name=document.createElement('label');name.htmlFor='preference-'+link.link;name.className='name';name.textContent=linkName(link.link);
  const move=document.createElement('button');move.textContent=index===0?'↓':'↑';move.title=L(index===0?'下移':'上移');move.setAttribute('aria-label',move.title+' '+linkName(link.link));move.disabled=saving||!state.settings.fnId;move.onclick=()=>save([...state.settings.links].reverse());
  const check=document.createElement('input');check.type='checkbox';check.id='preference-'+link.link;check.checked=link.enabled;check.disabled=saving||!state.settings.fnId;check.setAttribute('aria-label',linkName(link.link));check.onchange=()=>save(state.settings.links.map(entry=>({...entry,enabled:entry.link===link.link?check.checked:entry.enabled})));
  row.append(grip,name,move,check);row.ondragstart=e=>{dragging=true;e.dataTransfer.setData('text/plain',String(index));row.classList.add('dragging');};row.ondragend=()=>{dragging=false;row.classList.remove('dragging');};row.ondragover=e=>e.preventDefault();row.ondrop=e=>{e.preventDefault();dragging=false;if(!saving&&e.dataTransfer.getData('text/plain')===String(1-index))save([...state.settings.links].reverse());};
  $('priorities').append(row);
 }
 $('reset').disabled=saving||!state.settings.fnId;
}
async function save(links){
 if(saving||!state?.settings.fnId)return;saving=true;notice();renderPreferences();
 try{state.settings=await (await request('preferences',{fnId:state.settings.fnId,links})).json();}catch(e){notice(e.message);}finally{saving=false;renderPreferences();}
}
$('reset').onclick=()=>save([{link:'ipv6',enabled:true},{link:'fn-connect',enabled:true}]);
async function refresh(){
 try{const next=await(await request('state')).json();if(saving||dragging)return;state=next;applyLayout(params.get('layout')?params.get('layout')==='mobile':!!state.mobile);$('unavailable').hidden=!state.infoOnly;$('version').textContent=L('版本')+' '+state.version;$('commit').textContent='Commit '+state.commit;$('nas').textContent='NAS · '+(state.settings.fnId||'—');$('current').textContent=state.status.path?linkName(state.status.path):L('尚未连接');renderPreferences();selectionChanged();}catch(e){notice(e.message);}
}
const phases={preparing:'准备测试',checking:'检查链路条件',queued:'等待连接任务',connecting:'正在建立真实链路',verifying:'正在验证 NAS',latency:'正在测试延迟',download:'正在测试下载',upload:'正在测试上传','upload-warmup':'正在预热上传','upload-waiting':'等待 NAS 接收数据','upload-confirming':'等待 NAS 完成确认','upload-complete':'上传完成',restoring:'正在恢复正常选路',completed:'测速完成',canceled:'测速已取消',failed:'测试失败'};
function measurements(result){return ['latencyMs','downloadMbps','uploadMbps'].map((key,i)=>typeof result[key]==='number'?result[key].toFixed(1)+(i?' Mbps':' ms'):'—');}
function renderTest(value){
 last=value;$('live').hidden=!value.active;$('phase').textContent=(value.link?linkName(value.link)+' · ':'')+L(phases[value.stage]||value.stage||'准备测试');
 const relay=value.link==='fn-connect';$('relay').hidden=!relay;$('relay-segment').hidden=!relay;
 $('topology').className=value.active&&['upload','download','latency'].includes(value.stage)?value.stage:'';animatePacket($('topology').className);
 $('metric').textContent=value.stage==='latency'?(value.rttMs||0).toFixed(1)+' ms':value.stage==='upload-warmup'?L('预热中…'):value.stage==='upload-waiting'?L('等待接收…'):value.stage==='upload-confirming'?L('等待确认…'):['upload','upload-complete','download'].includes(value.stage)?(value.mbps||0).toFixed(1)+' Mbps':'';
 const family=(value.stage||'').startsWith('upload')?'upload':value.stage;
 if(historyStage!==family){historyValues=[];historyStage=family;}
 if(['download','upload'].includes(value.stage)){historyValues.push(Math.max(0,value.mbps||0));historyValues=historyValues.slice(-60);}
 const max=Math.max(1,...historyValues);$('graph').querySelector('polyline').setAttribute('points',historyValues.map((v,i)=>`${i*600/59},${60-v/max*55}`).join(' '));$('graph').toggleAttribute('hidden',!historyValues.length);
 $('progress').value=value.progress||0;
 const rows=value.results||[];
 $('partial').textContent=rows.map(row=>linkName(row.link)+' · '+measurements(row).map((v,i)=>v==='—'?'':L(['延迟','下载','上传'][i])+' '+v).filter(Boolean).join('  ')+(row.error?' · '+L(row.error):'')).join('\n');
 $('results').hidden=!!value.active||!rows.length;
 if(!value.active){
  $('result-rows').replaceChildren();for(const row of rows){const tr=document.createElement('tr');for(const text of [linkName(row.link),...measurements(row)]){const td=document.createElement('td');td.textContent=text;tr.append(td);}$('result-rows').append(tr);}
  notice([value.error,value.restoreError,...rows.map(row=>row.error?linkName(row.link)+': '+L(row.error):'')].filter(Boolean).join('\n'));
 }
 followTestBottom();
}
let testScrollFrame=0;
function followTestBottom(){
 if(testScrollFrame)return;
 testScrollFrame=requestAnimationFrame(()=>{testScrollFrame=0;if(currentPage==='links')window.scrollTo({top:document.documentElement.scrollHeight,behavior:'instant'});});
}
const testSizeObserver=new ResizeObserver(()=>{if(controller)followTestBottom();});
for(const id of ['live','results'])testSizeObserver.observe($(id));
$('dismiss').onclick=()=>notice();
$('start').onclick=()=>{selection=selectionChanged();if(!selection.length)return;setChoices(false);$('confirm').showModal();};
$('confirm-cancel').onclick=()=>$('confirm').close();
$('confirm-start').onclick=async()=>{
 $('confirm').close();const previous=controller;if(previous)previous.abort();const current=new AbortController();controller=current;
 $('cancel').hidden=false;$('start').textContent=L('重新测试');notice();historyValues=[];historyStage='';renderTest({active:true,stage:'preparing',results:[]});

 try{
  const response=await request('test',{link:selection.length===2?'all':selection[0]},current.signal), reader=response.body.getReader(),decoder=new TextDecoder();let pending='';let ended=false;
  while(true){const chunk=await reader.read();if(controller!==current){await reader.cancel();return;}pending+=decoder.decode(chunk.value||new Uint8Array(),{stream:!chunk.done});let newline;while((newline=pending.indexOf('\n'))!==-1){const line=pending.slice(0,newline);pending=pending.slice(newline+1);if(line.trim()){const event=JSON.parse(line);renderTest(event);if(!event.active)ended=true;}}if(chunk.done)break;}
  if(!ended)throw new Error(L('测试连接已中断，请重试'));
 }catch(e){if(controller===current)renderTest({...last,active:false,stage:e.name==='AbortError'?'canceled':'failed',error:e.name==='AbortError'?'':e.message});}
 finally{if(controller===current){controller=undefined;$('cancel').hidden=true;}}
};
$('cancel').onclick=()=>controller?.abort();
window.addEventListener('pagehide',()=>{controller?.abort();animatePacket('');});
// Only local settings containers provide this narrow launch-at-login bridge.
function nativeState(data){if(data?.type!=='fncpn-settings')return;$('startup').hidden=mobile;$('auto-start').checked=!!data.autoStart;if(data.error)notice(data.error);}
window.chrome?.webview?.addEventListener('message',event=>nativeState(event.data));
window.fncpnSettingsState=nativeState;
function nativeSend(action){const value='fncpn-settings:'+action;if(window.chrome?.webview)window.chrome.webview.postMessage(value);else window.webkit?.messageHandlers?.fncpnSettings?.postMessage(value);}
$('auto-start').onchange=()=>nativeSend($('auto-start').checked?'enable':'disable');
nativeSend('ready');selectionChanged();await refresh();
setInterval(()=>{if(!document.hidden){nativeSend('ready');if(!controller)refresh();}},5000);


function animatePacket(stage){
 if(stage===packetStage)return;
 packetStage=stage;cancelAnimationFrame(packetFrame);
 const packets=[...$('packet-layer').querySelectorAll('circle')];
 packets.forEach(packet=>packet.setAttribute('visibility','hidden'));
 if(!stage||matchMedia('(prefers-reduced-motion: reduce)').matches)return;
 packetStarted=performance.now();
 const ease=t=>t*t*(3-2*t);
 function tick(now){
  if(!packetStage)return;
  const seconds=(now-packetStarted)/1000,cycle=seconds%1;
  const latency=stage==='latency',down=latency?cycle>=.25:stage==='download';
  const topology=$('topology').getBoundingClientRect();
  const lines=[...$('topology').querySelectorAll(down?'.down':'.up')].map(line=>line.getBoundingClientRect()).filter(rect=>rect.width>0);
  if(lines.length){
   const first=lines[0],last=lines.at(-1),left=first.left-topology.left,right=last.right-topology.left;
   $('packet-layer').setAttribute('viewBox',`0 0 ${topology.width} ${topology.height}`);
   packets.forEach((packet,index)=>{
    // Match macOS: three evenly spaced packets, 0.65 traversals per second.
    // Latency keeps one accelerated ping/pong, 250 ms per direction and a pause.
    const progress=latency?ease(down?(cycle-.25)/.25:cycle/.25):(seconds*.65+index/3)%1;
    const x=down?right-(right-left)*progress:left+(right-left)*progress;
    const visible=(!latency||index===0&&cycle<.5)&&lines.some(rect=>x>=rect.left-topology.left&&x<=rect.right-topology.left);
    packet.setAttribute('cx',x);packet.setAttribute('cy',first.top-topology.top+first.height/2);
    packet.setAttribute('visibility',visible?'visible':'hidden');
   });
  }
  packetFrame=requestAnimationFrame(tick);
 }
 packetFrame=requestAnimationFrame(tick);
}
