import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./sidebar_layout.js', import.meta.url), 'utf8');
function setup({saved = {}, desktop = true, blocked = false} = {}) {
  const events = {}, elements = {};
  for (const id of ['app', 'rail-collapse', 'sessions-collapse', 'sessions-launcher', 'sessions-backdrop', 'sescol']) {
    elements[id] = {dataset: {}, attributes: {}, hidden: false, inert: false,
      setAttribute(name, value) { this.attributes[name] = value; },
      focus() { this.focused = true; }, contains(target) { return target === this; }};
  }
  const media = {matches: desktop, addEventListener(_name, listener) { this.change = listener; }};
  const store = new Map([['fleet-sidebar-layout-v1', JSON.stringify(saved)]]);
  const context = {document: {querySelector: selector => elements[selector.slice(1)],
    addEventListener: (name, listener) => { events[name] = listener; }},
    matchMedia: () => media, localStorage: {
      getItem(key) { if (blocked) throw Error('blocked'); return store.get(key); },
      setItem(key, value) { if (blocked) throw Error('blocked'); store.set(key, value); }}};
  vm.createContext(context); vm.runInContext(source, context); context.FleetSidebarLayout.init();
  return {elements, media, store, events, api: context.FleetSidebarLayout};
}

test('设备和会话列表独立收起并分别保存，刷新不恢复临时浮层', () => {
  const {elements: e, store} = setup();
  e['rail-collapse'].onclick();
  assert.equal(e.app.dataset.railCollapsed, 'true');
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  assert.equal(e['sessions-collapse'].attributes['aria-pressed'], 'true');
  e['sessions-collapse'].onclick();
  assert.equal(e['sessions-collapse'].attributes['aria-pressed'], 'false');
  assert.equal(e['sessions-collapse'].dataset.pinned, 'false');
  assert.equal(e['sescol'].inert, true);
  assert.equal(e['sessions-launcher'].hidden, false);
  e['sessions-launcher'].onclick();
  assert.equal(e.app.dataset.sessionsOpen, 'true');
  assert.equal(e['sescol'].inert, false);
  const reloaded = setup({saved: JSON.parse(store.get('fleet-sidebar-layout-v1'))});
  assert.equal(reloaded.elements.app.dataset.railCollapsed, 'true');
  assert.equal(reloaded.elements.app.dataset.sessionsCollapsed, 'true');
  assert.equal(reloaded.elements.app.dataset.sessionsOpen, 'false');
});

test('呼出后按 Escape 或外部点击收回，点击列表内部不收回', () => {
  const {elements: e, events} = setup({saved: {sessionsCollapsed: true}});
  e['sessions-launcher'].onclick();
  events.pointerdown({target: e.sescol});
  assert.equal(e.app.dataset.sessionsOpen, 'true');
  let prevented = false;
  events.keydown({key: 'Escape', preventDefault() { prevented = true; }});
  assert.equal(e.app.dataset.sessionsOpen, 'false');
  assert.equal(e['sessions-launcher'].focused, true);
  assert.equal(prevented, true);
  e['sessions-launcher'].onclick();
  events.pointerdown({target: {}});
  assert.equal(e.app.dataset.sessionsOpen, 'false');
});

test('选择会话关闭浮层，固定列表恢复宽栏和键盘访问', () => {
  const {elements: e, api} = setup({saved: {sessionsCollapsed: true}});
  e['sessions-launcher'].onclick(); api.close();
  assert.equal(e.app.dataset.sessionsOpen, 'false');
  assert.equal(e.sescol.inert, true);
  e['sessions-launcher'].onclick(); e['sessions-collapse'].onclick();
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  assert.equal(e['sessions-collapse'].attributes['aria-pressed'], 'true');
  assert.equal(e['sessions-collapse'].dataset.pinned, 'true');
  assert.equal(e.sescol.inert, false);
  assert.equal(e['sessions-launcher'].hidden, true);
});

test('切文件模式关闭浮层和入口，返回会话仍保留收起状态', () => {
  const {elements: e, api} = setup({saved: {sessionsCollapsed: true}});
  e['sessions-launcher'].onclick(); e.app.dataset.mode = 'files'; api.sync();
  assert.equal(e['sessions-launcher'].hidden, true);
  assert.equal(e['sessions-backdrop'].hidden, true);
  e.app.dataset.mode = 'sessions'; api.sync();
  assert.equal(e['sessions-launcher'].hidden, false);
});

test('移动断点不受桌面收起偏好影响，返回桌面也不会自动弹出列表', () => {
  const {elements: e, media} = setup({saved: {railCollapsed: true, sessionsCollapsed: true}});
  e['sessions-launcher'].onclick(); media.matches = false; media.change();
  assert.equal(e.sescol.inert, false);
  assert.equal(e['sessions-launcher'].hidden, true);
  assert.equal(e.app.dataset.sessionsOpen, 'false');
  media.matches = true; media.change();
  assert.equal(e.sescol.inert, true);
  assert.equal(e['sessions-launcher'].hidden, false);
});

test('浏览器禁用存储时收起与呼出仍可使用', () => {
  const {elements: e} = setup({blocked: true});
  e['rail-collapse'].onclick(); e['sessions-collapse'].onclick(); e['sessions-launcher'].onclick();
  assert.equal(e.app.dataset.railCollapsed, 'true');
  assert.equal(e.app.dataset.sessionsOpen, 'true');
});

test('终端和自绘聊天选择会话均关闭浮层，切模式同步布局', async () => {
  const app = await readFile(new URL('./app.js', import.meta.url), 'utf8');
  assert.match(app, /function selectSes\([^]*?FleetSidebarLayout\?\.close\(\)/);
  assert.match(app, /async function openChatSession\([^]*?FleetSidebarLayout\?\.close\(\)/);
  assert.match(app, /function setMode\([^]*?FleetSidebarLayout\?\.sync\(\)/);
});

test('会话列表脱离栅格成为浮层时，主窗口仍固定在第三列而不落入零宽列', async () => {
  const css = await readFile(new URL('./style.css', import.meta.url), 'utf8');
  assert.match(css, /#win\s*\{\s*grid-column:\s*3;/);
  assert.match(css, /#app\[data-mode="files"\] #win\s*\{\s*grid-column:\s*2;/);
  assert.match(css, /#app\[data-sessions-collapsed="true"\]\[data-sessions-open="true"\]\[data-mode="sessions"\] #sescol\s*\{[^}]*grid-area:\s*auto;/);
});
