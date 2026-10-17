import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFileSync } from 'node:fs';

const app = readFileSync(new URL('./app.js', import.meta.url), 'utf8');
function source(name) {
  const match = new RegExp(`(?:async )?function ${name}\\(`).exec(app);
  assert.ok(match, name);
  return app.slice(match.index, app.indexOf('\n}', match.index) + 2);
}
function element(tag, props = {}, ...children) {
  return {
    tag, ...props, children: children.filter(Boolean), scrollTop: 0,
    append(...nodes) { nodes.forEach(n => { n.parent = this; this.children.push(n); }); },
    replaceChildren(...nodes) { this.children = nodes; },
    remove() { this.parent.children = this.parent.children.filter(n => n !== this); },
    querySelector() { return null; },
    classList: { toggle() {}, remove() {} },
  };
}
function text(node) { return (node.text || '') + (node.children || []).map(text).join('\n'); }
function harness(overrides = {}) {
  const wrap = element('div');
  const state = {
    mode: 'sessions', assistant: 'codex', scope: 'active', sessionMacId: 'm4', sessionSearch: '',
    sessionView: 'recent', sessionResults: [], sessionErrors: {}, sessionProjects: [], collapsed: new Set(),
    nodes: { m4: true }, assistantInfo: {}, sessionCursors: {}, counts: {},
    ...overrides,
  };
  const context = {
    state, MACS: [{ id: 'm3' }, { id: 'm4' }], sessionLoadSeq: 0,
    $: () => wrap,
    $$: () => wrap.children.filter(n => n.dataset?.sessionFailure),
    h: element, clear: node => node.replaceChildren(), macName: id => id === 'm4' ? 'mac-dev' : 'Mac Mini',
    requestAnimationFrame() {}, maybeLoadMoreSessions() {}, syncEmptyChatComposer() {}, syncDshBanner() {},
    sessionRow: session => element('div', { class: 'ses', text: session.title }),
    groupSessionsByProject: () => [], FleetCore: { orderedSessionGroups: () => [] },
    setSessionsLoading() {}, renderSessionSkeleton() {}, sessionHasMore: () => true,
    assistantCapabilities: () => ({ sessionCursor: true }), normalizeDeviceSessions: (macId, data) => data.sessions.map(s => ({ ...s, macId })),
    api: async () => ({ projects: [] }), sessionKey: session => `${session.macId}/${session.sessionId}`,
    observeSessionActivity() {}, updateCachedChatFromSession() {},
    probeAssistantInfo: async () => ({ codexAppServerMode: 'shared', codexAppServerConnected: false }),
  };
  vm.createContext(context);
  vm.runInContext([
    'sessionLoadError', 'sessionConnectionProblem', 'sessionConnectionFailures', 'syncSessionConnectionNotes',
    'renderSessionResults', 'sessionTargetMacs', 'loadSessions',
  ].map(source).join('\n'), context);
  return { context, state, wrap, render: () => context.renderSessionResults() };
}

test('failed lists show reason and recovery, never claim active, archived, or searched sessions are empty', () => {
  for (const scope of ['active', 'all']) for (const sessionView of ['recent', 'project']) for (const sessionSearch of ['', 'query']) {
    const h = harness({ scope, sessionView, sessionSearch, sessionErrors: { m4: {
      code: 'appserver_unavailable', status: 503, info: { codexAppServerMode: 'shared', codexAppServerConnected: false },
    } } });
    h.render();
    const output = text(h.wrap);
    assert.match(output, /mac-dev · 无法加载会话/);
    assert.match(output, /原因：设备在线，但 ChatGPT 共享连接尚未建立/);
    assert.match(output, /解决办法：.*等当前任务结束后完全退出/);
    assert.doesNotMatch(output, /没有未归档会话|没有已归档会话|没有匹配的会话|暂无会话/);
  }
});

test('genuine empty lists retain their empty states and partial success retains sessions', () => {
  const empty = harness(); empty.render();
  assert.match(text(empty.wrap), /没有未归档会话/);
  const partial = harness({ sessionMacId: 'all', sessionResults: [{ macId: 'm3', title: 'Available session' }],
    sessionErrors: { m4: { status: 502 } } });
  partial.render();
  assert.match(text(partial.wrap), /Available session/);
  assert.match(text(partial.wrap), /无法连接设备服务/);
  assert.doesNotMatch(text(partial.wrap), /没有未归档会话/);
});

test('failure reasons separate authorization, offline, transport, timeout, DSH, and unknown errors', () => {
  const { context, state } = harness();
  const cases = [
    [{ status: 401 }, /授权已失效/, /重新登录/],
    [{ status: 403 }, /没有设备访问权限/, /账号一致/],
    [{ status: 404 }, /设备已移除/, /刷新设备列表/],
    [{ status: 503, code: '设备需要升级并重新关联' }, /授权协议需要更新/, /更新/],
    [{ status: 504 }, /连接设备超时/, /网络/],
    [{ status: 504, code: 'appserver_timeout' }, /ChatGPT 会话服务响应超时/, /Codex Desktop/],
    [{ status: 500, code: 'chat_failed', message: 'dsh_host_unavailable' }, /DeepSeek Desktop/, /DSH Desktop/],
    [{ status: 503, code: 'appserver_unavailable', info: { codexAppServerConnected: true } }, /会话服务暂时不可用/, /更新 Fleet/],
    [{ status: 500 }, /暂时无法确定原因/, /Fleet 日志/],
  ];
  for (const [error, reason, solution] of cases) {
    const problem = context.sessionConnectionProblem('m4', error);
    assert.match(problem.reason, reason); assert.match(problem.solution, solution);
  }
  state.nodes.m4 = false;
  assert.match(context.sessionConnectionProblem('m4', { status: 502 }).reason, /设备离线/);
  state.gatewayDown = true;
  assert.match(context.sessionConnectionProblem('m4', { status: 502 }).reason, /Fleet 网关/);
  assert.match(context.sessionConnectionProblem('m4', { status: 401 }).reason, /授权已失效/);
});

test('load failure keeps typed backend evidence and uses a bounded fresh info probe', async () => {
  const { context } = harness();
  const error = Object.assign(new Error('连接不可用'), { status: 503, code: 'appserver_unavailable' });
  let probes = 0;
  context.probeAssistantInfo = async () => { probes++; return { codexAppServerMode: 'shared', codexAppServerConnected: false }; };
  const failure = await context.sessionLoadError('m4', error, 'codex');
  assert.equal(failure.status, 503); assert.equal(failure.code, 'appserver_unavailable');
  assert.equal(failure.info.codexAppServerConnected, false);
  await context.sessionLoadError('m4', { status: 502 }, 'codex');
  assert.equal(probes, 1);
  context.probeAssistantInfo = async () => { throw new Error('probe timeout'); };
  const unknown = await context.sessionLoadError('m4', error, 'codex');
  assert.equal(unknown.info, null);
  assert.doesNotMatch(context.sessionConnectionProblem('m4', unknown).reason, /共享连接尚未建立/);
});

test('retry recovers the actual list and clears the connection problem', async () => {
  const h = harness({ sessionErrors: { m4: { status: 502 } } });
  h.context.sessionQuery = async () => ({ sessions: [{ sessionId: 'restored', title: 'Restored session' }] });
  h.render();
  const button = h.wrap.children[0].children[0].children[1];
  await button.onclick();
  assert.equal(Object.keys(h.state.sessionErrors).length, 0);
  assert.match(text(h.wrap), /Restored session/);
  assert.doesNotMatch(text(h.wrap), /无法加载会话|没有未归档会话/);
});

test('failed pagination preserves other device errors and stops automatic retry loops', async () => {
  const h = harness({ sessionMacId: 'all', nodes: { m3: true, m4: true }, sessionErrors: { m3: { status: 502 } }, sessionCursors: { m4: 'next' } });
  h.context.sessionQuery = async () => { throw Object.assign(new Error('timeout'), { status: 504 }); };
  await h.context.loadSessions({ append: true });
  assert.deepEqual(Object.keys(h.state.sessionErrors).sort(), ['m3', 'm4']);
  assert.equal(h.state.sessionCursors.m4, '');
});

test('connection notices and DSH degraded notes have no tinted card background', () => {
  const css = readFileSync(new URL('./style.css', import.meta.url), 'utf8');
  assert.match(css, /\.session-connection-note\s*\{[^}]*background:\s*transparent/);
  assert.doesNotMatch(css.match(/\.session-partial-note\s*\{[^}]*}/)?.[0] || '', /background|border-radius|box-shadow/);
});

test('soft project refresh updates AI title in row, cached chat and active header', async () => {
  const session = { macId: 'm4', assistant: 'codex', sessionId: 'thread', title: 'old preview', mtime: 1 };
  const fresh = { ...session, title: 'AI generated title', mtime: 2 };
  const chat = { title: session.title };
  const label = { textContent: session.title };
  const row = { dataset: { mac: 'm4', assistant: 'codex', sid: 'thread' }, classList: { toggle() {} },
    querySelector: selector => selector === '.t' ? label : null };
  const { context, state } = harness({ sessionView: 'project', sessionResults: [session], chatCache: new Map([['cached', chat]]), chat });
  let headerTitle = '';
  Object.assign(context, {
    sessionKey: session => `${session.macId}\n${session.assistant}\n${session.sessionId}`,
    $$: () => [row], sessionQuery: async () => ({ sessions: [fresh] }),
    chatCacheKey: () => 'cached', renderChatOwnershipHead: value => { if (value === state.chat) headerTitle = value.title; },
    sessionHasVisibleRequest: () => false, updateSessionStateDot() {}, syncSessionRuntimeIndicators() {},
    syncSessionConnectionNotes() {},
  });
  vm.runInContext(['updateChatUpdatedAt', 'updateCachedChatFromSession', 'refreshSessionsSoft'].map(source).join('\n'), context);
  await context.refreshSessionsSoft();
  assert.equal(state.sessionResults[0].title, fresh.title);
  assert.equal(label.textContent, fresh.title);
  assert.equal(chat.title, fresh.title);
  assert.equal(headerTitle, fresh.title);
  assert.equal(chat.updatedAt, 2);
});
