import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { configureDesktopEnvironment } from '../mac/codex-desktop-env.mjs';

function fixture(t, initial = {}) {
  const dir = fs.mkdtempSync('/private/tmp/fce.'); t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const values = { ...initial }, mutations = [];
  const launchctl = (command, key, value) => {
    if (command === 'getenv') return values[key] || '';
    mutations.push([command, key, value]);
    if (command === 'setenv') values[key] = value; else delete values[key];
    return '';
  };
  return { values, mutations, launchctl, launcher: path.join(dir, 'codex-desktop-launch.sh'), snapshotPath: path.join(dir, 'environment.json') };
}

test('REQ-06: owned hook survives repeated enable; restore preserves later user edits', t => {
  const f = fixture(t, { CODEX_APP_SERVER_WS_URL: 'ws://127.0.0.1:1234/rpc' });
  configureDesktopEnvironment({ ...f, mode: 'shared' });
  assert.equal(f.values.CODEX_CLI_PATH, f.launcher);
  assert.equal(f.values.CODEX_APP_SERVER_FORCE_CLI, '1');
  assert.equal(f.values.CODEX_APP_SERVER_WS_URL, undefined);
  const original = fs.readFileSync(f.snapshotPath, 'utf8');
  configureDesktopEnvironment({ ...f, mode: 'shared' });
  assert.equal(fs.readFileSync(f.snapshotPath, 'utf8'), original);
  f.values.CODEX_CLI_PATH = '/user/another-codex';
  configureDesktopEnvironment({ ...f, mode: 'clear' });
  assert.equal(f.values.CODEX_CLI_PATH, '/user/another-codex');
  assert.equal(f.values.CODEX_APP_SERVER_WS_URL, 'ws://127.0.0.1:1234/rpc');
  assert.equal(f.values.CODEX_APP_SERVER_FORCE_CLI, undefined);
  assert.equal(fs.existsSync(f.snapshotPath), false);
});

test('REQ-06: external CLI override conflicts before any mutation', t => {
  const f = fixture(t, { CODEX_CLI_PATH: '/custom/bin/codex' });
  assert.throws(() => configureDesktopEnvironment({ ...f, mode: 'shared' }), /CLI.*override/);
  assert.deepEqual(f.mutations, []);
  assert.equal(fs.existsSync(f.snapshotPath), false);
});

test('REQ-06: partial environment failure restores exact pre-operation state', t => {
  const initial = { CODEX_APP_SERVER_USE_LOCAL_DAEMON: '1', CODEX_APP_SERVER_FORCE_CLI: '0' };
  const f = fixture(t, initial); let failed = false;
  const launchctl = (...args) => {
    if (args[0] === 'setenv' && args[1] === 'CODEX_APP_SERVER_FORCE_CLI' && args[2] === '1' && !failed) { failed = true; throw new Error('injected launchctl failure'); }
    return f.launchctl(...args);
  };
  assert.throws(() => configureDesktopEnvironment({ ...f, mode: 'shared', launchctl }), /injected launchctl failure/);
  assert.deepEqual(f.values, initial);
  assert.equal(fs.existsSync(f.snapshotPath), false);
});

test('REQ-06: clear without owned snapshot leaves user environment intact', t => {
  const f = fixture(t, { CODEX_CLI_PATH: '/custom/bin/codex', CODEX_APP_SERVER_WS_URL: 'ws://127.0.0.1:1234/rpc' });
  configureDesktopEnvironment({ ...f, mode: 'clear' });
  assert.deepEqual(f.mutations, []);
});

test('REQ-06/07: transaction rollback restores immediate shared state, not original pre-install values', t => {
  const f = fixture(t, { CODEX_APP_SERVER_WS_URL: 'ws://127.0.0.1:1234/rpc' });
  configureDesktopEnvironment({ ...f, mode: 'shared' });
  configureDesktopEnvironment({ ...f, mode: 'commit' });
  const shared = { ...f.values };
  configureDesktopEnvironment({ ...f, mode: 'shared' });
  configureDesktopEnvironment({ ...f, mode: 'rollback' });
  assert.deepEqual(f.values, shared);
  configureDesktopEnvironment({ ...f, mode: 'clear' });
  assert.equal(f.values.CODEX_APP_SERVER_WS_URL, 'ws://127.0.0.1:1234/rpc');
});
