import test from 'node:test';
import assert from 'node:assert/strict';
import { classifyInvocation, classifyHandshake, validateRuntimeConfig } from '../mac/codex-desktop-bridge.mjs';

const app = '/Applications/ChatGPT.app/Contents/MacOS/ChatGPT';
const primary = (version = '26.1007.71326') => ({
  id: '__codex_initialize__', method: 'initialize', params: {
    clientInfo: { name: 'Codex Desktop', title: 'Codex Desktop', version },
    capabilities: { experimentalApi: true, requestAttestation: true, optOutNotificationMethods: ['turn/diff/updated'] },
  },
});

test('REQ-01: classify command structure, never plugins argument text', () => {
  for (const args of [['app-server'], ['-c', 'plugins.any.enabled=true', 'app-server', '--analytics-default-enabled'], ['app-server', '-c', 'some.value="app-server"']]) {
    assert.equal(classifyInvocation(args, app, app), 'inspect');
  }
  for (const args of [['--version'], ['app-server', '--help'], ['app-server', 'daemon', 'version'], ['app-server', '--listen', 'stdio://'], ['app-server', '--listen=ws://127.0.0.1:1234'], ['-c', 'x="app-server"', 'exec', 'hi']]) {
    assert.equal(classifyInvocation(args, app, app), 'passthrough');
  }
  assert.equal(classifyInvocation(['app-server'], '/bin/zsh', app), 'passthrough');
});

test('REQ-01: initialize profile distinguishes primary, validator and sandbox', () => {
  for (const version of ['26.1007.71326', '27.1']) {
    for (const attestation of [true, false]) {
      const message = primary(version); message.params.capabilities.requestAttestation = attestation;
      assert.equal(classifyHandshake(message), 'shared');
    }
  }
  assert.equal(classifyHandshake({ id: 'network-initialize', method: 'initialize', params: { clientInfo: { name: 'codex_desktop' }, capabilities: { experimentalApi: true } } }), 'passthrough');
  assert.equal(classifyHandshake({ id: 'x', method: 'initialize', params: { clientInfo: { name: 'codex_latex_preview' } } }), 'passthrough');
  const changed = primary(); delete changed.params.capabilities.requestAttestation;
  assert.equal(classifyHandshake(changed), 'unsupported');
  assert.equal(classifyHandshake({ method: 'turn/start' }), 'unsupported');
});

test('REQ-03: reject network exposure and ambiguous listener endpoints', () => {
  const base = { listenURL: 'ws://127.0.0.1:47682', proxySocket: '/tmp/fleet-test.sock', stateDir: '/tmp/fleet-test', codexHome: '/tmp/codex' };
  assert.equal(validateRuntimeConfig(base).endpoint, 'ws://127.0.0.1:47682/rpc');
  for (const listenURL of ['ws://0.0.0.0:1234', 'ws://100.64.0.3:1234', 'ws://localhost:1234', 'wss://127.0.0.1:1234', 'ws://user@127.0.0.1:1234', 'ws://127.0.0.1:1234/rpc', 'ws://127.0.0.1:1234?x=1', 'ws://127.0.0.1:0']) {
    assert.throws(() => validateRuntimeConfig({ ...base, listenURL }), /listener/);
  }
  for (const key of ['proxySocket', 'stateDir', 'codexHome']) assert.throws(() => validateRuntimeConfig({ ...base, [key]: '../relative' }), /absolute/);
});

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn } from 'node:child_process';
import readline from 'node:readline';
import { once } from 'node:events';

const root = path.resolve(import.meta.dirname, '..');
async function fixture(t) {
  const dir = fs.mkdtempSync('/private/tmp/fcb.');
  const portServer = net.createServer(); portServer.listen(0, '127.0.0.1'); await once(portServer, 'listening');
  const port = portServer.address().port; await new Promise(resolve => portServer.close(resolve));
  const codexBin = path.join(dir, 'codex');
  fs.writeFileSync(codexBin, `#!${process.execPath}\n` + fs.readFileSync(path.join(root, 'tests/fixtures/codex-echo-server.mjs')), { mode: 0o700 });
  const config = { listenURL: `ws://127.0.0.1:${port}`, proxySocket: path.join(dir, 'proxy.sock'), stateDir: path.join(dir, 'state'), codexHome: path.join(dir, 'home') };
  const children = [];
  function client(args = ['app-server'], clientConfig = config) {
    const child = spawn(process.execPath, [path.join(root, 'tests/fixtures/codex-bridge-client.mjs'), JSON.stringify({ config: clientConfig, codexBin, args })], { env: { ...process.env, ECHO_SPAWN_LOG: path.join(dir, 'spawns') }, stdio: ['pipe', 'pipe', 'pipe'] });
    children.push(child);
    const lines = [], waiters = []; let stderr = '';
    child.stderr.on('data', data => { stderr += data; });
    const rl = readline.createInterface({ input: child.stdout });
    rl.on('line', line => { const msg = JSON.parse(line); if (waiters.length) waiters.shift().resolve(msg); else lines.push(msg); });
    child.on('exit', () => { while (waiters.length) waiters.shift().reject(new Error(`bridge exited: ${stderr}`)); });
    return { child, stderr: () => stderr, send: msg => child.stdin.write(JSON.stringify(msg) + '\n'), next: () => lines.length ? Promise.resolve(lines.shift()) : new Promise((resolve, reject) => { const timer = setTimeout(() => reject(new Error(`bridge response timeout: ${stderr}`)), 20000); waiters.push({ resolve: value => { clearTimeout(timer); resolve(value); }, reject: err => { clearTimeout(timer); reject(err); } }); }) };
  }
  t.after(async () => {
    for (const child of children) child.kill('SIGTERM');
    await Promise.all(children.map(child => child.exitCode != null || child.signalCode ? null : once(child, 'exit')));
    // guard must finish reclaiming its own server before fixture removal.
    for (let i = 0; i < 100 && fs.existsSync(path.join(config.stateDir, 'runtime.json')); i++) await new Promise(r => setTimeout(r, 20));
    fs.rmSync(dir, { recursive: true, force: true });
  });
  return { dir, config, client };
}

test('REQ-02/04/05: concurrent Desktop clients, bidirectional RPC and shared lifetime', { timeout: 40000 }, async t => {
  const f = await fixture(t); const a = f.client(), b = f.client();
  a.send(primary()); b.send(primary());
  const [ra, rb] = await Promise.all([a.next(), b.next()]);
  assert.equal(ra.result.pid, rb.result.pid);
  assert.equal(fs.statSync(f.config.proxySocket).mode & 0o777, 0o600);
  assert.equal(fs.statSync(path.join(f.config.stateDir, 'runtime.json')).mode & 0o777, 0o600);
  const spawns = fs.readFileSync(path.join(f.dir, 'spawns'), 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(spawns.length, 1);
  a.send({ id: 1, method: 'test/setName', params: { id: 'thread-a', name: '共同会话' } }); await a.next();
  b.send({ id: 2, method: 'test/getName', params: { id: 'thread-a' }, unknown: 'retained' });
  const response = await b.next(); assert.equal(response.result.name, '共同会话'); assert.equal(response.result.received.unknown, 'retained');
  b.send({ id: 3, method: 'test/requestApproval' });
  assert.equal((await b.next()).method, 'item/commandExecution/requestApproval'); await b.next();
  b.send({ id: 'approval-9', result: { decision: 'decline' } });
  assert.deepEqual((await b.next()).result.received.result, { decision: 'decline' });
  a.child.stdin.end();
  b.send({ id: 4, method: 'test/alive' }); assert.equal((await b.next()).result.pid, ra.result.pid);
  const c = f.client(); c.send(primary()); assert.equal((await c.next()).result.pid, ra.result.pid);
});

test('REQ-04: a JSON-RPC line above 16MiB remains complete', { timeout: 40000 }, async t => {
  const f = await fixture(t); const a = f.client(); a.send(primary()); await a.next();
  const payload = '字'.repeat(6 * 1024 * 1024);
  a.send({ id: 'large', method: 'test/large', params: { payload } });
  const result = await a.next(); assert.equal(result.result.received.params.payload, payload);
});

test('REQ-01: network helper keeps stdio and does not publish shared state', { timeout: 30000 }, async t => {
  const f = await fixture(t); const a = f.client();
  const message = { id: 'network-initialize', method: 'initialize', params: { clientInfo: { name: 'codex_desktop' }, capabilities: { experimentalApi: true } } };
  a.send(message); assert.deepEqual(await a.next(), message);
  assert.equal(fs.existsSync(f.config.stateDir), false);
});

test('REQ-03: occupied loopback port is rejected without attaching or fallback', { timeout: 30000 }, async t => {
  const f = await fixture(t); const blocker = net.createServer(s => s.destroy()); blocker.listen(new URL(f.config.listenURL).port, '127.0.0.1'); await once(blocker, 'listening');
  t.after(() => blocker.close()); const a = f.client(); a.send(primary());
  await assert.rejects(a.next(), /occupied|EADDRINUSE/);
  assert.equal(fs.existsSync(f.config.proxySocket), false);
});

test('REQ-05: owner SIGKILL reaps runtime before a fresh owner starts', { timeout: 30000 }, async t => {
  const f = await fixture(t); const a = f.client(); a.send(primary());
  const oldPid = (await a.next()).result.pid;
  const state = JSON.parse(fs.readFileSync(path.join(f.config.stateDir, 'runtime.json')));
  process.kill(state.owner.pid, 'SIGKILL');
  for (let i = 0; i < 200 && fs.existsSync(path.join(f.config.stateDir, 'runtime.json')); i++) await new Promise(r => setTimeout(r, 25));
  assert.equal(fs.existsSync(path.join(f.config.stateDir, 'runtime.json')), false);
  assert.throws(() => process.kill(oldPid, 0), /ESRCH/);
  const b = f.client(); b.send(primary()); assert.notEqual((await b.next()).result.pid, oldPid);
});

test('REQ-03: ordinary proxy files are preserved on startup failure', { timeout: 30000 }, async t => {
  const f = await fixture(t); fs.writeFileSync(f.config.proxySocket, 'user content', { mode: 0o600 });
  const a = f.client(); a.send(primary()); await assert.rejects(a.next(), /refusing to replace proxy/);
  assert.equal(fs.readFileSync(f.config.proxySocket, 'utf8'), 'user content');
});

test('REQ-03: shared state symlink is rejected without reading its target', { timeout: 30000 }, async t => {
  const f = await fixture(t); fs.mkdirSync(f.config.stateDir, { mode: 0o700 });
  fs.symlinkSync(f.dir, path.join(f.config.stateDir, 'runtime.lock'));
  const a = f.client(); a.send(primary()); await assert.rejects(a.next(), /ELOOP|unsafe/);
});

test('REQ-02: a second client with conflicting configuration cannot attach to the owner', { timeout: 30000 }, async t => {
  const f = await fixture(t); const a = f.client(); a.send(primary()); await a.next();
  const b = f.client(['app-server'], { ...f.config, codexHome: f.config.codexHome + '-other' }); b.send(primary());
  await assert.rejects(b.next(), /another Desktop instance or configuration/);
  assert.equal(fs.readFileSync(path.join(f.dir, 'spawns'), 'utf8').trim().split('\n').length, 1);
});
