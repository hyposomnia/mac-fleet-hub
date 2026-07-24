import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const src = await readFile(new URL('./chat_model.js', import.meta.url), 'utf8');
const appSrc = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const markdownSrc = await readFile(new URL('./markdown.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const styleCSS = await readFile(new URL('./style.css', import.meta.url), 'utf8');
const serviceWorker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
const markedSrc = await readFile(new URL('./vendor/marked.min.js', import.meta.url), 'utf8');
const sandbox = { globalThis: {} };
vm.createContext(sandbox);
vm.runInContext(src, sandbox);
const { createChatState, appendUserMessage, removeMessage, prependHistory, reduceChatEvent, normalizeDiffFiles, chatPhase, followupAckId, uuidV7TimeMs } = sandbox.globalThis.FleetChatModel;

const fixedAppNowMs = new Date(2026, 6, 23, 23, 0, 0).getTime();
class FixedAppDate extends Date {
  constructor(...args) { super(...(args.length ? args : [fixedAppNowMs])); }
  static now() { return fixedAppNowMs; }
  static parse(value) { return Date.parse(value); }
  static UTC(...args) { return Date.UTC(...args); }
}
function testElement(tag) {
  return {
    nodeType: 1,
    tagName: tag,
    className: '',
    dataset: {},
    attributes: {},
    children: [],
    textContent: '',
    append(...nodes) { this.children.push(...nodes); },
    appendChild(node) { this.children.push(node); return node; },
    setAttribute(name, value) { this.attributes[name] = String(value); },
  };
}
function testTextNode(text) {
  return { nodeType: 3, textContent: String(text), children: [] };
}
function nodeText(node) {
  if (!node) return '';
  return String(node.textContent || '') + (node.children || []).map(nodeText).join('');
}
const appSandbox = {
  document: {
    addEventListener() {},
    createElement: testElement,
    createElementNS: (_ns, tag) => testElement(tag),
    createTextNode: testTextNode,
  },
  EventSource: { CLOSED: 2 },
  Date: FixedAppDate,
};
vm.createContext(appSandbox);
vm.runInContext(`${appSrc}\n;globalThis.__chatCacheTest = { chatCacheVictim, isChatConnectionKept, updateChatUpdatedAt, formatChatDate, chatAssistantMetaText, chatUserMetaText, chatMessageMetaVisibility, applyChatMetadataDefaults, enqueueChatFollowup, removeChatFollowup, normalizeChatDraft, chatToolStatus, chatToolDuration, chatToolActivityLabel, chatToolHasExpandableBody, chatActivityGroupSummaryText, chatActivityActiveSummarySegments, renderChatActivityGroup, isInternalChatTool: typeof isInternalChatTool === 'function' ? isInternalChatTool : () => false, state };`, appSandbox);
const { chatCacheVictim, isChatConnectionKept, updateChatUpdatedAt, formatChatDate, chatAssistantMetaText, chatUserMetaText, chatMessageMetaVisibility, applyChatMetadataDefaults, enqueueChatFollowup, removeChatFollowup, normalizeChatDraft, chatToolStatus, chatToolDuration, chatToolActivityLabel, chatToolHasExpandableBody, chatActivityGroupSummaryText, chatActivityActiveSummarySegments, renderChatActivityGroup, isInternalChatTool, state: appState } = appSandbox.__chatCacheTest;
function toolLabelText(item) {
  const status = chatToolStatus(item.status);
  return chatToolActivityLabel(item, status, chatToolDuration(item.durationMs)).map(nodeText).join('');
}
const directiveSandbox = { globalThis: {} };
vm.createContext(directiveSandbox);
vm.runInContext(markdownSrc, directiveSandbox);
const parseCodexDirective = directiveSandbox.globalThis.FleetMarkdown.parseCodexDirective || (() => null);
const splitCodexContent = directiveSandbox.globalThis.FleetMarkdown.splitCodexContent || (() => []);

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

test('dashboard typography uses one UI scale and reserves monospace for technical content', () => {
  const typeTokens = Object.fromEntries(
    [...styleCSS.matchAll(/--t-([a-z]+):\s*([^;]+);/g)]
      .map(([, name, value]) => [name, value.trim()]),
  );
  assert.deepEqual(typeTokens, {
    caption: '11px',
    secondary: '12px',
    body: '13px',
    title: '15px',
    display: '17px',
  });
  assert.doesNotMatch(styleCSS, /--t-(?:2xs|xs|sm|base|md|lg|xl|2xl|3xl)\b/);
  assert.deepEqual(
    [...new Set([...styleCSS.matchAll(/font-size:\s*([^;}]+)/g)].map(([, value]) => value.trim()))].sort(),
    [
      'var(--t-body)',
      'var(--t-caption)',
      'var(--t-display)',
      'var(--t-secondary)',
      'var(--t-title)',
    ],
  );
  assert.deepEqual(
    [...new Set([...styleCSS.matchAll(/font-family:\s*([^;}]+)/g)].map(([, value]) => value.trim()))].sort(),
    ['var(--font)', 'var(--mono)'],
  );

  const weights = [...new Set(
    [...styleCSS.matchAll(/font-weight:\s*(\d+)/g)].map(([, value]) => Number(value)),
  )].sort((a, b) => a - b);
  assert.deepEqual(weights, [400, 500, 600, 700]);

  for (const selector of ['.ping-chip', '.ses-time', '.win-head .mt']) {
    const rule = styleCSS.match(new RegExp(`${selector.replaceAll('.', '\\.')}\\s*\\{[^}]*\\}`))?.[0] || '';
    assert.ok(rule, `missing ${selector} rule`);
    assert.doesNotMatch(rule, /font-family:\s*var\(--mono\)/);
    assert.match(rule, /font-variant-numeric:\s*tabular-nums/);
  }

  assert.doesNotMatch(appSrc, /class:\s*'chat-msg-meta mono'/);
  assert.doesNotMatch(appSrc, /class:\s*'chat-diff-stats mono'/);
  assert.doesNotMatch(appSrc, /class:\s*'chat-tool-exit mono'/);
  assert.match(appSrc, /class:\s*'chat-msg-meta tnum'/);
  assert.match(appSrc, /class:\s*'chat-diff-stats tnum'/);
  assert.match(appSrc, /class:\s*'chat-tool-exit tnum'/);

  for (const id of [
    'st-dmax', 'st-dscroll', 'st-mmax', 'st-mscroll', 'st-autoclose',
    'st-chat-cache-max', 'st-chat-cache-count', 'st-chat-cache-bytes', 'st-chat-cache-each',
  ]) {
    const element = indexHTML.match(new RegExp(`<[^>]+id="${id}"[^>]*>`))?.[0] || '';
    assert.ok(element, `missing #${id}`);
    assert.doesNotMatch(element, /\bmono\b/);
    assert.match(element, /\btnum\b/);
  }

  assert.match(styleCSS, /\.chat-markdown code\s*\{[^}]*font-family:\s*var\(--mono\)/s);
  assert.match(styleCSS, /\.chat-tool pre\s*\{[^}]*font-family:\s*var\(--mono\)/s);
  assert.match(styleCSS, /\.grp-h \.gpath\s*\{[^}]*font-family:\s*var\(--mono\)/s);
});

test('custom chat header omits redundant badge and metadata', () => {
  const source = appSrc.match(/function showChatPane[\s\S]*?\n}\n\nfunction chatAtBottom/)?.[0] || '';
  assert.ok(source);
  assert.doesNotMatch(source, /class: 'badge/);
  assert.doesNotMatch(source, /Codex Desktop-backed/);
  assert.match(source, /\$\('#win-meta'\)\.textContent = '';/);
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

test('jump-to-bottom control uses an accessible inline SVG icon', () => {
  assert.match(indexHTML, /<button id="chat-jump"[^>]*aria-label="跳到底部"[^>]*>/);
  assert.match(indexHTML, /<svg class="chat-jump-icon"[^>]*aria-hidden="true">/);
  assert.doesNotMatch(indexHTML, />跳到底部<\/button>/);
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

test('Codex git directives are parsed as structured status blocks', () => {
  assert.deepEqual(
    JSON.parse(JSON.stringify(parseCodexDirective('::git-commit{cwd="/Users/hjc/Git_Repositories/mac-fleet-hub"}'))),
    {
      name: 'git-commit',
      label: '已提交代码',
      attrs: { cwd: '/Users/hjc/Git_Repositories/mac-fleet-hub' },
    },
  );
});

test('Codex directives inside fenced code remain Markdown', () => {
  const parts = splitCodexContent('```text\n::git-commit{cwd="/tmp/repo"}\n```');
  assert.deepEqual(Array.from(parts, (part) => part.type), ['markdown']);
});

test('assistant deltas merge by item id', () => {
  let state = createChatState();
  state = reduceChatEvent(state, { type: 'assistant_delta', itemId: 'a1', data: { delta: 'he' } });
  state = reduceChatEvent(state, { type: 'assistant_delta', itemId: 'a1', data: { delta: 'llo' } });
  assert.equal(state.messages.length, 1);
  assert.equal(state.items.a1.text, 'hello');
});

test('Codex object status and turn lifecycle drive the running phase', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'thread_status',
    data: { status: { type: 'active', activeFlags: [] } },
  });
  assert.equal(chatPhase({ type: 'active' }), 'running');
  assert.equal(state.phase, 'running');

  state = reduceChatEvent(state, {
    type: 'turn_started', turnId: 'turn-7', data: { turn: { id: 'turn-7' } },
  });
  assert.equal(state.phase, 'running');
  assert.equal(state.activeTurnId, 'turn-7');

  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 'turn-7', data: { turn: { id: 'turn-7', status: 'completed' } },
  });
  assert.equal(state.phase, 'idle');
  assert.equal(state.activeTurnId, '');
});

test('follow-up queue is FIFO and removing one item preserves the others', () => {
  const chat = { followups: [] };
  const images = [{ id: 'img-1', name: 'one.png' }];
  const first = enqueueChatFollowup(chat, 'first', images, 'follow-1');
  const second = enqueueChatFollowup(chat, 'second', [], 'follow-2');
  assert.equal(first.id, 'follow-1');
  assert.deepEqual(Array.from(chat.followups, (item) => item.id), ['follow-1', 'follow-2']);
  assert.notEqual(first.images, images);

  const removed = removeChatFollowup(chat, 'follow-1');
  assert.equal(removed.text, 'first');
  assert.deepEqual(Array.from(chat.followups, (item) => item.id), ['follow-2']);
  assert.equal(second.text, 'second');
});

test('follow-up acknowledgement uses the Codex client message id', () => {
  assert.equal(followupAckId({
    type: 'user_done',
    data: { clientId: 'follow-1', text: 'guided' },
  }), 'follow-1');
  assert.equal(followupAckId({
    type: 'assistant_done',
    data: { clientId: 'follow-1' },
  }), '');
  assert.equal(followupAckId({
    type: 'user_done',
    data: { text: 'legacy message without id' },
  }), '');
});

test('self-drawn composer contains native stop control and follow-up queue', () => {
  assert.match(indexHTML, /id="chat-followups"/);
  assert.match(indexHTML, /class="ic chat-stop-icon"/);
  assert.match(indexHTML, /chat-stop-icon"[^>]*><rect x="6" y="6" width="12" height="12" rx="1\.5"/);
  assert.match(indexHTML, /data-action="send"/);
  assert.match(styleCSS, /\.chat-send \.chat-stop-icon\s*\{\s*display:\s*none/);
  assert.match(styleCSS, /chat-send\[data-action="interrupt"\]/);
  assert.match(styleCSS, /#chat-composer\s*\{\s*padding:\s*8px 10px max\(10px,\s*env\(safe-area-inset-bottom,\s*0px\)\)/);
});

test('self-drawn approval menu mirrors Codex three presets', () => {
  assert.match(indexHTML, /id="chat-approval-popover"/);
  assert.doesNotMatch(indexHTML, /<option value="never">/);
  assert.match(appSrc, /value:\s*'untrusted',\s*label:\s*'请求批准'/);
  assert.match(appSrc, /value:\s*'on-request',\s*label:\s*'替我审批'/);
  assert.match(appSrc, /value:\s*'full-access',\s*label:\s*'完全访问权限'/);
  assert.match(appSrc, /return 'on-request';/);
  assert.match(appSrc, /trigger\.dataset\.value\s*=\s*selected\.value/);
  assert.match(appSrc, /https:\/\/developers\.openai\.com\/codex\/concepts\/sandboxing#how-you-control-it/);
  assert.match(styleCSS, /\.chat-approval-choice\.full-access\s*\{\s*color:\s*#f04b14/);
  assert.match(styleCSS, /\.chat-approval-trigger\[data-value="full-access"\][^{]*\{[^}]*color:\s*#f04b14/s);
  assert.match(styleCSS, /\.chat-approval-popover\s*\{[^}]*width:\s*min\(380px,/s);
  assert.match(styleCSS, /\.chat-approval-title\s*\{[^}]*font-size:\s*var\(--t-body\)/s);
  assert.match(styleCSS, /\.chat-approval-desc\s*\{[^}]*font-size:\s*var\(--t-secondary\)/s);
  assert.match(styleCSS, /\.chat-approval-popover,\s*\.chat-options-popover\s*\{[^}]*position:\s*fixed;[^}]*bottom:\s*0/s);
});

test('self-drawn user message time renders outside the bubble', () => {
  assert.match(appSrc, /class:\s*'chat-user-wrap'/);
  assert.match(styleCSS, /\.chat-user-wrap\s*\{\s*display:\s*flex;\s*flex-direction:\s*column;\s*align-items:\s*flex-end/);
  assert.match(styleCSS, /\.chat-row\.user \.chat-card\s*\{[^}]*max-width:\s*100%/s);
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

test('tool activity rows mirror Codex inline summaries', () => {
  assert.match(appSrc, /function chatToolActivityLabel\(item, status, duration\)/);
  assert.match(appSrc, /function renderChatActivityGroup\(items\)/);
  assert.match(appSrc, /class:\s*'chat-tool-verb'/);
  assert.match(appSrc, /class:\s*'chat-tool-command mono'/);
  assert.match(appSrc, /'chat-tool-path mono'/);
  assert.match(appSrc, /chatToolTimerLabel\(status, duration\)/);
  assert.match(styleCSS, /\.chat-tool-summary\s*\{[^}]*inline-flex[^}]*gap:\s*4px/s);
  assert.match(styleCSS, /\.chat-tool-label\s*\{[^}]*inline-flex[^}]*white-space:\s*nowrap/s);
  assert.match(styleCSS, /\.chat-tool-section-title\s*\{/);
  assert.doesNotMatch(styleCSS, /\.chat-tool-label\s*\{[^}]*margin:\s*0 0 5px/s);
  assert.doesNotMatch(styleCSS, /\.chat-tool-title\s*\{/);
  assert.doesNotMatch(styleCSS, /\.chat-tool-subtitle\s*\{/);
});

test('consecutive activity summaries collapse like Codex groups', () => {
  const items = [
    { type: 'tool', kind: 'commandExecution', summary: 'sed -n 1,80p server/dashboard/app.js', status: 'completed' },
    { type: 'tool', kind: 'commandExecution', summary: 'npm test', status: 'completed' },
    { type: 'tool', kind: 'webSearch', summary: 'Codex activity', status: 'completed' },
  ];
  assert.equal(chatActivityGroupSummaryText(items), '已读取文件运行了一个命令已搜索网页');
  const group = renderChatActivityGroup(items);
  assert.equal(group.className, 'chat-row tool activity-group');
  assert.match(nodeText(group), /已读取文件运行了一个命令已搜索网页/);
  assert.match(nodeText(group), /npm test/);
});

test('running activity group uses the active item summary', () => {
  assert.deepEqual([...chatActivityActiveSummarySegments({ kind: 'commandExecution', summary: 'bash -lc npm test', status: 'inProgress' })], ['正在运行命令']);
  assert.deepEqual([...chatActivityActiveSummarySegments({ kind: 'mcpToolCall', title: 'Chrome · Read', summary: 'chrome · read', status: 'inProgress' })], ['正在使用 Chrome 集成']);
});

test('tool activity labels follow Codex command and MCP wording', () => {
  assert.equal(toolLabelText({ kind: 'commandExecution', summary: '/bin/zsh -lc pwd', status: 'inProgress', durationMs: 3200 }), '正在运行命令，已持续 3.2 秒');
  assert.equal(toolLabelText({ kind: 'commandExecution', summary: 'pwd', status: 'completed', durationMs: 853 }), '已运行pwd，耗时 853 ms');
  assert.equal(toolLabelText({ kind: 'commandExecution', summary: 'ssh rtm uptime', status: 'interrupted', durationMs: 3000 }), '已停止ssh rtm uptime，耗时 3.0 秒');
  assert.equal(toolLabelText({ kind: 'mcpToolCall', title: '运行 JavaScript', summary: 'node_repl · js', status: 'completed' }), '已运行 1 条命令');
  assert.equal(toolLabelText({ kind: 'mcpToolCall', title: '调用内部浏览器', summary: 'node_repl · js', status: 'inProgress' }), '正在使用浏览器');
});

test('read-only tool activities stay collapsed like Codex summaries', () => {
  assert.equal(chatToolHasExpandableBody({ kind: 'fileRead', summary: 'server/dashboard/app.js', detail: 'large file' }), false);
  assert.equal(chatToolHasExpandableBody({ kind: 'webSearch', summary: 'fleet', output: 'results' }), false);
  assert.equal(chatToolHasExpandableBody({ kind: 'commandExecution', summary: 'sed -n 1,80p server/dashboard/app.js', output: 'content' }), false);
  assert.equal(chatToolHasExpandableBody({ kind: 'commandExecution', summary: 'pwd' }), true);
  assert.equal(chatToolHasExpandableBody({ kind: 'mcpToolCall', title: 'Notion · Search', detail: '{"query":"fleet"}' }), true);
});

test('node_repl transport calls stay visible unless explicitly internal', () => {
  assert.equal(isInternalChatTool({
    type: 'tool',
    kind: 'mcpToolCall',
    title: 'node_repl · js',
    summary: 'node_repl · js',
  }), false);
  assert.equal(isInternalChatTool({
    type: 'tool',
    kind: 'mcpToolCall',
    title: 'node_repl · js',
    summary: 'node_repl · js',
    internal: true,
  }), true);
  assert.equal(isInternalChatTool({
    type: 'tool',
    kind: 'mcpToolCall',
    title: 'Notion · Search',
    summary: 'notion · search',
  }), false);
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
    data: { text: 'hello', completedAtMs: new Date(2026, 6, 22, 21, 44, 31).getTime(), usage: { inputTokens: 3807, cachedInputTokens: 3617, outputTokens: 89 } },
  });
  assert.equal(chatUserMetaText(state.items.u1), '昨天 21:42:10');
  assert.equal(chatAssistantMetaText(state.items.a1), 'gpt-5.6-sol, xhigh  |  in 3,807 (95% cached) / out 89  |  昨天 21:44:31');
  assert.equal(state.items.a1.durationMs, 91000);
});

test('chat metadata dates use compact relative labels', () => {
  const now = new Date(2026, 6, 23, 23, 0, 0).getTime();
  assert.equal(formatChatDate(new Date(2026, 6, 23, 22, 45, 55).getTime(), now), '22:45:55');
  assert.equal(formatChatDate(new Date(2026, 6, 22, 22, 45, 55).getTime(), now), '昨天 22:45:55');
  assert.equal(formatChatDate(new Date(2026, 6, 21, 22, 45, 55).getTime(), now), '前天 22:45:55');
  assert.equal(formatChatDate(new Date(2026, 0, 3, 22, 46, 47).getTime(), now), '1-3 22:46:47');
  assert.equal(formatChatDate(new Date(2025, 6, 23, 22, 46, 58).getTime(), now), '2025-7-23 22:46:58');
});

test('history metadata falls back to Codex UUIDv7 turn time and session defaults', () => {
  const turnId = '019f8a89-9787-7ae0-8b12-4e0ee30cc6d1';
  assert.equal(uuidV7TimeMs(turnId), 1784735700871);
  let state = prependHistory(createChatState(), [
    { type: 'user_done', itemId: 'u1', turnId, data: { text: 'merge commit push deploy' } },
    { type: 'assistant_done', itemId: 'a1', turnId, data: { text: 'done' } },
  ]);
  const chat = { model: state, selectedModel: 'gpt-5.6-sol', selectedEffort: 'xhigh' };
  applyChatMetadataDefaults(chat);
  state = chat.model;
  assert.equal(chatUserMetaText(state.items.u1), '昨天 23:55:00');
  assert.equal(chatAssistantMetaText(state.items.a1), 'gpt-5.6-sol, xhigh  |  昨天 23:55:00');
});

test('assistant metadata waits for turn completion and uses the last assistant item', () => {
  let state = createChatState();
  state = reduceChatEvent(state, {
    type: 'user_done', itemId: 'u1', turnId: 't1',
    data: { text: 'question', createdAtMs: new Date(2026, 6, 22, 21, 42, 10).getTime() },
  });
  state = reduceChatEvent(state, {
    type: 'assistant_done', itemId: 'a1', turnId: 't1',
    data: { text: 'first', model: 'gpt-5.6-sol', reasoningEffort: 'xhigh', completedAtMs: new Date(2026, 6, 22, 21, 44, 31).getTime() },
  });
  state = reduceChatEvent(state, {
    type: 'assistant_done', itemId: 'a2', turnId: 't1',
    data: { text: 'second', model: 'gpt-5.6-sol', reasoningEffort: 'xhigh', completedAtMs: new Date(2026, 6, 22, 21, 44, 31).getTime() },
  });
  let visible = chatMessageMetaVisibility(state);
  assert.equal(visible.has('u1'), true);
  assert.equal(visible.has('a1'), false);
  assert.equal(visible.has('a2'), false);
  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 't1',
    data: { turn: { id: 't1', status: 'completed' } },
  });
  visible = chatMessageMetaVisibility(state);
  assert.equal(visible.has('a2'), true);
  assert.equal(visible.get('a2').id, 'a2');
});

test('assistant metadata is placed after tools when the turn completes', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'assistant_done', itemId: 'a1', turnId: 't1',
    data: { text: 'working', model: 'gpt-5.6-sol', completedAtMs: 1784730000000 },
  });
  state = reduceChatEvent(state, {
    type: 'tool_done', itemId: 'tool1', turnId: 't1',
    data: { kind: 'commandExecution', command: 'pwd', status: 'completed' },
  });
  let visible = chatMessageMetaVisibility(state);
  assert.equal(visible.has('a1'), false);
  assert.equal(visible.has('tool1'), false);
  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 't1',
    data: { turn: { id: 't1', status: 'completed' } },
  });
  visible = chatMessageMetaVisibility(state);
  assert.equal(visible.has('a1'), false);
  assert.equal(visible.has('tool1'), true);
  assert.equal(visible.get('tool1').id, 'a1');
});

test('active resumed turn does not expose historical metadata before completion', () => {
  let state = prependHistory(createChatState(), [
    {
      type: 'assistant_done', itemId: 'a1', turnId: 't1',
      data: { text: 'still working', phase: 'commentary', model: 'gpt-5.6-sol' },
    },
  ]);
  assert.equal(chatMessageMetaVisibility(state).has('a1'), false);
  state = reduceChatEvent(state, {
    type: 'thread_status',
    data: { status: 'idle' },
  });
  assert.equal(chatMessageMetaVisibility(state).has('a1'), false);
});

test('historical final answer exposes metadata without a live turn_done event', () => {
  const state = prependHistory(createChatState(), [
    {
      type: 'assistant_done', itemId: 'a1', turnId: 't1',
      data: { text: 'done', phase: 'final_answer', model: 'gpt-5.6-sol' },
    },
  ]);
  assert.equal(chatMessageMetaVisibility(state).has('a1'), true);
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
  assert.equal(state.items.a1.turnComplete, true);
});

test('thread token usage notification backfills the completed turn with last usage', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'turn_usage', turnId: 't1',
    data: {
      tokenUsage: {
        total: { inputTokens: 120, outputTokens: 14 },
        last: { input_tokens: 114146, cached_input_tokens: 108439, output_tokens: 1935 },
      },
    },
  });
  state = reduceChatEvent(state, {
    type: 'assistant_done', itemId: 'a1', turnId: 't1',
    data: { text: 'done', model: 'gpt-5.6-sol' },
  });
  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 't1',
    data: { turn: { id: 't1', status: 'completed' } },
  });
  assert.deepEqual(JSON.parse(JSON.stringify(state.items.a1.usage)), { inputTokens: 114146, outputTokens: 1935, cachedInputTokens: 108439 });
  assert.match(chatAssistantMetaText(state.items.a1), /in 114,146 \(95% cached\) \/ out 1,935/);
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

test('failed turn keeps its error and exits running state', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'turn_started', turnId: 't1', data: { turn: { id: 't1' } },
  });
  state = reduceChatEvent(state, {
    type: 'error', turnId: 't1', data: { message: 'missing tool output' },
  });
  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 't1', data: { turn: { id: 't1', status: 'failed' } },
  });
  assert.equal(state.phase, 'idle');
  assert.equal(state.activeTurnId, '');
  assert.equal(state.error, 'missing tool output');
});

test('optimistic user message can be removed without discarding later events', () => {
  let state = appendUserMessage(createChatState(), 'pending', 'optimistic-1', []);
  state = reduceChatEvent(state, {
    type: 'assistant_delta', itemId: 'a1', turnId: 't1', data: { delta: 'live' },
  });
  state = removeMessage(state, 'optimistic-1');
  assert.deepEqual(Array.from(state.messages), ['a1']);
  assert.equal(state.items['optimistic-1'], undefined);
  assert.equal(state.items.a1.text, 'live');
});

test('composer draft normalization rejects null-like persisted values', () => {
  assert.equal(normalizeChatDraft(null), '');
  assert.equal(normalizeChatDraft(undefined), '');
  assert.equal(normalizeChatDraft('null'), '');
  assert.equal(normalizeChatDraft('undefined'), '');
  assert.equal(normalizeChatDraft('hello'), 'hello');
});

test('send and stop source contracts reject stale active state', () => {
  assert.match(appSrc, /typeof started\.turnId !== 'string'/);
  assert.match(appSrc, /FleetChatModel\.removeMessage\(chat\.model, optimisticId\)/);
  assert.match(appSrc, /e\.code === 'no_active_turn'/);
  assert.match(appSrc, /type: 'thread_status', data: \{ status: 'idle' \}/);
});
