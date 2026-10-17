import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./native_dialogs.js', import.meta.url), 'utf8');

class Element {
  constructor(tagName, attributes = {}, text = '') {
    this.tagName = tagName.toUpperCase();
    this.attributes = { ...attributes };
    this.id = attributes.id || '';
    this.hidden = 'hidden' in attributes;
    this.disabled = 'disabled' in attributes;
    this.type = attributes.type || (tagName === 'button' ? 'submit' : 'text');
    this.value = attributes.value || '';
    this.checked = false;
    this.maxLength = Number(attributes.maxlength ?? -1);
    this.placeholder = attributes.placeholder || '';
    this.name = attributes.name || '';
    this.labels = [];
    this.children = [];
    this.parentElement = null;
    this.rendered = true;
    this.text = text;
    this.events = [];
    this.clicks = 0;
    this.classList = { contains: name => (this.attributes.class || '').split(/\s+/).includes(name) };
  }
  get textContent() { return this.text + this.children.map(child => child.textContent).join(''); }
  set textContent(value) { this.text = value; this.children = []; }
  get innerText() {
    const raw = node => node.text + node.children.map(raw).join('');
    if (!this.getClientRects().length) return raw(this);
    const blocks = new Set(['DIV', 'SECTION', 'HEADER', 'FOOTER', 'LI', 'PRE', 'H1', 'H2', 'H3']);
    const visible = (node, root = false) => {
      if (!root && (node.hidden || !node.rendered)) return '';
      if (node.tagName === 'BR') return '\n';
      const content = node.text + node.children.map(child => visible(child)).join('');
      const boundary = root ? '' : node.tagName === 'P' ? '\n\n' : blocks.has(node.tagName) ? '\n' : '';
      return boundary + content + boundary;
    };
    return visible(this, true).replace(/\n{3,}/g, '\n\n').replace(/^\n+|\n+$/g, '');
  }
  get options() { return this.querySelectorAll('option'); }
  getAttribute(name) { return this.attributes[name] ?? null; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  append(...children) {
    for (const child of children) { child.parentElement = this; this.children.push(child); }
  }
  remove() {
    this.parentElement.children = this.parentElement.children.filter(child => child !== this);
    this.parentElement = null;
  }
  contains(element) { return element === this || this.children.some(child => child.contains(element)); }
  matches(selector) {
    return selector.split(',').some(part => {
      const trimmed = part.trim();
      if (trimmed.startsWith('.')) return this.classList.contains(trimmed.slice(1));
      if (trimmed.startsWith('#')) return this.id === trimmed.slice(1);
      const match = /^([\w-]+)?(?:\[([\w-]+)(?:="([^"]*)")?\])?$/.exec(trimmed);
      assert.ok(match, 'fixture must implement every selector used by the adapter');
      if (match[1] && match[1].toUpperCase() !== this.tagName) return false;
      if (!match[2]) return true;
      const attribute = match[2] === 'hidden' ? (this.hidden ? '' : null) : this.getAttribute(match[2]);
      return match[3] === undefined ? attribute !== null : attribute === match[3];
    });
  }
  closest(selector) { return this.matches(selector) ? this : this.parentElement?.closest(selector) || null; }
  querySelectorAll(selector) {
    return this.children.flatMap(child => [...(child.matches(selector) ? [child] : []), ...child.querySelectorAll(selector)]);
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
  getClientRects() {
    for (let ancestor = this; ancestor; ancestor = ancestor.parentElement) {
      if (ancestor.hidden || !ancestor.rendered) return [];
    }
    return [{}];
  }
  dispatchEvent(event) {
    this.events.push(event.type);
    this[`on${event.type}`]?.(event);
    if (event.bubbles) this.document?.dispatchEvent(event);
    return true;
  }
  click() { this.clicks++; this.onclick?.(); }
}

function element(tag, attributes, text, ...children) {
  const result = new Element(tag, attributes, text);
  result.append(...children);
  return result;
}

function harness({ native = true, pathname = '/', mainFrame = true, bridge = true, ready = true, identity = 'synthetic-owner-a', routes = true } = {}) {
  const app = element('div', { id: 'app' });
  const head = element('head');
  const messages = [], logs = [], microtasks = [], observers = [], closes = [], originalRoutes = [], subagentCloses = [], fileMenuCloses = [];
  const listeners = new Map();
  const document = {
    head, createElement: tag => element(tag),
    getElementById: id => app.id === id ? app : app.querySelector(`#${id}`),
    querySelectorAll: selector => app.querySelectorAll(selector),
    addEventListener(name, listener) {
      if (!listeners.has(name)) listeners.set(name, []);
      listeners.get(name).push(listener);
    },
    dispatchEvent(event) { for (const listener of listeners.get(event.type) || []) listener(event); },
  };
  const bindDocument = node => { node.document = document; node.children.forEach(bindDocument); };
  const close = id => {
    closes.push(id);
    const dialog = document.getElementById(id);
    if (dialog) dialog.hidden = true;
  };
  const context = {
    __fleetNativeVersion: native ? 1 : 0, document, location: { pathname },
    FleetWorkspace: { snapshot: () => ({ ready, identity }) },
    state: { chat: { id: 'synthetic-chat' } },
    queueMicrotask: callback => microtasks.push(callback),
    Event: class { constructor(type, options = {}) { this.type = type; this.bubbles = !!options.bubbles; } },
    MutationObserver: class {
      constructor(callback) { this.callback = callback; observers.push(this); }
      observe(target, options) { this.target = target; this.options = options; }
    },
    closeOverlay: close,
    closeChatApproval: () => close('chat-approval-popover'),
    closeChatOptions: () => close('chat-options-popover'),
    closeFileSettings: () => close('file-settings-menu'),
    closeFileMenus() {
      fileMenuCloses.push('close');
      for (const menu of document.querySelectorAll('.file-row-menu')) menu.hidden = true;
    },
    setChatSubagentPanel(chat, mode) {
      subagentCloses.push({ chat, mode });
      close('chat-subagent-drawer');
    },
    console: Object.fromEntries(['log', 'warn', 'error', 'info', 'debug'].map(name => [name, (...args) => logs.push(args)])),
  };
  if (routes) {
    context.openHostModal = id => { originalRoutes.push({ type: 'host', id }); return Promise.resolve(); };
    context.openUnifiedSettings = page => { originalRoutes.push({ type: 'settings', page }); return Promise.resolve(); };
  }
  const originalFunctions = { host: context.openHostModal, settings: context.openUnifiedSettings };
  if (bridge) context.webkit = { messageHandlers: { fleet: { postMessage: message => messages.push(JSON.parse(JSON.stringify(message))) } } };
  context.top = mainFrame ? context : {};
  vm.createContext(context);
  vm.runInContext(source, context);
  const flush = () => {
    let count = 0;
    while (microtasks.length) { assert.ok(count++ < 30, 'microtasks must settle'); microtasks.shift()(); }
  };
  return {
    context, app, head, document, messages, logs, observers, closes, originalRoutes, originalFunctions, subagentCloses, fileMenuCloses, flush,
    get api() { return context.FleetNativeDialogs; },
    get payload() { return messages.at(-1)?.payload; },
    setReady(value) { ready = value; },
    setIdentity(value) { identity = value; },
    rerun() { vm.runInContext(source, context); },
    attach(node) { app.append(node); bindDocument(app); },
    open(id = 'file-name-modal', ...children) {
      const overlay = ['device-modal', 'new-project-modal', 'file-name-modal', 'file-path-modal', 'confirm-file-delete', 'confirm-kill'].includes(id);
      const dialog = element('div', { id, class: overlay ? 'overlay' : '' }, '', ...children);
      app.append(dialog); bindDocument(app); context.FleetNativeDialogs.publish(); return dialog;
    },
    mutate(node = app, attribute = 'hidden') {
      for (const observer of observers) {
        if (observer.target.contains(node) && observer.options.attributeFilter.includes(attribute)) observer.callback();
      }
      flush();
    },
    emit(type) { document.dispatchEvent({ type }); flush(); },
    item(label) { return [...this.payload.items, ...this.payload.footer].find(item => item.label === label); },
  };
}

test('ordinary Web, non-main frames, missing bridge, and auxiliary pages do not install', () => {
  for (const options of [{ native: false }, { bridge: false }, { mainFrame: false }, { pathname: '/auth' }, { pathname: '/account' }, { pathname: '/preview.html' }]) {
    const current = harness(options);
    assert.equal(current.api, undefined);
    assert.equal(current.messages.length, 0);
    assert.equal(current.head.children.length, 0);
    assert.equal(current.observers.length, 0);
  }
  assert.ok(harness({ pathname: '/index.html' }).api);
});

test('installation is idempotent and unchanged payloads do not post repeatedly', () => {
  const current = harness();
  assert.deepEqual(current.messages, [{ type: 'native-dialog', payload: null }]);
  current.api.publish();
  current.rerun();
  assert.equal(current.messages.length, 1);
  assert.equal(current.head.children.length, 1);
  assert.equal(current.observers.length, 1);
  assert.ok(Object.isFrozen(current.api));
});

test('visible dialogs project controls and footer, excluding hidden descendants and close controls', () => {
  const current = harness();
  const unrendered = element('button', {}, 'not rendered');
  unrendered.rendered = false;
  const dialog = current.open('file-name-modal',
    element('div', { class: 'modal-head' }, '文件名称', element('button', { 'data-close': 'file-name-modal' }, '关闭')),
    element('h2', {}, '重命名'), element('p', { role: 'status' }, '请输入名称'),
    element('button', { 'aria-pressed': 'true' }, '选中项', element('p', {}, '嵌套文本')),
    element('a', { href: '/help' }, '帮助'),
    element('div', { hidden: '' }, '', element('button', {}, 'hidden button'), element('input', { value: 'hidden value' })),
    element('input', { type: 'hidden', value: 'hidden credential' }), element('input', { type: 'file' }), unrendered,
    element('div', { class: 'modal-foot' }, '', element('button', { 'data-close': 'file-name-modal' }, '取消'), element('button', { class: 'danger' }, '删除')));
  assert.equal(current.payload.id, dialog.id);
  assert.equal(current.payload.title, '文件名称');
  assert.deepEqual(current.payload.items.map(item => item.label), ['重命名', '请输入名称', '选中项 嵌套文本', '帮助']);
  assert.equal(current.item('选中项 嵌套文本').selected, true);
  assert.equal(current.item('重命名').kind, 'heading');
  assert.deepEqual(current.payload.footer.map(item => item.label), ['删除']);
  assert.equal(current.item('删除').destructive, true);
  dialog.hidden = true;
  current.mutate(dialog);
  assert.equal(current.payload, null);
});

test('field assignment respects labels, maxlength, checkbox, number, textarea and bubbling events', () => {
  const current = harness();
  const field = element('input', { maxlength: '4', placeholder: '备用标签' });
  field.labels = [element('label', {}, '文件名称')];
  const notes = element('textarea', { 'aria-label': '说明' });
  const count = element('input', { type: 'number', name: '数量', value: '2' });
  const toggle = element('input', { type: 'checkbox', 'aria-label': '允许访问' });
  current.open('file-name-modal', field, notes, count, toggle);
  for (const [label, value] of [['文件名称', 'abcdef'], ['说明', 'line one\nline two'], ['数量', '3'], ['允许访问', 'true']]) {
    current.api.act('file-name-modal', current.item(label).id, value);
  }
  current.flush();
  assert.equal(field.value, 'abcd');
  assert.equal(notes.value, 'line one\nline two');
  assert.equal(count.value, '3');
  assert.equal(toggle.checked, true);
  for (const control of [field, notes, count, toggle]) assert.deepEqual(control.events, ['input', 'change']);
  assert.equal(current.item('数量').kind, 'number');
  assert.equal(current.item('允许访问').kind, 'toggle');
  assert.equal(current.item('允许访问').value, 'true');
  assert.equal(current.item('说明').value, notes.value);
});

test('select rejects unknown and disabled choices without writing or dispatching events', () => {
  const current = harness();
  const select = element('select', { 'aria-label': '项目', value: 'first' }, '',
    element('option', { value: 'first' }, '项目一'), element('option', { value: 'second' }, '项目二'),
    element('option', { value: 'disabled', disabled: '' }, '不可选项目'));
  current.open('new-project-modal', select);
  const picker = current.item('项目');
  assert.equal(picker.kind, 'picker');
  assert.deepEqual(picker.options.map(option => [option.value, option.disabled]), [['first', false], ['second', false], ['disabled', true]]);
  for (const value of ['unknown', 'disabled']) assert.throws(() => current.api.act('new-project-modal', picker.id, value), /选项已变化/);
  assert.equal(select.value, 'first');
  assert.equal(select.events.length, 0);
  current.api.act('new-project-modal', picker.id, 'second');
  current.flush();
  assert.equal(current.item('项目').value, 'second');
  assert.deepEqual(select.events, ['input', 'change']);
});

test('disabled, aria-disabled, removed and stale control IDs cannot act', () => {
  const current = harness();
  const button = element('button', {}, '保存');
  current.open('file-name-modal', button);
  const id = current.item('保存').id;
  for (const disabled of ['disabled', 'aria-disabled']) {
    if (disabled === 'disabled') button.disabled = true;
    else button.setAttribute('aria-disabled', 'true');
    assert.throws(() => current.api.act('file-name-modal', id), /操作已失效/);
    button.disabled = false; button.setAttribute('aria-disabled', 'false');
  }
  assert.throws(() => current.api.act('file-name-modal', 'unknown-control'), /操作已失效/);
  button.remove();
  assert.throws(() => current.api.act('file-name-modal', id), /操作已失效/);
  current.api.publish();
  assert.throws(() => current.api.act('file-name-modal', id), /操作已失效/);
  assert.equal(button.clicks, 0);
});

test('submit buttons check form validity before clicking and actions schedule publication', () => {
  const current = harness();
  const submit = element('button', { type: 'submit' }, '创建');
  let valid = false;
  submit.form = { checkValidity: () => valid };
  current.open('file-name-modal', submit);
  const id = current.item('创建').id;
  assert.throws(() => current.api.act('file-name-modal', id), /有效的表单/);
  assert.equal(submit.clicks, 0);
  valid = true;
  submit.onclick = () => { submit.disabled = true; };
  current.api.act('file-name-modal', id);
  current.flush();
  assert.equal(submit.clicks, 1);
  assert.equal(current.item('创建').disabled, true);
});

test('close routes each dialog through its H5 closer and reopening publishes again', () => {
  for (const id of ['device-modal', 'new-project-modal', 'file-name-modal', 'file-path-modal', 'confirm-file-delete', 'confirm-kill', 'chat-approval-popover', 'chat-options-popover', 'file-settings-menu']) {
    const current = harness();
    const button = element('button', {}, '继续');
    const dialog = current.open(id, button);
    const control = current.item('继续').id;
    current.api.act(id, '__close');
    assert.deepEqual(current.closes, [id]);
    assert.equal(current.payload, null);
    assert.throws(() => current.api.act(id, control), /弹层已变化/);
    const count = current.messages.length;
    dialog.hidden = false;
    current.mutate(dialog);
    assert.equal(current.messages.length, count + 1);
    assert.equal(current.payload.id, id);
    current.api.act(id, current.item('继续').id);
    current.flush();
    assert.equal(button.clicks, 1);
  }
});

test('an action can close its dialog and open another without accepting the old target', () => {
  const current = harness();
  const next = element('button', {}, '下一步');
  const first = current.open('device-modal', next);
  next.onclick = () => { first.hidden = true; current.open('new-project-modal', element('button', {}, '项目')); };
  const id = current.item('下一步').id;
  current.api.act('device-modal', id);
  current.flush();
  assert.equal(current.payload.id, 'new-project-modal');
  assert.throws(() => current.api.act('device-modal', id), /弹层已变化/);
  assert.throws(() => current.api.act('new-project-modal', id), /操作已失效/);
});

test('authentication loss clears projection and rejects actions before the scheduled cleanup', () => {
  const current = harness();
  current.open('file-name-modal', element('button', {}, '保存'));
  const id = current.item('保存').id;
  current.setReady(false);
  assert.throws(() => current.api.act('file-name-modal', id), /弹层已变化/);
  current.emit('fleet:auth-lost');
  assert.equal(current.payload, null);
  const count = current.messages.length;
  current.api.publish();
  assert.equal(current.messages.length, count);
  current.setReady(true);
  current.api.publish();
  assert.equal(current.payload.id, 'file-name-modal');
  const locked = harness({ ready: false });
  locked.open('file-name-modal', element('button', {}, '不可访问'));
  assert.equal(locked.payload, null);
});

test('input/change and mutation updates coalesce while stable control IDs survive publication', () => {
  const current = harness();
  const field = element('input', { 'aria-label': '名称' });
  current.open('file-name-modal', field);
  const id = current.item('名称').id;
  const count = current.messages.length;
  field.value = '新名称';
  current.document.dispatchEvent({ type: 'input' });
  current.document.dispatchEvent({ type: 'change' });
  current.flush();
  assert.equal(current.messages.length, count + 1);
  assert.equal(current.item('名称').id, id);
  field.disabled = true;
  current.mutate(field, 'disabled');
  assert.equal(current.item('名称').disabled, true);
});

test('password and hidden credentials never enter bridge payloads, logs, or adapter-created nodes', () => {
  const current = harness();
  const sentinel = 'synthetic-private-value-not-a-real-key';
  const password = element('input', { type: 'password', 'aria-label': '密码', value: sentinel });
  current.open('file-name-modal', password,
    element('input', { type: 'hidden', value: sentinel }),
    element('div', { hidden: '' }, '', element('p', {}, sentinel)));
  const secure = current.item('密码');
  assert.equal(secure.kind, 'secure');
  assert.equal(secure.value, '');
  current.api.act('file-name-modal', secure.id, sentinel + '-changed');
  current.flush();
  assert.ok(password.value === sentinel + '-changed', 'secure field must still receive the full value');
  assert.equal(current.item('密码').value, '');
  assert.ok(!JSON.stringify(current.messages).includes(sentinel), 'bridge payloads must omit synthetic credentials');
  assert.ok(!JSON.stringify(current.logs).includes(sentinel), 'logs must omit synthetic credentials');
  assert.ok(!current.head.textContent.includes(sentinel), 'injected nodes must omit synthetic credentials');
  assert.ok(current.messages.every(message => [...(message.payload?.items || []), ...(message.payload?.footer || [])].every(item => /^control-\d+$/.test(item.id))), 'projected IDs must remain opaque');
});

test('regression: a control hidden after publication is rejected before the next mutation callback', () => {
  const current = harness();
  const button = element('button', {}, '执行');
  current.open('file-name-modal', button);
  const id = current.item('执行').id;
  button.hidden = true;
  assert.throws(() => current.api.act('file-name-modal', id), /操作已失效/);
  assert.equal(button.clicks, 0);
});

test('regression: hidden inline text never leaks into a visible projected label', () => {
  const current = harness();
  const sentinel = 'synthetic-inline-private-marker';
  current.open('file-name-modal', element('p', {}, '可见说明', element('span', { hidden: '' }, sentinel)));
  assert.ok(!JSON.stringify(current.messages).includes(sentinel), 'hidden inline text must not enter projected labels');
});

test('regression: aria-disabled changes refresh native disabled state without unrelated events', () => {
  const current = harness();
  const button = element('button', {}, '执行');
  current.open('file-name-modal', button);
  button.setAttribute('aria-disabled', 'true');
  current.mutate(button, 'aria-disabled');
  assert.equal(current.item('执行').disabled, true);
});

test('regression: a dialog inside a hidden ancestor is not selected for projection', () => {
  const current = harness();
  const dialog = current.open('file-name-modal', element('button', {}, '执行'));
  current.app.hidden = true;
  current.mutate(current.app);
  assert.equal(current.payload, null);
  assert.equal(dialog.hidden, false);
});

test('H5 settings and device entry points route to native sheets without opening Web overlays', async () => {
  const current = harness();
  current.context.openHostModal('m2');
  assert.deepEqual(current.messages.at(-1), { type: 'native-sheet', sheet: 'device', device: 'm2' });
  for (const page of ['account', 'add-device', 'appearance', 'automation', 'sessions']) {
    const result = current.context.openUnifiedSettings(page);
    assert.equal(typeof result?.then, 'function');
    await result;
    assert.deepEqual(current.messages.at(-1), { type: 'native-sheet', sheet: 'settings', page });
  }
  assert.equal(current.originalRoutes.length, 0);
  assert.equal(current.app.children.length, 0);
  const settingsRoute = current.context.openUnifiedSettings;
  current.rerun();
  assert.equal(current.context.openUnifiedSettings, settingsRoute);
});

test('route wrappers do not post after authentication loss or change ordinary Web entry points', async () => {
  const current = harness();
  current.setReady(false);
  const count = current.messages.length;
  current.context.openHostModal('m2');
  await current.context.openUnifiedSettings('automation');
  assert.equal(current.messages.length, count);
  assert.equal(current.originalRoutes.length, 0);
  for (const options of [{ native: false }, { mainFrame: false }, { pathname: '/account' }]) {
    const ordinary = harness(options);
    assert.equal(ordinary.context.openHostModal, ordinary.originalFunctions.host);
    assert.equal(ordinary.context.openUnifiedSettings, ordinary.originalFunctions.settings);
    await ordinary.context.openHostModal('m2');
    await ordinary.context.openUnifiedSettings('appearance');
    assert.deepEqual(ordinary.originalRoutes, [{ type: 'host', id: 'm2' }, { type: 'settings', page: 'appearance' }]);
    assert.equal(ordinary.messages.length, 0);
  }
  const absent = harness({ routes: false });
  assert.equal(absent.context.openHostModal, undefined);
  assert.equal(absent.context.openUnifiedSettings, undefined);
});

test('projected device settings actions reach the native device editor', () => {
  const current = harness();
  const settings = element('button', { type: 'button', 'aria-label': '测试设备 设置' });
  const picker = current.open('device-modal', settings);
  settings.onclick = () => { picker.hidden = true; current.context.openHostModal('m2'); };
  current.api.act('device-modal', current.item('测试设备 设置').id);
  current.flush();
  assert.ok(current.messages.some(message => message.type === 'native-sheet' && message.sheet === 'device' && message.device === 'm2'));
  assert.equal(current.payload, null);
  assert.equal(current.originalRoutes.length, 0);
});

test('identity changes reject old values, clicks and closes before any new publication', () => {
  const current = harness();
  const field = element('input', { 'aria-label': '名称', value: 'unchanged' });
  const save = element('button', {}, '保存');
  current.open('file-name-modal', field, save);
  const fieldID = current.item('名称').id;
  const saveID = current.item('保存').id;
  for (const identity of ['synthetic-owner-b', '']) {
    current.setIdentity(identity);
    assert.throws(() => current.api.act('file-name-modal', fieldID, 'changed'), /弹层已变化/);
    assert.throws(() => current.api.act('file-name-modal', saveID), /弹层已变化/);
    assert.throws(() => current.api.act('file-name-modal', '__close'), /弹层已变化/);
  }
  assert.equal(field.value, 'unchanged');
  assert.equal(save.clicks, 0);
  assert.equal(current.closes.length, 0);
});

test('live action validation rejects CSS-hidden controls and hidden ancestors before observer updates', () => {
  for (const mode of ['control-css', 'ancestor-hidden', 'dialog-ancestor-hidden']) {
    const current = harness();
    const button = element('button', {}, '执行');
    const container = element('div', {}, '', button);
    current.open('file-name-modal', container);
    const id = current.item('执行').id;
    if (mode === 'control-css') button.rendered = false;
    else if (mode === 'ancestor-hidden') container.hidden = true;
    else current.app.hidden = true;
    assert.throws(() => current.api.act('file-name-modal', id), /操作已失效|弹层已变化/);
    assert.equal(button.clicks, 0);
  }
});

test('subagent drawer projects visible detail text, list items and code without duplicate parent text', () => {
  const current = harness();
  current.open('chat-subagent-drawer',
    element('strong', { id: 'chat-subagent-detail-name' }, '测试子代理'),
    element('span', { id: 'chat-subagent-detail-state' }, '运行中'),
    element('span', { id: 'chat-subagent-detail-meta' }, '测试项目'),
    element('div', { class: 'chat-subagent-detail-empty' }, '正在加载…'),
    element('p', {}, '状态说明'),
    element('pre', {}, 'synthetic code\nsecond line'),
    element('li', {}, '列表项'),
    element('li', {}, '父项', element('p', {}, '子项正文')),
    element('div', { hidden: '' }, '', element('pre', {}, 'hidden detail')),
    element('button', {}, '打开详情', element('p', {}, '按钮内说明')));
  assert.equal(current.payload.title, 'Sub Agent');
  assert.deepEqual(current.payload.items.map(item => item.label), [
    '测试子代理', '运行中', '测试项目', '正在加载…', '状态说明', 'synthetic code second line', '列表项', '子项正文', '打开详情 按钮内说明',
  ]);
  assert.ok(current.payload.items.slice(0, -1).every(item => item.kind === 'text'));
  assert.equal(current.payload.items.at(-1).kind, 'button');
  current.api.act('chat-subagent-drawer', '__close');
  assert.equal(current.subagentCloses.length, 1);
  assert.equal(current.subagentCloses[0].chat, current.context.state.chat);
  assert.equal(current.subagentCloses[0].mode, 'closed');
  assert.equal(current.payload, null);
});

test('subagent drawer supports list/detail reentry and identity loss blocks closing a new owner chat', () => {
  const current = harness();
  const select = element('button', {}, '测试子代理');
  const list = element('div', {}, '', select);
  const detail = element('div', { hidden: '' }, '', element('p', {}, '详情正文'));
  const drawer = current.open('chat-subagent-drawer', list, detail);
  select.onclick = () => { list.hidden = true; detail.hidden = false; };
  const id = current.item('测试子代理').id;
  current.api.act(drawer.id, id);
  current.flush();
  assert.deepEqual(current.payload.items.map(item => item.label), ['详情正文']);
  assert.throws(() => current.api.act(drawer.id, id), /操作已失效/);
  current.api.act(drawer.id, '__close');
  drawer.hidden = false;
  current.mutate(drawer);
  assert.equal(current.payload.id, drawer.id);
  current.setIdentity('synthetic-owner-b');
  assert.throws(() => current.api.act(drawer.id, '__close'), /弹层已变化/);
  assert.equal(current.subagentCloses.length, 1);
});

test('file settings project view buttons and the hidden-file toggle through existing H5 handlers', () => {
  const current = harness();
  const list = element('button', { type: 'button', 'aria-label': '列表视图', 'aria-pressed': 'true' });
  const toggle = element('input', { type: 'checkbox' });
  toggle.labels = [element('label', {}, '显示隐藏文件')];
  current.open('file-settings-menu', list, toggle);
  assert.equal(current.payload.title, '文件设置');
  assert.equal(current.item('列表视图').selected, true);
  assert.equal(current.item('显示隐藏文件').kind, 'toggle');
  current.api.act('file-settings-menu', current.item('列表视图').id);
  current.api.act('file-settings-menu', current.item('显示隐藏文件').id, 'true');
  current.flush();
  assert.equal(list.clicks, 1);
  assert.equal(toggle.checked, true);
});

test('file menu preserves sort groups and explicit direction selection for UIKit', () => {
  const current = harness();
  const group = element('div', { 'data-native-menu-group': '排序方式' });
  const ascending = element('button', { type: 'button', 'aria-pressed': 'true' }, '创建时间升序');
  const descending = element('button', { type: 'button', 'aria-pressed': 'false' }, '创建时间降序');
  group.append(ascending, descending);
  current.open('file-settings-menu', group);
  assert.equal(current.item('创建时间升序').group, '排序方式');
  assert.equal(current.item('创建时间升序').selected, true);
  assert.equal(current.item('创建时间降序').selected, false);
  current.api.act('file-settings-menu', current.item('创建时间降序').id);
  assert.equal(descending.clicks, 1);
});

test('generic dialog projection excludes settings sheets and image/skill surfaces', () => {
  for (const id of ['host-modal', 'fleet-settings-modal', 'settings-modal', 'automation-modal', 'chat-image-viewer', 'chat-skill-menu']) {
    const current = harness();
    current.open(id, element('button', {}, '边界检查'));
    assert.equal(current.payload, null, `${id} stays outside generic projection`);
  }
});

test('regression: permission and model radio selections marked aria-checked reach native drawers', () => {
  for (const id of ['chat-approval-popover', 'chat-options-popover']) {
    const current = harness();
    current.open(id, element('button', { role: 'menuitemradio', 'aria-checked': 'true', class: 'selected' }, '当前选项'));
    assert.equal(current.item('当前选项').selected, true, `${id} must preserve radio selection`);
  }
});

test('regression: current device wrapper selects the main choice without selecting its settings action', () => {
  const current = harness();
  current.open('device-modal', element('div', { class: 'device-option', 'aria-current': 'true' }, '',
    element('button', { class: 'device-option-main' }, '当前设备'), element('button', { class: 'device-option-info' }, '设备设置')));
  assert.equal(current.item('当前设备').selected, true);
  assert.equal(current.item('设备设置').selected, false);
});

test('regression: publication after an identity change cannot reauthorize the previous owner control ID', () => {
  const current = harness();
  const button = element('button', {}, '执行');
  const dialog = current.open('file-name-modal', button);
  const id = current.item('执行').id;
  current.setIdentity('synthetic-owner-b');
  current.api.publish();
  assert.equal(dialog.hidden, true);
  assert.equal(current.payload, null);
  assert.throws(() => current.api.act('file-name-modal', id), /弹层已变化|操作已失效/);
  assert.equal(button.clicks, 0);
  dialog.hidden = false;
  current.api.publish();
  const nextID = current.item('执行').id;
  assert.notEqual(nextID, id);
  assert.throws(() => current.api.act('file-name-modal', id), /操作已失效/);
  current.api.act('file-name-modal', nextID);
  current.flush();
  assert.equal(button.clicks, 1);
});

test('fixture innerText skips hidden and display-none descendants while preserving visible block boundaries', () => {
  const cssHidden = element('span', {}, 'not visible');
  cssHidden.rendered = false;
  const node = element('div', {}, 'visible',
    element('span', { hidden: '' }, 'hidden marker'), cssHidden,
    element('span', {}, ' inline'), element('br'), element('p', {}, 'paragraph'));
  assert.equal(node.innerText, 'visible inline\n\nparagraph');
  assert.ok(node.textContent.includes('hidden marker'));
  assert.ok(node.textContent.includes('not visible'));
  const hiddenRoot = element('div', { hidden: '' }, 'hidden root', element('span', {}, ' raw'));
  assert.equal(hiddenRoot.innerText, 'hidden root raw');
});

test('ARIA checked/current mutations refresh permissions, models and device selection without other events', () => {
  for (const id of ['chat-approval-popover', 'chat-options-popover']) {
    const current = harness();
    const button = element('button', { role: 'menuitemradio', 'aria-checked': 'false' }, '选项');
    current.open(id, button);
    assert.equal(current.item('选项').selected, false);
    button.setAttribute('aria-checked', 'true');
    current.mutate(button, 'aria-checked');
    assert.equal(current.item('选项').selected, true);
  }
  const current = harness();
  const wrapper = element('div', { class: 'device-option', 'aria-current': 'false' }, '',
    element('button', { class: 'device-option-main' }, '设备'));
  current.open('device-modal', wrapper);
  assert.equal(current.item('设备').selected, false);
  wrapper.setAttribute('aria-current', 'true');
  current.mutate(wrapper, 'aria-current');
  assert.equal(current.item('设备').selected, true);
});

function fileMenu(current, filename) {
  const effects = { renamed: [], deleted: [], downloaded: [] };
  const rename = element('button', { type: 'button' }, '重命名');
  const remove = element('button', { type: 'button', class: 'danger' }, '删除');
  const download = element('a', { href: '/synthetic/download', download: filename }, '下载');
  const menu = element('div', { class: 'file-row-menu', hidden: '' }, '', download, rename, remove);
  download.onclick = () => effects.downloaded.push(filename);
  rename.onclick = () => {
    current.context.closeFileMenus();
    const field = element('input', { 'aria-label': '文件名称', value: filename });
    const save = element('button', { type: 'submit' }, '保存');
    save.form = { checkValidity: () => Boolean(field.value) };
    save.onclick = () => { effects.renamed.push(field.value); current.context.closeOverlay('file-name-modal'); };
    current.open('file-name-modal', field, element('div', { class: 'modal-foot' }, '', save));
  };
  remove.onclick = () => {
    current.context.closeFileMenus();
    const confirm = element('button', { type: 'button', class: 'danger' }, '确认删除');
    confirm.onclick = () => { effects.deleted.push(filename); current.context.closeOverlay('confirm-file-delete'); };
    current.open('confirm-file-delete', element('p', {}, `删除 ${filename}？`), element('div', { class: 'modal-foot' }, '', confirm));
  };
  current.attach(menu);
  return { menu, effects, show() { current.context.closeFileMenus(); menu.hidden = false; current.api.publish(); } };
}

test('dynamic file menus receive opaque distinct IDs and switching files rejects old actions', () => {
  const current = harness();
  const first = fileMenu(current, 'synthetic-first.txt');
  const second = fileMenu(current, 'synthetic-second.txt');
  first.show();
  const firstID = current.payload.id;
  const firstAction = current.item('重命名').id;
  assert.match(firstID, /^native-file-menu-\d+$/);
  assert.equal(current.payload.title, '文件操作');
  assert.deepEqual(current.payload.items.map(item => item.label), ['下载', '重命名', '删除']);
  assert.equal(current.item('删除').destructive, true);
  second.show();
  const secondID = current.payload.id;
  assert.match(secondID, /^native-file-menu-\d+$/);
  assert.notEqual(firstID, secondID);
  assert.throws(() => current.api.act(firstID, firstAction), /弹层已变化/);
  assert.throws(() => current.api.act(secondID, firstAction), /操作已失效/);
  current.api.act(secondID, '__close');
  assert.equal(current.payload, null);
  first.show();
  assert.equal(current.payload.id, firstID);
  assert.equal(first.effects.renamed.length + second.effects.renamed.length, 0);
});

test('file menu cancel and download close through closeFileMenus without rename/delete side effects', () => {
  const current = harness();
  const file = fileMenu(current, 'synthetic-download.txt');
  file.show();
  const id = current.payload.id;
  const count = current.fileMenuCloses.length;
  current.api.act(id, '__close');
  assert.equal(current.fileMenuCloses.length, count + 1);
  assert.equal(current.payload, null);
  assert.equal(file.menu.hidden, true);
  file.show();
  current.api.act(current.payload.id, current.item('下载').id);
  current.flush();
  assert.deepEqual(file.effects.downloaded, ['synthetic-download.txt']);
  assert.equal(file.effects.renamed.length + file.effects.deleted.length, 0);
  assert.equal(file.menu.hidden, true);
  assert.equal(current.payload, null);
});

test('file menu rename reaches the naming drawer, cancels safely, and saves only the selected file', () => {
  for (const cancel of [true, false]) {
    const current = harness();
    const first = fileMenu(current, 'synthetic-first.txt');
    const second = fileMenu(current, 'synthetic-second.txt');
    second.show();
    const menuID = current.payload.id;
    const oldAction = current.item('重命名').id;
    current.api.act(menuID, oldAction);
    current.flush();
    assert.equal(current.payload.id, 'file-name-modal');
    assert.equal(second.menu.hidden, true);
    assert.equal(current.item('文件名称').value, 'synthetic-second.txt');
    assert.throws(() => current.api.act(menuID, oldAction), /弹层已变化/);
    if (cancel) current.api.act('file-name-modal', '__close');
    else {
      current.api.act('file-name-modal', current.item('文件名称').id, 'synthetic-renamed.txt');
      current.api.act('file-name-modal', current.item('保存').id);
      current.flush();
    }
    assert.equal(current.payload, null);
    assert.deepEqual(second.effects.renamed, cancel ? [] : ['synthetic-renamed.txt']);
    assert.equal(first.effects.renamed.length + first.effects.deleted.length + second.effects.deleted.length, 0);
  }
});

test('file menu delete reaches confirmation, cancel is harmless and confirmation targets the selected file', () => {
  for (const cancel of [true, false]) {
    const current = harness();
    const first = fileMenu(current, 'synthetic-first.txt');
    const second = fileMenu(current, 'synthetic-second.txt');
    second.show();
    current.api.act(current.payload.id, current.item('删除').id);
    current.flush();
    assert.equal(current.payload.id, 'confirm-file-delete');
    assert.equal(current.item('确认删除').destructive, true);
    assert.equal(second.menu.hidden, true);
    if (cancel) current.api.act('confirm-file-delete', '__close');
    else {
      current.api.act('confirm-file-delete', current.item('确认删除').id);
      current.flush();
    }
    assert.equal(current.payload, null);
    assert.deepEqual(second.effects.deleted, cancel ? [] : ['synthetic-second.txt']);
    assert.equal(first.effects.deleted.length + first.effects.renamed.length + second.effects.renamed.length, 0);
  }
});

test('identity changes hide all dynamic file menus and invalidate control IDs even after reopening', () => {
  const current = harness();
  const first = fileMenu(current, 'synthetic-first.txt');
  const second = fileMenu(current, 'synthetic-second.txt');
  first.show();
  const firstID = current.payload.id;
  const oldAction = current.item('删除').id;
  current.setIdentity('synthetic-owner-b');
  current.api.publish();
  assert.equal(first.menu.hidden, true);
  assert.equal(second.menu.hidden, true);
  assert.equal(current.payload, null);
  first.show();
  assert.equal(current.payload.id, firstID);
  assert.notEqual(current.item('删除').id, oldAction);
  assert.throws(() => current.api.act(firstID, oldAction), /操作已失效/);
  assert.equal(first.effects.deleted.length + second.effects.deleted.length, 0);
});

test('browser: inert projected controls retain H5 click, form and checkbox behavior', {
  skip: !process.env.NATIVE_DIALOGS_PLAYWRIGHT_MODULE,
}, async browserTest => {
  const { pathToFileURL } = await import('node:url');
  const playwright = await import(pathToFileURL(process.env.NATIVE_DIALOGS_PLAYWRIGHT_MODULE).href);
  const engine = process.env.NATIVE_DIALOGS_BROWSER || 'chromium';
  const browser = await playwright[engine].launch(engine === 'chromium' ? {
    executablePath: process.env.NATIVE_DIALOGS_CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true,
  } : { headless: true });
  const appSource = await readFile(new URL('./app.js', import.meta.url), 'utf8');
  const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
  const helpersStart = appSource.indexOf('function openFileNameModal(');
  const helpersEnd = appSource.indexOf('// ============================================================', helpersStart);
  const checkboxStart = appSource.indexOf("$('#file-show-hidden').onchange = (event) => {");
  const checkboxEnd = appSource.indexOf('\n  };', checkboxStart) + '\n  };'.length;
  assert.ok(helpersStart >= 0 && helpersEnd > helpersStart && checkboxStart >= 0 && checkboxEnd > checkboxStart);
  const handlers = appSource.slice(helpersStart, helpersEnd) + '\n' + appSource.slice(checkboxStart, checkboxEnd);

  async function fixture(run) {
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    try {
      await page.route('http://native-dialog.test/', route => route.fulfill({ contentType: 'text/html', body:
        '<!doctype html><html data-native="ios"><body><button id="outside" type="button">Outside</button><div id="app"></div></body></html>' }));
      await page.goto('http://native-dialog.test/');
      await page.evaluate(markup => {
        const template = new DOMParser().parseFromString(markup, 'text/html');
        for (const id of ['file-name-modal', 'confirm-file-delete', 'file-settings-menu']) {
          document.querySelector('#app').append(document.importNode(template.getElementById(id), true));
        }
        window.evidence = { requests: [], reloads: 0, persisted: 0, rendered: 0, submits: [], checkboxEvents: [] };
        window.posts = [];
        window.__fleetNativeVersion = 1;
        window.webkit = { messageHandlers: { fleet: { postMessage: value => posts.push(value) } } };
        window.FleetWorkspace = { snapshot: () => ({ ready: true, identity: 'synthetic-owner' }) };
        window.state = { fileMacId: 'm2', filePath: '/synthetic', filePreviewPath: '', fileShowHidden: false };
        window.$ = selector => document.querySelector(selector);
        window.api = async (device, route, options) => { evidence.requests.push({ device, route, body: JSON.parse(options.body) }); return {}; };
        window.openOverlay = id => { document.getElementById(id).hidden = false; };
        window.closeOverlay = id => { document.getElementById(id).hidden = true; };
        window.closeFileSettings = () => { document.querySelector('#file-settings-menu').hidden = true; };
        window.closeFilePreview = () => {};
        window.toast = () => {};
        window.loadFileDirectory = () => { evidence.reloads++; };
        window.persistUIState = () => { evidence.persisted++; };
        window.syncFileSettingsUI = () => {};
        window.renderFileEntries = () => { evidence.rendered++; };
        document.querySelector('#file-name-form').addEventListener('submit', event => evidence.submits.push(event.submitter?.id));
        for (const type of ['click', 'input', 'change']) document.querySelector('#file-show-hidden').addEventListener(type, event => evidence.checkboxEvents.push(event.type));
        window.projected = () => posts.filter(message => message.type === 'native-dialog').at(-1)?.payload;
        window.nativeAct = (label, value) => {
          const dialog = projected();
          const item = [...dialog.items, ...dialog.footer].find(item => item.label === label);
          if (!item) throw new Error('Missing projected fixture control');
          FleetNativeDialogs.act(dialog.id, item.id, value);
        };
      }, markup);
      await page.addScriptTag({ content: handlers + `
        document.querySelector('#file-name-form').onsubmit = submitFileName;
        document.querySelector('#file-delete-confirm').onclick = deleteFileTarget;
      ` });
      await page.addScriptTag({ content: source });
      await run(page);
      assert.deepEqual(errors, []);
    } finally { await page.close(); }
  }

  async function openName(page, mode = 'mkdir') {
    await page.evaluate(mode => {
      document.querySelector('#outside').focus();
      openFileNameModal(mode, mode === 'rename' ? { name: 'synthetic-old.txt', path: '/synthetic/old.txt' } : null);
      FleetNativeDialogs.publish();
    }, mode);
    const projection = await page.evaluate(() => ({
      inert: document.querySelector('#file-name-modal').inert,
      hiddenFromAccessibility: document.querySelector('#file-name-modal').getAttribute('aria-hidden'),
      containsField: projected().items.some(item => item.kind === 'field'),
    }));
    assert.deepEqual(projection, { inert: true, hiddenFromAccessibility: 'true', containsField: true });
  }

  try {
    await browserTest.test(`${engine}: inert fields cannot take focus, including the real opener's animation-frame focus`, async () => fixture(async page => {
      await openName(page);
      const focus = await page.evaluate(async () => {
        await new Promise(resolve => requestAnimationFrame(resolve));
        const input = document.querySelector('#file-name-input');
        input.focus();
        input.select();
        return { active: document.activeElement.id, inputInert: !!input.closest('[inert]') };
      });
      assert.deepEqual(focus, { active: 'outside', inputInert: true });
      await page.keyboard.press('Tab');
      assert.equal(await page.evaluate(() => !!document.activeElement.closest('[inert]')), false);
    }));

    for (const mode of ['mkdir', 'rename']) {
      await browserTest.test(`${engine}: native.act submits the real ${mode} form inside an inert overlay`, async () => fixture(async page => {
        await openName(page, mode);
        await page.evaluate(mode => {
          nativeAct('名称', mode === 'mkdir' ? 'synthetic-folder' : 'synthetic-renamed.txt');
          nativeAct(mode === 'mkdir' ? '创建' : '保存');
        }, mode);
        await page.waitForFunction(() => evidence.reloads === 1);
        const result = await page.evaluate(() => ({ ...evidence, closed: document.querySelector('#file-name-modal').hidden, projectionClosed: projected() === null }));
        assert.deepEqual(result.requests, [{ device: 'm2', route: `file/${mode}`, body: {
          path: mode === 'mkdir' ? '/synthetic' : '/synthetic/old.txt', name: mode === 'mkdir' ? 'synthetic-folder' : 'synthetic-renamed.txt',
        } }]);
        assert.deepEqual(result.submits, ['file-name-submit']);
        assert.equal(result.closed && result.projectionClosed, true);
      }));
    }

    await browserTest.test(`${engine}: inert form still rejects invalid input and accepts a corrected retry`, async () => fixture(async page => {
      await openName(page);
      const invalid = await page.evaluate(() => {
        let rejected = false;
        try { nativeAct('创建'); } catch (error) { rejected = error.message.includes('有效的表单'); }
        return { rejected, requests: evidence.requests.length, submits: evidence.submits.length, active: document.activeElement.id };
      });
      assert.deepEqual(invalid, { rejected: true, requests: 0, submits: 0, active: 'outside' });
      await page.evaluate(() => { nativeAct('名称', 'synthetic-corrected'); nativeAct('创建'); });
      await page.waitForFunction(() => evidence.reloads === 1);
      assert.equal(await page.evaluate(() => evidence.requests[0].body.name), 'synthetic-corrected');
    }));

    await browserTest.test(`${engine}: native.act element.click invokes the real delete handler under inert`, async () => fixture(async page => {
      await page.evaluate(() => {
        confirmFileDelete({ name: 'synthetic-delete.txt', path: '/synthetic/delete.txt', kind: 'file' });
        FleetNativeDialogs.publish();
        if (!document.querySelector('#confirm-file-delete').inert) throw new Error('Fixture is not inert');
        nativeAct('删除');
      });
      await page.waitForFunction(() => evidence.reloads === 1);
      assert.deepEqual(await page.evaluate(() => evidence.requests), [{ device: 'm2', route: 'file/delete', body: { path: '/synthetic/delete.txt', recursive: false } }]);
      assert.equal(await page.evaluate(() => projected() === null), true);
    }));

    await browserTest.test(`${engine}: checkbox assignment and element.click both run the real hidden-file onchange handler`, async () => fixture(async page => {
      await page.evaluate(() => {
        document.querySelector('#file-settings-menu').hidden = false;
        FleetNativeDialogs.publish();
        if (!document.querySelector('#file-settings-menu').inert) throw new Error('Fixture is not inert');
        nativeAct('显示隐藏文件', 'true');
      });
      const assigned = await page.evaluate(() => ({ checked: document.querySelector('#file-show-hidden').checked, businessValue: state.fileShowHidden, events: [...evidence.checkboxEvents], persisted: evidence.persisted, rendered: evidence.rendered }));
      assert.deepEqual(assigned, { checked: true, businessValue: true, events: ['input', 'change'], persisted: 1, rendered: 1 });
      await page.evaluate(() => nativeAct('显示隐藏文件', 'false'));
      await page.evaluate(() => nativeAct('显示隐藏文件'));
      const clicked = await page.evaluate(() => ({ checked: document.querySelector('#file-show-hidden').checked, businessValue: state.fileShowHidden, events: evidence.checkboxEvents.slice(-3), persisted: evidence.persisted, rendered: evidence.rendered }));
      assert.deepEqual(clicked, { checked: true, businessValue: true, events: ['click', 'input', 'change'], persisted: 3, rendered: 3 });
    }));
  } finally { await browser.close(); }
});

test('file menus project the trigger rectangle and Web view icon identities', () => {
  const current = harness();
  const trigger = element('button', { id: 'file-settings' });
  trigger.getBoundingClientRect = () => ({ x: 300, y: 20, width: 44, height: 44 });
  current.attach(trigger);
  current.open('file-settings-menu',
    element('button', { 'data-file-view': 'icons', 'aria-label': '图标视图' }),
    element('button', { 'data-file-view': 'list', 'aria-label': '列表视图', 'aria-pressed': 'true' }),
    element('button', { 'data-file-view': 'columns', 'aria-label': '分栏视图' }));
  assert.deepEqual(current.payload.anchor, { x: 300, y: 20, width: 44, height: 44 });
  assert.deepEqual(current.payload.items.map(item => item.icon), ['FileIcons', 'FileList', 'FileColumns']);
  assert.equal(current.payload.items[1].selected, true);
});
