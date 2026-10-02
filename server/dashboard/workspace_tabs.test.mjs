import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const source = await readFile(new URL('./workspace_tabs.js', import.meta.url), 'utf8');
const preview = await readFile(new URL('./preview.js', import.meta.url), 'utf8');
function setup() {
  class Node {
    constructor() { this.dataset = {}; this.attrs = {}; this.children = []; this.events = {}; this.hidden = false; }
    setAttribute(key, value) { this.attrs[key] = value; }
    append(node) { node.parent = this; this.children.push(node); }
    replaceChildren() { this.children = []; }
    remove() { this.parent.children = this.parent.children.filter(node => node !== this); }
    focus() { this.focused = true; }
    addEventListener(type, listener) { this.events[type] = listener; }
  }
  const elements = Object.fromEntries(['win', 'workspace-tabs', 'workspace-preview', 'chat-pane', 'chat-scroll', 'file-preview-open', 'win-title', 'frames', 'frame'].map(id => [id, new Node()]));
  const context = {URL, URLSearchParams, location: {origin: 'https://fleet.test'}, document: {
    querySelector(selector) { return elements[selector.slice(1)] || null; }, createElement() { return new Node(); },
  }};
  vm.createContext(context); vm.runInContext(preview, context); vm.runInContext(source, context);
  let opened = 0;
  context.FleetWorkspaceTabs.init({onOpen() { opened++; }});
  return {api: context.FleetWorkspaceTabs, elements, get opened() { return opened; }};
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
  assert.equal(api.previewTarget(url('/repo/a.py')).detail, 'M1 · /repo/a.py');
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
  assert.equal(chat.inert, true);
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
  assert.equal(e['workspace-tabs'].hidden, true);
  assert.equal(e['workspace-preview'].hidden, true);
  assert.equal(e['chat-pane'].inert, false);
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
  const full = clickLink(url('/tmp/c.txt')); e['file-preview-open'].events.click(full);
  assert.equal(full.prevented, true);
  assert.equal(e['workspace-preview'].children.length, 2);
  api.reset(); assert.equal(e['workspace-preview'].children.length, 0);
});

test('tabs support keyboard selection and delete while closing never removes the conversation tab', () => {
  const {api, elements: e} = setup(); api.open(url('a.py'));
  e['workspace-tabs'].children[1].children[0].onkeydown({key: 'Home', preventDefault() {}});
  assert.equal(e.win.dataset.workspacePreview, 'false');
  e['workspace-tabs'].children[0].children[0].onkeydown({key: 'End', preventDefault() {}});
  assert.equal(e.win.dataset.workspacePreview, 'true');
  e['workspace-tabs'].children[1].children[0].onkeydown({key: 'Delete', preventDefault() {}});
  assert.equal(e['workspace-tabs'].hidden, true);
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
  assert.match(css, /#win\[data-workspace-preview="true"\] \.win-body > :not\(#workspace-preview\)/);
});
