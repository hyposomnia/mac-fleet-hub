import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const model = await readFile(new URL('./annotation_model.js', import.meta.url), 'utf8');
const source = await readFile(new URL('./annotation_ui.js', import.meta.url), 'utf8');

function setup() {
  class Element {
    constructor(tagName = 'div') {
      Object.assign(this, {tagName: tagName.toUpperCase(), nodeType: 1, children: [], style: {}, dataset: {}, events: new Map(), hidden: false});
    }
    get childNodes() { return this.children; }
    append(...nodes) { for (const node of nodes) { node.parentElement = this; node.ownerDocument = this.ownerDocument; this.children.push(node); } }
    prepend(node) { this.children.unshift(node); }
    replaceChildren(...nodes) { this.children = nodes; }
    setAttribute(name, value) { this[name] = value; }
    getBoundingClientRect() { return {left: 20, right: 180, top: 30, bottom: 50, width: 160, height: 20}; }
    matches(selector) {
      return (selector.includes('.annotation-owned') && this.className?.includes('annotation-owned')) ||
        (selector.includes('[data-annotation-message]') && !!this.dataset.annotationMessage) ||
        (selector.includes('button:not') && this.tagName === 'BUTTON');
    }
    closest(selector) { return this.matches(selector) ? this : this.parentElement?.closest(selector) || null; }
    contains(node) { return node === this || this.children.some(child => child === node || child.contains?.(node)); }
    addEventListener(name, callback) { this.events.set(name, [...(this.events.get(name) || []), callback]); }
    emit(name, event = {}) { for (const callback of this.events.get(name) || []) callback({target: this, pointerId: 1, ...event}); }
    dispatchEvent(event) { this.emit(event.type); }
    focus() {}
  }
  const doc = new Element(), frameDoc = new Element(), target = new Element();
  let clears = 0;
  const nativeSelection = {removeAllRanges() { clears++; this.isCollapsed = true; this.rangeCount = 0; }, isCollapsed: true, rangeCount: 0};
  const messageNodes = [], dock = new Element();
  Object.assign(doc, {
    body: new Element(), createElement: tag => Object.assign(new Element(tag), {ownerDocument: doc}),
    createElementNS: (_, tag) => new Element(tag),
    createRange: () => ({setStart() {}, setEnd() {}, getClientRects: () => [target.getBoundingClientRect()]}),
    querySelector: selector => selector === '#chat-annotations' ? dock : null,
    querySelectorAll: () => messageNodes,
    getSelection: () => nativeSelection,
  });
  frameDoc.querySelector = () => null;
  let selected = false, changed = 0, sequence = 0, selectionChanged;
  const timers = new Map();
  const context = {
    document: doc, TextEncoder, URL, Event, innerWidth: 1000, innerHeight: 800,
    location: {origin: 'https://fleet.example'}, crypto: {randomUUID: () => `note-${++sequence}`},
    matchMedia: () => ({matches: false}), getSelection: () => nativeSelection, addEventListener() {},
    setTimeout(callback) { const id = ++sequence; timers.set(id, callback); return id; },
    clearTimeout(id) { timers.delete(id); },
  };
  vm.createContext(context);
  vm.runInContext(model, context);
  vm.runInContext(source, context);
  const chat = {macId: 'm1', assistant: 'codex', sessionId: 'session', annotations: []};
  const api = context.FleetAnnotationUI.init({getChat: () => chat, onChange: () => changed++});
  const rect = target.getBoundingClientRect();
  const frame = {
    src: 'https://fleet.example/view?path=test.txt', isConnected: true, hidden: false,
    contentDocument: frameDoc, getBoundingClientRect: () => ({left: 0, top: 0, right: 1000, bottom: 800}),
    contentWindow: {getSelection: () => nativeSelection, FleetPreview: {
      annotationSelection: () => selected ? {text: 'selected text', locator: {exact: 'selected text', startOffset: 0, endOffset: 13}, rect} : null,
      annotationRects: () => [rect],
      clearAnnotationSelection: () => { selected = false; },
      onAnnotationSelection: callback => { selectionChanged = callback; },
    }},
  };
  api.registerPreview(frame, {detail: 'test.txt', url: frame.src}, 'm1\ncodex\nsession');
  const [menu, editor, layer] = doc.body.children;
  const markers = () => layer.children.filter(node => node.className.includes('annotation-marker'));
  const highlights = () => layer.children.filter(node => node.className.includes('annotation-highlight'));
  const emit = (name, event = {}, eventDoc = frameDoc) => eventDoc.emit(name, {target, ...event});
  const flush = () => { while (timers.size) { const [id, callback] = timers.entries().next().value; timers.delete(id); callback(); } };
  return {doc, chat, menu, editor, dock, emit, flush, markers, highlights,
    select() { selected = true; }, selectionChanged: () => selectionChanged(), changes: () => changed,
    clears: () => clears,
    selectNative(messages, {from = 0, to, parentBoundary = false, excludedText = ''} = {}) {
      const texts = [];
      for (const [index, paragraphs] of messages.entries()) {
        const node = doc.createElement('div');
        node.dataset.annotationMessage = `message-${index}`;
        for (const text of paragraphs) {
          const p = doc.createElement('p'), child = {nodeType: 3, data: text};
          p.append(child); node.append(p); texts.push(child);
        }
        if (excludedText) { const button = doc.createElement('button'); button.append({nodeType: 3, data: excludedText}); node.append(button); }
        messageNodes.push(node);
      }
      const last = texts[texts.length - 1];
      const range = {
        startContainer: parentBoundary ? doc.body : texts[0], startOffset: parentBoundary ? 0 : from,
        endContainer: parentBoundary ? doc.body : last, endOffset: parentBoundary ? messages.length : to ?? last.data.length,
        intersectsNode: node => messageNodes.includes(node) || texts.includes(node),
        getBoundingClientRect: () => target.getBoundingClientRect(),
      };
      Object.assign(nativeSelection, {isCollapsed: false, rangeCount: 1, getRangeAt: () => range});
      return {target: messageNodes[0], range};
    },
  };
}

test('drag selection waits for release, including preview callbacks and selectionchange timers', () => {
  const ui = setup();
  ui.emit('pointerdown'); ui.select(); ui.emit('selectionchange'); ui.flush();
  assert.equal(ui.menu.hidden, true);
  ui.selectionChanged(); ui.flush();
  assert.equal(ui.menu.hidden, true);
  ui.emit('pointerup'); ui.flush();
  assert.equal(ui.menu.hidden, false);
  assert.equal(ui.highlights().length, 1);
  assert.equal(ui.markers().length, 0, 'a pending selection must not get an annotation number');
});

test('only saved annotations get numbers, including saved quotes without an optional comment', () => {
  const ui = setup();
  ui.select(); ui.emit('pointerup'); ui.flush();
  ui.menu.children[0].onclick();
  assert.equal(ui.editor.hidden, false);
  assert.equal(ui.markers().length, 0, 'opening the editor does not save an annotation');
  ui.editor.children[0].value = '';
  ui.editor.children[1].onclick();
  assert.equal(ui.chat.annotations.length, 1);
  assert.equal(ui.changes(), 1);
  assert.deepEqual(ui.markers().map(node => node.textContent), ['1']);
  ui.emit('pointerdown'); ui.select(); ui.emit('pointerup'); ui.flush();
  assert.equal(ui.menu.hidden, false);
  assert.deepEqual(ui.markers().map(node => node.textContent), ['1'], 'a second unsaved quote must not create number 2');
});

test('keyboard and native selection handle changes still show the menu without a pointer release', () => {
  const keyboard = setup();
  keyboard.select(); keyboard.emit('keyup', {key: 'Shift'});
  assert.equal(keyboard.menu.hidden, false);
  const handles = setup();
  handles.select(); handles.emit('selectionchange'); handles.flush();
  assert.equal(handles.menu.hidden, false);
});

test('cancelled drags do not leave selection blocked and delayed release tasks cannot interrupt a new drag', () => {
  const ui = setup();
  ui.emit('pointerdown'); ui.select(); ui.emit('selectionchange'); ui.emit('pointercancel'); ui.flush();
  assert.equal(ui.menu.hidden, true);
  ui.select(); ui.emit('selectionchange'); ui.flush();
  assert.equal(ui.menu.hidden, false, 'native handles work again after cancellation');
  ui.emit('pointerdown'); ui.select(); ui.emit('pointerup');
  ui.emit('pointerdown'); ui.select(); ui.flush();
  assert.equal(ui.menu.hidden, true, 'an older release task must not pop up during a newer drag');
  ui.emit('pointerup'); ui.flush();
  assert.equal(ui.menu.hidden, false);
});

test('a pointer held in the conversation also blocks callbacks from registered previews', () => {
  const ui = setup();
  ui.emit('pointerdown', {}, ui.doc); ui.select(); ui.selectionChanged(); ui.flush();
  assert.equal(ui.menu.hidden, true);
  ui.emit('pointerup', {}, ui.doc); ui.flush(); ui.selectionChanged();
  assert.equal(ui.menu.hidden, false);
});

test('whole-paragraph selection accepts parent boundaries and consecutive clicks preserve the native range', () => {
  const ui = setup();
  const {target} = ui.selectNative([['整段文本，带有段尾换行。']], {parentBoundary: true});
  const cleared = ui.clears();
  for (let click = 1; click <= 3; click++) {
    ui.emit('pointerdown', {target, detail: click}, ui.doc);
    assert.equal(ui.clears(), cleared, 'multi-click must not clear the browser selection');
    assert.equal(ui.menu.hidden, true, 'menu still waits for mouse release');
    ui.emit('pointerup', {target, detail: click}, ui.doc); ui.flush();
    assert.equal(ui.menu.hidden, false);
  }
  ui.menu.children[0].onclick(); ui.editor.children[1].onclick();
  assert.equal(ui.chat.annotations[0].text, '整段文本，带有段尾换行。');
  assert.equal(ui.chat.annotations[0].fleetSource.locator.itemId, 'message-0');
  assert.equal(ui.editor.children[0].placeholder, '添加注释');
  const trigger = ui.dock.children[0].children[0];
  assert.equal(trigger.children.some(node => node.className === 'annotation-chip-chevron'), false);
});

test('long multi-paragraph selection preserves inline offsets and excludes toolbar text', () => {
  const ui = setup();
  const long = '长段落内容。'.repeat(1800);
  const {target} = ui.selectNative([['前面内容', long, '末尾内容']], {from: 2, to: 2, excludedText: '复制代码'});
  ui.emit('pointerup', {target}, ui.doc); ui.flush();
  assert.equal(ui.menu.hidden, false);
  ui.menu.children[0].onclick(); ui.editor.children[1].onclick();
  assert.equal(ui.chat.annotations[0].text, `内容\n${long}\n末尾`);
  assert.equal(ui.chat.annotations[0].fleetSource.locator.startOffset, 2);
});

test('selection spanning messages saves each source, ignores a zero-length trailing boundary, and leaves blank clicks closed', () => {
  const ui = setup();
  const {target} = ui.selectNative([['第一条'], ['第二条'], ['未选中']], {to: 0});
  ui.emit('pointerdown', {target}, ui.doc); ui.emit('pointerup', {target}, ui.doc); ui.flush();
  assert.equal(ui.menu.hidden, false);
  ui.menu.children[0].onclick();
  ui.editor.children[0].value = '针对这两段的注释'; ui.editor.children[1].onclick();
  assert.deepEqual(Array.from(ui.chat.annotations, note => [note.text, note.fleetSource.locator.itemId, note.annotation]), [
    ['第一条', 'message-0', '针对这两段的注释'], ['第二条', 'message-1', '针对这两段的注释'],
  ]);
  assert.equal(ui.changes(), 1);
  ui.emit('pointerdown', {}, ui.doc); ui.emit('pointerup', {}, ui.doc); ui.flush();
  assert.equal(ui.menu.hidden, true);
});
