import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const source = await readFile(new URL('./compact_composer.js', import.meta.url), 'utf8');
function setup() {
  const nodes = Object.fromEntries(['win', 'chat-pane', 'chat-composer', 'chat-input', 'chat-preview-output', 'chat-preview-output-text', 'chat-preview-output-icon'].map(id => [id, {
    dataset: {}, hidden: false, style: {}, attrs: {}, events: {}, textContent: '',
    addEventListener(name, fn) { this.events[name] = fn; },
    setAttribute(name, value) { this.attrs[name] = value; },
  }]));
  const input = nodes['chat-input']; input.value = '第一行草稿\n第二行草稿';
  const observers = [];
  const context = {document: {activeElement: null, hidden: false, querySelector: s => nodes[s.slice(1)] || null},
    MutationObserver: class { constructor(fn) { observers.push(fn); } observe() {} },
  };
  vm.runInNewContext(source, context);
  let reads = 0, returns = 0;
  const ui = context.FleetCompactComposer.init({onRead() { reads++; }, onReturn() { returns++; }});
  return {nodes, input, ui, api: context.FleetCompactComposer, context, sync: () => observers.forEach(fn => fn()), get reads() { return reads; }, get returns() { return returns; }};
}
const model = (text = '第一行\n最近一行\n') => ({messages: ['u', 'a', 'r', 't'], items: {
  u: {type: 'user', text: '追加输入'}, a: {type: 'assistant', text},
  r: {type: 'reasoning', summary: '隐藏推理'}, t: {type: 'tool', output: '子代理内部输出'},
}});

test('summary uses the latest nonempty assistant line, never user, reasoning or tool output', () => {
  const {api} = setup();
  assert.equal(api.outputSummary({model: model(), running: true}).text, '最近一行');
  assert.equal(api.outputSummary({model: model(), running: true}).status, 'running');
  assert.equal(api.outputSummary({model: model(), unread: true}).status, 'unread');
  assert.equal(api.outputSummary({model: model()}).status, 'complete');
  assert.equal(api.outputSummary({model: model('')}).text, '');
});

test('preview focus expands, blur collapses and preserves a multiline draft', () => {
  const s = setup(); s.nodes.win.dataset.workspacePreview = 'true'; s.sync();
  s.input.events.focus();
  assert.equal(s.nodes.win.dataset.compactComposerExpanded, 'true');
  s.input.events.blur();
  assert.equal(s.nodes.win.dataset.compactComposerExpanded, 'false');
  assert.equal(s.input.value, '第一行草稿\n第二行草稿');
  s.nodes.win.dataset.workspacePreview = 'false'; s.sync(); s.input.events.focus();
  assert.equal(s.nodes.win.dataset.compactComposerExpanded, 'false');
});

test('background completion stays unread until returning to visible conversation', () => {
  const s = setup(); s.nodes.win.dataset.workspacePreview = 'true'; s.sync();
  s.ui.update({model: model(), running: true, unread: false});
  assert.equal(s.nodes['chat-preview-output'].dataset.state, 'running');
  s.ui.update({model: model(), running: false, unread: true});
  assert.equal(s.nodes['chat-preview-output'].dataset.state, 'unread');
  assert.equal(s.nodes['chat-preview-output-text'].textContent, '最近一行');
  assert.equal(s.reads, 0);
  s.nodes['chat-preview-output'].events.click(); assert.equal(s.returns, 1);
  s.nodes.win.dataset.workspacePreview = 'false'; s.sync();
  assert.equal(s.reads, 1);
  assert.equal(s.nodes['chat-preview-output'].hidden, true);
});

test('closed conversation and hidden browser do not acknowledge replies, no output hides summary', () => {
  const s = setup(); s.nodes['chat-pane'].hidden = true; s.ui.update({model: model(), unread: true});
  s.sync(); assert.equal(s.reads, 0); assert.equal(s.nodes['chat-preview-output'].hidden, true);
  s.nodes['chat-pane'].hidden = false; s.context.document.hidden = true; s.sync(); assert.equal(s.reads, 0);
  s.context.document.hidden = false; s.nodes.win.dataset.workspacePreview = 'true'; s.ui.update({model: model('')});
  assert.equal(s.nodes['chat-preview-output'].hidden, true);
});

test('reset and conversation switch replace private summary text instead of retaining it', () => {
  const s = setup(); s.nodes.win.dataset.workspacePreview = 'true';
  s.ui.update({model: model(), unread: true});
  s.ui.update({model: model('另一个会话的输出')});
  assert.equal(s.nodes['chat-preview-output-text'].textContent, '另一个会话的输出');
  s.ui.update();
  assert.equal(s.nodes['chat-preview-output-text'].textContent, '');
  assert.equal(s.nodes['chat-preview-output'].hidden, true);
  assert.doesNotMatch(s.nodes['chat-preview-output'].attrs['aria-label'], /最近一行|另一个会话/);
});
