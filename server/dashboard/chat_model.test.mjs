import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const src = await readFile(new URL('./chat_model.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const serviceWorker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
const markedSrc = await readFile(new URL('./vendor/marked.min.js', import.meta.url), 'utf8');
const sandbox = { globalThis: {} };
vm.createContext(sandbox);
vm.runInContext(src, sandbox);
const { createChatState, appendUserMessage, prependHistory, reduceChatEvent, normalizeDiffFiles } = sandbox.globalThis.FleetChatModel;

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

test('completed command history becomes a tool card', () => {
  const state = prependHistory(createChatState(), [
    { type: 'tool_done', itemId: 'cmd1', data: { command: 'pwd', cwd: '/tmp', output: '/tmp\n', status: 'completed' } },
  ]);
  assert.equal(state.items.cmd1.type, 'tool');
  assert.equal(state.items.cmd1.title, 'pwd');
  assert.equal(state.items.cmd1.output, '/tmp\n');
});
