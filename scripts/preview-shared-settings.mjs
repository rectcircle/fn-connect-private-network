#!/usr/bin/env node
// Render and exercise the production settings UI with a local fixture only.
import http from 'node:http';
import {readFile,mkdir,mkdtemp,rm,writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import path from 'node:path';
import {spawn} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..'),out=path.resolve(process.argv[2]||path.join(root,'dist/shared-settings-preview'));
await mkdir(out,{recursive:true});
const chrome=process.env.CHROME||'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const directory=await mkdtemp(path.join(tmpdir(),'fncpn-settings-'));
let preferences=[{link:'ipv6',enabled:true},{link:'fn-connect',enabled:true}],tests=0,changed=0,mobile=false;
const harness=`window.addEventListener('load',async()=>{try{
const $=id=>document.getElementById(id),check=(v,m)=>{if(!v)throw Error(m)},wait=()=>new Promise(r=>setTimeout(r,320));
for(let i=0;i<40&&!$('nas').textContent.includes('Home NAS');i++)await wait();
if(innerWidth===390){check(!$('settings-menu').hidden&&$('general').hidden,'mobile menu not default');$('mobile-about').click();check($('general').getAnimations().length>0,'forward slide missing');check($('general-title').textContent==='关于','mobile About missing');history.back();await new Promise(r=>setTimeout(r,32));check($('settings-menu').getAnimations().length>0,'back slide missing');await wait();$('mobile-links').click();check(getComputedStyle($('priorities').querySelector('button')).display==='none','mobile reorder arrows remained');}else{check(!$('general').hidden&&$('links').hidden,'default page');$('links-tab').click();}
check(getComputedStyle($('selector')).textAlign==='center','selector text not centered');check(document.querySelector('.local input').disabled&&document.querySelector('.local input').checked,'LAN is not an enabled disabled row');const before=$('start').getBoundingClientRect().x;$('selector').click();$('choose-ipv6').click();check(!$('choices').hidden,'multi-select closed');check($('start').getBoundingClientRect().x===before,'selection shifted actions');$('choose-ipv6').click();$('choices-done').click();
check($('choices').hidden,'Done failed');$('start').click();check($('confirm').open,'confirmation missing');$('confirm-cancel').click();check(!$('confirm').open,'cancel failed');
if(innerWidth===390){$('start').click();history.back();await wait();check(!$('confirm').open&&!$('settings-menu').hidden,'back left confirmation open');$('mobile-links').click();}
const rows=$('priorities').children;rows[0].querySelector('.grip').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));await wait();check($('priorities').children[0].querySelector('.name').textContent.includes('FN Connect'),'reorder failed');
$('start').click();$('confirm-start').click();await wait();await wait();check(Math.abs(document.documentElement.scrollHeight-innerHeight-scrollY)<3||document.documentElement.scrollHeight<=innerHeight,'test updates did not keep the page at the bottom');check(!$('results').hidden&&$('result-rows').children.length===2,'results missing');check($('startup').hidden===(innerWidth===390),'startup visibility incorrect');
check(document.documentElement.scrollWidth<=innerWidth,'horizontal overflow');document.body.dataset.qa='passed';
}catch(e){document.body.dataset.qa='failed';document.body.dataset.error=e.message;}});`;
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://localhost'),base='/app/fncpn/settings';res.setHeader('Content-Type','application/json');
 if(u.pathname===base){let html=await readFile(path.join(root,'internal/client/settingsweb/index.html'),'utf8');html=html.replace('</body>','<script src="settings/harness.js"></script></body>');res.setHeader('Content-Type','text/html');res.end(html);return;}
 if(u.pathname===base+'/harness.js'){res.setHeader('Content-Type','text/javascript');res.end(harness);return;}
 if(u.pathname===base+'/api/state'){res.end(JSON.stringify({version:'1.2.0',commit:'fixture-preview',mobile,settings:{fnId:'Home NAS',links:preferences},status:{path:'fn-connect'}}));return;}
 if(u.pathname===base+'/api/preferences'){let body='';for await(const chunk of req)body+=chunk;preferences=JSON.parse(body).links;changed++;res.end(JSON.stringify({fnId:'Home NAS',links:preferences}));return;}
 if(u.pathname===base+'/api/test'){tests++;const results=[{link:'ipv6',latencyMs:18.1,downloadMbps:235.1,uploadMbps:86.4},{link:'fn-connect',latencyMs:34.2,downloadMbps:123.2,uploadMbps:56.4}];res.setHeader('Content-Type','application/x-ndjson');res.end([{active:true,stage:'latency',link:'fn-connect',rttMs:34.2,results:[]},{active:true,stage:'upload',link:'fn-connect',mbps:56.4,progress:.5,results:results.slice(0,1)},{active:true,stage:'upload-confirming',link:'fn-connect',results},{active:false,stage:'completed',link:'fn-connect',results}].map(x=>JSON.stringify(x)+'\n').join(''));return;}
 const name=u.pathname.slice((base+'/').length);if(!['settings.js','settings.css','en.json'].includes(name)){res.statusCode=404;res.end();return;}
 const data=await readFile(path.join(root,'internal/client/settingsweb',name));res.setHeader('Content-Type',name.endsWith('.js')?'text/javascript':name.endsWith('.css')?'text/css':'application/json');
 if(name==='settings.js')res.end(data.toString().replace("nativeSend('ready');", "window.__settingsPreviewRender=renderTest;window.fncpnSettingsState({type:'fncpn-settings',autoStart:true});nativeSend('ready');"));else res.end(data);
});
try{
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const address=`http://127.0.0.1:${server.address().port}/app/fncpn/settings?fncpn_proxy=${'a'.repeat(64)}`;
 for(const [name,width,height] of [['desktop',1000,920],['mobile',390,1100]]){
  mobile=name==='mobile';preferences=[{link:'ipv6',enabled:true},{link:'fn-connect',enabled:true}];
  const child=spawn(chrome,['--headless','--disable-gpu','--no-first-run','--remote-debugging-pipe',`--user-data-dir=${directory}/${name}`],{stdio:['ignore','ignore','pipe','pipe','pipe']});
  let sequence=0,pending=new Map(),buffer='',closed=false;
  const ended=new Promise(resolve=>child.once('close',()=>{closed=true;for(const call of pending.values())call.reject(Error('Chrome closed before completing preview'));pending.clear();resolve();}));
  const cdp=(method,params={},sessionId)=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});child.stdio[3].write(JSON.stringify({id,method,params,...(sessionId?{sessionId}:{})})+'\0');});
  child.stderr.resume();
  child.stdio[4].on('data',chunk=>{buffer+=chunk.toString();let end;while((end=buffer.indexOf('\0'))!==-1){const message=JSON.parse(buffer.slice(0,end));buffer=buffer.slice(end+1);const call=pending.get(message.id);if(call){pending.delete(message.id);if(message.error)call.reject(Error(JSON.stringify(message.error)));else call.resolve(message.result);}}});
  const guard=setTimeout(()=>child.kill(),30000);
  try{
   const {targetId}=await cdp('Target.createTarget',{url:'about:blank'});
   const {sessionId}=await cdp('Target.attachToTarget',{targetId,flatten:true});
   await cdp('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:name==='mobile'},sessionId);
   await cdp('Page.navigate',{url:address},sessionId);
   const result=await cdp('Runtime.evaluate',{expression:`new Promise((resolve,reject)=>{let count=0;const timer=setInterval(()=>{if(document.body?.dataset.qa){clearInterval(timer);resolve({qa:document.body.dataset.qa,error:document.body.dataset.error,width:innerWidth,scroll:document.documentElement.scrollWidth})}else if(count++>300){clearInterval(timer);resolve({qa:'timeout'})}},30)})`,awaitPromise:true,returnByValue:true},sessionId);
   const qa=result.result.value;
   if(qa.qa!=='passed'||qa.width!==width||qa.scroll>width)throw Error(name+' failed: '+JSON.stringify(qa));
   const screenshot=await cdp('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},sessionId);
   await writeFile(path.join(out,name+'.png'),Buffer.from(screenshot.data,'base64'));
   if(name==='mobile'){
    const diagram=await cdp('Runtime.evaluate',{expression:`new Promise(resolve=>{window.__settingsPreviewRender({active:true,stage:'download',link:'fn-connect',mbps:31.5,results:[]});setTimeout(()=>{const icons=[...document.querySelectorAll('#topology .device')].map(n=>n.getBoundingClientRect().top),line=document.querySelector('.segment .down').getBoundingClientRect(),top=document.getElementById('topology').getBoundingClientRect();resolve({icons,packets:document.querySelectorAll('#packet-layer circle').length,cy:Number(document.getElementById('packet').getAttribute('cy')),expected:line.top-top.top+line.height/2})},150)})`,awaitPromise:true,returnByValue:true},sessionId);
    const d=diagram.result.value;if(Math.max(...d.icons)-Math.min(...d.icons)>1||d.packets!==3||Math.abs(d.cy-d.expected)>1)throw Error('mobile packet alignment failed: '+JSON.stringify(d));
    const live=await cdp('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},sessionId);await writeFile(path.join(out,'mobile-relay-download.png'),Buffer.from(live.data,'base64'));
    await cdp('Runtime.evaluate',{expression:`history.back();new Promise(resolve=>setTimeout(resolve,120))`,awaitPromise:true},sessionId);
    const menu=await cdp('Page.captureScreenshot',{format:'png',captureBeyondViewport:true},sessionId);await writeFile(path.join(out,'mobile-menu.png'),Buffer.from(menu.data,'base64'));
   }
   const dom=await cdp('Runtime.evaluate',{expression:'document.documentElement.outerHTML',returnByValue:true},sessionId);await writeFile(path.join(out,name+'.html'),dom.result.value);
  }finally{clearTimeout(guard);if(!closed)child.kill();await ended;}

 }
 if(tests!==2||changed!==2)throw new Error('confirmation or saving issued unexpected requests: '+JSON.stringify({tests,changed}));
 console.log('Desktop and mobile interaction/layout checks passed. '+out);
}finally{server.close();await rm(directory,{recursive:true,force:true,maxRetries:5,retryDelay:100});}
