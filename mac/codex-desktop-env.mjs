import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { privateDirectory, readPrivateJSON } from './codex-desktop-bridge.mjs';

const keys = ['CODEX_CLI_PATH', 'CODEX_APP_SERVER_FORCE_CLI', 'CODEX_APP_SERVER_WS_URL', 'CODEX_APP_SERVER_USE_LOCAL_DAEMON'];
function launchctl(command, key, value) {
  return execFileSync('/bin/launchctl', [command, key, ...(value === undefined ? [] : [value])], { encoding: 'utf8' }).replace(/\n$/, '');
}

export function configureDesktopEnvironment({ mode, launcher, snapshotPath, previousValues = {}, launchctl: run = launchctl }) {
  if (!['shared', 'clear', 'check', 'commit', 'rollback'].includes(mode)) throw new Error('expected shared, clear, check, commit or rollback mode');
  if (!path.isAbsolute(launcher) || !path.isAbsolute(snapshotPath)) throw new Error('Desktop environment paths must be absolute');
  const operationPath = snapshotPath + '.operation';
  const removeOperation = () => { try { fs.unlinkSync(operationPath); } catch (error) { if (error.code !== 'ENOENT') throw error; } };
  if (mode === 'commit') { removeOperation(); return; }
  const current = Object.fromEntries(keys.map(key => [key, run('getenv', key)]));
  let saved;
  try { saved = readPrivateJSON(snapshotPath); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  if (saved && (saved.schema !== 1 || !keys.every(key => typeof saved.previous?.[key] === 'string' && typeof saved.applied?.[key] === 'string'))) throw new Error('invalid Desktop environment snapshot');
  const desired = { CODEX_CLI_PATH: launcher, CODEX_APP_SERVER_FORCE_CLI: '1', CODEX_APP_SERVER_WS_URL: '', CODEX_APP_SERVER_USE_LOCAL_DAEMON: '' };
  if (mode !== 'clear' && mode !== 'rollback' && current.CODEX_CLI_PATH && current.CODEX_CLI_PATH !== launcher && current.CODEX_CLI_PATH !== saved?.applied.CODEX_CLI_PATH) throw new Error('external CODEX_CLI_PATH override conflicts with Fleet hook');
  if (mode === 'check') return;
  if (mode === 'clear' && !saved) return;
  const fresh = !saved;
  let operation;
  if (mode === 'rollback') {
    try { operation = readPrivateJSON(operationPath); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
    if (!keys.every(key => typeof operation.before?.[key] === 'string' && typeof operation.applied?.[key] === 'string')) throw new Error('invalid environment operation');
  }
  if (mode === 'shared') {
    privateDirectory(path.dirname(snapshotPath));
    const temporary = operationPath + '.' + process.pid;
    fs.writeFileSync(temporary, JSON.stringify({ before: current, applied: desired, snapshotBefore: saved ?? null }), { mode: 0o600, flag: 'wx' });
    fs.renameSync(temporary, operationPath);
  }
  if (mode === 'shared' && fresh) {
    privateDirectory(path.dirname(snapshotPath));
    const original = { ...current };
    for (const key of keys.slice(2)) {
      if (previousValues[key] !== undefined) {
        if (typeof previousValues[key] !== 'string') throw new Error('invalid legacy environment value');
        original[key] = previousValues[key];
      }
    }
    saved = { schema: 1, previous: original, applied: desired };
    fs.writeFileSync(snapshotPath, JSON.stringify(saved), { mode: 0o600, flag: 'wx' });
  }
  const mutate = (key, value) => value ? run('setenv', key, value) : run('unsetenv', key);
  const changed = [];
  try {
    for (const key of keys) {
      if (mode === 'clear' && current[key] !== saved.applied[key]) continue;
      if (mode === 'rollback' && current[key] !== operation.applied[key]) continue;
      const value = mode === 'shared' ? desired[key] : mode === 'rollback' ? operation.before[key] : saved.previous[key];
      if (value === current[key]) continue;
      // Include attempted operations: a launchctl timeout may follow a mutation.
      changed.push(key); mutate(key, value);
    }
  } catch (error) {
    const failures = [];
    for (const key of changed.reverse()) {
      try { mutate(key, current[key]); } catch (rollback) { failures.push(rollback.message); }
    }
    if (fresh && mode === 'shared' && failures.length === 0) fs.unlinkSync(snapshotPath);
    if (failures.length === 0) removeOperation();
    throw new Error(`${error.message}${failures.length ? '; environment rollback failed: ' + failures.join('; ') : ''}`);
  }
  if (mode === 'rollback') {
    if (operation.snapshotBefore) fs.writeFileSync(snapshotPath, JSON.stringify(operation.snapshotBefore), { mode: 0o600 });
    else if (fs.existsSync(snapshotPath)) fs.unlinkSync(snapshotPath);
  }
  if (mode === 'clear') fs.unlinkSync(snapshotPath);
  if (mode === 'clear' || mode === 'rollback') removeOperation();
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const directory = path.dirname(process.argv[2]);
    readPrivateJSON(process.argv[2]);
    const scope = execFileSync('/bin/launchctl', ['managername'], { encoding: 'utf8' }).trim() === 'Aqua' ? 'Aqua' : 'Background';
    let previousValues = {};
    if (process.argv[4]) {
      const legacy = readPrivateJSON(process.argv[4]);
      previousValues = legacy.user && legacy.aqua ? legacy[scope === 'Aqua' ? 'aqua' : 'user'] : legacy;
      if (!previousValues || !keys.slice(2).every(key => typeof previousValues[key] === 'string')) throw new Error('invalid legacy environment snapshot');
    }
    configureDesktopEnvironment({ mode: process.argv[3] || 'clear', launcher: path.join(directory, 'codex-desktop-launch.sh'), snapshotPath: path.join(directory, `codex-desktop-environment.${scope}.json`), previousValues });
  } catch (error) { process.stderr.write(`mac-fleet Desktop environment: ${error.message}\n`); process.exit(1); }
}
