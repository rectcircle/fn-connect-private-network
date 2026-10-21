import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {presentation,normalizeFNID,describeFailure,directReasons,factorMessages} from './presentation.js';

// Run the actual entry point with a small DOM/bridge fixture, including its handlers.
async function page() {
 const elements=new Map(),events={};
 const element=id=>{
  if(!elements.has(id))elements.set(id,{textContent:'',value:'',dataset:{},className:'',checked:false,querySelector:selector=>selector==='.button-label'?null:element(id+selector),addEventListener(type,fn){this[type]=fn;},focus(){}});
  return elements.get(id);
 };
 let authorized=false,expireAdmin=false;
 const status=()=>({fnId:'home',username:'user',status:{state:authorized?'LOCAL':'PAUSED'}});
 const Call=async method=>method==='diagnose'?{result:status()}:method==='authorization-begin'?{result:{requestId:'request'}}:method==='authorize-native'?authorized?{result:{}}:{error:{code:'UNAVAILABLE',message:'login failed'}}:{result:{}};
 const source=(await readFile(new URL('./main.js',import.meta.url),'utf8')).replace(/^import .*;\n/gm,'');
 const dependencies={document:{getElementById:element,querySelectorAll:()=>[],documentElement:{dataset:{}}},fallbackSymbols:{},translations:{},Ready:async()=>{},Call,Info:async()=>({language:'zh',symbols:{},version:'test'}),SetAutoStart:async()=>{},UpdateTray:async()=>{},OpenAdmin:async()=>expireAdmin?{error:{code:'AUTH_REQUIRED'}}:{},OpenNAS:async()=>{},OpenLogs:async()=>{},Copy:()=>{},Quit:()=>{},EventsOn:(name,fn)=>{events[name]=fn;},WindowShow:()=>{},WindowGetSize:async()=>({w:494}),WindowSetSize:()=>{},normalizeFNID,presentation,describeFailure,directReasons,factorMessages,setInterval:()=>{},console};
 const run=new (Object.getPrototypeOf(async function(){}).constructor)(...Object.keys(dependencies),source+'\nreturn {login,refresh};');
 const api=await run(...Object.values(dependencies));
 return {element,events,api,authorize(){authorized=true;},expireAdmin(){expireAdmin=true;},async submit(){element('fnid').value='home';await element('login-form').submit({preventDefault(){}});}};
}
test('failed login stays in form and successful login clears both error areas',async()=>{
 const p=await page();await p.api.login();await p.submit();
 assert.match(p.element('login-error').textContent,/login failed/);
 assert.equal(p.element('notice').textContent,'');
 assert.doesNotMatch(p.element('diagnostics').textContent,/login failed/);
 p.events['daemon-error']('temporary daemon error');p.authorize();await p.submit();
 assert.equal(p.element('login').hidden,true);
 assert.equal(p.element('login-error').textContent,'');assert.equal(p.element('notice').textContent,'');
});
test('expired administration login error cannot carry over on return to main page',async()=>{
 const p=await page();p.expireAdmin();await p.element('admin').onclick();
 assert.equal(p.element('login').hidden,false);
 assert.match(p.element('login-error').textContent,/管理后台登录已过期/);
 assert.equal(p.element('notice').textContent,'');
 await p.element('return').onclick();
 assert.equal(p.element('login').hidden,true);
 assert.equal(p.element('notice').textContent,'');assert.equal(p.element('login-error').textContent,'');
 assert.doesNotMatch(p.element('diagnostics').textContent,/管理后台登录已过期/);
});
test('daemon errors are shown and cleared when starting a new login',async()=>{
 const p=await page();p.events['daemon-error']('service unavailable');
 assert.equal(p.element('notice').textContent,'service unavailable');
 await p.api.login();assert.equal(p.element('notice').textContent,'');
});
