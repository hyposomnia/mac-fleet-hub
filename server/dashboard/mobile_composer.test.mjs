import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const source = await readFile(new URL('./mobile_composer.js', import.meta.url), 'utf8');
const css = await readFile(new URL('./mobile_composer.css', import.meta.url), 'utf8');

function setup({mobile = true, native = false, ready = true, viewport = true} = {}) {
  function element() {
    const events = new Map();
    const properties = new Map();
    return {
      dataset: {}, hidden: false, value: '', selectionStart: 3, selectionEnd: 3, scrollHeight: 44,
      events, style: {height: '80px', setProperty: (key, value) => properties.set(key, value), getPropertyValue: key => properties.get(key)},
      addEventListener(name, callback) { events.set(name, [...(events.get(name) || []), callback]); },
      emit(name, event = {}) { for (const callback of events.get(name) || []) callback(event); },
      classList: {contains: () => false},
    };
  }
  const input = element(), composer = element(), pane = element(), html = element();
  const nodes = {'#chat-input': input, '#chat-composer': composer, '#chat-pane': pane};
  const doc = Object.assign(element(), {
    documentElement: html, activeElement: null, hidden: false, readyState: 'loading',
    querySelector: selector => ready ? nodes[selector] || null : null,
  });
  const viewportNode = Object.assign(element(), {height: 800, width: 390, scale: 1});
  const media = Object.assign(element(), {matches: mobile});
  const frames = [], timers = [], observers = [];
  const context = Object.assign(element(), {
    document: doc, innerHeight: 800, innerWidth: 390,
    visualViewport: viewport ? viewportNode : null, __fleetNativeVersion: native ? 1 : undefined,
    matchMedia: () => media,
    requestAnimationFrame: callback => frames.push(callback), setTimeout: callback => timers.push(callback),
    MutationObserver: class {
      constructor(callback) { this.callback = callback; observers.push(this); }
      observe(target, options) { this.target = target; this.options = options; }
    },
  });
  vm.createContext(context);
  vm.runInContext(source, context);
  const flush = () => {
    while (timers.length) timers.shift()();
    while (frames.length) frames.shift()();
  };
  function focus() { doc.activeElement = input; input.emit('focus'); flush(); }
  function blur() { doc.activeElement = null; input.emit('blur'); flush(); }
  function height(value) { viewportNode.height = value; viewportNode.emit('resize'); flush(); }
  return {input, composer, pane, html, doc, viewport: viewportNode, media, context, observers, flush, focus, blur, height,
    api: context.FleetMobileComposer, mount() { ready = true; doc.emit('DOMContentLoaded'); flush(); }};
}

test('mobile starts collapsed, focus expands and blur restores one line without editing a multiline draft or selection', () => {
  const state = setup();
  state.input.value = '第一行草稿\n第二行草稿';
  const selection = [state.input.selectionStart, state.input.selectionEnd];
  assert.equal(state.html.dataset.mobileComposer, 'true');
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  state.focus();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.blur();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  assert.equal(state.input.value, '第一行草稿\n第二行草稿');
  assert.deepEqual([state.input.selectionStart, state.input.selectionEnd], selection);
  assert.equal(state.input.style.height, '80px');
});

test('visual keyboard dismissal collapses even when WK keeps the textarea focused; tapping resumes editing', () => {
  const state = setup(); state.focus(); state.height(460);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.height(800);
  assert.equal(state.doc.activeElement, state.input);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  state.input.emit('pointerdown'); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.height(460);
  state.height(800);
  state.height(460);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
});

test('native keyboard notifications work when WK resizes its own viewport before page observation', () => {
  const state = setup({mobile: false, native: true}); state.focus();
  assert.equal(state.html.dataset.mobileComposer, 'true');
  state.api.setKeyboardOpen(true);
  state.api.setKeyboardOpen(false); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  state.api.setKeyboardOpen(true); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.api.setKeyboardOpen(null); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
});

test('short viewport changes, pinch zoom and rotation do not masquerade as keyboard dismissal', () => {
  const state = setup(); state.focus(); state.height(730);
  state.height(800);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.viewport.scale = 2; state.height(400); state.height(800);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.viewport.scale = 1; state.height(460);
  state.viewport.width = 844; state.height(390);
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
});

test('input grows to a five-line cap, shrinks after deletion and ignores empty placeholder scroll height', () => {
  const state = setup(); state.input.scrollHeight = 500; state.focus();
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), '44px');
  state.input.value = '很长的草稿\n'.repeat(30); state.input.emit('input'); state.flush();
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), '144px');
  state.input.value = '短草稿'; state.input.scrollHeight = 68; state.input.emit('input'); state.flush();
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), '68px');
  state.input.value = ''; state.input.emit('input'); state.flush();
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), '44px');
});

test('programmatically restored session drafts resize through the original app style update', () => {
  const state = setup(); state.focus();
  state.input.value = '另一个会话\n的草稿'; state.input.scrollHeight = 92; state.input.style.height = '92px';
  const observer = state.observers.find(item => item.target === state.input);
  observer.callback(); state.flush();
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), '92px');
  observer.callback(); state.flush();
  assert.equal(state.input.value, '另一个会话\n的草稿');
});

test('tool pointer holds its position through blur until attachment, model or approval click completes', () => {
  const state = setup(); state.focus();
  let clicks = 0, submits = 0;
  state.composer.addEventListener('click', () => clicks++);
  state.composer.onsubmit = () => submits++;
  state.composer.emit('pointerdown', {target: {id: 'chat-options-trigger'}});
  state.blur();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.doc.emit('pointerup');
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.composer.emit('click'); state.composer.onsubmit(); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  assert.equal(clicks, 1); assert.equal(submits, 1);
});

test('duplicate document-end injection and init add no duplicate listeners, preserving existing business handlers', () => {
  const state = setup();
  const controller = state.api.init();
  const originalSubmit = () => 'queue-or-stop'; state.composer.onsubmit = originalSubmit;
  vm.runInContext(source, state.context);
  assert.equal(state.context.FleetMobileComposer, state.api);
  assert.equal(state.api.init(), controller);
  assert.equal(state.input.events.get('input').length, 1);
  assert.equal(state.input.events.get('focus').length, 1);
  assert.equal(state.doc.events.get('pointerup').length, 1);
  assert.equal(state.composer.onsubmit, originalSubmit);
  assert.equal(state.observers.length, 2);
});

test('desktop keeps its original sizing, and responsive changes enable and disable the shared layout', () => {
  const state = setup({mobile: false}); state.focus();
  assert.equal(state.html.dataset.mobileComposer, 'false');
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  assert.equal(state.input.style.height, '80px');
  assert.equal(state.input.style.getPropertyValue('--mobile-composer-input-height'), undefined);
  state.media.matches = true; state.media.emit('change'); state.flush();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'true');
  state.media.matches = false; state.media.emit('change'); state.flush();
  assert.equal(state.html.dataset.mobileComposer, 'false');
});

test('hidden panes and background documents collapse while retaining drafts; early web loading initializes at DOM ready', () => {
  const state = setup({ready: false});
  assert.equal(state.api.init(), undefined); state.mount(); state.focus();
  state.input.value = '保留'; state.pane.hidden = true; state.api.sync();
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  state.pane.hidden = false; state.doc.hidden = true; state.doc.emit('visibilitychange');
  assert.equal(state.composer.dataset.mobileComposerExpanded, 'false');
  assert.equal(state.input.value, '保留');
});

test('ordinary web loads the same independently injectable resources, scoped to mobile composer controls', async () => {
  const html = await readFile(new URL('./index.html', import.meta.url), 'utf8');
  assert.match(html, /href="mobile_composer\.css\?v=1"/);
  assert.match(html, /src="mobile_composer\.js\?v=1"/);
  for (const rule of css.split('}').filter(rule => rule.trim())) assert.match(rule.trim(), /^html\[data-mobile-composer="true"\]/);
  assert.match(css, /#chat-composer\[data-mobile-composer-ready="true"\]\s*\{\s*background: transparent;/);
  assert.match(css, /#chat-input\s*\{[^}]*height: 44px !important;/);
  assert.match(css, /data-mobile-composer-expanded="true"\][^{]*#chat-input\s*\{[^}]*grid-column: 1 \/ -1;[^}]*max-height: 144px;/);
  assert.doesNotMatch(source, /innerHTML|replaceChildren|preventDefault|stopPropagation|input\.value\s*=/);
});
