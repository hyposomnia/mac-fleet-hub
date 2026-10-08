import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./auth-client.js', import.meta.url), 'utf8').catch(() => '');
function storage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return { get length() { return values.size; }, key: (index) => [...values.keys()][index],
    getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)),
    removeItem: (key) => values.delete(key) };
}
function harness(respond = () => ({ user: { id: 'alice', role: 'user' }, csrf_token: 'csrf-alice' })) {
  assert.ok(source, 'shared authentication client is missing');
  const sandbox = { URL, Headers, Response };
  vm.runInNewContext(source, sandbox);
  const requests = [];
  const redirects = [];
  const localStorage = storage();
  const sessionStorage = storage();
  let lost = 0;
  const client = sandbox.FleetAuth.createClient({ localStorage, sessionStorage,
    location: { origin: 'https://fleet.test', pathname: '/account', search: '', replace: (url) => redirects.push(url) },
    onUnauthorized: () => { lost++; },
    fetch: async (url, options) => {
      requests.push({ url, options });
      const value = await respond(url, options);
      return value instanceof Response ? value : Response.json(value);
    } });
  return { client, requests, redirects, localStorage, sessionStorage, lost: () => lost };
}

test('authenticated JSON and multipart writes include me CSRF with same-origin credentials and no-store', async () => {
  const current = harness();
  await current.client.me();
  await current.client.json('/api/auth/password', { method: 'POST', body: { password: 'new' } });
  await current.client.fetch('/m1/api/chat/upload', { method: 'POST', body: 'multipart' });
  for (const { options } of current.requests) {
    assert.equal(options.credentials, 'same-origin');
    assert.equal(options.cache, 'no-store');
  }
  assert.equal(current.requests[1].options.headers.get('X-CSRF-Token'), 'csrf-alice');
  assert.equal(current.requests[2].options.headers.get('X-CSRF-Token'), 'csrf-alice');
  assert.equal(current.requests[2].options.headers.has('content-type'), false);
});

test('writes without a full session and cross-origin requests fail before transport', async () => {
  const current = harness();
  await assert.rejects(current.client.json('/api/devices/m1', { method: 'DELETE' }), /登录|session/i);
  await current.client.me();
  await assert.rejects(current.client.fetch('https://evil.test/api/delete', { method: 'POST' }), /origin|同源/i);
  assert.equal(current.requests.length, 1);
});

test('pending auth requests send exact JSON and invalid credentials stay on the form', async () => {
  const current = harness(() => new Response(JSON.stringify({ message: '验证码无效' }), { status: 401 }));
  const payload = { email: 'a@example.com', password: 'password', confirm_password: 'password' };
  await assert.rejects(current.client.json('/api/auth/register', { method: 'POST', body: payload }), /验证码无效/);
  assert.deepEqual(JSON.parse(current.requests[0].options.body), payload);
  assert.equal(current.requests[0].options.headers.has('X-CSRF-Token'), false);
  assert.equal(current.redirects.length, 0);
});

test('private keys are unavailable before me, isolated by identity and legacy state is discarded', async () => {
  let identity = 'alice';
  const current = harness(() => ({ user: { id: identity }, csrf_token: 'token' }));
  assert.throws(() => current.client.storageKey('fleet-pool'), /登录|session/i);
  current.localStorage.setItem('fleet-session-read-v2', 'legacy');
  await current.client.me();
  const aliceKey = current.client.storageKey('fleet-ui-state-v1');
  current.localStorage.setItem(aliceKey, 'alice paths');
  assert.equal(current.localStorage.getItem('fleet-session-read-v2'), null);
  identity = 'bob';
  await current.client.me();
  assert.notEqual(current.client.storageKey('fleet-ui-state-v1'), aliceKey);
  assert.equal(current.localStorage.getItem(current.client.storageKey('fleet-ui-state-v1')), null);
});

test('authenticated 401 clears both private storages and redirects once, retaining local enrollment return', async () => {
  let expired = false;
  const current = harness(() => expired ? new Response('', { status: 401 }) : { user: { id: 'alice' }, csrf_token: 'token' });
  await current.client.me();
  const key = current.client.storageKey('fleet-pool');
  current.localStorage.setItem(key, 'private');
  current.sessionStorage.setItem(key, 'private');
  current.localStorage.setItem('fleet-theme', 'light');
  expired = true;
  await assert.rejects(current.client.json('/api/devices'), /登录|session/i);
  assert.equal(current.localStorage.getItem(key), null);
  assert.equal(current.sessionStorage.getItem(key), null);
  assert.equal(current.localStorage.getItem('fleet-theme'), 'light');
  assert.equal(current.lost(), 1);
  assert.match(current.redirects[0], /^\/auth\?next=%2Faccount$/);
  current.client.invalidate();
  assert.equal(current.redirects.length, 1);
});

test('logout is a CSRF protected POST, clears state only after success and stops later private writes', async () => {
  const current = harness((url) => url === '/api/auth/logout' ? new Response(null, { status: 204 }) : { user: { id: 'alice' }, csrf_token: 'token' });
  await current.client.me();
  current.sessionStorage.setItem(current.client.storageKey('fleet-pool'), 'snapshot');
  await current.client.logout();
  assert.equal(current.requests[1].options.method, 'POST');
  assert.equal(current.requests[1].options.headers.get('X-CSRF-Token'), 'token');
  assert.equal(current.sessionStorage.length, 0);
  assert.throws(() => current.client.storageKey('fleet-pool'), /登录|session/i);
});

test('responses arriving after session loss cannot repopulate private UI', async () => {
  let complete;
  const current = harness((url) => url === '/api/devices' ? new Promise((resolve) => { complete = resolve; }) : { user: { id: 'alice' }, csrf_token: 'token' });
  await current.client.me();
  const pending = current.client.json('/api/devices');
  current.client.invalidate();
  complete({ devices: [{ id: 'm1' }] });
  await assert.rejects(pending, /登录|session/i);
});

test('safe return URLs reject protocol-relative, escaped and authentication loop destinations', () => {
  const { client } = harness();
  for (const target of [null, undefined, '', 'https://evil.test', '//evil.test', '/\\evil.test', '/auth/setup', '/%2f%2fevil.test', '/%5cevil.test']) {
    assert.equal(client.safeNext(target), '/');
  }
  assert.equal(client.safeNext('/enroll/confirm?code=abc'), '/enroll/confirm?code=abc');
});

test('blocked browser storage cannot prevent authentication or 401 cleanup', async () => {
  assert.ok(source);
  const sandbox = { URL, Headers, Response };
  vm.runInNewContext(source, sandbox);
  let lost = false;
  const client = sandbox.FleetAuth.createClient({ location: { origin: 'https://fleet.test', pathname: '/', search: '', replace() {} },
    get localStorage() { throw new Error('Storage blocked'); }, get sessionStorage() { throw new Error('Storage blocked'); },
    onUnauthorized: () => { lost = true; }, fetch: async () => Response.json({ user: { id: 'u1' }, csrf_token: 'token' }) });
  await client.me();
  client.invalidate();
  assert.equal(lost, true);
});

test('successful verification signals other tabs to discard their previous user state', async () => {
  const current = harness(() => ({ user: { id: 'bob' } }));
  await current.client.json('/api/auth/verify', { method: 'POST', body: { code: '123456' } });
  assert.ok(current.localStorage.getItem('fleet-auth-event'));
  assert.equal(current.client.user.id, 'bob');
});
