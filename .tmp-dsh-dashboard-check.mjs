// 临时自检（不入库）：把真实的 server/dashboard/app.js 放进 vm 沙箱，断言本次新增的能力门控、
// 审批按钮与降级横幅判定。复用 chat_model.test.mjs 的沙箱搭法。
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const appSrc = await readFile(new URL('./server/dashboard/app.js', import.meta.url), 'utf8');
const uploadSrc = await readFile(new URL('./server/dashboard/upload_model.js', import.meta.url), 'utf8');
const modelSrc = await readFile(new URL('./server/dashboard/chat_model.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./server/dashboard/index.html', import.meta.url), 'utf8');

const load = (src, sand) => { vm.createContext(sand); vm.runInContext(src, sand); return sand; };
const modelSandbox = load(modelSrc, { globalThis: {} });
const uploadSandbox = load(uploadSrc, { globalThis: {} });

function testElement(tag) {
  const el = {
    nodeType: 1, tagName: tag, className: '', dataset: {}, attributes: {}, children: [], textContent: '', parent: null,
    append(...nodes) { for (const n of nodes) { if (n?.nodeType === 1) n.parent = el; el.children.push(n); } },
    prepend(...nodes) { for (const n of nodes) { if (n?.nodeType === 1) n.parent = el; } el.children.unshift(...nodes); },
    replaceChildren(...nodes) { el.children = [...nodes]; },
    remove() { if (el.parent) el.parent.children = el.parent.children.filter((c) => c !== el); },
    setAttribute(name, value) { el.attributes[name] = String(value); },
    getAttribute(name) { return el.attributes[name] ?? null; },
  };
  return el;
}
// 会话列表容器：给同步横幅的 DOM 断言用（querySelector 真的在子节点里找）
const sessionGroups = testElement('div');
sessionGroups.querySelector = (sel) => {
  if (sel === '[data-dsh-degraded]') return sessionGroups.children.find((c) => c.dataset?.dshDegraded) || null;
  if (sel === '.grp, .empty, .ses') return sessionGroups.children.length ? testElement('div') : testElement('div');
  return null;
};
sessionGroups.querySelectorAll = () => [];
const assistantButtons = [
  { dataset: { assistant: 'codex' }, hidden: false },
  { dataset: { assistant: 'claude' }, hidden: false },
  { dataset: { assistant: 'dsh' }, hidden: true },
];
const appSandbox = {
  document: {
    documentElement: { style: { setProperty() {}, removeProperty() {} }, setAttribute() {}, classList: { toggle() {} } },
    addEventListener() {},
    createElement: testElement,
    createElementNS: (_ns, tag) => testElement(tag),
    createTextNode: (text) => ({ nodeType: 3, textContent: String(text), children: [] }),
    querySelector(sel) { return sel === '#session-groups' ? sessionGroups : null; },
    querySelectorAll(sel) { return sel === '[data-assistant="dsh"]' ? assistantButtons.filter((b) => b.dataset.assistant === 'dsh') : []; },
  },
  localStorage: { getItem: () => null, setItem() {} },
  EventSource: class { constructor() { this.readyState = 1; } close() {} },
  FleetChatModel: modelSandbox.globalThis.FleetChatModel,
  FleetUploadModel: uploadSandbox.globalThis.FleetUploadModel,
  FleetMarkdown: { renderMarkdown: (t) => { const n = testElement('div'); n.textContent = String(t || ''); return n; } },
  matchMedia: () => ({ matches: false }),
};
load(`${appSrc}
;globalThis.__probe = {
  normalizeAssistant, assistantLabel, assistantCapabilities, canSelfDrawChat, dshReady, dshDegraded,
  syncAssistantTabs, syncDshBanner, renderChatApprovalRequest, state,
  setMacs(list, online) { MACS = list.map((id) => ({ id })); state.nodes = online || {}; },
};`, appSandbox);
const app = appSandbox.__probe;
const { state } = app;

// 1) 白名单：三值 + 非法值回退 codex
assert.equal(app.normalizeAssistant('codex'), 'codex');
assert.equal(app.normalizeAssistant('claude'), 'claude');
assert.equal(app.normalizeAssistant('dsh'), 'dsh');
assert.equal(app.normalizeAssistant('garbage'), 'codex');
assert.equal(app.normalizeAssistant(''), 'codex');
assert.equal(app.normalizeAssistant(undefined), 'codex');
assert.equal(app.normalizeAssistant('DSH'), 'codex');
console.log('✓ normalizeAssistant: codex/claude/dsh 原样，garbage/""/undefined/"DSH" → codex');

// 2) 标签
assert.equal(app.assistantLabel('codex'), 'Codex');
assert.equal(app.assistantLabel('claude'), 'Claude');
assert.equal(app.assistantLabel('dsh'), 'DeepSeek');
assert.equal(app.assistantLabel('garbage'), 'Codex');
console.log('✓ assistantLabel: dsh → DeepSeek');

// 3) 能力表（含 dsh 的 /api/info 门控）
app.setMacs(['m1', 'm2'], { m1: true, m2: true });
state.macId = 'm1';
state.sessionMacId = 'all';
const cap = (a, m) => app.assistantCapabilities(a, m);
assert.deepEqual({ ...cap('codex') }, { selfDraw: true, sessionCursor: true, terminal: true });
assert.deepEqual({ ...cap('claude') }, { selfDraw: false, sessionCursor: false, terminal: true });
assert.equal(cap('dsh').selfDraw, false, 'DSH 没有 /api/info 时不可自绘');
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: false } } };
assert.equal(app.dshReady('m1'), true);
assert.equal(cap('dsh', 'm1').selfDraw, true);
assert.equal(cap('dsh', 'm2').selfDraw, false, '另一台没报能力');
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: true } } };
assert.equal(cap('dsh', 'm1').selfDraw, false, 'degraded 不自绘');
state.assistantInfo = { m1: { dsh: { enabled: false, hostRunning: true, degraded: false } } };
assert.equal(cap('dsh', 'm1').selfDraw, false, 'enabled=false 不自绘');
state.assistantInfo = { m1: {} };
assert.equal(cap('dsh', 'm1').selfDraw, false, '旧 agent 没有 dsh 块');
console.log('✓ assistantCapabilities: codex 自绘、claude 不自绘、dsh 需 enabled && !degraded');

// 4) canSelfDrawChat 三值
state.selfDraw = true;
state.mode = 'sessions';
assert.equal(app.canSelfDrawChat('codex', 'm1'), true);
assert.equal(app.canSelfDrawChat('claude', 'm1'), false);
assert.equal(app.canSelfDrawChat('dsh', 'm1'), false);
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: false } } };
assert.equal(app.canSelfDrawChat('dsh', 'm1'), true);
state.mode = 'files';
assert.equal(app.canSelfDrawChat('codex', 'm1'), false);
state.mode = 'sessions'; state.selfDraw = false;
assert.equal(app.canSelfDrawChat('codex', 'm1'), false);
state.selfDraw = true;
console.log('✓ canSelfDrawChat: 自绘开关 × 模式 × 能力');

// 5) 审批按钮按助手渲染
function buttonLabels(node, out = []) {
  if (!node) return out;
  if (node.tagName === 'button') out.push(String(node.textContent || [...(node.children || [])].map((c) => c.textContent).join('')));
  for (const child of node.children || []) buttonLabels(child, out);
  return out;
}
const findButtons = (node, out = []) => {
  if (!node) return out;
  if (node.tagName === 'button') out.push(node);
  for (const child of node.children || []) findButtons(child, out);
  return out;
};
const command = { requestId: 'r1', status: 'pending', kind: 'command', command: 'ls', raw: {} };
const permission = { requestId: 'r2', status: 'pending', kind: 'permission', raw: { permissions: { network: true } } };
state.assistant = 'dsh';
let node = app.renderChatApprovalRequest(command);
assert.deepEqual(buttonLabels(node), ['允许一次', '拒绝']);
node = app.renderChatApprovalRequest(permission);
assert.deepEqual(buttonLabels(node), ['允许一次', '拒绝']);
assert.ok(!buttonLabels(node).includes('本会话允许'));
state.assistant = 'codex';
assert.deepEqual(buttonLabels(app.renderChatApprovalRequest(command)), ['允许一次', '本会话允许', '拒绝']);
assert.deepEqual(buttonLabels(app.renderChatApprovalRequest(permission)), ['允许本轮', '允许本会话', '拒绝']);
state.assistant = 'claude';
assert.deepEqual(buttonLabels(app.renderChatApprovalRequest(command)), ['允许一次', '本会话允许', '拒绝']);
console.log('✓ 审批按钮: dsh 只给「允许一次 / 拒绝」，codex / claude 维持三个按钮');

// 6) tab 显隐 + 降级横幅判定
state.assistant = 'dsh';
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: false } } };
app.syncAssistantTabs();
assert.equal(assistantButtons.find((b) => b.dataset.assistant === 'dsh').hidden, false, 'enabled → 放出 DeepSeek tab');
assert.equal(app.dshDegraded(), false);
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: false, degraded: true } } };
assert.equal(app.dshDegraded(), true, 'degraded=true → 横幅');
assert.equal(assistantButtons.find((b) => b.dataset.assistant === 'dsh').hidden, false, '降级仍保留只读入口');
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: false, degraded: false } } };
assert.equal(app.dshDegraded(), true, 'hostRunning=false 也算降级');
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: false } } };
state.assistant = 'codex';
assert.equal(app.dshDegraded(), false, '非 dsh 助手不出横幅');
console.log('✓ DeepSeek tab: enabled 才显示；降级（degraded/hostRunning=false）出横幅');

// 7) index.html：两处 seg 都有 DeepSeek，且默认 hidden
const dshButtons = [...indexHTML.matchAll(/<button data-assistant="dsh"[^>]*>DeepSeek<\/button>/g)];
assert.equal(dshButtons.length, 2, '桌面 + 移动各一个');
assert.ok(dshButtons.every((m) => /\bhidden\b/.test(m[0])), '默认隐藏');
console.log('✓ index.html: 两处 seg 各加一个默认 hidden 的 DeepSeek 按钮');

// 8) 降级横幅：文案 + 顶部插入 + 幂等
state.assistant = 'dsh';
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: true, degraded: false } } };
app.syncDshBanner();
assert.equal(sessionGroups.children.length, 0, '没降级不出横幅');
state.assistantInfo = { m1: { dsh: { enabled: true, hostRunning: false, degraded: true } } };
app.syncDshBanner();
assert.equal(sessionGroups.children.length, 1);
assert.equal(sessionGroups.children[0].className, 'session-partial-note');
assert.equal(sessionGroups.children[0].dataset.dshDegraded, '1');
assert.equal(sessionGroups.children[0].textContent, 'DSH Desktop 未运行，当前仅显示磁盘会话（只读）');
sessionGroups.children[0].remove(); // 让下一次 prepend 的计数干净：真正的幂等由 syncDshBanner 内部摘旧节点保证
app.syncDshBanner();
assert.equal(sessionGroups.children.length, 1, '重复调用不叠加');
console.log('✓ 降级横幅: 文案与样式类正确，且幂等（不叠加）');

// 9) dsh 不可用时回退 codex（探测不到能力就不停在空列表上）
const fallback = {
  document: {
    documentElement: { style: { setProperty() {}, removeProperty() {} }, setAttribute() {}, classList: { toggle() {} } },
    addEventListener() {},
    createElement: testElement,
    createElementNS: (_ns, tag) => testElement(tag),
    createTextNode: (text) => ({ nodeType: 3, textContent: String(text), children: [] }),
    querySelector() { return null; },
    querySelectorAll() { return []; },
  },
  localStorage: { getItem: () => null, setItem() {} },
  EventSource: class { constructor() { this.readyState = 1; } close() {} },
  FleetChatModel: modelSandbox.globalThis.FleetChatModel,
  FleetUploadModel: uploadSandbox.globalThis.FleetUploadModel,
  FleetMarkdown: { renderMarkdown: (t) => { const n = testElement('div'); n.textContent = String(t || ''); return n; } },
  matchMedia: () => ({ matches: false }),
};
vm.createContext(fallback);
vm.runInContext(`${appSrc}
;setAssistant = (a) => { globalThis.__fellBackTo = a; };
globalThis.__probe2 = { syncAssistantTabs, state };
globalThis.__probe2.state.assistant = 'dsh';
globalThis.__probe2.state.assistantInfo = {};
syncAssistantTabs();`, fallback);
assert.equal(fallback.__fellBackTo, 'codex');
console.log('✓ dsh 不可用（探测不到能力）→ 自动回退 codex');

console.log('\n全部自检通过');
