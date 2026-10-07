import assert from 'node:assert/strict';
import { spawn, execFileSync } from 'node:child_process';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createServer } from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { once } from 'node:events';

const application = resolve(process.argv[2] || '');
assert.ok(application.startsWith('/private/tmp/') || application.startsWith('/tmp/'), 'Only isolated development bundles may be tested');
assert.equal(JSON.parse(readFileSync(join(application, 'Contents/Resources/release-status.json'))).release_ready, false);
const home = mkdtempSync('/private/tmp/fu-');
const runtime = join(home, '.macfleet/desktop/runtime');
mkdirSync(runtime, { recursive: true, mode: 0o700 });
const background = join(runtime, 'Fleet Agent.app');
cpSync(join(application, 'Contents/Library/LoginItems/Fleet Agent.app'), background, { recursive: true });
const agent = join(background, 'Contents/MacOS/fleet-agent');
const bin = join(background, 'Contents/Resources/bin');
assert.ok(!background.startsWith(application + '/'));
const children = [];
const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('FLEET_') && !key.startsWith('CODEX_') && key !== 'TMUX'));
Object.assign(environment, { HOME: home, PATH: `${bin}:/usr/bin:/bin:/usr/sbin:/sbin`, FLEET_DESKTOP_MANAGED: '1', FLEET_BINDING_FILE: join(home, '.macfleet/desktop/binding.json') });
const execute = (program, args, input) => execFileSync(program, args, { env: environment, input, timeout: 15000, stdio: ['pipe', 'pipe', 'pipe'] });
const start = (program, args = []) => {
  const child = spawn(program, args, { env: environment, stdio: 'ignore' });
  child.on('error', () => {});
  children.push(child);
  return child;
};
async function waitFor(check) {
  let lastError;
  for (let attempt = 0; attempt < 100; attempt++) {
    try { return await check(); } catch (error) { lastError = error; }
    await delay(100);
  }
  throw lastError;
}
async function port() {
  const listener = createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const value = listener.address().port;
  await new Promise((done) => listener.close(done));
  return value;
}
try {
  assert.throws(() => execute(join(application, 'Contents/Library/Helpers/fleet-login-launcher'), ['invalid-action']));
  const daemon = start(agent);
  const status = await waitFor(() => JSON.parse(execute(agent, ['desktop', 'status'])));
  assert.equal(status.pid, daemon.pid);
  assert.equal(status.runtime.phase, 'unbound');
  assert.equal(status.binding, undefined);
  assert.equal(status.disk_access.state, 'unknown');
  assert.equal(status.disk_access.source, 'unverified-process');
  assert.equal(statSync(join(home, '.macfleet/desktop/control.sock')).mode & 0o777, 0o600);
  const appVersion = execute('/usr/bin/plutil', ['-extract', 'CFBundleShortVersionString', 'raw', join(application, 'Contents/Info.plist')]).toString().trim();
  const appBuild = execute('/usr/bin/plutil', ['-extract', 'CFBundleVersion', 'raw', join(application, 'Contents/Info.plist')]).toString().trim();
  assert.equal(status.version, `${appVersion}+${appBuild}`);
  const settings = JSON.stringify({ schema: 1, origin: 'https://fleet.example.test', auto_start: false });
  execute(agent, ['desktop', 'prepare-stop']);
  assert.throws(() => execute(agent, ['desktop', 'settings'], settings));
  execute(agent, ['desktop', 'resume']);
  execute(agent, ['desktop', 'settings'], settings);
  const exited = once(daemon, 'exit');
  daemon.kill('SIGTERM');
  const exitTimeout = setTimeout(() => daemon.kill('SIGKILL'), 5000);
  let termination;
  try { termination = await exited; } finally { clearTimeout(exitTimeout); }
  assert.equal(termination[0], 0, 'daemon must shut down gracefully');
  const restarted = start(agent);
  const restored = await waitFor(() => {
    const current = JSON.parse(execute(agent, ['desktop', 'status']));
    assert.equal(current.pid, restarted.pid);
    return current;
  });
  assert.deepEqual(restored.settings, JSON.parse(settings));
  assert.equal(restored.runtime.phase, 'unbound');
  assert.equal(restored.version, status.version);
  assert.throws(() => execute(agent, ['update']));
  console.log('PASS: standalone copy of actual bundled agent, private control socket, unbound status, no fabricated FDA evidence, legacy update blocked');
  console.log('PASS: matching app/agent version, maintenance rejects settings, graceful daemon restart preserves isolated configuration');

  const root = join(home, 'files');
  mkdirSync(root, { mode: 0o700 });
  writeFileSync(join(root, 'fixture.txt'), 'isolated file service');
  const database = join(home, 'files.db');
  const filebrowser = join(bin, 'filebrowser');
  execute(filebrowser, ['-d', database, 'config', 'init']);
  execute(filebrowser, ['-d', database, 'users', 'add', 'admin', 'isolated-runtime-test-password', '--perm.admin']);
  execute(filebrowser, ['-d', database, 'config', 'set', '--auth.method=noauth', '--baseURL', '/m12/files', '--root', root]);
  const filesPort = await port();
  start(filebrowser, ['-a', '127.0.0.1', '-p', String(filesPort), '-d', database, '-r', root, '-b', '/m12/files']);
  const fileToken = await waitFor(async () => {
    const response = await fetch(`http://127.0.0.1:${filesPort}/m12/files/api/login`, { method: 'POST', body: '{}', headers: { 'Content-Type': 'application/json' }, signal: AbortSignal.timeout(1000) });
    assert.equal(response.status, 200);
    return response.text();
  });
  const listing = await waitFor(async () => {
    const response = await fetch(`http://127.0.0.1:${filesPort}/m12/files/api/resources/`, { headers: { 'X-Auth': fileToken }, signal: AbortSignal.timeout(1000) });
    assert.equal(response.status, 200);
    return response.json();
  });
  assert.ok(listing.items.some(item => item.name === 'fixture.txt'));
  console.log('PASS: bundled filebrowser initializes and serves only isolated test files over loopback');

  const terminalPort = await port();
  start(join(bin, 'ttyd'), ['-i', '127.0.0.1', '-p', String(terminalPort), '-b', '/m12/term', '-W', '/usr/bin/true']);
  await waitFor(async () => {
    const response = await fetch(`http://127.0.0.1:${terminalPort}/m12/term/`, { signal: AbortSignal.timeout(1000) });
    assert.equal(response.status, 200);
    assert.ok((await response.text()).includes('terminal'));
  });
  const socket = join(home, 'tmux.sock');
  execute(join(bin, 'tmux'), ['-S', socket, '-f', '/dev/null', 'new-session', '-d', '-s', 'fleet-fixture', '/bin/sleep 30']);
  try { execute(join(bin, 'tmux'), ['-S', socket, 'has-session', '-t', 'fleet-fixture']); }
  finally { execute(join(bin, 'tmux'), ['-S', socket, 'kill-server']); }
  console.log('PASS: bundled ttyd responds and bundled tmux creates an isolated session without Homebrew');
} finally {
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null || !child.pid) continue;
    const exited = once(child, 'exit');
    child.kill('SIGTERM');
    const timeout = setTimeout(() => child.kill('SIGKILL'), 3000);
    try { await exited; } finally { clearTimeout(timeout); }
  }
  rmSync(home, { recursive: true });
}
