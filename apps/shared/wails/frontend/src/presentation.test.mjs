import test from 'node:test';
import assert from 'node:assert/strict';
import {normalizeFNID,presentation,describeFailure} from './presentation.js';
test('FN Connect ID normalization matches supported login forms',()=>{
 for(const value of ['Home-Nas','https://fnos.net/home-nas','https://home-nas.fnos.net','home-nas.fnos.net'])assert.equal(normalizeFNID(value),'home-nas');
 for(const value of ['-bad','bad-','http://evil.example/a','a/b','中','a'.repeat(64)])assert.equal(normalizeFNID(value),null);
});
test('permission failures distinguish reauthentication from local privilege failures',()=>{
 assert.equal(presentation({state:'ERROR',lastError:{code:'PERMISSION_DENIED',httpStatus:403,operation:'privileged.apply'}}).needsLogin,false);
 assert.equal(presentation({state:'ERROR',lastError:{code:'PERMISSION_DENIED',httpStatus:403,operation:'authorization.session'}}).needsLogin,true);
 assert.equal(presentation({state:'RELAY'}).connected,true);
 assert.equal(presentation({state:'SERVER_UNAVAILABLE'}).needsLogin,false);
 assert.equal(presentation({lastError:{remoteCode:'TWO_FACTOR_REQUIRED'}}).needsLogin,true);
});
test('version mismatch shows both versions and the correct upgrade direction',()=>{
 const message=describeFailure({code:'VERSION_INCOMPATIBLE',clientVersion:'1.1.1',serverVersion:'2.0.0',upgradeTarget:'client'});
 assert.match(message,/升级客户端/);assert.match(message,/1.1.1/);assert.match(message,/2.0.0/);
});
test('native connection failure gives checks without reclassifying other failures',()=>{
 const message='服务不可用。请检查网络连接、NAS 上的 FN Connect 是否已启用，以及 FN Connect ID 是否正确。';
 for(const code of ['UNAVAILABLE','TIMEOUT']) {
  const text=describeFailure({code,operation:'native_session.connect',message:'old server message',detail:'DNS failed'});
  assert.ok(text.startsWith(message)); assert.ok(text.includes('DNS failed'));
 }
 assert.ok(!describeFailure({code:'AUTH_REQUIRED',operation:'native_session.authenticate',message:'bad password'}).includes('FN Connect ID 是否正确'));
 assert.ok(!describeFailure({code:'UNAVAILABLE',operation:'privileged.apply',message:'daemon stopped'}).includes('FN Connect ID 是否正确'));
});
