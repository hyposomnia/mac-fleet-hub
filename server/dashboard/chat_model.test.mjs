import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const src = await readFile(new URL('./chat_model.js', import.meta.url), 'utf8');
const appSrc = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const markdownSrc = await readFile(new URL('./markdown.js', import.meta.url), 'utf8');
const previewSrc = await readFile(new URL('./preview.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const styleCSS = await readFile(new URL('./style.css', import.meta.url), 'utf8');
const serviceWorker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
const manifest = JSON.parse(await readFile(new URL('./manifest.webmanifest', import.meta.url), 'utf8'));
const markedSrc = await readFile(new URL('./vendor/marked.min.js', import.meta.url), 'utf8');
const sandbox = { globalThis: {} };
vm.createContext(sandbox);
vm.runInContext(src, sandbox);
const { createChatState, appendUserMessage, appendSteeringMessage, removeMessage, prependHistory, reduceChatEvent, normalizeDiffFiles, chatPhase, chatTurnProgress, followupAckId, uuidV7TimeMs } = sandbox.globalThis.FleetChatModel;

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
    replaceChildren(...nodes) { this.children = [...nodes]; },
    setAttribute(name, value) { this.attributes[name] = String(value); },
  };
}
function testTextNode(text) {
  return { nodeType: 3, textContent: String(text), children: [] };
}
class TestEventSource {
  static CLOSED = 2;
  constructor(url) { this.url = url; this.readyState = 1; }
  close() { this.readyState = TestEventSource.CLOSED; }
}
function nodeText(node) {
  if (!node) return '';
  return String(node.textContent || '') + (node.children || []).map(nodeText).join('');
}
const appSandbox = {
  document: {
    documentElement: {
      attributes: {},
      style: { setProperty() {}, removeProperty() {} },
      setAttribute(name, value) { this.attributes[name] = String(value); },
      getAttribute(name) { return this.attributes[name] || null; },
    },
    addEventListener() {},
    createElement: testElement,
    createElementNS: (_ns, tag) => testElement(tag),
    createTextNode: testTextNode,
    querySelector() { return null; },
    querySelectorAll() { return []; },
  },
  EventSource: TestEventSource,
  FleetChatModel: sandbox.globalThis.FleetChatModel,
  FleetMarkdown: {
    renderMarkdown(text) {
      const node = testElement('div');
      node.className = 'chat-markdown';
      node.textContent = String(text || '');
      return node;
    },
  },
  Date: FixedAppDate,
};
vm.createContext(appSandbox);
vm.runInContext(`${appSrc}\n;globalThis.__chatCacheTest = { chatCacheVictim, isChatConnectionKept, isSessionRunning, updateChatUpdatedAt, formatChatDate, chatAssistantMetaText, chatUserMetaText, chatMessageMetaVisibility, applyChatMetadataDefaults, enqueueChatFollowup, removeChatFollowup, normalizeChatDraft, isNoActiveTurnError, createDeviceScopeButton, chatSkillTriggerAt, parseChatSkillInput, chatSkillTokenNames, mergeChatComposerText, mergeChatAttachments, chatComposerAction: typeof chatComposerAction === 'function' ? chatComposerAction : null, chatImageSrc, chatToolStatus, chatToolDuration, chatToolActivityLabel, chatToolHasExpandableBody, isChatActivityItem, isChatTraceItem: typeof isChatTraceItem === 'function' ? isChatTraceItem : null, chatRenderUnits: typeof chatRenderUnits === 'function' ? chatRenderUnits : null, renderChatTurnProgress: typeof renderChatTurnProgress === 'function' ? renderChatTurnProgress : null, chatActivityGroupSummaryText, chatActivityActiveSummarySegments, chatActivityGroupIconKind, renderChatActivityGroup, renderChatActivityRun: typeof renderChatActivityRun === 'function' ? renderChatActivityRun : null, renderChatItem: typeof renderChatItem === 'function' ? renderChatItem : null, sessionRow, sessionStatus, filterFileEntries, sortFileEntries: typeof sortFileEntries === 'function' ? sortFileEntries : null, normalizeFileView: typeof normalizeFileView === 'function' ? normalizeFileView : null, truncateFileColumns: typeof truncateFileColumns === 'function' ? truncateFileColumns : null, fileColumnRequestCurrent: typeof fileColumnRequestCurrent === 'function' ? fileColumnRequestCurrent : null, fileIconName: typeof fileIconName === 'function' ? fileIconName : null, activeFileLocationID: typeof activeFileLocationID === 'function' ? activeFileLocationID : null, applyTheme: typeof applyTheme === 'function' ? applyTheme : null, visualKeyboardInset: typeof visualKeyboardInset === 'function' ? visualKeyboardInset : null, sessionBackDragOffset: typeof sessionBackDragOffset === 'function' ? sessionBackDragOffset : null, isSessionBackSwipe: typeof isSessionBackSwipe === 'function' ? isSessionBackSwipe : null, ensurePendingChatStarted: typeof ensurePendingChatStarted === 'function' ? ensurePendingChatStarted : null, filePreviewTypeLabel, filePreviewLocation, chatTurnPinText: typeof chatTurnPinText === 'function' ? chatTurnPinText : () => '', isInternalChatTool: typeof isInternalChatTool === 'function' ? isInternalChatTool : () => false, state };`, appSandbox);
const { chatCacheVictim, isChatConnectionKept, isSessionRunning, updateChatUpdatedAt, formatChatDate, chatAssistantMetaText, chatUserMetaText, chatMessageMetaVisibility, applyChatMetadataDefaults, enqueueChatFollowup, removeChatFollowup, normalizeChatDraft, isNoActiveTurnError, createDeviceScopeButton, chatSkillTriggerAt, parseChatSkillInput, chatSkillTokenNames, mergeChatComposerText, mergeChatAttachments, chatComposerAction, chatImageSrc, chatToolStatus, chatToolDuration, chatToolActivityLabel, chatToolHasExpandableBody, isChatActivityItem, isChatTraceItem, chatRenderUnits, renderChatTurnProgress, chatActivityGroupSummaryText, chatActivityActiveSummarySegments, chatActivityGroupIconKind, renderChatActivityGroup, renderChatActivityRun, renderChatItem, sessionRow, sessionStatus, filterFileEntries, sortFileEntries, normalizeFileView, truncateFileColumns, fileColumnRequestCurrent, fileIconName, activeFileLocationID, applyTheme, visualKeyboardInset, sessionBackDragOffset, isSessionBackSwipe, ensurePendingChatStarted, filePreviewTypeLabel, filePreviewLocation, chatTurnPinText, isInternalChatTool, state: appState } = appSandbox.__chatCacheTest;
vm.runInContext('globalThis.__updateChatSkillMenuTest = updateChatSkillMenu;', appSandbox);
const updateChatSkillMenu = appSandbox.__updateChatSkillMenuTest;
function toolLabelText(item) {
  const status = chatToolStatus(item.status);
  return chatToolActivityLabel(item, status, chatToolDuration(item.durationMs)).map(nodeText).join('');
}
function nodesWithClass(node, className, matches = []) {
  if (!node) return matches;
  const classes = node.className || node.attributes?.class || '';
  if (String(classes).split(/\s+/).includes(className)) matches.push(node);
  for (const child of node.children || []) nodesWithClass(child, className, matches);
  return matches;
}
const directiveSandbox = { globalThis: {} };
vm.createContext(directiveSandbox);
vm.runInContext(markdownSrc, directiveSandbox);
const parseCodexDirective = directiveSandbox.globalThis.FleetMarkdown.parseCodexDirective || (() => null);
const splitCodexContent = directiveSandbox.globalThis.FleetMarkdown.splitCodexContent || (() => []);
const previewSandbox = {
  globalThis: { location: { origin: 'https://fleet.example.test', pathname: '/', search: '' } },
  URLSearchParams,
};
vm.createContext(previewSandbox);
vm.runInContext(previewSrc, previewSandbox);
const { resolveLocalLink, resourceURL, fileEndpoint, isPreviewRoute, previewRequest, isTextPreviewPath, textPreviewMode } = previewSandbox.globalThis.FleetPreview;

test('chat model and app use the same versioned shell URLs', () => {
  const styleURL = indexHTML.match(/style\.css\?v=([a-zA-Z0-9_-]+)/);
  const markdownURL = indexHTML.match(/markdown\.js\?v=([a-zA-Z0-9_-]+)/);
  const previewURL = indexHTML.match(/preview\.js\?v=([a-zA-Z0-9_-]+)/);
  const modelURL = indexHTML.match(/chat_model\.js\?v=([a-zA-Z0-9_-]+)/);
  const appURL = indexHTML.match(/app\.js\?v=([a-zA-Z0-9_-]+)/);
  assert.ok(styleURL);
  assert.ok(markdownURL);
  assert.ok(previewURL);
  assert.ok(modelURL);
  assert.ok(appURL);
  assert.equal(styleURL[1], appURL[1]);
  assert.equal(markdownURL[1], appURL[1]);
  assert.equal(previewURL[1], appURL[1]);
  assert.equal(modelURL[1], appURL[1]);
  assert.match(serviceWorker, new RegExp(`/style\\.css\\?v=${styleURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/markdown\\.js\\?v=${markdownURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/preview\\.js\\?v=${previewURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/chat_model\\.js\\?v=${modelURL[1]}`));
  assert.match(serviceWorker, new RegExp(`/app\\.js\\?v=${appURL[1]}`));
  assert.match(indexHTML, /vendor\/purify\.min\.js\?v=3\.2\.6/);
  assert.match(indexHTML, /vendor\/marked\.min\.js\?v=15\.0\.12/);
  assert.match(serviceWorker, /vendor\/purify\.min\.js\?v=3\.2\.6/);
  assert.match(serviceWorker, /vendor\/marked\.min\.js\?v=15\.0\.12/);
  const markdownScript = indexHTML.indexOf('<script src="markdown.js');
  const previewScript = indexHTML.indexOf('<script src="preview.js');
  assert.ok(indexHTML.indexOf('<script src="vendor/purify.min.js') < markdownScript);
  assert.ok(indexHTML.indexOf('<script src="vendor/marked.min.js') < markdownScript);
  assert.ok(markdownScript < indexHTML.indexOf('<script src="app.js'));
  assert.ok(markdownScript < previewScript);
  assert.ok(previewScript < indexHTML.indexOf('<script src="app.js'));
});

test('local supported file links resolve to the originating Mac preview route', () => {
  const absolute = resolveLocalLink('/Users/test/My%20Project/docs/plan.md', { macId: 'm2', cwd: '/Users/test/My Project' });
  const absoluteParams = new URLSearchParams(absolute.split('?')[1]);
  assert.equal(absolute.split('?')[0], '/view');
  assert.equal(absoluteParams.get('mac'), 'm2');
  assert.equal(absoluteParams.get('path'), '/Users/test/My Project/docs/plan.md');
  assert.equal(absoluteParams.get('cwd'), '/Users/test/My Project');

  assert.match(resolveLocalLink('preview/page.html:12', { macId: 'm1', cwd: '/repo' }), /^\/view\?/);
  assert.match(resolveLocalLink('../media/demo.mp4', { macId: 'm3', cwd: '/repo/docs' }), /^\/view\?/);
  assert.match(resolveLocalLink('/Users/test/secret.txt', { macId: 'm2' }), /^\/view\?/);
  assert.match(resolveLocalLink('/Users/test/report.pdf', { macId: 'm2' }), /^\/view\?/);
  assert.equal(resolveLocalLink('https://example.com/file.md', { macId: 'm2' }), '');
  assert.equal(resolveLocalLink('/Users/test/image.png', { macId: 'unknown' }), '');
});

test('preview helpers build protected media URLs and parse only /view routes', () => {
  assert.equal(
    resourceURL('./image.png', { macId: 'm2', cwd: '/Users/test/docs' }),
    '/m2/api/file/content?path=.%2Fimage.png&cwd=%2FUsers%2Ftest%2Fdocs',
  );
  assert.equal(resourceURL('https://example.com/image.png', { macId: 'm2' }), '');
  assert.match(fileEndpoint('content', 'm2', '/Users/test/clip.mp4', { download: true }), /download=1/);
  assert.equal(isPreviewRoute('/view'), true);
  assert.equal(isPreviewRoute('/view/'), true);
  assert.equal(isPreviewRoute('/'), false);
  assert.deepEqual(
    JSON.parse(JSON.stringify(previewRequest('?mac=m2&path=%2FUsers%2Ftest%2Fplan.md&cwd=%2Frepo'))),
    { macId: 'm2', path: '/Users/test/plan.md', cwd: '/repo', embed: false },
  );
  assert.equal(previewRequest('?mac=m2&path=%2Ftmp%2Fnote.txt&embed=1').embed, true);
});

test('preview page keeps HTML in a scriptless sandbox and media in native controls', () => {
  const iframe = indexHTML.match(/<iframe id="preview-html"[^>]*>/)?.[0] || '';
  assert.match(iframe, /sandbox="allow-popups allow-popups-to-escape-sandbox"/);
  assert.doesNotMatch(iframe, /allow-scripts|allow-same-origin/);
  assert.match(indexHTML, /<video id="preview-video" controls playsinline preload="metadata">/);
  assert.match(indexHTML, /<audio id="preview-audio" controls preload="metadata">/);
  assert.match(indexHTML, /<div id="preview-text"[^>]*data-preview-kind="text"/);
  assert.match(indexHTML, /<iframe id="preview-pdf"[^>]*data-preview-kind="pdf"/);
  assert.match(previewSrc, /meta\.kind === 'text'/);
  assert.match(previewSrc, /meta\.kind === 'pdf'/);
  assert.match(previewSrc, /dataset\.previewEmbed = request\.embed/);
  assert.match(previewSrc, /FORBID_TAGS:[\s\S]*?'script'/);
  assert.match(previewSrc, /"script-src 'none'"/);
  assert.match(appSrc, /FleetMarkdown\.renderMarkdown\(item\.text, chatMediaSrc, chatLinkHref\)/);
});

test('text previews expose one persistent auto-wrap toggle in both preview headers', () => {
  assert.equal(isTextPreviewPath('/Users/test/config.json'), true);
  assert.equal(isTextPreviewPath('/Users/test/main.go:12:4'), true);
  assert.equal(isTextPreviewPath('/Users/test/readme.md'), false);
  assert.equal(isTextPreviewPath('/Users/test/image.png'), false);
  assert.match(indexHTML, /id="preview-wrap"[^>]*aria-pressed="false"[^>]*hidden/);
  assert.match(indexHTML, /id="file-preview-wrap"[^>]*aria-pressed="false"[^>]*hidden/);
  assert.match(styleCSS, /\.preview-text\.is-wrapped > textarea\s*\{[^}]*white-space:\s*pre-wrap;[^}]*overflow-wrap:\s*anywhere;/s);
  assert.match(previewSrc, /localStorage\?\.setItem\(TEXT_WRAP_KEY/);
  assert.match(appSrc, /contentWindow\?\.FleetPreview\?\.setTextWrap\(enabled, false\)/);
});

test('text previews map source formats into a read-only line-numbered code viewer', () => {
  assert.equal(textPreviewMode('/repo/config.json'), 'application/json');
  assert.equal(textPreviewMode('/repo/app.tsx'), 'text/typescript-jsx');
  assert.equal(textPreviewMode('/repo/main.go:12:4'), 'text/x-go');
  assert.equal(textPreviewMode('/repo/settings.yaml'), 'text/x-yaml');
  assert.equal(textPreviewMode('/repo/.env'), 'text/x-sh');
  assert.equal(textPreviewMode('/repo/notes.txt'), null);

  assert.match(indexHTML, /vendor\/codemirror\/lib\/codemirror\.css\?v=5\.65\.20/);
  assert.match(indexHTML, /<div id="preview-text"[^>]*data-preview-kind="text"[^>]*>[\s\S]*?<textarea id="preview-code"/);
  assert.ok(indexHTML.indexOf('vendor/codemirror/lib/codemirror.js') < indexHTML.indexOf('preview.js'));
  assert.match(previewSrc, /CodeMirror\.fromTextArea\(textarea,\s*\{/);
  assert.match(previewSrc, /lineNumbers:\s*true/);
  assert.match(previewSrc, /readOnly:\s*true/);
  assert.match(previewSrc, /setOption\('lineWrapping', value\)/);
  assert.match(styleCSS, /\.preview-text \.CodeMirror-linenumber\s*\{/);
  assert.match(serviceWorker, /\.\.\.CODEMIRROR_ASSETS/);
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
    control: '16px',
    display: '17px',
    page: '28px',
  });
  assert.doesNotMatch(styleCSS, /--t-(?:2xs|xs|sm|base|md|lg|xl|2xl|3xl)\b/);
  assert.deepEqual(
    [...new Set([...styleCSS.matchAll(/font-size:\s*([^;}]+)/g)].map(([, value]) => value.trim()))].sort(),
    [
      'var(--t-body)',
      'var(--t-caption)',
      'var(--t-control)',
      'var(--t-display)',
      'var(--t-page)',
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
  assert.match(styleCSS, /\.grp-h \.gpath::after\s*\{[^}]*font-family:\s*var\(--mono\)/s);
  assert.match(indexHTML, /class="v"><span id="hm-ip" class="mono">/);
});

test('custom chat header omits redundant badge and metadata', () => {
  const source = appSrc.match(/function showChatPane[\s\S]*?\n}\n\nfunction chatAtBottom/)?.[0] || '';
  assert.ok(source);
  assert.doesNotMatch(source, /class: 'badge/);
  assert.doesNotMatch(source, /Codex Desktop-backed/);
  assert.match(source, /\$\('#win-meta'\)\.textContent = '';/);
});

test('background ttyd frames cannot steal focus from dashboard controls', () => {
  const source = appSrc.match(/function hookTerm[\s\S]*?\n}\n\n\/\/ 关掉一个池条目/)?.[0] || '';
  assert.ok(source);
  assert.match(source, /const originalFocus = term\.focus\.bind\(term\)/);
  assert.match(source, /term\.focus = \(\.\.\.args\) =>/);
  assert.match(source, /entry\.iframe\.classList\.contains\('show'\)/);
  assert.match(source, /document\.activeElement/);
  assert.match(source, /active !== entry\.iframe/);
  assert.match(source, /return originalFocus\(\.\.\.args\)/);
});

test('Codex is the first and default session assistant', () => {
  const tabs = [...indexHTML.matchAll(/<button data-assistant="([^"]+)" role="tab" aria-selected="([^"]+)">/g)]
    .map((match) => ({ assistant: match[1], selected: match[2] }));
  assert.deepEqual(tabs, [
    { assistant: 'codex', selected: 'true' },
    { assistant: 'claude', selected: 'false' },
    { assistant: 'codex', selected: 'true' },
    { assistant: 'claude', selected: 'false' },
  ]);
  assert.match(appSrc, /assistant:\s*'codex',\s*\/\/ claude \| codex/);
});

test('settings menu owns archive browsing and session settings', () => {
  const header = indexHTML.match(/<header class="sc-head">[\s\S]*?<\/header>/)?.[0] || '';
  assert.ok(header);
  assert.match(header, /class="sc-head-actions"[\s\S]*id="new-session"/);
  assert.match(header, /data-device-scope-slot="sessions"/);
  assert.match(header, /id="session-view-toggle"[\s\S]*id="session-search"[\s\S]*id="new-session-mobile"/);
  assert.doesNotMatch(header, /session-summary|session-count|session-view-label|个未归档|按项目分组/);
  assert.doesNotMatch(header, /id="refresh-btn"/);
  assert.doesNotMatch(indexHTML, /data-scope=/);
  assert.match(indexHTML, /id="user-name">设置</);
  assert.deepEqual(
    [...indexHTML.matchAll(/<button data-act="([^"]+)"/g)].map((match) => match[1]),
    ['theme', 'archive', 'settings', 'logout', 'theme', 'archive', 'settings', 'logout'],
  );
  assert.equal((indexHTML.match(/>显示已归档会话</g) || []).length, 2);
  assert.equal((indexHTML.match(/>会话设置</g) || []).length, 3);
  assert.doesNotMatch(indexHTML, /data-settings-tab="sessions"|id="st-show-archived"/);
  assert.match(appSrc, /SESSION_ARCHIVE_KEY\s*=\s*'fleet-show-archived-sessions'/);
  assert.match(appSrc, /localStorage\.setItem\(SESSION_ARCHIVE_KEY/);
  assert.match(appSrc, /const unavailable = state\.mode === 'files';[\s\S]*?button\.disabled = unavailable;/);
  assert.match(appSrc, /else if \(b\.dataset\.act === 'archive'\) toggleArchivedSessions\(\)/);
  assert.match(styleCSS, /\.menu button:disabled\s*\{[^}]*opacity:\s*\.48;/s);
  assert.match(styleCSS, /\.sc-head-actions\s*\{/);
});

test('self-drawn Codex is enabled by default and configured in the self-drawn tab', () => {
  assert.match(indexHTML, /data-settings-tab="chat"[^>]*>自绘</);
  assert.match(indexHTML, /id="st-selfdraw"[^>]*type="checkbox"[^>]*checked/);
  assert.match(indexHTML, />为Codex默认使用自绘界面（近Codex Desktop）</);
  assert.doesNotMatch(indexHTML, /data-act="selfdraw"|>自绘界面<\/button>/);
  assert.match(appSrc, /localStorage\.getItem\(SELF_DRAW_KEY\) !== '0'/);
  assert.match(appSrc, /const nextSelfDraw = \$\('#st-selfdraw'\)\.checked;/);
  assert.match(appSrc, /setSelfDraw\(nextSelfDraw\)/);
});

test('mobile session toolbar keeps square actions on both sides of search', () => {
  assert.match(
    styleCSS,
    /@media \(max-width: 860px\) and \(pointer: coarse\)[\s\S]*?\.session-search-row\s*\{[^}]*grid-template-columns:\s*46px minmax\(0, 1fr\) 46px;/s,
  );
  assert.match(
    styleCSS,
    /@media \(max-width: 860px\) and \(pointer: coarse\)[\s\S]*?\.session-search-row \.iconbtn\s*\{[^}]*width:\s*46px;[^}]*height:\s*46px;/s,
  );
});

test('new session controls distinguish unscoped and project sessions', () => {
  assert.match(indexHTML, /id="new-session-mobile"[^>]*title="新建无项目会话"/);
  assert.match(appSrc, /function requestNewSession\(\)[\s\S]*?newSessionIn\('',\s*\{\s*unscoped:\s*true\s*\}\);/s);
  assert.match(appSrc, /class:\s*'gpath badge project-new-session'[\s\S]*?newSessionIn\(g\.cwd,\s*\{\s*macId:\s*projectMacId\s*\}\)/s);
  assert.match(appSrc, /class:\s*'gpath badge project-new-session'[\s\S]*?svgIcon\('ic',\s*'M12 5v14M5 12h14'\)/s);
  assert.doesNotMatch(indexHTML, /id="projects-modal"|id="project-list"/);
  assert.doesNotMatch(appSrc, /function showProjects\(/);
});

test('fresh Codex detail starts locally and creates its thread on first submit', () => {
  const draftStart = appSrc.slice(appSrc.indexOf('function newSessionIn('), appSrc.indexOf('// 终端头：'));
  const deferredStart = appSrc.slice(appSrc.indexOf('async function ensurePendingChatStarted('), appSrc.indexOf('async function submitChatInput('));
  const submit = appSrc.slice(appSrc.indexOf('async function submitChatInput('), appSrc.indexOf('function chatTurnOptions('));
  assert.match(draftStart, /openPendingChatSession\(/);
  assert.doesNotMatch(draftStart, /'chat\/start'/);
  assert.match(deferredStart, /'chat\/start'/);
  assert.match(submit, /await ensurePendingChatStarted\(chat\)[\s\S]*?raw = typeof input\.value === 'string' \? input\.value : '';/s);
});

test('pending Codex chat resolves the home directory once and migrates to the real thread', async () => {
  const previousFetch = appSandbox.fetch;
  const previousCache = appState.chatCache;
  const previousChat = appState.chat;
  const calls = [];
  appSandbox.fetch = async (url, options = {}) => {
    calls.push({ url, options });
    const data = url.endsWith('/api/info')
      ? { fileRoot: '/Users/test' }
      : {
          sessionId: 'thread-real', cwd: '/Users/test', approvalMode: 'on-request',
          models: [], model: '', effort: '', serviceTier: '',
        };
    return { ok: true, status: 200, async json() { return data; } };
  };
  const chat = {
    cacheKey: 'm1\ndraft-local', macId: 'm1', sessionId: 'draft-local', cwd: '',
    title: '新Codex会话 · 无项目', pendingStart: true, unscoped: true,
    startPromise: null, starting: false, loading: false, events: null,
    approvalMode: 'full-access', approvalConfirmedMode: 'full-access',
    models: [], efforts: [], serviceTiers: [], selectedModel: '', selectedEffort: '', selectedServiceTier: '',
  };
  appState.chat = null;
  appState.chatCache = new Map([[chat.cacheKey, chat]]);
  try {
    await Promise.all([ensurePendingChatStarted(chat), ensurePendingChatStarted(chat)]);
    assert.deepEqual(calls.map((call) => call.url), ['/m1/api/info', '/m1/api/chat/start']);
    assert.equal(JSON.parse(calls[1].options.body).cwd, '/Users/test');
    assert.equal(chat.sessionId, 'thread-real');
    assert.equal(chat.cacheKey, 'm1\nthread-real');
    assert.equal(chat.pendingStart, false);
    assert.equal(chat.approvalMode, 'full-access');
    assert.equal(appState.chatCache.has('m1\ndraft-local'), false);
    assert.equal(appState.chatCache.get('m1\nthread-real'), chat);
  } finally {
    chat.events?.close();
    appSandbox.fetch = previousFetch;
    appState.chatCache = previousCache;
    appState.chat = previousChat;
  }
});

test('session grouping omits aggregate badges and the view toggle has a visible pressed state', () => {
  const source = appSrc.match(/function renderSessionResults[\s\S]*?\n}\n\nasync function loadSessions/)?.[0] || '';
  assert.ok(source);
  assert.doesNotMatch(source, /class:\s*'gc badge'/);
  assert.doesNotMatch(styleCSS, /\.grp-h \.gc/);
  assert.match(styleCSS, /#session-view-toggle\[aria-pressed="true"\]\s*\{[^}]*background:\s*var\(--accent-soft\)/s);
  assert.match(styleCSS, /@media \(max-width:\s*860px\)[\s\S]*?\.session-search-row\s*\{\s*grid-template-columns:\s*46px\s*minmax\(0,\s*1fr\)\s*46px/s);
});

test('session pagination loads automatically near the scroll boundary', () => {
  assert.doesNotMatch(indexHTML, /id="sessions-more"/);
  assert.doesNotMatch(styleCSS, /\.sessions-more\s*\{/);
  assert.match(appSrc, /function maybeLoadMoreSessions\(\)/);
  assert.match(appSrc, /scrollHeight\s*-\s*wrap\.scrollTop\s*-\s*wrap\.clientHeight\s*<=\s*240/);
  assert.match(appSrc, /\$\('#session-groups'\)\.onscroll\s*=\s*maybeLoadMoreSessions/);
  assert.match(appSrc, /loadSessions\(\{\s*append:\s*true\s*\}\)/);
});

test('mobile layout requires both a narrow viewport and touch-first input', () => {
  const query = '@media (max-width: 860px) and (pointer: coarse)';
  assert.equal(styleCSS.split(query).length - 1, 2);
  assert.doesNotMatch(styleCSS, /@media \(max-width: 860px\) \{/);
  assert.match(appSrc, /const MOBILE_MEDIA = '\(max-width: 860px\) and \(pointer: coarse\)'/);
  assert.match(appSrc, /const isMobile = \(\) => matchMedia\(MOBILE_MEDIA\)\.matches/);
});

test('mobile session list does not create a second safe-area block', () => {
  const mobileRules = styleCSS.match(/@media \(max-width:\s*860px\) and \(pointer:\s*coarse\)[\s\S]*$/)?.[0] || '';
  assert.ok(mobileRules);
  assert.match(mobileRules, /\.sc-body\s*\{[^}]*padding:\s*8px;[^}]*scroll-padding-bottom:\s*8px;/s);
  assert.doesNotMatch(mobileRules, /\.sc-body\s*\{[^}]*safe-area-inset-bottom/s);
});

test('session list aggregates online devices while row actions retain their source Mac', () => {
  assert.match(appSrc, /sessionMacId:\s*'all'/);
  assert.match(appSrc, /sessionCursors:\s*\{\}/);
  assert.match(appSrc, /MACS\.filter\(\(m\) => state\.nodes\[m\.id\]\)/);
  assert.match(appSrc, /Promise\.all\(targets\.map\(async \(macId\)/);
  assert.match(appSrc, /macId,\s*assistant:\s*session\.assistant \|\| state\.assistant/);
  assert.match(appSrc, /dataset:\s*\{\s*sid,\s*mac:\s*macId,\s*assistant\s*\}/);
  assert.match(appSrc, /api\(session\.macId,\s*'sessions\/action'/);
  assert.match(appSrc, /termSes\(sid,\s*s\.title,\s*macId,\s*assistant\)/);
  assert.match(appSrc, /query\.set\('archived',\s*String\(state\.scope === 'all'\)\)/);
  assert.match(appSrc, /query\.set\('scope',\s*state\.scope === 'all' \? 'all' : 'active'\)/);
});

test('session rows remove redundant device, assistant, and idle labels', () => {
  const previousMac = appState.sessionMacId;
  const previousScope = appState.scope;
  const previousView = appState.sessionView;
  try {
    appState.scope = 'active';
    appState.sessionView = 'project';
    appState.sessionMacId = 'all';
    const allDevices = sessionRow({
      sessionId: 'thread-device-label', macId: 'm1', assistant: 'codex',
      title: 'Audit PWA', cwd: '/repo/mac-fleet-hub', mtime: fixedAppNowMs - 11 * 60e3,
      status: 'idle',
    });
    assert.equal(nodesWithClass(allDevices, 'session-device-name').length, 1);
    assert.equal(nodesWithClass(allDevices, 'session-project-name').length, 0);
    assert.doesNotMatch(nodeText(allDevices), /Codex/);

    appState.sessionMacId = 'm1';
    const selectedDevice = sessionRow({
      sessionId: 'thread-selected-device', macId: 'm1', assistant: 'codex',
      title: 'Audit PWA', cwd: '/repo/mac-fleet-hub', mtime: fixedAppNowMs - 11 * 60e3,
      status: 'idle',
    });
    assert.equal(nodesWithClass(selectedDevice, 'session-device-name').length, 0);
    assert.equal(nodesWithClass(selectedDevice, 'session-project-name').length, 0);
    assert.equal(nodesWithClass(selectedDevice, 'ses-status')[0]?.textContent, '');
    assert.equal(sessionStatus({ status: 'idle' }).text, '');
    assert.doesNotMatch(nodeText(selectedDevice), /Codex/);
    assert.match(
      styleCSS,
      /\.ses-meta:not\(:has\(\.session-device-name,\s*\.session-project-name\)\)\s*\{\s*display:\s*none;\s*\}/,
    );

    appState.sessionView = 'recent';
    const recent = sessionRow({
      sessionId: 'thread-recent-project', macId: 'm1', assistant: 'codex',
      title: 'Audit PWA', cwd: '/repo/mac-fleet-hub', mtime: fixedAppNowMs - 11 * 60e3,
      status: 'idle',
    });
    assert.equal(nodesWithClass(recent, 'session-project-name')[0]?.textContent, 'mac-fleet-hub');

    appState.scope = 'all';
    assert.equal(sessionStatus({ status: 'idle' }).text, '已归档');
  } finally {
    appState.sessionMacId = previousMac;
    appState.scope = previousScope;
    appState.sessionView = previousView;
  }
});

test('session state uses one right-side text label without leading or running dots', () => {
  const waiting = sessionRow({
    sessionId: 'thread-waiting', macId: 'm1', assistant: 'codex',
    title: 'Waiting for input', mtime: fixedAppNowMs, status: 'active', waiting: true,
  });
  const running = sessionRow({
    sessionId: 'thread-running', macId: 'm1', assistant: 'codex',
    title: 'Working', mtime: fixedAppNowMs, status: 'active',
  });
  assert.equal(nodesWithClass(waiting, 'dot').length, 0);
  assert.equal(nodesWithClass(waiting, 'session-running-status').length, 0);
  assert.equal(nodesWithClass(nodesWithClass(waiting, 'ses-top')[0], 'ses-status')[0]?.textContent, '等待回复');
  assert.equal(nodesWithClass(nodesWithClass(running, 'ses-top')[0], 'ses-status')[0]?.textContent, '正在进行');
  assert.match(
    styleCSS,
    /\.ses\.session-running \.ses-time,\s*\.ses\.conn \.ses-time,\s*\.ses\.session-waiting \.ses-time\s*\{\s*display:\s*none;\s*\}/,
  );
  assert.doesNotMatch(styleCSS, /session-running-status/);
  assert.doesNotMatch(styleCSS, /\.ses-status\.running\s*\{\s*display:\s*none;\s*\}/);
});

test('custom file browser stays on one device and shares the protected preview route', () => {
  for (const id of [
    'file-browser', 'file-locations', 'file-breadcrumbs',
    'file-list', 'file-preview-frame', 'file-upload-input', 'file-settings',
    'file-settings-menu', 'file-show-hidden',
    'file-preview-type', 'file-preview-size', 'file-preview-modified',
    'file-preview-location', 'file-preview-device',
  ]) {
    assert.match(indexHTML, new RegExp(`id="${id}"`));
  }
  assert.match(indexHTML, /data-device-scope-slot="files"/);
  const fileSources = indexHTML.match(/<aside class="file-sources">[\s\S]*?<\/aside>/)?.[0] || '';
  const fileToolbar = indexHTML.match(/<header class="file-toolbar">[\s\S]*?<\/header>/)?.[0] || '';
  assert.doesNotMatch(fileSources, /file-settings-trigger/);
  assert.match(fileToolbar, /id="file-upload"[\s\S]*id="file-new-folder"[\s\S]*id="file-settings"/);
  assert.doesNotMatch(indexHTML, /id="file-upload-side"/);
  for (const endpoint of ['file/list', 'file/mkdir', 'file/upload', 'file/rename', 'file/delete']) {
    assert.match(appSrc, new RegExp(endpoint.replace('/', '\\/')));
  }
  assert.match(appSrc, /if \(context === 'sessions'\)[\s\S]*id:\s*'all'/);
  assert.match(appSrc, /fileMacId:\s*null/);
  assert.match(appSrc, /filePreviewRoute\(state\.fileMacId,\s*entry\.path,\s*true\)/);
  assert.match(appSrc, /function dismissFilePreview\(\)[\s\S]*?closeFilePreview\(\)[\s\S]*?history\.back\(\)/);
  assert.match(appSrc, /filePreviewDismissing[\s\S]*?replaceFleetHistory\(target\)/);
  assert.doesNotMatch(appSrc, /`\$\{apiBase\(state\.macId\)\}\/files\/`/);
});

test('file preview presents native-style type and home-relative location details', () => {
  assert.equal(
    filePreviewTypeLabel({ name: '录音.MP3', extension: '.mp3', mime: 'audio/mpeg' }),
    'MP3 音频',
  );
  assert.equal(
    filePreviewTypeLabel({ name: 'design.png', extension: '.png', mime: 'image/png' }),
    'PNG 图像',
  );
  assert.equal(filePreviewLocation('/Users/demo/Downloads/design.png', '/Users/demo'), '~/Downloads');
  assert.equal(filePreviewLocation('/Users/demo/design.png', '/Users/demo'), '~');
  assert.match(styleCSS, /\.file-preview-stage\s*\{[^}]*flex:\s*1 1 auto/);
  assert.match(styleCSS, /\.file-preview-info\s*\{[^}]*border-top:/);
  assert.match(styleCSS, /\.file-preview-head \.iconbtn\s*\{\s*width:\s*44px;\s*height:\s*44px/);
});

test('file browser selects local SVG icons by filename, extension, and MIME', async () => {
  const cases = [
    [{ name: 'src', kind: 'folder' }, 'folder'],
    [{ name: 'video-single.py', extension: '.py', kind: 'file' }, 'python'],
    [{ name: 'COMPONENT.TSX', kind: 'file' }, 'react'],
    [{ name: 'package.json', extension: '.json', kind: 'file' }, 'npm'],
    [{ name: 'pyproject.toml', extension: '.toml', kind: 'file' }, 'python'],
    [{ name: 'recording', mime: 'audio/mpeg', kind: 'file' }, 'audio'],
    [{ name: 'photo.unknown', mime: 'image/heic', kind: 'file' }, 'image'],
    [{ name: 'archive.unknown', mime: 'application/x-7z-compressed', kind: 'file' }, 'zip'],
    [{ name: 'README', mime: 'text/plain', kind: 'file' }, 'document'],
  ];
  for (const [entry, expected] of cases) assert.equal(fileIconName(entry), expected);

  const icons = [
    'audio', 'c', 'console', 'cpp', 'csharp', 'css', 'dart', 'database', 'docker',
    'document', 'exe', 'font', 'git', 'go', 'html', 'image', 'java', 'javascript',
    'json', 'kotlin', 'lock', 'log', 'lua', 'markdown', 'npm', 'pdf', 'php',
    'powerpoint', 'powershell', 'python', 'r', 'react', 'ruby', 'rust', 'sass',
    'settings', 'svelte', 'swift', 'table', 'toml', 'typescript', 'video', 'vue',
    'word', 'xml', 'yaml', 'zip',
  ];
  await Promise.all(icons.map(async (icon) => {
    const svg = await readFile(new URL(`./icons/file-types/${icon}.svg`, import.meta.url), 'utf8');
    assert.match(svg, /^<svg\b/);
    assert.ok(serviceWorker.includes(`'${icon}'`), `${icon} is not precached`);
  }));
  assert.match(serviceWorker, /`\/icons\/file-types\/\$\{name\}\.svg`/);
  assert.match(styleCSS, /\.file-glyph \.file-type-icon\s*\{[^}]*object-fit:\s*contain/);
  assert.match(styleCSS, /\.file-row\.is-hidden \.file-type-icon,[\s\S]*?filter:\s*grayscale\(1\)/);
});

test('file browser hides dotfiles by default and can reveal them without changing search', () => {
  const entries = [
    { name: '.git', hidden: true },
    { name: 'notes.md', hidden: false },
    { name: '.notes-cache', hidden: true },
  ];
  assert.equal(filterFileEntries(entries, false, '').map((entry) => entry.name).join(','), 'notes.md');
  assert.equal(filterFileEntries(entries, true, '').map((entry) => entry.name).join(','), '.git,notes.md,.notes-cache');
  assert.equal(filterFileEntries(entries, true, 'notes').map((entry) => entry.name).join(','), 'notes.md,.notes-cache');
  assert.match(appSrc, /fileShowHidden:\s*false/);
  assert.match(appSrc, /fileShowHidden:\s*state\.fileShowHidden/);
  assert.match(appSrc, /class:\s*`file-row\$\{entry\.hidden \? ' is-hidden' : ''\}`/);
  assert.match(styleCSS, /\.file-row\.is-hidden \.file-name-cell strong,[\s\S]*?color:\s*var\(--text-3\)/);
  assert.match(styleCSS, /\.file-row\.is-hidden \.file-glyph\s*\{[^}]*background:\s*var\(--surface-2\);[^}]*color:\s*var\(--text-3\)/);
});

test('file browser sorts folders and files by modified time or byte size without mutating API data', () => {
  const entries = [
    { name: 'small.txt', kind: 'file', modifiedAt: 200, size: 10 },
    { name: 'Archive', kind: 'folder', modifiedAt: 100, size: 0 },
    { name: 'big.zip', kind: 'file', modifiedAt: 100, size: 50 },
    { name: 'Current', kind: 'folder', modifiedAt: 300, size: 0 },
  ];

  assert.equal(typeof sortFileEntries, 'function');
  assert.deepEqual(
    [...sortFileEntries(entries, 'modifiedAt', 'desc')].map((entry) => entry.name),
    ['Current', 'Archive', 'small.txt', 'big.zip'],
  );
  assert.deepEqual(
    [...sortFileEntries(entries, 'modifiedAt', 'asc')].map((entry) => entry.name),
    ['Archive', 'Current', 'big.zip', 'small.txt'],
  );
  assert.deepEqual(
    [...sortFileEntries(entries, 'size', 'desc')].map((entry) => entry.name),
    ['Archive', 'Current', 'big.zip', 'small.txt'],
  );
  assert.deepEqual(entries.map((entry) => entry.name), ['small.txt', 'Archive', 'big.zip', 'Current']);
  assert.match(indexHTML, /data-file-sort="modifiedAt"/);
  assert.match(indexHTML, /data-file-sort="size"/);
});

test('file browser supports persistent icon, list, and column views', () => {
  assert.equal(normalizeFileView('icons'), 'icons');
  assert.equal(normalizeFileView('list'), 'list');
  assert.equal(normalizeFileView('columns'), 'columns');
  assert.equal(normalizeFileView('tiles'), 'list');
  const fileToolbar = indexHTML.match(/<header class="file-toolbar">[\s\S]*?<\/header>/)?.[0] || '';
  assert.doesNotMatch(fileToolbar, /id="file-view-switch"/);
  assert.match(indexHTML, /id="file-settings-menu"[\s\S]*?id="file-view-switch"/);
  assert.match(indexHTML, /id="file-view-switch"[^>]*role="group"[^>]*aria-label="查看方式"/);
  for (const view of ['icons', 'list', 'columns']) {
    assert.match(indexHTML, new RegExp(`data-file-view="${view}"`));
  }
  assert.match(indexHTML, /data-file-view="list"[^>]*aria-pressed="true"/);
  assert.match(appSrc, /state\.fileView\s*=\s*normalizeFileView\(saved\.fileView\)/);
  assert.match(appSrc, /fileView:\s*state\.fileView/);
  assert.doesNotMatch(styleCSS, /\.file-view-switch\s*\{[^}]*order:\s*3/);
});

test('file browser highlights only the most specific matching location', () => {
  const locations = [
    { id: 'home', path: '/Users/demo' },
    { id: 'desktop', path: '/Users/demo/Desktop' },
    { id: 'projects', path: '/Users/demo/Git_Repositories' },
  ];
  assert.equal(activeFileLocationID('/Users/demo/Desktop', locations), 'desktop');
  assert.equal(activeFileLocationID('/Users/demo/Desktop/work', locations), 'desktop');
  assert.equal(activeFileLocationID('/Users/demo/Git_Repositories/app', locations), 'projects');
  assert.equal(activeFileLocationID('/Users/demo/Movies', locations), 'home');
  assert.equal(activeFileLocationID('/tmp', locations), '');
});

test('column view truncates stale descendants and rejects stale async responses', () => {
  const original = [
    { path: '/Users/demo', selectedPath: '/Users/demo/Documents' },
    { path: '/Users/demo/Documents', selectedPath: '/Users/demo/Documents/work' },
    { path: '/Users/demo/Documents/work', selectedPath: '' },
  ];
  const truncated = truncateFileColumns(original, 0, '/Users/demo/Downloads');
  assert.equal(truncated.length, 1);
  assert.equal(truncated[0].selectedPath, '/Users/demo/Downloads');
  assert.notEqual(truncated[0], original[0]);

  const previous = {
    mode: appState.mode,
    fileMacId: appState.fileMacId,
    fileView: appState.fileView,
    fileColumns: appState.fileColumns,
  };
  try {
    appState.mode = 'files';
    appState.fileMacId = 'm1';
    appState.fileView = 'columns';
    appState.fileColumns = [{ path: '/Users/demo', selectedPath: '/Users/demo/Documents' }];
    const request = {
      seq: 0,
      macId: 'm1',
      columnIndex: 0,
      path: '/Users/demo/Documents',
    };
    assert.equal(fileColumnRequestCurrent(request), true);
    appState.fileColumns[0].selectedPath = '/Users/demo/Downloads';
    assert.equal(fileColumnRequestCurrent(request), false);
  } finally {
    Object.assign(appState, previous);
  }
});

test('hidden files stay muted in icon and column views', () => {
  assert.match(appSrc, /class:\s*`file-icon-item\$\{entry\.hidden \? ' is-hidden' : ''\}`/);
  assert.match(appSrc, /class:\s*`file-column-row\$\{entry\.hidden \? ' is-hidden' : ''\}`/);
  assert.match(styleCSS, /\.file-icon-item\.is-hidden[\s\S]*?color:\s*var\(--text-3\)/);
  assert.match(styleCSS, /\.file-column-row\.is-hidden[\s\S]*?color:\s*var\(--text-3\)/);
});

test('PWA shell supports install, offline navigation, updates, and native shortcuts', () => {
  assert.equal(manifest.id, '/');
  assert.deepEqual(manifest.display_override, ['window-controls-overlay', 'standalone', 'minimal-ui']);
  assert.ok(manifest.icons.some((icon) => icon.sizes === '192x192' && icon.type === 'image/png'));
  assert.ok(manifest.icons.some((icon) => icon.sizes === '512x512' && icon.purpose === 'maskable'));
  assert.deepEqual(manifest.shortcuts.map((shortcut) => shortcut.url), ['/?mode=sessions', '/?mode=files']);
  assert.match(indexHTML, /rel="apple-touch-icon" href="icons\/icon-180\.png"/);
  assert.match(indexHTML, /id="network-status"/);
  assert.match(indexHTML, /id="pwa-install"[^>]*class="pwa-install"/);
  assert.match(indexHTML, /id="pwa-install-now"/);
  assert.match(serviceWorker, /Promise\.allSettled\(SHELL\.map/);
  assert.match(serviceWorker, /request\.mode === 'navigate'/);
  assert.match(serviceWorker, /cache\.match\('\/index\.html'\)/);
  assert.match(serviceWorker, /SKIP_WAITING/);
  assert.match(serviceWorker, /self\.addEventListener\('install',[\s\S]*?await self\.skipWaiting\(\)/);
  assert.match(serviceWorker, /\^\\\/m\\d\+/);
  assert.match(appSrc, /beforeinstallprompt/);
  assert.match(appSrc, /controllerchange/);
  assert.match(appSrc, /registration\.update\(\)\.catch\(\(\) => \{\}\)/);
  assert.match(appSrc, /addEventListener\('offline'/);
  assert.match(
    styleCSS,
    /#m-menu\.mobile-global-menu\s*\{[^}]*position:\s*fixed;[^}]*top:\s*calc\(env\(safe-area-inset-top,\s*0px\)\s*\+\s*58px\)/s,
  );
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

test('session running indicator prefers synchronized chat state and falls back to Desktop status', () => {
  const session = { sessionId: 'thread-status', status: 'active' };
  assert.equal(isSessionRunning(session, 'm1'), true);
  assert.equal(isSessionRunning({ ...session, status: 'idle' }, 'm1'), false);

  const cacheKey = 'm1\nthread-status';
  const chat = { historyReady: true, submitting: false, model: { phase: 'running' } };
  appState.chatCache.set(cacheKey, chat);
  assert.equal(isSessionRunning({ ...session, status: 'idle' }, 'm1'), true);

  chat.model.phase = 'idle';
  assert.equal(isSessionRunning(session, 'm1'), false);

  chat.historyReady = false;
  assert.equal(isSessionRunning(session, 'm1'), true);
  appState.chatCache.delete(cacheKey);
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

test('stale inactive events cannot clear a newer active turn', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'turn_started', turnId: 'turn-new', data: { turn: { id: 'turn-new' } },
  });
  state = reduceChatEvent(state, {
    type: 'thread_status', data: { status: 'idle', reconciledTurnId: 'turn-old' },
  });
  assert.equal(state.phase, 'running');
  assert.equal(state.activeTurnId, 'turn-new');

  state = reduceChatEvent(state, {
    type: 'turn_done', turnId: 'turn-old', data: { turn: { id: 'turn-old', status: 'completed' } },
  });
  assert.equal(state.phase, 'running');
  assert.equal(state.activeTurnId, 'turn-new');
});

test('no-active errors support structured and legacy agent responses', () => {
  assert.equal(isNoActiveTurnError({ code: 'no_active_turn', message: '当前没有任务' }), true);
  assert.equal(isNoActiveTurnError({ code: 'chat_failed', message: 'codex app-server turn/steer failed: no active turn to steer' }), true);
  assert.equal(isNoActiveTurnError({ code: 'chat_failed', message: 'connection reset' }), false);
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

test('composer sends running input to the follow-up queue and only stops when empty', () => {
  const idle = { model: { phase: 'idle' } };
  const running = { model: { phase: 'running' } };

  assert.equal(chatComposerAction(idle, false), 'send');
  assert.equal(chatComposerAction(idle, true), 'send');
  assert.equal(chatComposerAction(running, false), 'interrupt');
  assert.equal(chatComposerAction(running, true), 'queue');
  assert.match(appSrc, /submitChatInput\(\{ forceQueue: action === 'queue' \}\)/);
});

test('command enter steers the running turn before the skill menu handles enter', () => {
  const commandEnter = appSrc.indexOf("if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)");
  const skillMenu = appSrc.indexOf('const menu = state.chat?.skillMenu;', commandEnter);

  assert.notEqual(commandEnter, -1);
  assert.ok(commandEnter < skillMenu);
  assert.match(appSrc.slice(commandEnter, skillMenu), /submitChatInput\(\{ forceQueue: e\.shiftKey \}\);\s*return;/);
});

test('chat skill trigger recognizes dollar and slash only at token start', () => {
  assert.deepEqual(
    { ...chatSkillTriggerAt('$de', 3) },
    { start: 0, end: 3, marker: '$', query: 'de' },
  );
  assert.deepEqual(
    { ...chatSkillTriggerAt('fix /d', 6) },
    { start: 4, end: 6, marker: '/', query: 'd' },
  );
  assert.deepEqual(
    { ...chatSkillTriggerAt('$browser:control', 16) },
    { start: 0, end: 16, marker: '$', query: 'browser:control' },
  );
  assert.equal(chatSkillTriggerAt('path/foo', 8), null);
  assert.equal(chatSkillTriggerAt('cost$dev', 8), null);
  assert.equal(chatSkillTriggerAt('/dev done', 9), null);
});

test('pending Codex chat keeps slash commands available before thread creation', () => {
  const previousQuerySelector = appSandbox.document.querySelector;
  const previousChat = appState.chat;
  const input = { value: '/de', selectionStart: 3 };
  const menu = testElement('div');
  menu.hidden = true;
  appSandbox.document.querySelector = (selector) => ({
    '#chat-input': input,
    '#chat-skill-menu': menu,
  })[selector] || null;
  appState.chat = {
    pendingStart: true,
    skillsLoaded: true,
    skills: [{ id: 'skill-dev', name: 'dev', description: 'Develop' }],
    skillMenu: null,
  };
  try {
    updateChatSkillMenu();
    assert.equal(menu.hidden, false);
    assert.equal(menu.children.length, 1);
    assert.equal(appState.chat.skillMenu.items[0].name, 'dev');
  } finally {
    appSandbox.document.querySelector = previousQuerySelector;
    appState.chat = previousChat;
  }
});

test('chat skill parsing supports dollar and slash, deduplicates, and keeps unknown tokens', () => {
  const available = [
    { name: 'dev', description: 'Develop' },
    { name: 'ui', description: 'Design' },
    { id: 'browser-skill', name: 'browser:control-in-app-browser', description: 'Browser' },
  ];
  const parsed = parseChatSkillInput(
    '$dev 修复登录\n/ui 调整界面 /dev $browser:control-in-app-browser /unknown',
    available,
  );
  assert.equal(parsed.text, '修复登录\n调整界面 /unknown');
  assert.deepEqual(
    Array.from(parsed.skills, (skill) => skill.name),
    ['dev', 'ui', 'browser:control-in-app-browser'],
  );

  const unknown = parseChatSkillInput('/missing keep', available);
  assert.equal(unknown.text, '/missing keep');
  assert.deepEqual(Array.from(unknown.skills), []);
});

test('same-name skill parsing keeps app-server first-result priority', () => {
  const available = [
    { id: 'agents-copy', name: 'tavily', description: 'Agents copy' },
    { id: 'codex-copy', name: 'tavily', description: 'Codex copy' },
  ];
  const parsed = parseChatSkillInput('$tavily search', available);
  assert.equal(parsed.skills.length, 1);
  assert.equal(parsed.skills[0].id, 'agents-copy');
});

test('skill token detection covers plugin-prefixed names before a list retry', () => {
  assert.deepEqual(
    Array.from(chatSkillTokenNames('$browser:control-in-app-browser inspect /dev')),
    ['browser:control-in-app-browser', 'dev'],
  );
  assert.deepEqual(Array.from(chatSkillTokenNames('plain text /tmp/file')), []);
});

test('failed turn recovery preserves text order and deduplicates uploaded images', () => {
  assert.equal(mergeChatComposerText('failed request', ''), 'failed request');
  assert.equal(mergeChatComposerText('failed request', 'new draft'), 'failed request\nnew draft');
  const failed = [{ localId: 'local-1', id: 'upload-1' }, { localId: 'local-2', id: 'upload-2' }];
  const current = [{ localId: 'local-2', id: 'upload-2' }, { localId: 'local-3', id: 'upload-3' }];
  assert.deepEqual(
    Array.from(mergeChatAttachments(failed, current), (image) => image.id),
    ['upload-1', 'upload-2', 'upload-3'],
  );
});

test('local user image path resolves through the selected Mac media endpoint', () => {
  appState.macId = 'm2';
  appState.chat = null;
  assert.equal(
    chatImageSrc({ path: '/Users/test/Library/Caches/mac-fleet-hub/chat-uploads/session/image.png' }),
    '/m2/api/chat/media?path=%2FUsers%2Ftest%2FLibrary%2FCaches%2Fmac-fleet-hub%2Fchat-uploads%2Fsession%2Fimage.png',
  );
});

test('failed steering removes the optimistic transcript item before queue recovery', () => {
  let model = createChatState();
  model = appendSteeringMessage(model, 'change direction', 'steer-1', [], 'turn-1');
  assert.equal(model.messages.length, 1);

  model = reduceChatEvent(model, {
    type: 'steer_failed',
    itemId: 'steer-1',
    data: { clientId: 'steer-1', message: 'turn already completed' },
  });

  assert.deepEqual(Array.from(model.messages), []);
  assert.equal(model.items['steer-1'], undefined);
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
  assert.equal((styleCSS.match(/\.chat-send\s*\{\s*width:\s*36px;\s*height:\s*36px;/g) || []).length, 2);
  assert.match(styleCSS, /#chat-composer\s*\{\s*padding:\s*8px 10px 4px;\s*background:\s*transparent;\s*backdrop-filter:\s*none;/);
  assert.doesNotMatch(styleCSS, /#chat-composer\s*\{\s*padding:\s*8px 10px [^;}]*safe-area-inset-bottom/);
});

test('mobile chat composer uses the root canvas and follows a resized visual viewport', () => {
  assert.equal(visualKeyboardInset(false, 894, 543), 0);
  assert.equal(visualKeyboardInset(true, 894, 543), 351);
  assert.equal(visualKeyboardInset(true, 894, 775), 0);
  assert.equal(visualKeyboardInset(true, 894, 774), 120);
  assert.equal(visualKeyboardInset(true, 543, 543), 0);
  assert.equal(visualKeyboardInset(true, Number.NaN, 543), 0);
  assert.doesNotMatch(styleCSS, /#win\.chat-open/);
  assert.doesNotMatch(styleCSS, /bottom:\s*calc\(-1 \* env\(safe-area-inset-bottom/);
  assert.doesNotMatch(styleCSS, /#chat-pane\s*\{\s*bottom:\s*var\(--kb/);
  assert.doesNotMatch(styleCSS, /#mobile-input\s*\{[^}]*translateY\([^)]*--kb/s);
  assert.match(styleCSS, /html:has\(#app\.term-open #chat-pane:not\(\[hidden\]\)\),\s*body:has\(#app\.term-open #chat-pane:not\(\[hidden\]\)\)\s*\{\s*background:\s*var\(--chat-bg\);\s*\}/);
  assert.match(styleCSS, /#app\.term-open #win:has\(#chat-pane:not\(\[hidden\]\)\)\s*\{\s*background:\s*var\(--chat-bg\);\s*\}/);
  assert.doesNotMatch(styleCSS, /#chat-pane(?:::(?:before|after)|\s*::(?:before|after))/);
  assert.match(styleCSS, /html\.visual-keyboard-open #app\.term-open #win\s*\{[^}]*top:\s*var\(--visual-viewport-top, 0px\);[^}]*bottom:\s*auto;[^}]*height:\s*var\(--visual-viewport-height, 100dvh\);/s);
  assert.doesNotMatch(appSrc, /classList\.(?:add|remove)\('chat-open'\)/);
  assert.match(appSrc, /function setVisualKeyboardInset\(value/);
  assert.match(appSrc, /setProperty\('--visual-viewport-top'/);
  assert.match(appSrc, /setProperty\('--visual-viewport-height'/);
  assert.match(appSrc, /classList\.toggle\('visual-keyboard-open', inset > 0 && viewportHeight > 0\)/);
  assert.match(appSrc, /function releaseVisualKeyboard\(\)[\s\S]*?setVisualKeyboardInset\(0\);/);
  assert.match(appSrc, /let visualViewportBaselineHeight = vv\.height;/);
  assert.match(appSrc, /visualViewportBaselineHeight = Math\.max\(visualViewportBaselineHeight, vv\.height\);/);
  assert.match(appSrc, /visualKeyboardInset\(focused, visualViewportBaselineHeight, vv\.height\)/);
  assert.match(appSrc, /const syncKbAfterFocus\s*=\s*\(event\)\s*=>/);
  assert.match(appSrc, /event\.target\?\.matches\?\.\('#chat-input, #cmd-input'\)/);
  assert.match(appSrc, /document\.addEventListener\('focusin',\s*syncKbAfterFocus\)/);
  assert.match(appSrc, /document\.addEventListener\('focusout',\s*clearKbAfterBlur\)/);
  assert.match(appSrc, /syncKb\(false\);/);
  assert.match(appSrc, /const syncKbFromActiveElement\s*=\s*\(\)\s*=>\s*syncKb\(\)/);
  assert.match(appSrc, /setTimeout\(syncKbFromActiveElement,\s*350\)/);
});

test('chat image attachments open through one accessible fullscreen viewer', () => {
  const row = renderChatItem({ type: 'user', text: '', images: [{ url: 'blob:shot', name: 'shot.png' }] });
  const controls = nodesWithClass(row, 'chat-attachment-preview');
  assert.equal(controls.length, 1);
  assert.equal(controls[0].tagName, 'button');
  assert.equal(controls[0].attributes['aria-label'], '查看大图：shot.png');
  assert.equal(typeof controls[0].onclick, 'function');
  assert.match(indexHTML, /id="chat-image-viewer"[^>]*role="dialog"[^>]*aria-modal="true"/);
  assert.match(indexHTML, /id="chat-image-viewer-close"/);
  assert.match(styleCSS, /\.chat-image-viewer\s*\{[^}]*position:\s*fixed;[^}]*inset:\s*0;[^}]*z-index:\s*160;/s);
  assert.match(appSrc, /event\.target\.closest\?\.\('\.chat-markdown img'\)[\s\S]*?event\.preventDefault\(\);/);
  assert.match(appSrc, /if \(!\$\('#chat-image-viewer'\)\.hidden\)\s*\{\s*closeChatImageViewer\(\);/s);
});

test('self-drawn approval menu mirrors Codex three presets', () => {
  assert.match(indexHTML, /id="chat-approval-popover"/);
  assert.doesNotMatch(indexHTML, /<option value="never">/);
  assert.match(appSrc, /value:\s*'untrusted',\s*label:\s*'请求批准'/);
  assert.match(appSrc, /value:\s*'on-request',\s*label:\s*'替我审批'/);
  assert.match(appSrc, /value:\s*'full-access',\s*label:\s*'完全访问权限'/);
  assert.match(appSrc, /return 'on-request';/);
  assert.match(appSrc, /trigger\.dataset\.value\s*=\s*selected\.value/);
  assert.match(appSrc, /api\(chat\.macId,\s*'chat\/settings'/);
  assert.match(appSrc, /if\s*\(chat\.approvalMode\)\s*turnOptions\.approvalMode\s*=\s*chat\.approvalMode/);
  assert.match(appSrc, /已开启完全访问，当前任务后续审批将自动允许。/);
  assert.match(appSrc, /https:\/\/developers\.openai\.com\/codex\/concepts\/sandboxing#how-you-control-it/);
  assert.match(styleCSS, /\.chat-approval-choice\.full-access\s*\{\s*color:\s*#f04b14/);
  assert.match(styleCSS, /\.chat-approval-trigger\[data-value="full-access"\][^{]*\{[^}]*color:\s*#f04b14/s);
  assert.match(styleCSS, /\.chat-approval-popover\s*\{[^}]*width:\s*min\(380px,/s);
  assert.match(styleCSS, /\.chat-approval-title\s*\{[^}]*font-size:\s*var\(--t-body\)/s);
  assert.match(styleCSS, /\.chat-approval-desc\s*\{[^}]*font-size:\s*var\(--t-secondary\)/s);
  assert.match(styleCSS, /\.chat-approval-popover,\s*\.chat-options-popover\s*\{[^}]*position:\s*fixed;[^}]*bottom:\s*0/s);
});

test('self-drawn option submenu grows to the available viewport height', () => {
  assert.match(styleCSS, /\.chat-options-submenu\s*\{[^}]*max-height:\s*calc\(100dvh - 160px\)/s);
  assert.match(styleCSS, /\.chat-options-choices\s*\{[^}]*max-height:\s*calc\(100dvh - 215px\)/s);
  assert.doesNotMatch(styleCSS, /max-height:\s*min\((?:370|430|460)px,/);
  assert.doesNotMatch(styleCSS, /max-height:\s*min\(72dvh,\s*460px\)/);
  assert.match(styleCSS, /\.chat-options-choices\s*\{\s*max-height:\s*none;\s*overflow-y:\s*visible;\s*\}/s);
});

test('self-drawn user message time renders outside the bubble', () => {
  assert.match(appSrc, /class:\s*'chat-user-wrap'/);
  assert.match(styleCSS, /\.chat-user-wrap\s*\{\s*display:\s*flex;\s*flex-direction:\s*column;\s*align-items:\s*flex-end/);
  assert.match(styleCSS, /\.chat-row\.user \.chat-card\s*\{[^}]*max-width:\s*100%/s);
  assert.match(styleCSS, /\.chat-user-wrap\s*\{\s*max-width:\s*88%;\s*\}/);
  assert.doesNotMatch(styleCSS, /\.chat-row\.user \.chat-card,\s*\.chat-tool/);
});

test('mobile session rows keep a compact touch target', () => {
  assert.match(styleCSS, /@media \(max-width: 860px\) and \(pointer: coarse\)[\s\S]*?\.ses\s*\{[^}]*min-height:\s*40px;[^}]*padding:\s*2px 8px;/s);
  assert.doesNotMatch(styleCSS, /@media \(max-width: 860px\) and \(pointer: coarse\)[\s\S]*?\.ses\s*\{\s*min-height:\s*68px;/);
});

test('mobile session rows center single-line content vertically', () => {
  assert.match(
    styleCSS,
    /@media \(max-width: 860px\) and \(pointer: coarse\)[\s\S]*?\.ses\s*\{[^}]*min-height:\s*40px;[^}]*justify-content:\s*center;/s,
  );
});

test('mobile title switch emphasizes only the selected mode', () => {
  assert.match(styleCSS, /\.mobile-title-switch button\s*\{[^}]*border-bottom:\s*3px solid transparent;[^}]*color:\s*var\(--text\);[^}]*font-weight:\s*500;/s);
  assert.match(styleCSS, /\.mobile-title-switch button\[aria-selected="true"\]\s*\{[^}]*border-bottom-color:\s*var\(--accent\);[^}]*color:\s*var\(--accent\);[^}]*font-weight:\s*700;/s);
});

test('mobile device picker sits compactly in each title row', () => {
  const sessionTitle = indexHTML.match(/<div class="sc-title-row">[\s\S]*?<\/div>\s*<div class="session-search-row">/)?.[0] || '';
  const fileTitle = indexHTML.match(/<header class="file-mobile-head">[\s\S]*?<\/header>/)?.[0] || '';
  assert.match(sessionTitle, /class="mobile-title-switch"[\s\S]*data-device-scope-slot="sessions"[\s\S]*class="sc-head-actions"/);
  assert.match(fileTitle, /class="mobile-title-switch"[\s\S]*data-device-scope-slot="files"[\s\S]*class="iconbtn bare mobile-menu-trigger"/);
  assert.match(styleCSS, /\.mobile-device-scope\s*\{[^}]*flex:\s*0 1 176px;[^}]*width:\s*auto;[^}]*min-width:\s*0;[^}]*min-height:\s*38px;/s);
  assert.match(styleCSS, /\.mobile-device-scope > span:not\(\.device-scope-avatar\)\s*\{\s*display:\s*none;\s*\}/);
  assert.match(styleCSS, /\.mobile-title-switch\s*\{[^}]*flex:\s*none;[^}]*white-space:\s*nowrap;/s);
});

test('session and file views reuse one device scope component', () => {
  const sessionButton = createDeviceScopeButton('sessions');
  const fileButton = createDeviceScopeButton('files');
  const childClasses = (button) => button.children.map((child) => child.className || child.attributes?.class || '');

  assert.equal(sessionButton.attributes.id, 'session-device-button');
  assert.equal(fileButton.attributes.id, 'file-device-button');
  assert.equal(sessionButton.dataset.deviceContext, 'sessions');
  assert.equal(fileButton.dataset.deviceContext, 'files');
  assert.deepEqual(childClasses(sessionButton), childClasses(fileButton));
  assert.equal(nodesWithClass(sessionButton, 'device-scope-avatar')[0]?.textContent, 'ALL');
  assert.equal(nodesWithClass(fileButton, 'device-scope-avatar')[0]?.textContent, 'M');
  assert.equal(typeof sessionButton.onclick, 'function');
  assert.equal(typeof fileButton.onclick, 'function');
  assert.match(appSrc, /mountDeviceScopeButtons\(\);/);
  assert.doesNotMatch(indexHTML, /id="(?:session|file)-device-button"/);
});

test('device picker lists the small fleet without a search field', () => {
  assert.doesNotMatch(indexHTML, /id="device-search"|class="device-searchbar"|placeholder="搜索设备"/);
  assert.doesNotMatch(appSrc, /device-search|const query = .*toLocaleLowerCase/);
  assert.doesNotMatch(styleCSS, /\.device-searchbar/);
});

test('mobile session detail supports a deliberate edge swipe back', () => {
  assert.equal(sessionBackDragOffset({ x: 8, y: 300, edge: true }, { x: 108, y: 312 }, 390), 100);
  assert.equal(sessionBackDragOffset({ x: 8, y: 300, edge: true }, { x: 500, y: 312 }, 390), 390);
  assert.equal(sessionBackDragOffset({ x: 8, y: 300, edge: true }, { x: -20, y: 302 }, 390), 0);
  assert.equal(sessionBackDragOffset({ x: 8, y: 300, edge: true }, { x: 70, y: 390 }, 390), 0);
  assert.equal(sessionBackDragOffset({ x: 48, y: 300, edge: false }, { x: 160, y: 305 }, 390), 0);
  assert.equal(isSessionBackSwipe({ x: 8, y: 300, edge: true }, { x: 110, y: 310 }), true);
  assert.equal(isSessionBackSwipe({ x: 8, y: 300, edge: true }, { x: 60, y: 305 }), false);
  assert.equal(isSessionBackSwipe({ x: 8, y: 300, edge: true }, { x: 120, y: 410 }), false);
  assert.equal(isSessionBackSwipe({ x: 48, y: 300, edge: false }, { x: 160, y: 305 }), false);
  assert.equal(isSessionBackSwipe({ x: 8, y: 300, edge: true }, { x: -100, y: 305 }), false);
  assert.match(indexHTML, /id="session-back-gesture"[^>]*aria-hidden="true"/);
  assert.match(styleCSS, /#app\.term-open #session-back-gesture\s*\{[^}]*display:\s*block;/s);
  assert.match(styleCSS, /#app\.term-open\.session-back-dragging #win\s*\{[^}]*transform:\s*translate3d\(var\(--session-back-x\), 0, 0\)/s);
  assert.match(styleCSS, /#app\.term-open\.session-back-settling #win\s*\{[^}]*transition:\s*transform/s);
  assert.match(appSrc, /addEventListener\('touchstart',[\s\S]*addEventListener\('touchend'/);
  assert.match(appSrc, /sessionBackDragOffset\(sessionBackTouch, point, window\.innerWidth\)/);
  assert.match(appSrc, /settleSessionBackGesture\(isSessionBackSwipe\(sessionBackTouch, end\)\)/);
  assert.doesNotMatch(appSrc, /new MutationObserver\([\s\S]*?term-open/);
  assert.equal((appSrc.match(/\$\('#app'\)\.classList\.remove\('term-open'\)/g) || []).length, 1);
});

test('mobile file back gesture moves only the browsing region', () => {
  const fileLayout = indexHTML.match(/<div class="file-layout">[\s\S]*?<aside class="file-sources">/)?.[0] || '';
  const fileContent = indexHTML.match(/<div class="file-browser-content">[\s\S]*?<\/footer>\s*<\/div>/)?.[0] || '';
  assert.match(fileLayout, /id="file-back-gesture"[^>]*aria-hidden="true"[^>]*hidden/);
  assert.match(fileContent, /id="file-list"[\s\S]*class="file-status"/);
  assert.ok(indexHTML.indexOf('class="file-table-head"') < indexHTML.indexOf('class="file-browser-content"'));
  assert.match(styleCSS, /#app\[data-mode="files"\]\.file-back-dragging \.file-browser-content\s*\{[^}]*transform:\s*translate3d\(var\(--file-back-x\), 0, 0\)/s);
  assert.match(styleCSS, /#app\[data-mode="files"\]\.file-back-settling \.file-browser-content\s*\{[^}]*transition:\s*transform/s);
  assert.match(styleCSS, /#app\[data-mode="files"\] #file-back-gesture\s*\{[^}]*position:\s*absolute;[^}]*inset:\s*0 auto 0 0;/s);
  assert.doesNotMatch(styleCSS, /file-back-(?:dragging|settling)[^{]*(?:#app|#file-browser|\.file-layout)\s*\{/);
  assert.match(appSrc, /function setFileBackOffset\(offset\)\s*\{\s*\$\('\.file-browser-content'\)/);
  assert.match(appSrc, /function wireFileBackGesture\(\)[\s\S]*?sessionBackDragOffset\(fileBackTouch, point, window\.innerWidth\)/);
  assert.match(appSrc, /settleFileBackGesture\(isSessionBackSwipe\(fileBackTouch, end\)\)/);
  assert.match(appSrc, /fileBackAwaitingHistory = true;[\s\S]*?history\.back\(\)/);
  assert.match(appSrc, /finally\s*\{\s*if \(fromFileBackGesture\) resetFileBackGesture\(\);\s*\}/);
});

test('root viewport stays fixed and uses the active app background', () => {
  assert.match(styleCSS, /html\s*\{[^}]*background:\s*var\(--bg\);[^}]*overscroll-behavior:\s*none;[^}]*-webkit-text-size-adjust:\s*100%;[^}]*text-size-adjust:\s*100%;/s);
  assert.match(styleCSS, /body\s*\{[^}]*overflow:\s*hidden;/s);
  assert.doesNotMatch(indexHTML, /maximum-scale|user-scalable\s*=\s*no/i);
});

test('dashboard and preview disable double-tap zoom without disabling pinch zoom', () => {
  assert.match(styleCSS, /html\s*\{[^}]*touch-action:\s*manipulation;/s);
  assert.match(indexHTML, /id="preview-page"/);
  assert.match(indexHTML, /id="app"/);
  assert.doesNotMatch(indexHTML, /maximum-scale|user-scalable\s*=\s*no/i);
});

test('manual theme selection updates the browser chrome color', () => {
  const themeMeta = testElement('meta');
  themeMeta.content = '';
  appSandbox.document.querySelector = (selector) => selector === 'meta[name="theme-color"]' ? themeMeta : null;
  applyTheme('light');
  assert.equal(themeMeta.content, '#f6f7f9');
  applyTheme('dark');
  assert.equal(themeMeta.content, '#090c12');
  assert.equal((indexHTML.match(/<meta name="theme-color"/g) || []).length, 1);
});

test('mobile text controls prevent iOS focus zoom without disabling pinch zoom', () => {
  assert.match(styleCSS, /--t-control:\s*16px;/);
  assert.match(styleCSS, /input:not\(\[type="checkbox"\]\):not\(\[type="radio"\]\):not\(\[type="file"\]\), textarea, select\s*\{\s*font-size:\s*var\(--t-control\);\s*\}/);
  assert.doesNotMatch(indexHTML, /maximum-scale|user-scalable\s*=\s*no/i);
});

test('self-drawn assistant messages fill the conversation column', () => {
  assert.match(styleCSS, /\.chat-row\.assistant \.chat-card\s*\{[^}]*width:\s*100%;[^}]*max-width:\s*none;/s);
  assert.doesNotMatch(styleCSS, /\.chat-row\.user \.chat-card,\s*\.chat-row\.assistant \.chat-card/);
});

test('turn pin follows the latest user message that has crossed the scroll top', () => {
  const row = (text, bottom) => ({
    dataset: { chatTurnPin: text },
    getBoundingClientRect: () => ({ bottom }),
  });
  const rows = [row('first question', 80), row('synced question', 220), row('latest question', 480)];
  assert.equal(chatTurnPinText(rows, 40), '');
  assert.equal(chatTurnPinText(rows, 120), 'first question');
  assert.equal(chatTurnPinText(rows, 300), 'synced question');
  assert.equal(chatTurnPinText(rows, 600), 'latest question');
  assert.match(appSrc, /row\.dataset\.chatTurnPin\s*=\s*firstChatLine\(item\.text\)/);
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

test('tool-only activity summaries keep the existing compatibility fallback', () => {
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

test('Codex activity traces omit internal reasoning items', () => {
  assert.equal(typeof isChatTraceItem, 'function');
  assert.equal(isChatTraceItem({ type: 'reasoning' }), false);
  assert.equal(isChatTraceItem({ type: 'tool', kind: 'commandExecution' }), true);
  assert.equal(isChatTraceItem({ type: 'tool', kind: 'imageView' }), true);
  assert.equal(isChatTraceItem({ type: 'tool', kind: 'computerUse' }), true);
  assert.equal(isChatTraceItem({ type: 'diff' }), true);
  assert.equal(isChatTraceItem({ type: 'context' }), false);
  assert.equal(isChatTraceItem({ type: 'assistant' }), false);
});

test('tool-only fallback keeps native standalone tool boundaries', () => {
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'commandExecution' }), true);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'fileRead' }), true);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'webSearch' }), true);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'mcpToolCall', title: 'Chrome · Read' }), true);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'dynamicToolCall' }), true);
  assert.equal(isChatActivityItem({ type: 'diff' }), true);

  assert.equal(isChatActivityItem({ type: 'tool', kind: 'mcpToolCall', title: 'computer-use · click' }), false);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'imageView' }), false);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'imageGeneration' }), false);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'collabAgentToolCall' }), false);
  assert.equal(isChatActivityItem({ type: 'tool', kind: 'subAgentActivity' }), false);
});

test('active reasoning appears immediately as a private thinking status', () => {
  let model = reduceChatEvent(createChatState(), {
    type: 'turn_started', turnId: 'turn-1', data: {},
  });
  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-1', turnId: 'turn-1',
    data: { status: 'inProgress', summary: '**Private reasoning**\n\nNever render this text' },
  });
  assert.equal(chatTurnProgress(model), 'thinking');
  assert.equal(chatRenderUnits(model.messages.map((id) => ({ id, item: model.items[id] }))).length, 0);

  const row = renderChatTurnProgress(chatTurnProgress(model));
  assert.equal(row.className, 'chat-row assistant thinking');
  assert.match(nodeText(row), /正在思考/);
  assert.doesNotMatch(nodeText(row), /Private reasoning|Never render/);

  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-2', turnId: 'turn-1',
    data: { status: 'completed', summary: 'Another private segment' },
  });
  assert.equal(chatTurnProgress(model), 'thinking');
  assert.equal(renderChatTurnProgress(chatTurnProgress(model)).className, 'chat-row assistant thinking');
  assert.equal(chatRenderUnits(model.messages.map((id) => ({ id, item: model.items[id] }))).length, 0);

  const idle = reduceChatEvent(model, {
    type: 'turn_done', turnId: 'turn-1', data: { status: 'completed' },
  });
  assert.equal(chatTurnProgress(idle), '');
  assert.equal(renderChatTurnProgress(chatTurnProgress(idle)), null);
});

test('turn progress follows event order instead of item insertion order', () => {
  let model = reduceChatEvent(createChatState(), {
    type: 'turn_started', turnId: 'turn-1', data: {},
  });
  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-1', turnId: 'turn-1',
    data: { status: 'inProgress', summary: 'First pass' },
  });
  model = reduceChatEvent(model, {
    type: 'tool_update', itemId: 'tool-1', turnId: 'turn-1',
    data: { kind: 'commandExecution', summary: 'pwd', status: 'inProgress' },
  });
  assert.equal(chatTurnProgress(model), '');

  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-1', turnId: 'turn-1',
    data: { status: 'inProgress', summary: 'Second pass' },
  });
  assert.deepEqual(Array.from(model.messages), ['reason-1', 'tool-1']);
  assert.equal(chatTurnProgress(model), 'thinking');

  model = reduceChatEvent(model, {
    type: 'plan_update', itemId: 'plan-1', turnId: 'turn-1',
    data: { status: 'inProgress', text: 'Visible plan' },
  });
  assert.equal(chatTurnProgress(model), '');

  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-2', turnId: 'turn-1',
    data: { status: 'completed', summary: 'Final pass' },
  });
  assert.equal(chatTurnProgress(model), 'thinking');

  model = reduceChatEvent(model, {
    type: 'assistant_delta', itemId: 'assistant-1', turnId: 'turn-1', data: { delta: 'Visible answer' },
  });
  assert.equal(chatTurnProgress(model), '');
});

test('late events from an old turn cannot overwrite active turn progress', () => {
  let model = reduceChatEvent(createChatState(), {
    type: 'turn_started', turnId: 'turn-2', data: {},
  });
  model = reduceChatEvent(model, {
    type: 'reasoning_update', itemId: 'reason-2', turnId: 'turn-2',
    data: { status: 'inProgress', summary: 'Current turn' },
  });
  model = reduceChatEvent(model, {
    type: 'tool_done', itemId: 'old-tool', turnId: 'turn-1',
    data: { kind: 'commandExecution', summary: 'old output', status: 'completed' },
  });
  assert.equal(chatTurnProgress(model), 'thinking');
});

test('reasoning stays hidden while native tool rows remain visible', () => {
  assert.equal(typeof renderChatActivityRun, 'function');
  const items = [
    {
      type: 'reasoning', summary: '**Inspecting files**\n\nReasoning detail',
      status: 'completed', durationMs: 0,
    },
    { type: 'tool', kind: 'commandExecution', summary: 'npm test', status: 'completed' },
    {
      type: 'tool', kind: 'imageView', title: '查看图片', summary: '/tmp/shot.png',
      mediaPath: '/tmp/shot.png', status: 'completed',
    },
  ];
  const rows = renderChatActivityRun(items);
  assert.equal(rows.length, 2);
  assert.deepEqual([...rows].map((row) => row.className), ['chat-row tool', 'chat-row tool']);
  const text = rows.map(nodeText).join(' ');
  assert.doesNotMatch(text, /Thought|Inspecting files|Reasoning detail/);
  assert.match(text, /npm test/);
  assert.match(text, /\/tmp\/shot\.png/);
});

test('tool-first traces keep Codex activity summaries instead of becoming Thought', () => {
  const rows = renderChatActivityRun([
    {
      type: 'tool', kind: 'commandExecution', summary: 'sed -n 1,80p server/dashboard/app.js',
      commandActions: [{ type: 'read', path: 'server/dashboard/app.js' }], status: 'completed',
    },
    { type: 'reasoning', summary: '**Checking the result**\n\nInternal transition', status: 'completed' },
    { type: 'diff', files: [{ path: 'server/dashboard/app.js' }], status: 'inProgress' },
  ]);
  assert.equal(rows.length, 1);
  assert.equal(rows[0].className, 'chat-row tool activity-group');
  assert.match(nodeText(rows[0]), /正在编辑文件/);
  assert.doesNotMatch(nodeText(rows[0]), /Thought|Internal transition/);
});

test('context compaction and messages cut Codex activity traces', () => {
  assert.equal(typeof chatRenderUnits, 'function');
  const units = chatRenderUnits([
    { id: 'reason-1', item: { type: 'reasoning', summary: '**One**' } },
    { id: 'tool-1', item: { type: 'tool', kind: 'commandExecution' } },
    { id: 'context-1', item: { type: 'context' } },
    { id: 'reason-2', item: { type: 'reasoning', summary: '**Two**' } },
    { id: 'assistant-1', item: { type: 'assistant', text: 'Done' } },
  ]);
  assert.deepEqual(
    JSON.parse(JSON.stringify(units.map((unit) => [unit.kind, unit.entries.map((entry) => entry.id)]))),
    [
      ['trace', ['tool-1']],
      ['item', ['context-1']],
      ['item', ['assistant-1']],
    ],
  );
});

test('tool-only runs preserve image and computer-use standalone rows', () => {
  assert.equal(typeof renderChatActivityRun, 'function');
  const rows = renderChatActivityRun([
    { type: 'tool', kind: 'commandExecution', summary: 'pwd', status: 'completed' },
    { type: 'tool', kind: 'imageView', summary: '/tmp/shot.png', status: 'completed' },
    { type: 'tool', kind: 'mcpToolCall', title: 'computer-use · click', status: 'completed' },
  ]);
  assert.equal(rows.length, 3);
  assert.deepEqual([...rows].map((row) => row.className), ['chat-row tool', 'chat-row tool', 'chat-row tool']);
});

test('Codex transcript exposes thinking state without reasoning content', () => {
  assert.doesNotMatch(appSrc, /chatThoughtIcon|chat-thought|Thought for|['"]Thought['"]/);
  assert.doesNotMatch(styleCSS, /chat-reasoning|chat-thought|chat-activity-trace/);
  assert.match(appSrc, /if \(entry\?\.item\?\.type === 'reasoning'\) continue;/);
  assert.match(appSrc, /if \(item\.type === 'reasoning'\) \{\s*return null;/);
  assert.match(appSrc, /renderChatTurnProgress\(FleetChatModel\.chatTurnProgress\(model\)\)/);
  assert.match(appSrc, /class: 'chat-thinking'/);
  assert.match(appSrc, /text: '正在思考'/);
  assert.match(styleCSS, /\.chat-thinking-dots span/);
  assert.match(appSrc, /上下文已自动压缩/);
});

test('activity group summary follows Codex part ordering and leading labels', () => {
  const items = [
    { type: 'tool', kind: 'mcpToolCall', title: 'Chrome · Read', summary: 'chrome · read', status: 'completed' },
    {
      type: 'tool', kind: 'commandExecution', summary: 'sed -n 1,80p .agents/skills/dev/SKILL.md',
      commandActions: [{ type: 'read', path: '/repo/.agents/skills/dev/SKILL.md' }], status: 'completed',
    },
    { type: 'diff', files: [{ path: 'app.js' }], status: 'completed' },
    { type: 'tool', kind: 'commandExecution', summary: 'rg activity server/dashboard', status: 'completed' },
    { type: 'tool', kind: 'commandExecution', summary: 'npm test', status: 'completed' },
    { type: 'tool', kind: 'webSearch', summary: 'Codex activity', status: 'completed' },
  ];
  assert.equal(
    chatActivityGroupSummaryText(items),
    '已使用 Chrome 集成加载了一个工具编辑了一个文件读取文件运行了一个命令已搜索网页',
  );
  assert.equal(chatActivityGroupIconKind(items), 'mcpToolCall');
  assert.equal(chatActivityGroupIconKind(items.slice(1)), 'commandExecution');
  assert.equal(chatActivityGroupIconKind(items.slice(2)), 'fileChange');
  assert.equal(chatActivityGroupIconKind(items.slice(3)), 'fileRead');
});

test('running activity group uses the active item summary', () => {
  assert.deepEqual([...chatActivityActiveSummarySegments({ kind: 'commandExecution', summary: 'bash -lc npm test', status: 'inProgress' })], ['正在运行命令']);
  assert.deepEqual([...chatActivityActiveSummarySegments({ kind: 'mcpToolCall', title: 'Chrome · Read', summary: 'chrome · read', status: 'inProgress' })], ['正在使用 Chrome 集成']);
  assert.deepEqual([...chatActivityActiveSummarySegments({ type: 'diff', status: 'inProgress' })], ['正在编辑文件']);
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
      status: 'inProgress',
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
  assert.equal(state.items.diff1.status, 'inProgress');
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

test('server requests preserve their native method and project dedicated interaction types', () => {
  let state = reduceChatEvent(createChatState(), {
    type: 'interaction_request',
    itemId: 'ask1',
    data: {
      requestId: '71',
      requestMethod: 'item/tool/requestUserInput',
      questions: [{ id: 'scope', header: 'Scope', question: 'Which scope?' }],
    },
  });
  assert.equal(state.items['71'].type, 'request_user_input');
  assert.equal(state.requests['71'].requestMethod, 'item/tool/requestUserInput');
  assert.equal(state.items['71'].questions[0].id, 'scope');

  state = reduceChatEvent(state, {
    type: 'interaction_request',
    itemId: 'mcp1',
    data: {
      requestId: '72',
      requestMethod: 'mcpServer/elicitation/request',
      serverName: 'demo',
      mode: 'form',
      requestedSchema: { type: 'object', properties: { name: { type: 'string' } } },
    },
  });
  assert.equal(state.items['72'].type, 'elicitation');
  assert.equal(state.items['72'].requestedSchema.properties.name.type, 'string');

  state = reduceChatEvent(state, {
    type: 'interaction_resolved',
    data: { requestId: '71', response: { answers: { scope: { answers: ['all'] } } } },
  });
  assert.equal(state.requests['71'].status, 'resolved');
});

test('image tool projection keeps a renderable media path and command actions', () => {
  const state = reduceChatEvent(createChatState(), {
    type: 'tool_update',
    itemId: 'image1',
    data: {
      kind: 'imageGeneration',
      summary: '/tmp/result.png',
      mediaPath: '/tmp/result.png',
      commandActions: [{ type: 'read', path: '/tmp/result.png' }],
    },
  });
  assert.equal(state.items.image1.mediaPath, '/tmp/result.png');
  assert.equal(state.items.image1.commandActions[0].type, 'read');
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

test('hidden reasoning cannot consume the completed turn metadata anchor', () => {
  const model = {
    messages: ['assistant-1', 'reasoning-1'],
    items: {
      'assistant-1': {
        id: 'assistant-1', type: 'assistant', turnId: 'turn-1', text: 'Done',
        turnComplete: true, model: 'gpt-5.6-sol', completedAtMs: fixedAppNowMs,
      },
      'reasoning-1': {
        id: 'reasoning-1', type: 'reasoning', turnId: 'turn-1',
        summary: 'Internal', status: 'completed',
      },
    },
  };
  const visible = chatMessageMetaVisibility(model);
  assert.equal(visible.get('assistant-1'), model.items['assistant-1']);
  assert.equal(visible.has('reasoning-1'), false);
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
  assert.match(appSrc, /restoreChatComposerItem\(chat, item\)/);
  assert.match(appSrc, /Skill 列表加载失败，消息未发送/);
  assert.match(appSrc, /isNoActiveTurnError\(e\)/);
  assert.match(appSrc, /reconcileChatInactiveTurn\(chat, observedTurnId\)/);
  assert.match(appSrc, /await flushChatFollowups\(chat\)/);
});

test('new Codex sessions open a local self-drawn draft before starting', () => {
  const source = appSrc.slice(appSrc.indexOf('async function newSessionIn('), appSrc.indexOf('// 终端头：'));
  const starter = appSrc.slice(appSrc.indexOf('async function ensurePendingChatStarted('), appSrc.indexOf('async function submitChatInput('));
  assert.match(source, /canSelfDrawChat\(\)/);
  assert.match(source, /openPendingChatSession\(/);
  assert.doesNotMatch(source, /'chat\/start'/);
  assert.match(source, /api\(macId,\s*'new'/);
  assert.match(starter, /'chat\/start'/);
  assert.match(starter, /state\.chatCache\.set\(newKey, chat\)/);
});

test('fresh Codex sessions initialize model options without calling resume', () => {
  const source = appSrc.match(/async function openChatSession\(s\) \{[\s\S]*?\n\}\n\nconst CHAT_EFFORT_LABELS/)?.[0] || '';
  assert.match(source, /if \(s\.fresh\)/);
  assert.match(source, /chat\.historyReady = true/);
  assert.match(source, /configureChatOptions\(chat,\s*s\.startOptions \|\| \{\}\)/);
  assert.match(source, /startChatEvents\(chat\)/);
  assert.match(source, /if \(s\.fresh\)[\s\S]*?return;[\s\S]*?'chat\/resume'/);
});
