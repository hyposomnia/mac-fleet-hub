import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const model = await readFile(new URL('./annotation_model.js', import.meta.url), 'utf8');
const source = await readFile(new URL('./annotation_ui.js', import.meta.url), 'utf8');

function setup() {
  class Element {
    constructor(tagName = 'div') {
      Object.assign(this, {tagName, children: [], style: {}, dataset: {}, events: new Map(), hidden: false});
    }
    append(...nodes) { this.children.push(...nodes); }
    replaceChildren(...nodes) { this.children = nodes; }
    setAttribute(name, value) { this[name] = value; }
    getBoundingClientRect() { return {left: 20, right: 180, top: 30, bottom: 50, width: 160, height: 20}; }
    closest(selector) { return selector.includes('.annotation-owned') && this.className?.includes('annotation-owned') ? this : null; }
    addEventListener(name, callback) { this.events.set(name, [...(this.events.get(name) || []), callback]); }
    emit(name, event = {}) { for (const callback of this.events.get(name) || []) callback({target: this, pointerId: 1, ...event}); }
    dispatchEvent(event) { this.emit(event.type); }
    focus() {}
  }
  const doc = new Element(), frameDoc = new Element(), target = new Element();
  const nativeSelection = {removeAllRanges() {}, isCollapsed: true, rangeCount: 0};
  Object.assign(doc, {body: new Element(), createElement: tag => new Element(tag), querySelector: () => null, getSelection: () => nativeSelection});
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
  return {doc, chat, menu, editor, emit, flush, markers, highlights,
    select() { selected = true; }, selectionChanged: () => selectionChanged(), changes: () => changed};
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
