import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const app = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const index = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const account = await readFile(new URL('./account.js', import.meta.url), 'utf8');
const worker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
function workerHarness(response = { ok: true, redirected: true, clone() { return this; } }) {
  const listeners = {};
  const puts = [];
  const sandbox = { URL, Response, fetch: async () => response,
    caches: { open: async () => ({ put: async (key) => puts.push(key), match: async () => null }), match: async () => null },
    self: { location: { origin: 'https://fleet.test' }, addEventListener: (name, callback) => { listeners[name] = callback; } } };
  vm.runInNewContext(worker, sandbox);
  return { sandbox, puts, listeners };
}

test('worker bypasses exact and nested protected pages and APIs without false prefix matches', () => {
  const { sandbox } = workerHarness();
  for (const path of ['/auth', '/auth/', '/auth/setup', '/account', '/account/sessions', '/admin', '/admin/users', '/enroll', '/enroll/confirm', '/oauth', '/oauth/authorize', '/oauth/consent', '/oauth/token', '/api', '/api/auth/me', '/m1/api/files']) {
    assert.equal(vm.runInNewContext(`isSensitivePath(${JSON.stringify(path)})`, sandbox), true, path);
  }
  assert.equal(vm.runInNewContext('isSensitivePath("/account.js")', sandbox), false);
});

test('worker never puts an authentication redirect in navigation shell cache', async () => {
  const current = workerHarness();
  let response;
  current.listeners.fetch({ request: { url: 'https://fleet.test/', mode: 'navigate', method: 'GET' }, respondWith: (promise) => { response = promise; } });
  await response;
  assert.deepEqual(current.puts, []);
});

test('all page shells reference shared absolute assets and initial private rendering is gated', async () => {
  for (const [path, page] of [['auth.html', 'auth'], ['account.html', 'account'], ['admin.html', 'admin'], ['enroll.html', 'enrollment']]) {
    const html = await readFile(new URL(`./${path}`, import.meta.url), 'utf8').catch(() => '');
    assert.ok(html, `missing ${path}`);
    assert.match(html, new RegExp(`data-fleet-page="${page}"`));
    assert.match(html, /src="\/auth-client.js\?v=/);
    assert.match(html, /src="\/account.js\?v=/);
    assert.match(html, /href="\/account.css\?v=/);
    assert.match(html, /data-auth="checking"/);
  }
  assert.ok(index.indexOf('src="auth-client.js') >= 0);
  assert.ok(index.indexOf('src="auth-client.js') < index.indexOf('src="preview.js'));
  assert.match(index, /id="auth-status"/);
});

test('dashboard awaits me before init/preview/polling and namespaces all private browser keys', async () => {
  const source = app.match(/async function initAuthenticatedDashboard\(\) \{[\s\S]*?^\}/m)?.[0];
  assert.ok(source, 'authenticated dashboard bootstrap missing');
  let finish;
  let started = 0;
  let appearanceKey;
  const user = { id: 'alice', role: 'user', email: 'alice@example.com' };
  const sandbox = { FleetAuth: { user, me: () => new Promise((resolve) => { finish = resolve; }), storageKey: (key) => `alice:${key}` },
    FleetDeviceAppearance: { bindAccount: key => { appearanceKey = key; } },
    SESSION_READ_KEY: 'fleet-session-read-v2', SESSION_ARCHIVE_KEY: 'fleet-show-archived-sessions', UI_STATE_KEY: 'fleet-ui-state-v1', POOL_SNAP_KEY: 'fleet-pool',
    state: {}, loadSessionReadState: () => new Map(), stopAuthenticatedDashboard() {}, init: () => { started++; },
    document: { documentElement: { dataset: {} }, addEventListener() {} }, $: () => ({}), $$: () => [] };
  vm.runInNewContext(source, sandbox);
  const pending = sandbox.initAuthenticatedDashboard();
  assert.equal(started, 0);
  finish({ user });
  await pending;
  assert.equal(started, 1);
  assert.equal(appearanceKey, 'alice:fleet-device-appearance-v1');
  for (const key of ['SESSION_READ_KEY', 'SESSION_ARCHIVE_KEY', 'UI_STATE_KEY', 'POOL_SNAP_KEY']) assert.match(sandbox[key], /^alice:/);
  assert.match(app, /DOMContentLoaded', initAuthenticatedDashboard/);
  assert.doesNotMatch(app, /sessionReadAt:\s*loadSessionReadState\(\)/);
});

test('owned device discovery uses /api/devices, logout uses POST client, and both menus open unified settings', () => {
  const roster = app.slice(app.indexOf('async function refreshNodes()'), app.indexOf('function markGatewayUnreachable'));
  assert.match(roster, /\/api\/devices/);
  assert.match(roster, /\.devices/);
  assert.doesNotMatch(roster, /givenName|nodes.json/);
  assert.match(account, /button\('退出登录', \(\) => auth\.logout\(\)/);
  assert.doesNotMatch(app, /\/auth\/logout\?rd/);
  assert.equal([...index.matchAll(/data-act="settings"/g)].length, 2);
  assert.doesNotMatch(index, /data-act="(?:account|admin|add-device|automation|logout)"/);
  for (const page of ['account', 'add-device', 'automation', 'sessions', 'appearance']) {
    assert.match(index, new RegExp(`data-settings-page="${page}"`));
  }
});

test('XHR uploads attach CSRF and invalidate on 401, private persistence stops on auth loss', () => {
  const upload = app.slice(app.indexOf('function sendFileUpload('), app.indexOf('function scheduleUploadedDirectoryRefresh'));
  assert.match(upload, /setRequestHeader\('X-CSRF-Token', FleetAuth.csrfToken\)/);
  assert.match(upload, /xhr.status === 401/);
  assert.match(upload, /FleetAuth.invalidate\(\)/);
  for (const name of ['persistUIState', 'persistSessionReadState', 'savePoolSnapshot', 'toggleArchivedSessions']) {
    const start = app.indexOf(`function ${name}(`);
    assert.match(app.slice(start, start + 210), /FleetAuth.*!FleetAuth.user/);
  }
});

test('session loss stops dashboard polling, closes chat streams and clears private in-memory state', () => {
  const source = app.match(/function stopAuthenticatedDashboard\(\) \{[\s\S]*?^\}/m)?.[0];
  assert.ok(source, 'dashboard session cleanup missing');
  const closed = [];
  const cleared = [];
  let workspaceResets = 0;
  let composerResets = 0;
  let appearanceResets = 0;
  const sandbox = { window: { FleetWorkspaceTabs: { reset: () => { workspaceResets++; } } },
    FleetDeviceAppearance: { reset: () => { appearanceResets++; } },
    removeEventListener() {}, refreshDeviceAppearance() {},
    compactComposer: { update: () => { composerResets++; } },
    authenticatedPollTimers: [1, 2], clearInterval: (timer) => cleared.push(timer), clearTimeout() {}, sessionSearchTimer: null,
    disposeChat: (chat) => closed.push(chat), state: { chat: { id: 'open' }, chatCache: new Map([['cached', { id: 'cached' }]]),
      pool: [{}], sessionReadAt: new Map([['read', 42]]), sessionResults: [{}], nodes: { m1: true }, counts: {}, filePaths: {},
      fileEntries: [{}], fileColumns: [{}], fileUploads: { items: [{}] }, assistantInfo: {} },
    MACS: [{}], macNames: { m1: 'private' }, automationAccessKeys: [{}], automationRecordKeys: [{}],
    automationEditingKey: {}, automationBindingSessions: [{}], $$: () => [] };
  vm.runInNewContext(source, sandbox);
  sandbox.stopAuthenticatedDashboard();
  assert.equal(workspaceResets, 1);
  assert.equal(composerResets, 1);
  assert.equal(appearanceResets, 1);
  assert.deepEqual(cleared, [1, 2]);
  assert.equal(closed.length, 2);
  assert.equal(sandbox.state.chatCache.size, 0);
  assert.equal(sandbox.state.sessionReadAt.size, 0);
  assert.equal(sandbox.state.pool.length, 0);
  assert.equal(sandbox.state.fileEntries.length, 0);
});

test('repeated session settings saves keep appearance objects out of integer preference requests', async () => {
  const source = app.match(/async function saveSettings\(\) \{[\s\S]*?^\}/m)?.[0];
  const requests = [];
  const sandbox = { BASE: '', SETTINGS_DEFAULT: {chatCacheMaxSessions: 6}, state: {settings: {chatCacheMaxSessions: 6}},
    $: () => ({value: '9'}), closeOverlay() {}, toast() {}, evictChatCache() {},
    fetch: async (_, options) => {
      requests.push(JSON.parse(options.body));
      return {ok: true, json: async () => ({chatCacheMaxSessions: 9, deviceAppearance: {m1: {icon: 'mini', color: 'teal'}}})};
    } };
  vm.runInNewContext(source, sandbox);
  await sandbox.saveSettings();
  await sandbox.saveSettings();
  assert.equal(requests.length, 2);
  for (const request of requests) assert.deepEqual(request, {chatCacheMaxSessions: 9});
  assert.equal(Object.hasOwn(sandbox.state.settings, 'deviceAppearance'), false);
});
