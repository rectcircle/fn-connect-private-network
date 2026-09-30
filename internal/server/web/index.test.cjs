const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
  .match(/<script>([\s\S]*?)<\/script>/)[1]
  .replace('refresh().then(watchChanges);', '');

const sample = {
  settings: {overlayCIDR: '10.253.203.0/24', listenPort: 54789, lanCIDRs: ['192.168.1.0/24']},
  network: {fresh: true, network: {active: true}},
  devices: []
};

function page(fetch) {
  const nodes = new Map();
  const get = id => {
    if (!nodes.has(id)) {
      nodes.set(id, {
        value: '', textContent: '', dataset: {}, disabled: false, listeners: {},
        replaceChildren() {},
        addEventListener(name, handler) { this.listeners[name] = handler; },
        querySelectorAll() { return ['overlayInput', 'portInput', 'lanInput', 'submit'].map(get); }
      });
    }
    return nodes.get(id);
  };
  const context = vm.createContext({
    location: {pathname: '/app/fncpn'},
    document: {
      getElementById: get, hidden: true, addEventListener() {},
      createElement() { return {append() {}}; }
    },
    URLSearchParams, AbortController, setTimeout, confirm: () => true, fetch
  });
  vm.runInContext(script, context);
  function render(snapshot = sample) {
    context.sample = snapshot;
    vm.runInContext('renderSnapshot(sample)', context);
  }
  render();
  return {get, render, submit: () => get('networkForm').listeners.submit({preventDefault() {}})};
}

test('snapshot refresh preserves unsaved network fields', () => {
  const ui = page();
  ui.get('overlayInput').value = '172.20.0.0/24';
  ui.get('portInput').value = '51820';
  ui.get('lanInput').value = '192.168.1.0/24, 192.168.2.0/24';
  ui.render();
  assert.equal(ui.get('overlayInput').value, '172.20.0.0/24');
  assert.equal(ui.get('portInput').value, '51820');
  assert.equal(ui.get('lanInput').value, '192.168.1.0/24, 192.168.2.0/24');
});

test('refresh preserves applying and failed states', async () => {
  let reject;
  const ui = page(() => new Promise((_, failure) => { reject = failure; }));
  ui.get('lanInput').value = '192.168.2.0/24';
  const pending = ui.submit();
  const applying = ui.get('networkState').textContent;
  assert.equal(ui.get('lanInput').disabled, true);
  ui.render();
  assert.equal(ui.get('networkState').textContent, applying);
  reject(new Error('apply failed'));
  await pending;
  const failed = ui.get('networkState').textContent;
  assert.notEqual(failed, applying);
  ui.render();
  assert.equal(ui.get('networkState').textContent, failed);
  assert.equal(ui.get('networkError').textContent, 'apply failed');
  assert.equal(ui.get('lanInput').value, '192.168.2.0/24');
  assert.equal(ui.get('lanInput').disabled, false);
});

test('successful save fills committed settings, not a stale watch response', async () => {
  const settings = {...sample.settings, lanCIDRs: ['192.168.2.0/24']};
  const ui = page(async (_, options) => ({
    ok: true,
    json: async () => options?.method === 'PUT'
      ? {settings}
      : {changed: true, cursor: 'old', snapshot: sample}
  }));
  ui.get('lanInput').value = ' 192.168.2.0/24 ';
  await ui.submit();
  assert.equal(ui.get('lanInput').value, '192.168.2.0/24');
  assert.equal(ui.get('networkError').textContent, '');
  assert.equal(ui.get('submit').disabled, false);
});
