import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const source = await readFile(new URL('./workspace_tabs.js', import.meta.url), 'utf8');
const preview = await readFile(new URL('./preview.js', import.meta.url), 'utf8');
function setup({onSelectChat = () => {}, onRevealFile = () => false} = {}) {
  class Node {
    constructor() { this.dataset = {}; this.attrs = {}; this.children = []; this.events = {}; this.hidden = false; this.inert = false; this.style = {}; }
    setAttribute(key, value) { this.attrs[key] = value; }
    removeAttribute(key) { delete this.attrs[key]; }
    getBoundingClientRect() { return {left: 20, bottom: 48, top: 20, height: 120, width: 320}; }
    querySelectorAll() { return []; }
    matches() { return true; }
    append(...nodes) { for (const node of nodes) { node.parent = this; this.children.push(node); } }
    replaceChildren() { this.children = []; }
    remove() { this.parent.children = this.parent.children.filter(node => node !== this); }
    focus() { this.focused = true; }
    addEventListener(type, listener) { this.events[type] = listener; }
  }
  const elements = Object.fromEntries(['win', 'workspace-tabs', 'workspace-preview', 'chat-pane', 'chat-scroll', 'file-preview-open', 'win-title', 'frames', 'frame', 'chat-composer', 'chat-input', 'chat-turn-pin'].map(id => [id, new Node()]));
  const timers = new Map(); let timerId = 0;
  elements['workspace-preview'].style = {setProperty() {}};
  elements['win-title'].textContent = '规划并创建 emotion-service 角色';
  const context = {URL, URLSearchParams, innerWidth: 900, innerHeight: 700,
    setTimeout(fn) { timers.set(++timerId, fn); return timerId; }, clearTimeout(id) { timers.delete(id); },
    addEventListener() {}, location: {origin: 'https://fleet.test'}, document: {
    body: new Node(), querySelector(selector) { return elements[selector.slice(1)] || null; }, createElement() { return new Node(); },
  }};
  vm.createContext(context); vm.runInContext(preview, context); vm.runInContext(source, context);
  let opened = 0, closed = 0;
  context.FleetWorkspaceTabs.init({onSelectChat, onRevealFile, onOpen() { opened++; }, onCloseChat() { closed++; elements['chat-pane'].hidden = true; }});
  return {api: context.FleetWorkspaceTabs, elements, body: context.document.body, flush() { const pending = [...timers.values()]; timers.clear(); pending.forEach(fn => fn()); }, get opened() { return opened; }, get closed() { return closed; }};
}
const url = (path, mac = 'm1', cwd = '/repo') => `/view?${new URLSearchParams({mac, path, cwd})}`;
function clickLink(href, extras = {}) {
  let prevented = false;
  return {button: 0, target: {closest() {return {href, hasAttribute: () => !!extras.download};}},
    preventDefault() {prevented = true;}, get prevented() {return prevented;}, ...extras};
}

test('file identity scopes device/cwd, normalizes relative paths and line locations, rejects external routes', () => {
  const {api} = setup();
  assert.equal(api.previewTarget(url('src/../a.py:12')).key, api.previewTarget(url('/repo/a.py')).key);
  assert.notEqual(api.previewTarget(url('a.py', 'm1')).key, api.previewTarget(url('a.py', 'm2')).key);
  assert.notEqual(api.previewTarget(url('a.py', 'm1', '/other')).key, api.previewTarget(url('a.py')).key);
  assert.equal(api.previewTarget('https://other.test/view?mac=m1&path=x.py'), null);
  assert.equal(api.previewTarget('/view?mac=invalid&path=x.py'), null);
  assert.equal(api.previewTarget('/account'), null);
  assert.equal(api.previewTarget(url('/repo/a.py')).detail, '/repo/a.py');
});

test('multiple files are retained, repeat links reuse a frame, selecting chat preserves the conversation DOM', () => {
  const state = setup(), {api, elements: e} = state;
  const chat = e['chat-pane'];
  assert.equal(api.open(url('a.py')), true);
  const first = e['workspace-preview'].children[0];
  api.open(url('b.md')); api.open(url('a.py:25'));
  assert.equal(e['workspace-preview'].children.length, 2);
  assert.equal(e['workspace-preview'].children[0], first);
  assert.equal(first.hidden, false);
  assert.equal(e['workspace-preview'].children[1].hidden, true);
  assert.equal(e.win.dataset.workspacePreview, 'true');
  assert.notEqual(chat.inert, true);
  assert.equal(e['chat-scroll'].inert, true);
  api.showChat();
  assert.equal(e['workspace-preview'].hidden, true);
  assert.equal(e['chat-pane'], chat);
  assert.equal(chat.inert, false);
});

test('closing a background tab preserves active file, last close restores chat and removes frames', () => {
  const {api, elements: e} = setup();
  api.open(url('a.py')); api.open(url('b.md'));
  const frame = e['workspace-preview'].children[1];
  e['workspace-tabs'].children[1].children[1].onclick();
  assert.equal(e['workspace-preview'].children[0], frame);
  assert.equal(frame.hidden, false);
  e['workspace-tabs'].children[1].children[1].onclick();
  assert.equal(e['workspace-preview'].children.length, 0);
  assert.equal(e['workspace-tabs'].hidden, false);
  assert.equal(e['workspace-preview'].hidden, true);
  assert.equal(e['chat-pane'].inert, false);
});

test('a lone conversation uses the same tab header before opening files and after reset', () => {
  const {api, elements: e} = setup();
  const checkSingle = () => {
    assert.equal(e.win.dataset.workspaceTabs, 'true');
    assert.equal(e['workspace-tabs'].hidden, false);
    assert.equal(e['workspace-tabs'].children.length, 1);
    assert.equal(e['workspace-tabs'].children[0].children[0].attrs['aria-selected'], 'true');
    assert.equal(e['workspace-tabs'].children[0].children[0].children[1].textContent, '规划并创建 emotion-service 角色');
  };
  checkSingle();
  api.open(url('a.md'));
  assert.equal(e['workspace-tabs'].children.length, 2);
  api.reset(); checkSingle();
});

test('ordinary file links are intercepted but external links, downloads and modifier clicks keep native behavior', () => {
  const {api, elements: e} = setup();
  const handler = e['chat-scroll'].events.click;
  const local = clickLink(url('a.py')); handler(local);
  assert.equal(local.prevented, true);
  for (const extra of [{download: true}, {metaKey: true}, {ctrlKey: true}, {button: 1}, {defaultPrevented: true}]) {
    const event = clickLink(url('b.md'), extra); handler(event); assert.equal(event.prevented, false);
  }
  const external = clickLink('https://example.test'); handler(external); assert.equal(external.prevented, false);
  assert.equal(e['workspace-preview'].children.length, 1);
  api.reset(); assert.equal(e['workspace-preview'].children.length, 0);
});

test('preview breadcrumb targets are delegated to the dashboard file manager', () => {
  let revealed;
  const {api} = setup({onRevealFile(target) { revealed = target; return true; }});
  const target = {macId: 'm2', path: '/Users/demo/project/docs', kind: 'folder'};
  assert.equal(api.revealFile(target), true);
  assert.deepEqual(revealed, target);
});

test('file-browser full preview keeps native browser opening without creating a conversation tab', async () => {
  const state = setup(), e = state.elements;
  const full = clickLink(url('/tmp/c.txt'));
  e['file-preview-open'].events.click?.(full);
  assert.equal(full.prevented, false);
  assert.equal(state.opened, 0);
  assert.equal(e['workspace-preview'].children.length, 0);
  assert.equal(e.win.dataset.workspacePreview, 'false');
  const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
  const link = html.match(/<a\b[^>]*id="file-preview-open"[^>]*>/)?.[0];
  assert.match(link, /target="_blank"/);
  assert.match(link, /rel="noopener"/);
});

test('tabs support keyboard selection and deleting a file restores the conversation tab', () => {
  const {api, elements: e} = setup(); api.open(url('a.py'));
  e['workspace-tabs'].children[1].children[0].onkeydown({key: 'Home', preventDefault() {}});
  assert.equal(e.win.dataset.workspacePreview, 'false');
  e['workspace-tabs'].children[0].children[0].onkeydown({key: 'End', preventDefault() {}});
  assert.equal(e.win.dataset.workspacePreview, 'true');
  e['workspace-tabs'].children[1].children[0].onkeydown({key: 'Delete', preventDefault() {}});
  assert.equal(e['workspace-tabs'].hidden, false);
  assert.equal(e.win.dataset.workspacePreview, 'false');
});

test('preview frame links open sibling tabs and switching pauses native media without changing HTML sandbox', () => {
  const {api, elements: e} = setup(); api.open(url('a.md'));
  const frame = e['workspace-preview'].children[0]; let paused = 0;
  const events = {};
  frame.contentDocument = {querySelector: () => null, querySelectorAll: () => [{pause() {paused++;}}],
    addEventListener(type, callback) {events[type] = callback;}};
  frame.onload(); const sibling = clickLink(url('b.py')); events.click(sibling);
  assert.equal(sibling.prevented, true);
  assert.equal(e['workspace-preview'].children.length, 2);
  assert.ok(paused > 0);
});

test('integration loads workspace before app, restores chat on navigation and isolates preview overlay', async () => {
  const [html, app, css] = await Promise.all(['index.html','app.js','style.css'].map(name => readFile(new URL('./'+name, import.meta.url),'utf8')));
  assert.ok(html.indexOf('workspace_tabs.js?v=') < html.indexOf('app.js?v='));
  assert.match(app, /FleetWorkspaceTabs\?\.init\(\{onOpen:/);
  assert.match(app, /function selectSes\([^]*?FleetWorkspaceTabs\?\.showChat\(\)/);
  assert.match(app, /async function openChatSession\([^]*?FleetWorkspaceTabs\?\.showChat\(\)/);
  assert.match(css, /\.workspace-preview-frame\[hidden\] \{ display: none; \}/);
  assert.match(css, /#win\[data-workspace-preview="true"\] \.win-body > :not\(#workspace-preview\):not\(#chat-pane\)/);
});

test('workspace tabs use compact icon labels and a single integrated header', async () => {
  const css = await readFile(new URL('./style.css', import.meta.url), 'utf8');
  assert.match(css, /\.workspace-tab-select \{[^}]*height: 28px;[^}]*padding: 0 9px;/);
  assert.doesNotMatch(css, /\.workspace-tab[^,{]*:hover/);
  assert.match(css, /#win\[data-workspace-tabs="true"\] > \.win-head \.info \{ display: none;/);
  const {api, elements: e} = setup(); api.open(url('a.md'));
  assert.equal(e['workspace-tabs'].children[0].children[0].children[1].textContent, '规划并创建 emotion-service 角色');
  assert.equal(e['workspace-tabs'].children[1].children[0].title, undefined);
  assert.equal(e['workspace-tabs'].children[1].children[1].attrs['aria-label'], '关闭 a.md');
});

test('hover cards reveal full titles and paths, cancel on leave, and clear on selection/reset', () => {
  const state = setup(), {api, elements: e, body} = state;
  api.open(url('a.md'));
  const button = e['workspace-tabs'].children[1].children[0];
  button.onpointerenter({pointerType: 'mouse'}); button.onpointerleave(); state.flush();
  assert.equal(body.children[0].hidden, true);
  button.onpointerenter({pointerType: 'mouse'}); state.flush();
  const card = body.children[0]; assert.equal(card.hidden, false);
  assert.equal(card.children[0].textContent, 'a.md');
  assert.equal(card.children[2].textContent, '/repo/a.md');
  assert.equal(button.attrs['aria-describedby'], card.id);
  button.onclick(); assert.equal(card.hidden, true);
  e['workspace-tabs'].children[0].children[0].onfocus(); state.flush();
  assert.equal(card.children[0].textContent, '规划并创建 emotion-service 角色');
  api.reset(); assert.equal(card.hidden, true);
  assert.ok(card.children.every(node => node.textContent === ''));
});

test('file tabs retain the original composer, draft, and submit handler while transcript is inert', () => {
  const {api, elements: e} = setup();
  const composer = e['chat-composer'], input = e['chat-input'];
  input.value = '继续检查这份文件'; let sent = 0; composer.onsubmit = () => {sent++;};
  api.open(url('a.md')); api.open(url('b.py'));
  assert.notEqual(e['chat-pane'].inert, true);
  assert.notEqual(composer.inert, true); assert.notEqual(input.inert, true);
  composer.onsubmit(); assert.equal(sent, 1);
  api.showChat(); assert.equal(e['chat-input'], input); assert.equal(input.value, '继续检查这份文件');
  assert.equal(e['chat-scroll'].inert, false);
});

test('clicking the conversation tab restores its pane after file navigation detached it', () => {
  let restored = 0;
  const {api, elements: e} = setup({onSelectChat() { restored++; e['chat-pane'].hidden = false; }});
  api.open(url('a.md'));
  const frame = e['workspace-preview'].children[0];
  e['chat-pane'].hidden = true;
  e['workspace-tabs'].children[0].children[0].onclick();
  assert.equal(restored, 1);
  assert.equal(e['chat-pane'].hidden, false);
  assert.equal(e.win.dataset.workspacePreview, 'false');
  assert.equal(e['workspace-preview'].children[0], frame);
});

test('keyboard return and closing the final active file restore the selected conversation', () => {
  let restored = 0;
  const {api, elements: e} = setup({onSelectChat() { restored++; e['chat-pane'].hidden = false; }});
  api.open(url('a.md'));
  e['chat-pane'].hidden = true;
  e['workspace-tabs'].children[1].children[0].onkeydown({key: 'Home', preventDefault() {}});
  assert.equal(restored, 1);
  assert.equal(e['chat-pane'].hidden, false);
  assert.equal(e['workspace-tabs'].children[0].children[0].focused, true);
  api.open(url('a.md'));
  e['chat-pane'].hidden = true;
  e['workspace-tabs'].children[1].children[1].onclick();
  assert.equal(restored, 2);
  assert.equal(e['chat-pane'].hidden, false);
  assert.equal(e.win.dataset.workspacePreview, 'false');
});

test('programmatic chat selection and explicitly closed conversations do not trigger automatic restore', () => {
  let restored = 0;
  const state = setup({onSelectChat() { restored++; }}), e = state.elements;
  state.api.open(url('a.md')); state.api.showChat();
  assert.equal(restored, 0, 'openChatSession uses showChat and must not recursively restore');
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(state.closed, 1);
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(restored, 0);
});


test('conversation and file tabs always expose close controls in selected and background states', () => {
  const state = setup(), e = state.elements;
  state.api.open(url('a.md'));
  assert.equal(e['workspace-tabs'].children[0].children[1].attrs['aria-label'], '关闭会话');
  for (const tab of e['workspace-tabs'].children) {
    assert.equal(tab.children.length, 2);
    assert.match(tab.children[1].attrs['aria-label'], /^关闭(?: |会话)/);
  }
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(state.closed, 1);
  assert.equal(e['workspace-tabs'].children.length, 1);
  assert.equal(e['workspace-preview'].children.length, 1);
  assert.equal(e.win.dataset.workspacePreview, 'true');
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(e['workspace-tabs'].hidden, true);
  assert.equal(e['workspace-preview'].hidden, true);
});

test('closing the active conversation selects a retained file and reopening chat restores its tab', () => {
  const state = setup(), e = state.elements;
  state.api.open(url('a.md')); state.api.showChat();
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(state.closed, 1);
  assert.equal(e.win.dataset.workspacePreview, 'true');
  e['chat-pane'].hidden = false;
  state.api.showChat();
  assert.equal(e['workspace-tabs'].children.length, 2);
  assert.equal(e.win.dataset.workspacePreview, 'false');
});

test('closing a lone conversation hides the tab row without creating or deleting any file', () => {
  const state = setup(), e = state.elements;
  e['workspace-tabs'].children[0].children[1].onclick();
  assert.equal(state.closed, 1);
  assert.equal(e['workspace-tabs'].children.length, 0);
  assert.equal(e['workspace-tabs'].hidden, true);
  assert.equal(e['workspace-preview'].children.length, 0);
});
