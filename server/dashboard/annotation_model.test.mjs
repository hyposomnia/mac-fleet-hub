import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';
const scope = { globalThis: {}, TextEncoder };
vm.createContext(scope);
try {
  vm.runInContext(await readFile(new URL('./annotation_model.js', import.meta.url), 'utf8'), scope);
} catch (e) {
  if (e.code !== 'ENOENT') throw e;
}
vm.runInContext(await readFile(new URL('./chat_model.js', import.meta.url), 'utf8'), scope);
const A = scope.globalThis.FleetAnnotations;
const native = await readFile(
  new URL('../../mac/fleet-agent/testdata/annotations/codex-v1.txt', import.meta.url),
  'utf8'
);

test('A09/A13 native wire reader restores snapshots and request, rejects quoted or malformed control data', () => {
  assert.ok(A, 'annotation model must be available');
  const parsed = A.readWire(native);
  assert.equal(parsed.text, '继续');
  assert.equal(parsed.annotations.version, 'codex-response-annotations-v1');
  assert.equal(parsed.annotations.items[0].text, '中文😀');
  assert.equal(parsed.annotations.items[0].source.endOffset, 6);
  for (const value of [
    '```text\n' + native + '\n```',
    'quoted ' + native,
    native.replace('"startOffset":2', '"startOffset":20'),
    native.replace('[{"text":', '[{"bad":'),
  ])
    assert.equal(A.readWire(value), null);
});

test('A08 snapshot binding survives new drafts/turns and never guesses between steered inputs', () => {
  const M = scope.globalThis.FleetChatModel;
  let state = M.createChatState();
  state = M.reduceChatEvent(state, { type: 'user_done', itemId: 'u1', turnId: 't1', data: { text: native } });
  state = M.reduceChatEvent(state, {
    type: 'assistant_done',
    itemId: 'a1',
    turnId: 't1',
    data: { text: 'answer :codex-annotation{index="1"}' },
  });
  assert.equal(state.items.u1.text, '继续');
  assert.equal(A.contextFor(state, 'a1', 'codex').candidates[0].items[0].text, '中文😀');
  state = M.reduceChatEvent(state, {
    type: 'user_done',
    itemId: 'u2',
    turnId: 't1',
    data: { text: native.replace('中文😀', 'next') },
  });
  state = M.reduceChatEvent(state, {
    type: 'assistant_done',
    itemId: 'a2',
    turnId: 't1',
    data: { text: 'after steer' },
  });
  assert.equal(A.contextFor(state, 'a1', 'codex').candidates.length, 1);
  assert.equal(A.contextFor(state, 'a2', 'codex').ambiguous, true);
  state = M.reduceChatEvent(state, {
    type: 'user_done',
    itemId: 'u3',
    turnId: 't2',
    data: { text: native.replace('中文😀', 'last') },
  });
  state = M.reduceChatEvent(state, {
    type: 'assistant_done',
    itemId: 'a3',
    turnId: 't2',
    data: { text: 'new turn' },
  });
  assert.equal(A.contextFor(state, 'a3', 'codex').candidates[0].items[0].text, 'last');
  assert.equal(A.contextFor(state, 'a1', 'codex').candidates[0].items[0].text, '中文😀');
});

test('A10 locator distinguishes repeated text and fails closed when context changed', () => {
  assert.equal(
    A.locate('first x second x', {
      exact: 'x',
      startOffset: 6,
      endOffset: 7,
      version: A.textVersion('first x second x'),
    }),
    6
  );
  assert.equal(A.locate('x and x', { exact: 'x', version: 'old', prefix: '', suffix: '' }), -1);
  assert.equal(
    A.locate('inserted left x right', { exact: 'x', version: 'old', prefix: 'left ', suffix: ' right' }),
    14
  );
});

test('A05 immediate reload recovers an uncommitted draft journal without crossing account or conversation', async () => {
  const values = new Map();
  scope.globalThis.localStorage = {
    get length() {
      return values.size;
    },
    key: (i) => [...values.keys()][i],
    getItem: (key) => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: (key) => values.delete(key),
  };
  const chat = {
    macId: 'm1',
    assistant: 'codex',
    sessionId: 'draft-a',
    draft: '即刻刷新',
    annotations: [{ id: 'note', text: '原文', annotation: '评论' }],
  };
  const saving = A.saveDraft('alice', chat).catch(() => {});
  const recovered = await A.loadDraft('alice', chat).catch(() => null);
  assert.ok(recovered, 'pending durable journal is missing');
  assert.equal(recovered.text, '即刻刷新');
  assert.equal(recovered.items[0].annotation, '评论');
  assert.equal(await A.loadDraft('bob', chat).catch(() => null), null);
  assert.equal(await A.loadDraft('alice', { ...chat, sessionId: 'other' }).catch(() => null), null);
  await saving;
});

test('A08 a continued output item after steer becomes explicit ambiguity; unchanged replay does not rebind older output', () => {
  const M = scope.globalThis.FleetChatModel;
  let state = M.createChatState();
  state = M.reduceChatEvent(state, { type: 'user_done', itemId: 'u1', turnId: 't', data: { text: native } });
  state = M.reduceChatEvent(state, {
    type: 'assistant_delta',
    itemId: 'a',
    turnId: 't',
    data: { delta: 'before ' },
  });
  state = M.reduceChatEvent(state, {
    type: 'user_done',
    itemId: 'u2',
    turnId: 't',
    data: { text: native.replace('中文😀', 'next') },
  });
  state = M.reduceChatEvent(state, {
    type: 'assistant_delta',
    itemId: 'a',
    turnId: 't',
    data: { delta: 'after :codex-annotation{index="1"}' },
  });
  assert.equal(A.contextFor(state, 'a', 'codex').ambiguous, true);
  assert.equal(A.contextFor(state, 'a', 'codex').candidates.length, 2);
  let older = M.createChatState();
  older = M.reduceChatEvent(older, {
    type: 'user_done',
    itemId: 'old-u',
    turnId: 't',
    data: { text: native },
  });
  older = M.reduceChatEvent(older, {
    type: 'assistant_done',
    itemId: 'old-a',
    turnId: 't',
    data: { text: 'finished' },
  });
  older = M.reduceChatEvent(older, {
    type: 'user_done',
    itemId: 'new-u',
    turnId: 't',
    data: { text: native.replace('中文😀', 'next') },
  });
  older = M.reduceChatEvent(older, {
    type: 'assistant_done',
    itemId: 'old-a',
    turnId: 't',
    data: { text: 'finished' },
  });
  assert.equal(A.contextFor(older, 'old-a', 'codex').candidates.length, 1);
});

test('A08 DeepSeek inbox messages without turn IDs bind at turn boundaries', () => {
  const M = scope.globalThis.FleetChatModel;
  let state = M.createChatState();
  const fleet = native
    .replace('an earlier Codex response', 'an earlier assistant response')
    .replaceAll('codex-annotation', 'fleet-annotation');
  for (const n of [1, 2]) {
    state = M.reduceChatEvent(state, {
      type: 'user_done',
      itemId: `du${n}`,
      data: { text: fleet.replace('中文😀', `第${n}轮字`) },
    });
    state = M.reduceChatEvent(state, { type: 'turn_started', turnId: `dt${n}`, data: {} });
    state = M.reduceChatEvent(state, {
      type: 'assistant_done',
      itemId: `da${n}`,
      turnId: `dt${n}`,
      data: { text: 'answer' },
    });
    state = M.reduceChatEvent(state, { type: 'turn_done', turnId: `dt${n}`, data: { status: 'completed' } });
  }
  assert.equal(A.contextFor(state, 'da1', 'dsh').candidates[0].items[0].text, '第1轮字');
  assert.equal(A.contextFor(state, 'da2', 'dsh').candidates.length, 1);
  assert.equal(A.contextFor(state, 'da2', 'dsh').candidates[0].items[0].text, '第2轮字');
});

test('A07 canonical indices never resolve 0, padded values, unsafe integers or injected attributes', () => {
  for (const value of ['0', '01', '-1', '1.0', '1e2', '9007199254740992', '1" onclick="x'])
    assert.equal(A.canonicalIndex(value), false, value);
  for (const value of ['1', '2', '20']) assert.equal(A.canonicalIndex(value), true, value);
});

test('A09 native reading uses native source spans and does not apply Fleet draft creation limits', () => {
  const span = native.replace('"endOffset":6', '"endOffset":9');
  assert.ok(A.readWire(span), 'native offsets are a location span, not a text-length assertion');
  const many = native.replace(
    /\[\{"text"[^]*?\]\n<\/response-annotations>/,
    JSON.stringify(Array.from({ length: 21 }, () => ({ text: 'quoted' }))) + '\n</response-annotations>'
  );
  const read = A.readWire(many);
  assert.equal(read?.annotations.items.length, 21);
  assert.throws(() => A.validateDraft(read.annotations.items), /20/);
});

test('A05/A10 source navigation stays within the exact device, assistant and conversation', () => {
  const note = { fleetSource: { deviceId: 'm1', assistant: 'codex', sessionId: 't' } };
  assert.equal(A.sourceBelongsTo(note, { macId: 'm1', assistant: 'codex', sessionId: 't' }), true);
  for (const chat of [
    { macId: 'm2', assistant: 'codex', sessionId: 't' },
    { macId: 'm1', assistant: 'dsh', sessionId: 't' },
    { macId: 'm1', assistant: 'codex', sessionId: 'other' },
  ])
    assert.equal(A.sourceBelongsTo(note, chat), false);
  assert.equal(A.sourceBelongsTo(note, null), false);
});

test('A06/A09 structured skill content preserves the native annotation text part and user display body', () => {
  const M = scope.globalThis.FleetChatModel;
  const content = [
    { type: 'skill', name: 'review', path: '/skills/review/SKILL.md' },
    { type: 'text', text: native },
  ];
  let state = M.reduceChatEvent(M.createChatState(), {
    type: 'user_done',
    itemId: 'native-skill',
    turnId: 't',
    data: { text: '$review\n' + native, content },
  });
  assert.equal(state.items['native-skill'].text, '继续');
  assert.equal(state.items['native-skill'].annotations.items.length, 1);
  state = M.reduceChatEvent(M.createChatState(), {
    type: 'user_done',
    itemId: 'fleet-skill',
    turnId: 't',
    data: {
      text: '$review\n' + native,
      content,
      wireText: native,
      displayText: '$review 继续',
      annotations: A.readWire(native).annotations,
    },
  });
  assert.equal(state.items['fleet-skill'].text, '$review 继续');
  assert.equal(state.items['fleet-skill'].wireText, native);
});
