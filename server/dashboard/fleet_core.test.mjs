import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

await import('./fleet_core.js');
const core = globalThis.FleetCore;

test('device normalization accepts Headscale and scoped device payloads', () => {
  assert.deepEqual(core.normalizeDevices({ nodes: [
    { givenName: 'gateway', online: true }, { givenName: 'mac10', online: 'true' },
    { name: 'MAC2', online: false }, { name: 'mac10', online: false },
  ] }), [{ id: 'm2', name: '', online: false }, { id: 'm10', name: '', online: false }]);
  assert.deepEqual(core.normalizeDevices({ devices: [{ id: 'm3', name: 'Studio', online: true }] }),
    [{ id: 'm3', name: 'Studio', online: true }]);
  assert.deepEqual(core.normalizeDevices([{ id: 123, name: 'mac1', online: true }]),
    [{ id: 'm1', name: '', online: true }]);
});

test('query builder preserves cursor, search, archived and Claude scope contracts', () => {
  const query = new URLSearchParams(core.sessionQuery({ assistant: 'codex', archived: true, cursor: 'a/b', search: '项目 +' }).split('?')[1]);
  assert.equal(query.get('cursor'), 'a/b');
  assert.equal(query.get('search'), '项目 +');
  assert.equal(query.get('archived'), 'true');
  assert.equal(core.sessionQuery({ assistant: 'claude', paginated: false }), 'sessions?assistant=claude&scope=active');
});

test('project grouping retains metadata and distinct paths with the same name', () => {
  const groups = core.groupSessions([
    { cwd: '/a/project', title: 'one' }, { cwd: '/b/project', title: 'two' }, { projectless: true },
  ], [{ cwd: '/a/project', name: 'Renamed' }, { cwd: '/empty', name: 'Empty' }]);
  assert.equal(groups.length, 4);
  assert.equal(groups[0].name, 'Renamed');
  assert.equal(groups[2].name, '无项目');
});

test('terminal attach uses default permissions and rejects cross-origin and cross-device URLs', async () => {
  let request;
  const api = async (device, path, options) => {
    request = { device, path, body: JSON.parse(options.body) };
    return { sid: 'claude-123', url: '/m1/term/?arg=claude-123' };
  };
  const result = await core.attachTerminal({ api, macId: 'm1', assistant: 'claude', sessionId: '123', origin: 'https://fleet.test:20443' });
  assert.equal(result.url, '/m1/term/?arg=claude-123');
  assert.deepEqual(request.body, { assistant: 'claude', sessionId: '123', mode: 'default' });
  for (const url of ['https://evil.test/m1/term/?arg=x', '/m2/term/?arg=x', '/m1/api/?arg=x', '/m1/term/']) {
    assert.throws(() => core.terminalURL(url, 'm1', 'https://fleet.test:20443'));
  }
  await assert.rejects(core.attachTerminal({ api, macId: 'm1', assistant: 'dsh', sessionId: '123', origin: 'https://fleet.test' }));
});

test('API errors retain backend status and error code through the shared transport', async () => {
  await assert.rejects(core.requestJSON(async () => ({ ok: false, status: 409, json: async () => ({ error: 'writer_busy', message: 'Busy' }) }), '/m1/api/open'),
    (error) => error.status === 409 && error.code === 'writer_busy' && error.message === 'Busy');
});

const workspaceSource = await readFile(new URL('./workspace.js', import.meta.url), 'utf8');
const bridgeSource = await readFile(new URL('./native_bridge.js', import.meta.url), 'utf8');

function workspaceHarness() {
  const calls = [];
  const state = {
    mode: 'sessions', assistant: 'claude', sessionMacId: 'all', nodes: { m1: true, m2: false },
    sessionResults: [{ sessionId: '123', macId: 'm1', assistant: 'claude', title: 'Claude test', cwd: '/project' }],
    sessionErrors: {}, scope: 'active', current: null, sessionProjects: [], collapsed: new Set(),
    sessionView: 'project', sessionSearch: '', assistantInfo: {},
  };
  const context = {
    state, FleetTheme: { preference: 'light' }, setThemePreference: (value) => { context.FleetTheme.preference = value; },
    MACS: [{ id: 'm1' }, { id: 'm2' }], FleetCore: core, location: { origin: 'https://fleet.test' },
    document: { documentElement: { dataset: {} }, querySelector: () => ({ getAttribute: () => 'false' }), getElementById: () => null },
    FleetChatModel: { chatPhase: (status) => status || 'idle' },
    sessionProjectInfo: core.projectInfo,
    sessionStatus: (_, running) => ({ label: running ? '进行中' : '已读', className: running ? 'running' : 'read' }),
    sessionMenuActions: (session) => session.assistant === 'codex' ? ['pin', 'unpin', 'rename', 'archive', 'unarchive', 'delete'] : [],
    dshNativeButtonState: () => ({}), dshEnabled: () => false, dshDegraded: () => false,
    macName: (id) => id, sessionIsUnread: () => false, isSessionRunning: () => false, sessionHasMore: () => false,
    setInterval: () => 1, clearInterval() {}, poolFind: () => null, poolShow: () => calls.push('poolShow'),
    poolAdd: (...values) => { calls.push(values); state.current = { url: values[4] }; },
    activateSession: (session) => { state.selectedSid = session.sessionId; state.selectedSessionMacId = session.macId; state.selectedSessionAssistant = session.assistant; },
    selectSes: (sessionId) => calls.push({ selected: sessionId }),
    api: async (macId, path, options) => { calls.push({ macId, path, body: JSON.parse(options.body) }); return { sid: 'term-123', url: '/m1/term/?arg=term-123' }; },
    canSelfDrawChat: () => true, setMode: (mode) => { state.mode = mode; },
    updateSessionFilterUI() {}, persistUIState: () => calls.push('persist'), renderSessionResults: () => calls.push('render'),
    loadSessions: async () => calls.push('load'), setAssistant: (assistant) => { state.assistant = assistant; },
    newSessionIn: async (cwd, options) => calls.push({ cwd, ...options }),
    mutateSession: async (session, action, value) => calls.push({ session, action, value }),
    backToList: () => calls.push('back'), setFileDevice: (device) => { state.fileMacId = device; },
  };
  vm.createContext(context);
  vm.runInContext(workspaceSource, context);
  return { controller: context.FleetWorkspace, context, state, calls };
}

test('shared controller routes Claude to ttyd without adding native-only terminal behavior to Web', async () => {
  const harness = workspaceHarness();
  const command = { command: 'open', sessionId: '123', macId: 'm1', assistant: 'claude' };
  await assert.rejects(harness.controller.execute(command), /未启用终端/);
  assert.equal(harness.calls.length, 0);
  const result = await harness.controller.execute(command, { allowTerminal: true });
  assert.equal(result.opened, true);
  assert.equal(harness.calls[0].path, 'open');
  assert.equal(harness.controller.snapshot().renderer, 'terminal');
  assert.equal(harness.controller.snapshot().selectedID, 'm1/claude/123');
});

test('shared controller rejects stale sessions and offline devices before starting terminal', async () => {
  const { controller, state, calls } = workspaceHarness();
  await assert.rejects(controller.execute({ command: 'open', sessionId: 'gone', macId: 'm1', assistant: 'claude' }, { allowTerminal: true }), /会话已变化/);
  state.nodes.m1 = false;
  await assert.rejects(controller.execute({ command: 'open', sessionId: '123', macId: 'm1', assistant: 'claude' }, { allowTerminal: true }), /设备离线/);
  assert.equal(calls.length, 0);
});

test('authentication loss publishes empty private data and blocks commands', async () => {
  const { controller, context } = workspaceHarness();
  context.FleetAuth = { user: null };
  context.state.curTitle = 'Private session';
  context.state.sessionErrors = { m1: 'Private device' };
  assert.equal(controller.snapshot().ready, false);
  assert.equal(controller.snapshot().sessions.length, 0);
  assert.equal(controller.snapshot().devices.length, 0);
  assert.equal(controller.snapshot().title, 'Fleet Hub');
  assert.equal(controller.snapshot().errors.length, 0);
  assert.equal(controller.snapshot().identity, '');
  await assert.rejects(controller.execute({ command: 'open' }), /请先登录/);
});

test('bridge is inert in ordinary Web and refuses subframes', () => {
  for (const subframe of [false, true]) {
    let subscriptions = 0;
    const context = {
      __fleetNativeVersion: subframe ? 1 : undefined,
      webkit: { messageHandlers: { fleet: { postMessage() {} } } },
      location: { pathname: '/' }, FleetWorkspace: { subscribe: () => { subscriptions++; } },
    };
    context.top = subframe ? {} : context;
    vm.createContext(context);
    vm.runInContext(bridgeSource, context);
    assert.equal(subscriptions, 0);
    assert.equal(context.FleetNative, undefined);
  }
});

test('native projection includes empty Desktop projects, scoped actions, and capability-gated assistants', () => {
  const { controller, state } = workspaceHarness();
  state.sessionResults[0].assistant = 'codex';
  state.sessionProjects = [{ cwd: '/empty', name: 'Saved empty project', macId: 'm1' }];
  const snapshot = controller.snapshot();
  assert.equal(snapshot.projects.length, 2);
  assert.equal(snapshot.projects[1].sessionIDs.length, 0);
  assert.equal(snapshot.projects[1].macId, 'm1');
  assert.deepEqual(Array.from(snapshot.assistants), ['codex', 'claude']);
  assert.ok(snapshot.sessions[0].actions.includes('rename'));
});

test('project order prioritizes pinned groups before recent groups and keeps live sessions before older ones', () => {
  const groups = core.orderedSessionGroups([
    { cwd: '/recent', mtime: 100 }, { cwd: '/pinned', mtime: 1, pinned: true },
    { cwd: '/recent', mtime: 50, live: true },
  ], [{ cwd: '/empty', macId: 'm1' }]);
  assert.equal(groups[0].key, 'cwd:/pinned');
  assert.equal(groups[1].arr[0].mtime, 50);
  assert.equal(groups[2].arr.length, 0);
});

test('native status retains agent running phase without a ttyd process or cached chat', () => {
  const { controller, state } = workspaceHarness();
  state.sessionResults[0].status = 'running';
  assert.equal(controller.snapshot().sessions[0].running, true);
  assert.equal(controller.snapshot().sessions[0].status, '进行中');
});

test('degraded DSH selection stays in the list without opening chat or ttyd', async () => {
  const { controller, context, state, calls } = workspaceHarness();
  state.assistant = 'dsh';
  state.sessionResults[0].assistant = 'dsh';
  context.canSelfDrawChat = () => false;
  const result = await controller.execute({ command: 'open', macId: 'm1', sessionId: '123', assistant: 'dsh' });
  assert.equal(result.opened, false);
  assert.equal(state.selectedSid, '123');
  assert.deepEqual(calls, [{ selected: '123' }]);
});

test('native view and collapse commands use Web state instead of independent sorting preferences', async () => {
  const { controller, state, calls } = workspaceHarness();
  await controller.execute({ command: 'view', view: 'recent' });
  assert.equal(state.sessionView, 'recent');
  assert.ok(calls.includes('persist'));
  await controller.execute({ command: 'collapse-project', project: 'cwd:/project' });
  assert.equal(controller.snapshot().projects[0].collapsed, true);
  await controller.execute({ command: 'collapse-project', project: 'cwd:/project' });
  assert.equal(controller.snapshot().projects[0].collapsed, false);
  await assert.rejects(controller.execute({ command: 'collapse-project', project: 'gone' }), /项目已变化/);
  await assert.rejects(controller.execute({ command: 'view', view: 'invalid' }), /不支持/);
});

test('native actions validate assistant identity, advertised capabilities, offline devices, and rename values', async () => {
  const { controller, state, calls } = workspaceHarness();
  state.sessionResults[0].assistant = 'codex';
  const command = { command: 'action', macId: 'm1', sessionId: '123', assistant: 'codex', action: 'rename', value: 'Renamed' };
  await controller.execute(command);
  assert.equal(calls[0].value, 'Renamed');
  await assert.rejects(controller.execute({ ...command, assistant: 'dsh' }), /不支持/);
  await assert.rejects(controller.execute({ ...command, value: '' }), /不能为空/);
  state.nodes.m1 = false;
  await assert.rejects(controller.execute(command), /离线/);
});

test('project creation rejects stale and cross-device targets and retains explicit project paths', async () => {
  const { controller, state, calls } = workspaceHarness();
  state.assistant = 'codex';
  const command = { command: 'new', project: 'cwd:/project', macId: 'm1' };
  await controller.execute(command);
  assert.equal(calls[0].cwd, '/project');
  assert.equal(calls[0].unscoped, false);
  await assert.rejects(controller.execute({ ...command, project: 'cwd:/gone' }), /项目已变化/);
  await controller.execute({ command: 'new', macId: 'm1' });
  assert.equal(calls[1].cwd, '');
  assert.equal(calls[1].unscoped, true);
});

test('file mode has a concrete scope and refuses all-device navigation or unauthorized admin pages', async () => {
  const { controller, state } = workspaceHarness();
  await controller.execute({ command: 'files', macId: 'm1' });
  assert.equal(controller.snapshot().deviceScope, 'm1');
  await assert.rejects(controller.execute({ command: 'device', device: 'all' }), /需要选择/);
  await assert.rejects(controller.execute({ command: 'page', page: 'admin' }), /管理员/);
  await assert.rejects(controller.execute({ command: 'assistant', assistant: 'dsh' }), /不支持/);
  await controller.execute({ command: 'sessions' });
  assert.equal(state.mode, 'sessions');
});
