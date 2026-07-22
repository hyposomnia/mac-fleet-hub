import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const src = await readFile(new URL('./chat_model.js', import.meta.url), 'utf8');
const appSrc = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const serviceWorker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
const markedSrc = await readFile(new URL('./vendor/marked.min.js', import.meta.url), 'utf8');
const sandbox = { globalThis: {} };
vm.createContext(sandbox);
vm.runInContext(src, sandbox);
const { createChatState, appendUserMessage, prependHistory, reduceChatEvent, normalizeDiffFiles } = sandbox.globalThis.FleetChatModel;

const appSandbox = { document: { addEventListener() {} }, EventSource: { CLOSED: 2 } };
vm.createContext(appSandbox);
vm.runInContext(`${appSrc}\n;globalThis.__chatCacheTest = { chatCacheVictim, isChatConnectionKept, updateChatUpdatedAt, chatAssistantMetaText, chatUserMetaText, state };`, appSandbox);
const { chatCacheVictim, isChatConnectionKept, updateChatUpdatedAt, chatAssistantMetaText, chatUserMetaText, state: appState } = appSandbox.__chatCacheTest;

test('chat model and app use the same versioned shell URLs', () => {
  const styleURL = indexHTML.match(/style\.css\?v=([a-zA-Z0-9_-]+)/);
  const markdownURL = indexHTML.match(/markdown\.js\?v=([a-zA-Z0-9_-]+)/);
  const modelURL = indexHTML.match(/chat_model\.js\?v=([a-zA-Z0-9_-]+)/);
  const appURL = indexHTML.match(/app\.js\?v=([a-zA-Z0-9_-]+)/);
  assert.ok(styleURL);
  assert.ok(markdownURL);
  assert.ok(modelURL);
  assert.ok(appURL);
  assert.equal(styleURL[1], appURL[1]);
  assert.equal(markdownURL[1], appURL[1]);
  assert.equal(modelURL[1], appURL[1]);
  assert.match(serviceWorker, new RegExp(`/style\\.css\\?v=${styleURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/markdown\\.js\\?v=${markdownURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/chat_model\\.js\\?v=${modelURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/app\\.js\\?v=${appURL[1]}`));
  assert.match(indexHTML, /vendor\/purify\.min\.js\?v=3\.2\.6/);
  assert.match(indexHTML, /vendor\/marked\.min\.js\?v=15\.0\.12/);
  assert.match(serviceWorker, /vendor\/purify\.min\.js\?v=3\.2\.6/);
  assert.match(serviceWorker, /vendor\/marked\.min\.js\?v=15\.0\.12/);
  const markdownScript = indexHTML.indexOf('<script src="markdown.js');
  assert.ok(indexHTML.indexOf('<script src="vendor/purify.min.js') < markdownScript);
  assert.ok(indexHTML.indexOf('<script src="vendor/marked.min.js') < markdownScript);
  assert.ok(markdownScript < indexHTML.indexOf('<script src="app.js'));
});

test('Codex is the first and default session assistant', () => {
  const tabs = [...indexHTML.matchAll(/<button data-assistant="([^"]+)" role="tab" aria-selected="([^"]+)">/g)]
    .map((match) => ({ assistant: match[1], selected: match[2] }));
  assert.deepEqual(tabs, [
    { assistant: 'codex', selected: 'true' },
    { assistant: 'claude', selected: 'false' },
  ]);
  assert.match(appSrc, /assistant:\s*'codex',\s*\/\/ claude \| codex/);
});

test('chat cache evicts the earliest updated non-current session', () => {
  const oldest = { updatedAt: 100, lastUsed: 999 };
  const newest = { updatedAt: 300, lastUsed: 1 };
  const current = { updatedAt: 50, lastUsed: 0 };
  const cache = new Map([['oldest', oldest], ['newest', newest], ['current', current]]);
  const victim = chatCacheVictim(cache, current);
  assert.equal(victim.key, 'oldest');
  assert.equal(victim.chat, oldest);
});

test('chat cache update time is monotonic and green status follows EventSource state', () => {
  const chat = { updatedAt: 200, events: { readyState: 1 } };
  updateChatUpdatedAt(chat, 100);
  assert.equal(chat.updatedAt, 200);
  updateChatUpdatedAt(chat, 300);
  assert.equal(chat.updatedAt, 300);

  appState.chatCache.set('m1\ns1', chat);
  assert.equal(isChatConnectionKept('m1', 's1'), true);
  chat.events.readyState = 2;
  assert.equal(isChatConnectionKept('m1', 's1'), false);
  appState.chatCache.clear();
});

test('vendored markdown parser formats common assistant response blocks', () => {
  const markdownSandbox = {};
  vm.createContext(markdownSandbox);
  vm.runInContext(markedSrc, markdownSandbox);
  const html = markdownSandbox.marked.parse('## 变更\n\n- **安全**链接\n\n```sh\nnode --test\n```');
  assert.match(html, /<h2>变更<\/h2>/);
  assert.match(html, /<li><strong>安全<\/strong>链接<\/li>/);
  assert.match(html, /<pre><code class="language-sh">node --test/);
});

test('assistant deltas merge by item id', () => {
  let state = createChatState();
  state = reduceChatEvent(state, { type: 'assistant_delta', itemId: 'a1', data: { delta: 'he' } });
  state = reduceChatEvent(state, { type: 'assistant_delta', itemId: 'a1', data: { delta: 'llo' } });
  assert.equal(state.messages.length, 1);
  assert.equal(state.items.a1.text, 'hello');
});

test('tool output appends to command card', () => {
  let state = createChatState();
  state = reduceChatEvent(state, { type: 'tool_delta', itemId: 'cmd1', data: { stream: 'stdout', delta: 'pwd\n' } });
  state = reduceChatEvent(state, { type: 'tool_delta', itemId: 'cmd1', data: { stream: 'stdout', delta: '/tmp\n' } });
  assert.equal(state.items.cmd1.type, 'tool');
  assert.equal(state.items.cmd1.output, 'pwd\n/tmp\n');
});

test('generic tool updates preserve kind, summary, status, and details', () => {
  let state = createChatState();
  state = reduceChatEvent(state, {
    type: 'tool_update', itemId: 'mcp1', data: {
      kind: 'mcpToolCall', title: 'Notion · Search', summary: 'fleet',
      detail: '{"query":"fleet"}', status: 'inProgress', durationMs: 80,
    },
  });
  state = reduceChatEvent(state, {
    type: 'tool_delta', itemId: 'mcp1', data: { kind: 'mcpToolCall', message: '正在读取页面' },
  });
  state = reduceChatEvent(state, {
    type: 'tool_update', itemId: 'mcp1', data: { kind: 'mcpToolCall', status: 'completed', output: '完成' },
  });
  assert.equal(state.items.mcp1.type, 'tool');
  assert.equal(state.items.mcp1.kind, 'mcpToolCall');
  assert.equal(state.items.mcp1.title, 'Notion · Search');
  assert.equal(state.items.mcp1.summary, 'fleet');
  assert.equal(state.items.mcp1.status, 'completed');
  assert.equal(state.items.mcp1.output, '完成');
  assert.equal(state.items.mcp1.progress, '正在读取页面');
});

test('file changes keep paths and count added and deleted diff lines', () => {
  const state = reduceChatEvent(createChatState(), {
    type: 'diff_update',
    itemId: 'diff1',
    data: {
      changes: [{
        path: 'server/dashboard/app.js',
        kind: 'update',
        diff: '--- a/server/dashboard/app.js\n+++ b/server/dashboard/app.js\n@@ -1,3 +1,4 @@\n-old\n+new\n+another\n context',
      }],
    },
  });
  assert.deepEqual(
    JSON.parse(JSON.stringify(state.items.diff1.files)),
    [{ path: 'server/dashboard/app.js', additions: 2, deletions: 1 }],
  );
});

test('file change normalization accepts explicit line counts', () => {
  const files = normalizeDiffFiles({ files: [{ filePath: 'new.txt', addedLines: 8, deletedLines: 0 }] });
  assert.deepEqual(JSON.parse(JSON.stringify(files)), [{ path: 'new.txt', additions: 8, deletions: 0 }]);
});

test('approval keeps app-server request id', () => {
  const state = reduceChatEvent(createChatState(), {
    type: 'approval_request',
    itemId: 'cmd1',
    data: { requestId: 42, itemId: 'cmd1', command: 'pwd', cwd: '/tmp' },
  });
  assert.equal(state.items['42'].type, 'approval');
  assert.equal(state.items['42'].requestId, '42');
  assert.equal(state.approvals['42'].command, 'pwd');
});

test('local user message is appended immediately', () => {
  const state = appendUserMessage(createChatState(), 'hello', 'u1');
  assert.deepEqual(Array.from(state.messages), ['u1']);
  assert.equal(state.items.u1.text, 'hello');
  assert.equal(Number.isFinite(state.items.u1.sentAtMs), true);
});

test('local user message keeps image attachments', () => {
  const state = appendUserMessage(createChatState(), '', 'u-img', [{ id: 'img1', url: 'blob:img1', name: 'shot.png' }]);
  assert.equal(state.items['u-img'].type, 'user');
  assert.equal(state.items['u-img'].images.length, 1);
  assert.equal(state.items['u-img'].images[0].name, 'shot.png');
});

test('history is prepended chronologically and deduplicated against live items', () => {
  let state = reduceChatEvent(createChatState(), { type: 'assistant_delta', itemId: 'a2', data: { delta: 'live' } });
  state = prependHistory(state, [
    { type: 'user_done', itemId: 'u1', data: { text: 'old question' } },
    { type: 'assistant_done', itemId: 'a2', data: { text: 'persisted' } },
  ]);
  assert.deepEqual(Array.from(state.messages), ['u1', 'a2']);
  assert.equal(state.items.u1.text, 'old question');
  assert.equal(state.items.a2.text, 'live');
});

test('user and assistant message metadata is normalized for rendering', () => {
  let state = createChatState();
  state = reduceChatEvent(state, {
    type: 'user_done', itemId: 'u1', turnId: 't1',
    data: { text: 'old question', createdAtMs: new Date(2026, 6, 22, 21, 42, 10).getTime() },
  });
  state = reduceChatEvent(state, {
    type: 'assistant_delta', itemId: 'a1', turnId: 't1',
    data: { delta: 'he', startedAtMs: new Date(2026, 6, 22, 21, 43, 0).getTime(), model: 'gpt-5.6-sol', reasoningEffort: 'xhigh' },
  });
  state = reduceChatEvent(state, {
    type: 'assistant_done', itemId: 'a1', turnId: 't1',
    data: { text: 'hello', completedAtMs: new Date(2026, 6, 22, 21, 44, 31).getTime(), usage: { inputTokens: 3807, outputTokens: 89 } },
  });
  assert.equal(chatUserMetaText(state.items.u1), '用户：2026-07-22 21:42:10');
  assert.equal(chatAssistantMetaText(state.items.a1), 'AI：gpt-5.6-sol, xhigh  |  in 3,807 / out 89  |  2026-07-22 21:44:31');
  assert.equal(state.items.a1.durationMs, 91000);
});

test('turn completion metadata backfills the last assistant message in that turn', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'assistant_delta', itemId: 'a1', turnId: 't1',
    data: { delta: 'done' },
  });
  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 't1',
    data: {
      turn: { id: 't1', model: 'gpt-new', reasoningEffort: 'high', completedAtMs: 1784730000000, usage: { input_tokens: 12, output_tokens: 3 } },
    },
  });
  assert.equal(state.items.a1.model, 'gpt-new');
  assert.equal(state.items.a1.effort, 'high');
  assert.deepEqual(JSON.parse(JSON.stringify(state.items.a1.usage)), { inputTokens: 12, outputTokens: 3 });
  assert.equal(state.items.a1.completedAtMs, 1784730000000);
});

test('completed command history becomes a tool card', () => {
  const state = prependHistory(createChatState(), [
    { type: 'tool_done', itemId: 'cmd1', data: { command: 'pwd', cwd: '/tmp', output: '/tmp\n', status: 'completed' } },
  ]);
  assert.equal(state.items.cmd1.type, 'tool');
  assert.equal(state.items.cmd1.title, '运行命令');
  assert.equal(state.items.cmd1.summary, 'pwd');
  assert.equal(state.items.cmd1.output, '/tmp\n');
});
