import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./sidebar_layout.js', import.meta.url), 'utf8');
function setup({saved = {}, desktop = true, blocked = false} = {}) {
  const events = {}, elements = {};
  for (const id of ['app', 'rail-collapse', 'rail-expand', 'sessions-collapse', 'sessions-launcher', 'sescol']) {
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

test('设备和会话列表独立收起，展开列表直接恢复宽栏并保存', () => {
  const {elements: e, store} = setup();
  e['rail-collapse'].onclick();
  assert.equal(e.app.dataset.railCollapsed, 'true');
  assert.equal(e['rail-expand'].focused, true);
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  e['sessions-collapse'].onclick();
  assert.equal(e.app.dataset.sessionsCollapsed, 'true');
  assert.equal(e['sessions-collapse'].attributes['aria-expanded'], 'false');
  assert.equal(e.sescol.inert, true);
  assert.equal(e['sessions-launcher'].hidden, false);
  assert.equal(e['sessions-launcher'].focused, true);
  e['sessions-launcher'].onclick();
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  assert.equal(e.sescol.inert, false);
  assert.equal(e['sessions-launcher'].hidden, true);
  assert.equal(e['sessions-collapse'].attributes['aria-expanded'], 'true');
  assert.equal(e['sessions-collapse'].focused, true);
  const reloaded = setup({saved: JSON.parse(store.get('fleet-sidebar-layout-v1'))});
  assert.equal(reloaded.elements.app.dataset.railCollapsed, 'true');
  assert.equal(reloaded.elements.app.dataset.sessionsCollapsed, 'false');
  reloaded.elements['rail-expand'].onclick();
  assert.equal(reloaded.elements.app.dataset.railCollapsed, 'false');
  assert.equal(reloaded.elements['rail-collapse'].hidden, false);
  assert.equal(reloaded.elements['rail-collapse'].focused, true);
  assert.equal(reloaded.elements['rail-expand'].disabled, true);
});

test('旧收起偏好仍有效，展开后选择会话或同步布局不自动收回列表', () => {
  const {elements: e, api, store} = setup({saved: {sessionsCollapsed: true}});
  assert.equal(e.sescol.inert, true);
  e['sessions-launcher'].onclick(); api.sync();
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  assert.equal(e.sescol.inert, false);
  const reloaded = setup({saved: JSON.parse(store.get('fleet-sidebar-layout-v1'))});
  assert.equal(reloaded.elements.app.dataset.sessionsCollapsed, 'false');
  e['sessions-collapse'].onclick(); api.sync();
  assert.equal(e.app.dataset.sessionsCollapsed, 'true');
  assert.equal(e.sescol.inert, true);
});

test('切文件模式隐藏入口，返回会话保持展开或收起偏好', () => {
  for (const collapsed of [true, false]) {
    const {elements: e, api} = setup({saved: {sessionsCollapsed: collapsed}});
    e.app.dataset.mode = 'files'; api.sync();
    assert.equal(e['sessions-launcher'].hidden, true);
    assert.equal(e.sescol.inert, true);
    e['sessions-launcher'].onclick();
    assert.equal(e.app.dataset.sessionsCollapsed, String(collapsed));
    e.app.dataset.mode = 'sessions'; api.sync();
    assert.equal(e['sessions-launcher'].hidden, !collapsed);
    assert.equal(e.sescol.inert, collapsed);
  }
});

test('移动断点不受桌面收起偏好影响，返回桌面保持原状态', () => {
  const {elements: e, media} = setup({saved: {railCollapsed: true, sessionsCollapsed: true}});
  media.matches = false; media.change();
  assert.equal(e.sescol.inert, false);
  assert.equal(e['sessions-launcher'].hidden, true);
  e['sessions-launcher'].onclick();
  assert.equal(e.app.dataset.sessionsCollapsed, 'true');
  media.matches = true; media.change();
  assert.equal(e.sescol.inert, true);
  assert.equal(e['sessions-launcher'].hidden, false);
});

test('浏览器禁用存储时仍可直接收起和展开', () => {
  const {elements: e} = setup({blocked: true});
  e['rail-collapse'].onclick(); e['sessions-collapse'].onclick(); e['sessions-launcher'].onclick();
  assert.equal(e.app.dataset.railCollapsed, 'true');
  assert.equal(e.app.dataset.sessionsCollapsed, 'false');
  assert.equal(e.sescol.inert, false);
});

test('展开列表不注册浮层关闭交互，页面与样式移除悬浮列表', async () => {
  const {events} = setup();
  assert.equal(events.pointerdown, undefined);
  assert.equal(events.keydown, undefined);
  const [app, html, css] = await Promise.all(['app.js', 'index.html', 'style.css'].map(name => readFile(new URL('./'+name, import.meta.url), 'utf8')));
  assert.doesNotMatch(app, /FleetSidebarLayout\?\.close/);
  assert.match(app, /function setMode\([^]*?FleetSidebarLayout\?\.sync\(\)/);
  assert.doesNotMatch(html, /sessions-backdrop|sessions-pinned|sessions-unpinned|data-pinned/);
  const collapse = html.match(/<button id="sessions-collapse"[^]*?<\/button>/)[0];
  const expand = html.match(/<button id="sessions-launcher"[^]*?<\/button>/)[0];
  assert.equal(collapse.match(/<svg[^]*?<\/svg>/)[0], expand.match(/<svg[^]*?<\/svg>/)[0]);
  assert.match(collapse, /class="sidebar-icon-pane"/);
  assert.match(css, /\[aria-expanded="true"\] \.sidebar-icon-pane\s*\{ fill: currentColor;/);
  assert.match(collapse, /aria-label="收起会话列表"/);
  assert.match(expand, /aria-label="展开会话列表"/);
  assert.doesNotMatch(css, /sessions-backdrop|data-sessions-open/);
  assert.match(css, /#win\s*\{\s*grid-column:\s*3;/);
  assert.match(css, /#app\[data-mode="files"\] #win\s*\{\s*grid-column:\s*2;/);
});
