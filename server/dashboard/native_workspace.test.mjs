import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./native_workspace.js', import.meta.url), 'utf8');
function harness({ native = true, pathname = '/', ready = true } = {}) {
  const messages = [], dispatched = [], pages = [], observers = [];
  const overlays = [{ hidden: true }];
  const context = {
    __fleetNativeVersion: native ? 1 : 0, location: { pathname },
    webkit: { messageHandlers: { fleet: { postMessage: message => messages.push(message) } } },
    document: { documentElement: { dataset: {} }, head: { append() {} }, createElement: () => ({}),
      querySelectorAll: () => overlays, getElementById: () => ({ id: 'user-btn' }) },
    FleetWorkspace: { snapshot: () => ({ ready }), publish() {} },
    FleetNative: { dispatch: message => dispatched.push(message) },
    async openUnifiedSettings(page) { pages.push(page); overlays[0].hidden = false; },
    MutationObserver: class { constructor(callback) { observers.push(callback); } observe() {} },
  };
  context.top = context;
  vm.createContext(context);
  vm.runInContext(source, context);
  return { context, messages, dispatched, pages, overlays, observers };
}

test('native settings use the same H5 dialog and track overlay dismissal', async () => {
  const { context, messages, pages, overlays, observers } = harness();
  await context.FleetNativeUI.dispatch({ id: 1, command: 'settings-page' });
  assert.deepEqual(pages, ['account']);
  assert.equal(messages.find(message => message.id === 1).opened, true);
  assert.equal(messages.at(-2).visible, true);
  assert.equal(context.document.documentElement.dataset.nativeOverlay, 'true');
  for (const page of ['appearance', 'automation', 'sessions', 'add-device']) {
    await context.FleetNativeUI.dispatch({ id: 2, command: 'settings-page', page });
    assert.equal(pages.at(-1), page);
  }
  overlays[0].hidden = true;
  observers[0]();
  assert.equal(context.document.documentElement.dataset.nativeOverlay, 'false');
});

test('native adapter rejects forced ttyd and temporarily removed Claude', async () => {
  const { context, dispatched, messages } = harness();
  await context.FleetNativeUI.dispatch({ id: 1, command: 'open', terminal: true });
  assert.equal(dispatched.length, 0);
  assert.match(messages.find(message => message.id === 1).error, /不支持主动切换/);
  await context.FleetNativeUI.dispatch({ id: 2, command: 'open', assistant: 'claude' });
  assert.equal(dispatched.length, 0);
  assert.match(messages.find(message => message.id === 2).error, /暂不支持 Claude/);
});

test('native settings require authentication and a known settings page', async () => {
  const locked = harness({ ready: false });
  await locked.context.FleetNativeUI.dispatch({ id: 1, command: 'settings-page' });
  assert.equal(locked.pages.length, 0);
  assert.match(locked.messages.find(message => message.id === 1).error, /请先登录/);
  const active = harness();
  await active.context.FleetNativeUI.dispatch({ id: 2, command: 'settings-page', page: 'admin' });
  assert.equal(active.pages.length, 0);
  assert.match(active.messages.find(message => message.id === 2).error, /不支持/);
});

test('ordinary Web and auxiliary pages do not install the native adapter', () => {
  assert.equal(harness({ native: false }).context.FleetNativeUI, undefined);
  assert.equal(harness({ pathname: '/account' }).context.FleetNativeUI, undefined);
  assert.equal(harness({ pathname: '/auth' }).context.FleetNativeUI, undefined);
});
