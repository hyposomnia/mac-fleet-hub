import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { privateDirectory, readPrivateJSON, validateRuntimeConfig, validateState, processIdentity } from './codex-desktop-bridge.mjs';
import { configureDesktopEnvironment } from './codex-desktop-env.mjs';

const files = ['codex-desktop-install.mjs', 'codex-desktop-bridge.mjs', 'codex-runtime-guard.mjs', 'codex-desktop-env.mjs', 'codex-desktop-env.sh', 'codex-desktop-launch.sh', 'codex-bin-resolve.sh'];
const configName = 'codex-desktop.json';
const snapshots = ['codex-desktop-environment.Aqua.json', 'codex-desktop-environment.Background.json'];
const transactionName = '.codex-hook-transaction.json';
function desktopRunning(config) {
  const commands = execFileSync('/bin/ps', ['-ww', '-axo', 'comm='], { encoding: 'utf8' }).split('\n').map(line => line.trim());
  return commands.includes(path.join(config.appPath, 'Contents', 'MacOS', 'ChatGPT'));
}
function readExisting(file) {
  try {
    const stat = fs.lstatSync(file);
    if (!stat.isFile() || stat.uid !== process.getuid()) throw new Error(`unsafe installed hook file: ${file}`);
    return { data: fs.readFileSync(file).toString('base64'), mode: stat.mode & 0o777 };
  } catch (error) { if (error.code === 'ENOENT') return null; throw error; }
}
function replace(file, data, mode) {
  const temp = `${file}.${randomUUID()}.new`;
  try { fs.writeFileSync(temp, data, { mode, flag: 'wx' }); fs.renameSync(temp, file); }
  finally { if (fs.existsSync(temp)) fs.unlinkSync(temp); }
}

export function installHook({ sourceDir, targetDir, config, desktopRunning: running = desktopRunning, checkOnly = false }) {
  validateRuntimeConfig(config);
  if (!path.isAbsolute(config.appPath) || !path.isAbsolute(targetDir)) throw new Error('hook paths must be absolute');
  if (fs.existsSync(targetDir)) privateDirectory(targetDir);
  const content = Object.fromEntries(files.map(name => [name, fs.readFileSync(path.join(sourceDir, name))]));
  content[configName] = Buffer.from(JSON.stringify(config));
  if (fs.existsSync(path.join(targetDir, transactionName))) throw new Error('unfinished hook installation; rollback before retrying');
  const previous = Object.fromEntries([...Object.keys(content), ...snapshots].map(name => [name, readExisting(path.join(targetDir, name))]));
  const current = Object.entries(content).every(([name, data]) => previous[name] && previous[name].mode === (name === configName ? 0o600 : 0o700) && Buffer.from(previous[name].data, 'base64').equals(data));
  if (current) return 'current';
  if (running(config)) throw new Error('Desktop must close before installing or migrating the shared runtime');
  if (checkOnly) return 'ready';
  privateDirectory(targetDir);
  fs.writeFileSync(path.join(targetDir, transactionName), JSON.stringify({ schema: 1, previous }), { mode: 0o600, flag: 'wx' });
  try {
    for (const [name, data] of Object.entries(content)) replace(path.join(targetDir, name), data, name === configName ? 0o600 : 0o700);
  } catch (error) {
    try { rollbackHook(targetDir); } catch (rollback) { throw new Error(`${error.message}; hook rollback failed: ${rollback.message}`); }
    throw error;
  }
  return 'staged';
}
export function rollbackHook(targetDir) {
  let transaction;
  try { transaction = readPrivateJSON(path.join(targetDir, transactionName)); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
  const expected = [...files, configName, ...snapshots];
  if (transaction.schema !== 1 || Object.keys(transaction.previous || {}).sort().join() !== expected.sort().join()) throw new Error('invalid hook transaction');
  for (const [name, saved] of Object.entries(transaction.previous)) {
    const file = path.join(targetDir, name);
    if (saved) replace(file, Buffer.from(saved.data, 'base64'), saved.mode);
    else if (fs.existsSync(file)) fs.unlinkSync(file);
  }
  fs.unlinkSync(path.join(targetDir, transactionName));
}
export function commitHook(targetDir) {
  try { readPrivateJSON(path.join(targetDir, transactionName)); fs.unlinkSync(path.join(targetDir, transactionName)); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, targetDir, appPath, listenURL, proxySocket, codexHome, stateDir] = process.argv.slice(2);
    if (command === 'install' || command === 'check') {
      configureDesktopEnvironment({ mode: 'check', launcher: path.join(targetDir, 'codex-desktop-launch.sh'), snapshotPath: path.join(targetDir, `codex-desktop-environment.${execFileSync('/bin/launchctl', ['managername'], { encoding: 'utf8' }).trim() === 'Aqua' ? 'Aqua' : 'Background'}.json`) });
      const result = installHook({ sourceDir: import.meta.dirname, targetDir, config: { appPath, listenURL, proxySocket, codexHome, stateDir }, checkOnly: command === 'check' });
      process.stdout.write(result + '\n');
    } else if (command === 'status') {
      const config = validateRuntimeConfig(readPrivateJSON(path.join(targetDir, configName)));
      const state = readPrivateJSON(path.join(config.stateDir, 'runtime.json'));
      validateState(state, config, JSON.parse(state.key)[0], processIdentity(state.desktop?.pid));
      const stat = fs.lstatSync(config.proxySocket);
      if (!stat.isSocket() || stat.uid !== process.getuid() || (stat.mode & 0o777) !== 0o600) throw new Error('unsafe runtime proxy');
      const response = await fetch(`http://127.0.0.1:${config.port}/readyz`, { signal: AbortSignal.timeout(1500) });
      if (!response.ok) throw new Error('runtime is not ready');
      await response.body?.cancel();
      process.stdout.write(JSON.stringify({ desktopPid: state.desktop.pid, ownerPid: state.owner.pid, codexPid: state.codex.pid, endpoint: state.endpoint }) + '\n');
    } else if (command === 'check-removal') {
      const config = readPrivateJSON(path.join(targetDir, configName));
      if (desktopRunning(config)) throw new Error('Desktop must close before removing its CLI hook');
    } else if (command === 'remove') {
      const config = readPrivateJSON(path.join(targetDir, configName));
      if (desktopRunning(config)) throw new Error('Desktop must close before removing its CLI hook');
      for (const name of [...files, configName, ...snapshots]) {
        const file = path.join(targetDir, name);
        if (readExisting(file)) fs.unlinkSync(file);
      }
    } else if (command === 'rollback') rollbackHook(targetDir);
    else if (command === 'commit') commitHook(targetDir);
    else throw new Error('expected install, check, commit or rollback');
  } catch (error) { process.stderr.write(`mac-fleet hook install: ${error.message}\n`); process.exit(1); }
}
