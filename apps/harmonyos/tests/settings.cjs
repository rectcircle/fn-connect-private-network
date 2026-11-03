// Exercise the native streaming controller, including fragmented UTF-8 and cancellation.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '../../..');
const cli = path.join(process.env.HOME, '.local/opt/harmonyos/command-line-tools', JSON.parse(fs.readFileSync(path.join(root, 'apps/harmonyos/toolchains.json')))[process.platform === 'linux' ? 'linuxCli' : 'cli'].version);
const ts = require(path.join(cli, 'hvigor/hvigor/node_modules/typescript'));
const source = fs.readFileSync(path.join(root, 'apps/harmonyos/entry/src/main/ets/controller/SettingsTest.ets'), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2020 } }).outputText;
let mode = 'complete', requests = [], options, target, canceledReceive;
class FakeRequest {
  listeners = {}; destroyed = false;
  on(name, fn) { this.listeners[name] = fn; }
  destroy() { this.destroyed = true; this.done?.(200); }
  async requestInStream(url, value) {
    target = url; options = value;
    const events = [{ active: true, stage: 'connecting', results: [] }];
    if (mode === 'complete') events.push({ active: false, stage: 'completed', results: [{ link: 'ipv6', error: '分片中文' }] });
    const bytes = Buffer.from(events.map(x => JSON.stringify({version:7,id:'native-speedtest',ok:true,result:x})+'\n').join(''));
    for (let i = 0; i < bytes.length && !this.destroyed; i += 3) {
      const chunk = bytes.subarray(i, i + 3);
      this.listeners.dataReceive(chunk.buffer.slice(chunk.byteOffset, chunk.byteOffset + chunk.length));
    }
    if (mode === 'cancel') return await new Promise(resolve => { this.done = resolve; canceledReceive(); });
    return 200;
  }
}
const sandbox = { exports: {}, Uint8Array, JSON, Error, require(name) {
  if (name === '@kit.NetworkKit') return { http: { createHttp() { const request = new FakeRequest(); requests.push(request); return request; }, RequestMethod: { POST: 'POST' } } };
  if (name === '@kit.ArkTS') return { util: { TextDecoder: class { decoder = new TextDecoder(); decodeWithStream(bytes, opts) { return this.decoder.decode(bytes, opts); } } } };
  if (name === '../platform/Messages') return { tr: x => x };
  if (name === '../platform/Client') return {};
  throw Error(name);
} };
vm.runInNewContext(code, sandbox);
(async () => {
  const { SettingsTest } = sandbox.exports;
  let ensured = 0;
  const client = { async ensure() { ensured++; }, async call(method) { assert.equal(method, 'native-transport'); return { version:7,url:'http://127.0.0.1:1234/ipc',token:'a'.repeat(64) }; } };
  const results = [];
  await new SettingsTest().run(client, 'all', status => results.push(status));
  assert.equal(ensured, 1); assert.equal(results.at(-1).results[0].error, '分片中文');
  assert.equal(target, 'http://127.0.0.1:1234/ipc');
  assert.equal(options.header.Authorization, 'Bearer '+'a'.repeat(64));
  assert.equal(options.usingProxy, false); assert.equal(options.maxRedirects, 0);
  assert.equal(JSON.parse(options.extraData).params.link, 'all'); assert.equal(requests.at(-1).destroyed, true);
  mode = 'partial'; await assert.rejects(new SettingsTest().run(client, 'ipv6', () => {}), /测试连接已中断/);
  mode = 'cancel'; const canceled = new SettingsTest(); canceledReceive = () => canceled.cancel();
  await canceled.run(client, 'ipv6', () => {}); assert.equal(requests.at(-1).destroyed, true);
  const late = new SettingsTest(); let failStartup;
  const pending = late.run({ async ensure() { return await new Promise((_, reject) => { failStartup = reject; }); } }, 'all', () => {});
  late.cancel(); failStartup(Error('late permission failure')); await pending;
  const count = requests.length;
  await assert.rejects(new SettingsTest().run({ async ensure() {}, async call() { return { url: 'https://external.example/settings' }; } }, 'all', () => {}), /无效的设置页面地址/);
  assert.equal(requests.length, count);
  console.log('Native settings stream checks passed.');
})().catch(error => { console.error(error); process.exitCode = 1; });
