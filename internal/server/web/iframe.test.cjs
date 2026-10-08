const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const test = require('node:test');

test('confirmation works in an iframe without allow-modals or allow-forms', {
  skip: !process.env.PLAYWRIGHT_MODULE,
  timeout: 60000
}, async t => {
  const {chromium} = require(process.env.PLAYWRIGHT_MODULE);
  const html = fs.readFileSync(path.join(__dirname, 'index.html'));
  const offline = {
    id: 'old-device', name: 'macos.shared', overlayAddress: '10.253.203.2/32',
    enabled: true, connectionState: 'disconnected'
  };
  let devices, revision, deletes, updates, rejectDelete, failDelete;
  const settings = {overlayCIDR: '10.253.203.0/24', listenPort: 54789, lanCIDRs: ['192.168.71.0/24']};
  function reset() {
    devices = [offline, {...offline, id: 'active-device', overlayAddress: '10.253.203.3/32', connectionState: 'connected'}];
    revision = 1;
    deletes = updates = 0;
    rejectDelete = failDelete = false;
    settings.overlayCIDR = '10.253.203.0/24';
  }
  const application = http.createServer((request, response) => {
    const url = new URL(request.url, 'http://localhost');
    const json = (status, body) => {
      response.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'});
      response.end(JSON.stringify(body));
    };
    if (url.pathname.endsWith('/snapshot')) {
      const send = () => {
        const changed = url.searchParams.get('after') !== String(revision);
        json(200, {changed, cursor: String(revision), ...(changed && {snapshot: {
          settings, devices, network: {fresh: true, network: {active: true, interface: 'fncpn0'}}
        }})});
      };
      if (url.searchParams.get('after') === String(revision)) setTimeout(send, 100).unref();
      else send();
    } else if (request.method === 'DELETE') {
      deletes++;
      setTimeout(() => {
        if (rejectDelete || failDelete) {
          if (rejectDelete) devices[0] = {...devices[0], connectionState: 'connected'};
          revision++;
          json(rejectDelete ? 412 : 503, {error: {
            code: rejectDelete ? 'FAILED_PRECONDITION' : 'UNAVAILABLE',
            message: rejectDelete ? 'device is online' : 'network apply failed',
            requestId: 'iframe-delete'
          }});
        } else {
          devices = devices.filter(device => device.id !== url.pathname.split('/').pop());
          revision++;
          response.writeHead(204);
          response.end();
        }
      }, 200);
    } else if (request.method === 'PUT') {
      updates++;
      let body = '';
      request.on('data', chunk => { body += chunk; });
      request.on('end', () => {
        Object.assign(settings, JSON.parse(body));
        revision++;
        json(200, {settings});
      });
    } else {
      response.writeHead(200, {
        'Content-Type': 'text/html; charset=utf-8',
        'Content-Security-Policy': "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'"
      });
      response.end(html);
    }
  });
  let appURL;
  const parent = http.createServer((request, response) => {
    const forms = request.url === '/forms' ? ' allow-forms' : '';
    response.writeHead(200, {'Content-Type': 'text/html; charset=utf-8'});
    response.end(`<html><body style="margin:0"><iframe id="app" title="FnCPN"
      sandbox="allow-scripts allow-same-origin${forms}" src="${appURL}/app/fncpn/"
      style="display:block;width:100%;height:100vh;border:0"></iframe></body></html>`);
  });
  let browser;
  const messages = [];
  try {
    await new Promise(resolve => application.listen(0, '127.0.0.1', resolve));
    appURL = `http://127.0.0.1:${application.address().port}`;
    await new Promise(resolve => parent.listen(0, '127.0.0.1', resolve));
    browser = await chromium.launch({channel: process.env.PLAYWRIGHT_CHANNEL || 'chrome', headless: true});
    for (const locale of ['zh-CN', 'en-US']) for (const width of [1280, 375, 320]) {
      reset();
      const page = await browser.newPage({
        viewport: {width, height: 800}, locale, colorScheme: width === 320 ? 'dark' : 'light'
      });
      page.on('console', message => { messages.push(message.text()); });
      page.on('pageerror', error => { messages.push(error.message); });
      page.on('dialog', dialog => { messages.push(`unexpected native dialog: ${dialog.type()}`); void dialog.dismiss(); });
      await page.goto(`http://127.0.0.1:${parent.address().port}`);
      const frame = page.frameLocator('#app');
      const remove = frame.locator('.delete-device').first();
      await remove.waitFor();
      assert.equal(await frame.locator('html').getAttribute('lang'), locale === 'zh-CN' ? 'zh-CN' : 'en');
      assert.equal(await remove.isEnabled(), true);
      assert.equal(await frame.locator('.delete-device').nth(1).isDisabled(), true);
      await remove.click();
      await frame.locator('#confirmation').waitFor({state: 'visible', timeout: 2000});
      assert.equal(deletes, 0);
      assert.match(await frame.locator('#confirmationMessage').innerText(), /macos\.shared.*10\.253\.203\.2/);
      assert.equal(await frame.locator('#confirmationCancel').evaluate(element => element === document.activeElement), true);
      const bounds = await frame.locator('#confirmation').evaluate(element => {
        const box = element.getBoundingClientRect();
        return {left: box.left, right: box.right, top: box.top, bottom: box.bottom, width: innerWidth, height: innerHeight};
      });
      assert.ok(bounds.left >= 0 && bounds.right <= bounds.width && bounds.top >= 0 && bounds.bottom <= bounds.height, JSON.stringify(bounds));
      if (process.env.FNCPN_SCREENSHOT_DIR) {
        await page.screenshot({path: path.join(process.env.FNCPN_SCREENSHOT_DIR, `iframe-confirm-${locale}-${width}.png`)});
      }
      await frame.locator('#confirmationCancel').click();
      await frame.locator('#confirmation').waitFor({state: 'hidden'});
      assert.equal(deletes, 0);
      await remove.click();
      await frame.locator('#confirmationCancel').press('Escape');
      await frame.locator('#confirmation').waitFor({state: 'hidden'});
      assert.equal(deletes, 0);
      await remove.click();
      await frame.locator('#confirmationAccept').click();
      await frame.locator('#count').filter({hasText: /^1$/}).waitFor();
      assert.equal(deletes, 1);
      assert.match(await frame.locator('#devices').innerText(), /10\.253\.203\.3/);

      // The existing network form requires form permission; modals stay forbidden.
      await page.goto(`http://127.0.0.1:${parent.address().port}/forms`);
      await frame.locator('#networkState').filter({hasText: locale === 'zh-CN' ? '已读取' : 'Loaded'}).waitFor();
      await frame.locator('#overlayInput').fill('10.200.0.0/24');
      await frame.locator('#networkForm button').click();
      await frame.locator('#confirmation').waitFor({state: 'visible'});
      assert.equal(updates, 0);
      await frame.locator('#confirmationCancel').click();
      assert.equal(updates, 0);
      await frame.locator('#networkForm button').click();
      await frame.locator('#confirmationAccept').click();
      await frame.locator('#networkState').filter({hasText: locale === 'zh-CN' ? '已应用' : 'Applied'}).waitFor();
      assert.equal(updates, 1);
      await page.close();
      t.diagnostic(`PASS ${locale} ${width}px: iframe confirmation, cancel, Escape, delete, overlay confirmation`);
    }
    reset();
    const changingPage = await browser.newPage({locale: 'zh-CN'});
    await changingPage.goto(`http://127.0.0.1:${parent.address().port}`);
    const changingFrame = changingPage.frameLocator('#app');
    await changingFrame.locator('.delete-device').first().click();
    await changingFrame.locator('#confirmation').waitFor({state: 'visible'});
    devices[0] = {...devices[0], connectionState: 'connected'};
    revision++;
    await changingFrame.locator('.connection').first().filter({hasText: '在线'}).waitFor();
    await changingFrame.locator('#confirmationAccept').click();
    await changingFrame.locator('#deviceError').filter({hasText: '已取消删除'}).waitFor();
    assert.equal(deletes, 0);
    await changingPage.close();
    t.diagnostic('PASS: device becoming online while confirmation is open cancels deletion');

    for (const failure of ['reconnected', 'apply']) {
      reset();
      rejectDelete = failure === 'reconnected';
      failDelete = failure === 'apply';
      const page = await browser.newPage();
      await page.goto(`http://127.0.0.1:${parent.address().port}`);
      const frame = page.frameLocator('#app');
      await frame.locator('.delete-device').first().click();
      await frame.locator('#confirmationAccept').click();
      await frame.locator('#deviceError').filter({hasText: 'iframe-delete'}).waitFor();
      assert.equal(await frame.locator('#devices tr').count(), 2);
      assert.equal(await frame.locator('.delete-device').first().isDisabled(), rejectDelete);
      await page.close();
    }
    assert.deepEqual(messages.filter(message => /unexpected native dialog|ignored call to .confirm|not defined|TypeError/i.test(message)), []);
  } finally {
    for (const message of messages.filter(message => !message.startsWith("Loading the image 'data:,'"))) t.diagnostic(message);
    if (browser) await browser.close();
    for (const server of [application, parent]) {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
  }
});
