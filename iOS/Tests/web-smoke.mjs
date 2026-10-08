import assert from 'node:assert/strict';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const browser = await chromium.launch({ headless: true, channel: 'chrome' });
const gateway = 'http://localhost:18765';
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
  const errors = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.goto(gateway);
  await page.locator('.ses[data-sid="codex-session"]').waitFor();
  assert.equal(await page.locator('#rail').isVisible(), true);
  assert.equal(await page.locator('#sescol').isVisible(), true);
  assert.equal(await page.evaluate(() => typeof FleetNative), 'undefined');
  await page.locator('.ses[data-sid="codex-session"]').click();
  await page.getByText('Fixture reply', { exact: true }).waitFor();
  await page.screenshot({ path: '/private/tmp/fleet-web-desktop.png' });
  await page.setViewportSize({ width: 393, height: 852 });
  await page.getByText('Fixture reply', { exact: true }).waitFor();
  assert.equal(await page.locator('#chat-pane').isVisible(), true);
  await page.close();

  const native = await browser.newPage({ viewport: { width: 393, height: 700 } });
  native.on('pageerror', (error) => errors.push(error.message));
  await native.addInitScript(() => {
    window.__fleetNativeVersion = 1;
    window.messages = [];
    window.webkit = { messageHandlers: { fleet: { postMessage: (message) => window.messages.push(message) } } };
  });
  const offset = (await (await fetch(gateway + '/__requests')).json()).length;
  await native.goto(gateway);
  await native.waitForFunction(() => window.messages.some((message) => message.type === 'snapshot' && message.payload.sessions.length));
  assert.equal(await native.locator('#rail').isVisible(), false);
  assert.equal(await native.locator('#sescol').isVisible(), false);
  await native.evaluate(() => FleetNative.dispatch({ id: 1, command: 'open', sessionId: 'codex-session', macId: 'm1', assistant: 'codex' }));
  await native.getByText('Fixture reply', { exact: true }).waitFor();
  await native.evaluate(() => FleetNative.dispatch({ id: 2, command: 'assistant', assistant: 'claude' }));
  await native.waitForFunction(() => FleetWorkspace.snapshot().sessions.some((session) => session.assistant === 'claude'));
  await native.evaluate(() => FleetNative.dispatch({ id: 3, command: 'open', sessionId: 'claude-session', macId: 'm1', assistant: 'claude' }));
  await native.frameLocator('.term-frame.show').getByText('Fixture ttyd connected').waitFor();
  await native.locator('#cmd-input').fill('hello');
  await native.locator('#send-btn').click();
  assert.equal(await native.frameLocator('.term-frame.show').locator('textarea').inputValue(), 'hello\n');
  await native.setViewportSize({ width: 620, height: 700 });
  await native.frameLocator('.term-frame.show').getByText('Fixture ttyd connected').waitFor();
  await native.evaluate(() => FleetNative.dispatch({ id: 4, command: 'open', sessionId: 'claude-session', macId: 'm1', assistant: 'claude' }));
  await native.waitForFunction(() => window.messages.some((message) => message.id === 4 && message.type === 'result'));
  await native.waitForTimeout(3200);
  const requests = (await (await fetch(gateway + '/__requests')).json()).slice(offset);
  assert.equal(requests.filter((request) => request.path === '/m1/api/open').length, 1, 'returning to ttyd reuses the existing iframe');
  assert.equal(requests.some((request) => request.query.includes('assistant=claude') && request.query.includes('sessionId=codex-session')), false,
    'cached Codex polling must retain its assistant identity');
  await native.screenshot({ path: '/private/tmp/fleet-native-web.png' });
  assert.deepEqual(errors, []);
  process.stdout.write('Web desktop/mobile + native bridge + ttyd input/cache smoke passed\n');
} finally { await browser.close(); }
