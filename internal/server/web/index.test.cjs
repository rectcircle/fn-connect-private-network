const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const rawScript = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
  .match(/<script>([\s\S]*?)<\/script>/)[1];
// The page auto-boots after the layout is available; tests drive it manually.
const script = rawScript.replace('boot();', '');

const sample = {
  settings: {overlayCIDR: '10.253.203.0/24', listenPort: 54789, lanCIDRs: ['192.168.1.0/24']},
  network: {fresh: true, network: {active: true}},
  devices: []
};

function page(fetch) {
  const nodes = new Map();
  const node = () => ({
    value: '', textContent: '', dataset: {}, disabled: false, listeners: {}, children: [],
    attributes: {}, open: false, returnValue: '', isConnected: true,
    focus() { document.activeElement = this; },
    showModal() { this.open = true; },
    close(value) {
      if (!this.open) return;
      this.open = false;
      if (value !== undefined) this.returnValue = value;
      this.listeners.close?.();
    },
    append(...items) { this.children.push(...items); },
    replaceChildren(...items) { this.children = items; },
    setAttribute(name, value) { this.attributes[name] = value; },
    addEventListener(name, handler) { this.listeners[name] = handler; },
    querySelectorAll() { return ['overlayInput', 'portInput', 'lanInput', 'submit'].map(get); }
  });
  const get = id => {
    if (!nodes.has(id)) {
      nodes.set(id, node());
    }
    return nodes.get(id);
  };
  const document = {
    getElementById: get, hidden: true, activeElement: null, addEventListener() {},
    createElement: node
  };
  const context = vm.createContext({
    location: {pathname: '/app/fncpn'},
    document, URLSearchParams, AbortController, setTimeout, fetch,
    confirm: () => { throw new Error('native confirm must not be used'); }
  });
  vm.runInContext(script, context);
  function render(snapshot = sample) {
    context.sample = snapshot;
    vm.runInContext('renderSnapshot(sample)', context);
  }
  render();
  return {
    get, render,
    evaluate: code => vm.runInContext(code, context),
    remove: index => get('devices').children[index].children[6].children[0],
    reply: async accepted => get(accepted ? 'confirmationAccept' : 'confirmationCancel').listeners.click(),
    submit: () => get('networkForm').listeners.submit({preventDefault() {}})
  };
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

test('HTTP error codes and root causes reach the management page', async () => {
  const ui = page(async () => ({
    ok: false, status: 403,
    json: async () => ({error: {
      code: 'PERMISSION_DENIED', message: 'request rejected',
      httpStatus: 403, detail: 'forbidden origin',
      operation: 'relay.handshake', requestId: 'request-123'
    }})
  }));
  await ui.submit();
  const text = ui.get('networkError').textContent;
  for (const part of ['PERMISSION_DENIED', '403', 'forbidden origin', 'relay.handshake', 'request-123']) {
    assert.ok(text.includes(part), `${part} missing from ${text}`);
  }
});

test('background network status errors are visible without replacing form errors', () => {
  const ui = page();
  ui.get('networkError').textContent = 'unsaved form error';
  ui.render({...sample, network: {...sample.network, lastError: {
    code: 'UNAVAILABLE', message: 'network apply failed',
    detail: 'nftables permission denied', operation: 'ipc.apply'
  }}});
  assert.match(ui.get('statusError').textContent, /nftables permission denied/);
  assert.equal(ui.get('networkError').textContent, 'unsaved form error');
  ui.render();
  assert.equal(ui.get('statusError').textContent, '');
});

const offline = {id: 'old-id', name: 'macos.shared', overlayAddress: '10.253.203.2/32', enabled: true, connectionState: 'disconnected'};
const online = {...offline, id: 'active-id', overlayAddress: '10.253.203.3/32', connectionState: 'connected'};
const populated = {...sample, devices: [offline, online, {...offline, id: 'unknown-id', connectionState: 'unknown'}]};
const snapshotResponse = snapshot => ({ok: true, status: 200, json: async () => ({cursor: 'next', changed: true, snapshot})});

test('only offline devices have an enabled delete button; stale snapshots disable all', () => {
  const ui = page();
  ui.render(populated);
  assert.equal(ui.remove(0).disabled, false);
  assert.equal(ui.remove(1).disabled, true);
  assert.equal(ui.remove(2).disabled, true);
  assert.match(ui.remove(0).attributes['aria-label'], /10\.253\.203\.2/);
  ui.render({...populated, network: {fresh: false}});
  for (const index of [0, 1, 2]) assert.equal(ui.remove(index).disabled, true);
});

test('delete confirmation cancellation and online guard send no request', async () => {
  let requests = 0;
  const ui = page(() => { requests++; });
  ui.render(populated);
  const pending = ui.remove(0).listeners.click();
  assert.equal(ui.get('confirmation').open, true);
  assert.equal(requests, 0);
  await ui.reply(false);
  await pending;
  await ui.remove(1).listeners.click();
  await ui.remove(2).listeners.click();
  assert.equal(requests, 0);
});

test('deletion uses ID, blocks duplicate submission, and preserves unsaved network fields', async () => {
  let release;
  let deletes = 0;
  const ui = page(async (url, options) => {
    if (options?.method === 'DELETE') {
      deletes++;
      assert.equal(url, '/app/fncpn/api/v1/admin/devices/old-id');
      return new Promise(resolve => { release = resolve; });
    }
    return snapshotResponse({...sample, devices: [online]});
  });
  ui.render({...sample, devices: [offline, online]});
  ui.get('lanInput').value = '192.168.2.0/24';
  const pending = ui.remove(0).listeners.click();
  assert.match(ui.get('confirmationMessage').textContent, /macos\.shared.*10\.253\.203\.2/);
  assert.equal(deletes, 0);
  await ui.reply(true);
  assert.equal(ui.remove(0).disabled, true);
  assert.equal(ui.remove(0).attributes['aria-busy'], 'true');
  assert.equal(ui.get('refresh').disabled, true);
  await ui.remove(0).listeners.click();
  ui.render({...sample, devices: [offline, online]});
  assert.equal(ui.remove(0).disabled, true);
  release({ok: true, status: 204});
  await pending;
  assert.equal(deletes, 1);
  assert.equal(ui.get('devices').children.length, 1);
  assert.match(ui.remove(0).attributes['aria-label'], /10\.253\.203\.3/);
  assert.equal(ui.get('lanInput').value, '192.168.2.0/24');
  assert.equal(ui.get('refresh').disabled, false);
});

test('backend refuses a reconnected device and error survives background snapshots', async () => {
  const reconnected = {...offline, connectionState: 'connected'};
  const ui = page(async (_, options) => options?.method === 'DELETE'
    ? {ok: false, status: 412, json: async () => ({error: {
      code: 'FAILED_PRECONDITION', message: 'device is online', requestId: 'delete-request'
    }})}
    : snapshotResponse({...sample, devices: [reconnected]}));
  ui.render({...sample, devices: [offline]});
  const pending = ui.remove(0).listeners.click();
  await ui.reply(true);
  await pending;
  assert.match(ui.get('deviceError').textContent, /FAILED_PRECONDITION.*device is online.*delete-request/);
  assert.equal(ui.remove(0).disabled, true);
  ui.render({...sample, devices: [reconnected]});
  assert.match(ui.get('deviceError').textContent, /delete-request/);
});

test('a snapshot started before deletion cannot restore the deleted row', async () => {
  let releaseOld;
  let snapshots = 0;
  const ui = page(async (_, options) => {
    if (options?.method === 'DELETE') return {ok: true, status: 204};
    if (++snapshots === 1) return new Promise(resolve => { releaseOld = resolve; });
    return snapshotResponse(sample);
  });
  ui.render({...sample, devices: [offline]});
  const oldRead = ui.evaluate('refresh()');
  const pending = ui.remove(0).listeners.click();
  await ui.reply(true);
  await pending;
  releaseOld(snapshotResponse({...sample, devices: [offline]}));
  await oldRead;
  assert.equal(ui.get('devices').children.length, 0);
});

test('successful deletion followed by refresh failure does not offer stale actions', async () => {
  const ui = page(async (_, options) => {
    if (options?.method === 'DELETE') return {ok: true, status: 204};
    throw new Error('snapshot unavailable');
  });
  ui.render({...sample, devices: [offline, {...online, connectionState: 'disconnected'}]});
  const pending = ui.remove(0).listeners.click();
  await ui.reply(true);
  await pending;
  assert.equal(ui.get('devices').children.length, 1);
  assert.equal(ui.remove(0).disabled, true);
  assert.match(ui.get('deviceError').textContent, /snapshot unavailable/);
});

test('confirmation observes state changes while open without issuing a delete', async () => {
  for (const state of ['connected', 'unknown', 'missing', 'unavailable']) {
    let requests = 0;
    const ui = page(() => { requests++; });
    ui.render({...sample, devices: [offline]});
    const pending = ui.remove(0).listeners.click();
    ui.render({
      ...sample,
      devices: state === 'missing' ? [] : [{...offline, connectionState: state === 'unavailable' ? 'disconnected' : state}],
      network: state === 'unavailable' ? {fresh: false} : sample.network
    });
    await ui.reply(true);
    await pending;
    assert.equal(requests, 0);
    assert.match(ui.get('deviceError').textContent, /已取消删除/);
  }
});

test('only one confirmation can be pending and closing does not reuse acceptance', async () => {
  const ui = page();
  const first = ui.evaluate('requestConfirmation("first", "message", "accept")');
  assert.equal(await ui.evaluate('requestConfirmation("second", "message", "accept")'), false);
  assert.equal(ui.get('confirmationTitle').textContent, 'first');
  await ui.reply(true);
  assert.equal(await first, true);
  const next = ui.evaluate('requestConfirmation("next", "message", "accept")');
  ui.get('confirmation').close();
  assert.equal(await next, false);
});

test('dialog initialization failure is visible and never sends a delete', async () => {
  let requests = 0;
  const ui = page(() => { requests++; });
  ui.render({...sample, devices: [offline]});
  ui.get('confirmation').showModal = () => { throw new Error('dialog unavailable'); };
  await ui.remove(0).listeners.click();
  assert.match(ui.get('deviceError').textContent, /dialog unavailable/);
  assert.equal(requests, 0);
});

test('Overlay change uses the same in-page confirmation before sending PUT', async () => {
  let updates = 0;
  const settings = {...sample.settings, overlayCIDR: '10.200.0.0/24'};
  const ui = page(async (_, options) => {
    if (options?.method === 'PUT') {
      updates++;
      return {ok: true, status: 200, json: async () => ({settings})};
    }
    return snapshotResponse({...sample, settings});
  });
  ui.get('overlayInput').value = settings.overlayCIDR;
  const canceled = ui.submit();
  assert.equal(ui.get('confirmation').open, true);
  assert.equal(updates, 0);
  await ui.reply(false);
  await canceled;
  assert.equal(updates, 0);
  const accepted = ui.submit();
  await ui.reply(true);
  await accepted;
  assert.equal(updates, 1);
  assert.equal(ui.get('overlayInput').value, settings.overlayCIDR);
});

test('non-admin caller sees a friendly notice instead of a snapshot permission error', async () => {
  let bootstrapCalls = 0;
  const ui = page(async url => {
    if (url === '/app/fncpn/api/v1/bootstrap') {
      bootstrapCalls++;
      return {ok: true, status: 200, json: async () => ({administrator: false})};
    }
    throw new Error(`snapshot must not be fetched for non-admin: ${url}`);
  });
  await ui.evaluate('boot()');
  assert.equal(bootstrapCalls, 1);
  assert.equal(ui.get('mainPanel').hidden, true);
  assert.equal(ui.get('accessDenied').hidden, false);
});
