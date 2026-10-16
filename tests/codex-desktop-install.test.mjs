import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { installHook, rollbackHook, commitHook } from '../mac/codex-desktop-install.mjs';

function fixture(t) {
  const dir = fs.mkdtempSync('/private/tmp/fci.'); t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return { sourceDir: path.resolve(import.meta.dirname, '../mac'), targetDir: path.join(dir, 'hook'), config: { appPath: '/Applications/ChatGPT.app', listenURL: 'ws://127.0.0.1:47682', proxySocket: path.join(dir, 'proxy.sock'), stateDir: path.join(dir, 'runtime'), codexHome: path.join(dir, 'home') }, desktopRunning: () => false };
}

test('REQ-07: hook installation stages persistent files and restores prior bytes on rollback', t => {
  const f = fixture(t); fs.mkdirSync(f.targetDir, { mode: 0o700 });
  const envFile = path.join(f.targetDir, 'codex-desktop-env.sh'); fs.writeFileSync(envFile, 'old helper', { mode: 0o700 });
  fs.writeFileSync(path.join(f.targetDir, 'user-note'), 'keep');
  installHook(f);
  assert.equal(fs.statSync(path.join(f.targetDir, 'codex-desktop.json')).mode & 0o777, 0o600);
  assert.ok(fs.existsSync(path.join(f.targetDir, 'codex-runtime-guard.mjs')));
  rollbackHook(f.targetDir);
  assert.equal(fs.readFileSync(envFile, 'utf8'), 'old helper');
  assert.equal(fs.existsSync(path.join(f.targetDir, 'codex-desktop.json')), false);
  assert.equal(fs.readFileSync(path.join(f.targetDir, 'user-note'), 'utf8'), 'keep');
});

test('REQ-07: active Desktop rejects migration before changing files; committed identical install is a no-op', t => {
  const f = fixture(t);
  assert.throws(() => installHook({ ...f, desktopRunning: () => true }), /Desktop.*close/);
  assert.equal(fs.existsSync(f.targetDir), false);
  installHook(f); commitHook(f.targetDir);
  assert.equal(installHook({ ...f, desktopRunning: () => true }), 'current');
  const changed = { ...f.config, listenURL: 'ws://127.0.0.1:47683' };
  assert.throws(() => installHook({ ...f, config: changed, desktopRunning: () => true }), /Desktop.*close/);
});

test('REQ-07: incomplete package fails before touching an existing hook', t => {
  const f = fixture(t); fs.mkdirSync(f.targetDir, { mode: 0o700 });
  fs.writeFileSync(path.join(f.targetDir, 'codex-desktop-env.sh'), 'old helper');
  assert.throws(() => installHook({ ...f, sourceDir: f.targetDir }), /ENOENT|missing/);
  assert.equal(fs.readFileSync(path.join(f.targetDir, 'codex-desktop-env.sh'), 'utf8'), 'old helper');
});
