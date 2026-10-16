import path from 'node:path';

export function classifyInvocation(args, parentCommand, desktopCommand) {
  if (parentCommand !== desktopCommand) return 'passthrough';
  const takesValue = new Set(['-c', '--config', '-p', '--profile', '-C', '--cd', '--enable', '--disable']);
  let command = false;
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (takesValue.has(arg)) { i++; continue; }
    if (arg === '--help' || arg === '-h' || arg === '--version' || arg === '--listen' || arg.startsWith('--listen=')) return 'passthrough';
    if (arg.startsWith('-')) continue;
    if (command || arg !== 'app-server') return 'passthrough';
    command = true;
  }
  return command ? 'inspect' : 'passthrough';
}

export function classifyHandshake(message) {
  if (message?.method !== 'initialize') return 'unsupported';
  const { clientInfo, capabilities } = message.params || {};
  if (clientInfo?.name === 'codex_desktop' && message.id === 'network-initialize') return 'passthrough';
  if (clientInfo?.name !== 'Codex Desktop') return 'passthrough';
  if (typeof capabilities?.requestAttestation === 'boolean' && Array.isArray(capabilities.optOutNotificationMethods)) return 'shared';
  return 'unsupported';
}

export function validateRuntimeConfig(config) {
  const match = typeof config.listenURL === 'string' && /^ws:\/\/127\.0\.0\.1:([0-9]{1,5})\/?$/.exec(config.listenURL);
  if (!match || Number(match[1]) < 1 || Number(match[1]) > 65535) throw new Error('shared listener must be ws://127.0.0.1:<port>');
  const port = Number(match[1]);
  const listenURL = `ws://127.0.0.1:${port}`;
  for (const key of ['proxySocket', 'stateDir', 'codexHome']) {
    if (typeof config[key] !== 'string' || !path.isAbsolute(config[key])) throw new Error(`${key} must be absolute`);
  }
  return { ...config, listenURL, endpoint: `${listenURL}/rpc`, port };
}

import fs from 'node:fs';
import net from 'node:net';
import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { randomUUID } from 'node:crypto';
import { fileURLToPath } from 'node:url';

const MAX_PENDING_BYTES = 64 * 1024 * 1024;
const STARTUP_MS = 15000;
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const uid = () => process.getuid();

export function processIdentity(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) return null;
  try {
    const line = execFileSync('/bin/ps', ['-ww', '-p', String(pid), '-o', 'uid=,ppid=,lstart=,comm='], {
      encoding: 'utf8', env: { ...process.env, LC_ALL: 'C' }, stdio: ['ignore', 'pipe', 'ignore'],
    }).trim();
    const fields = line.split(/\s+/);
    if (fields.length < 8) return null;
    return { pid, uid: Number(fields[0]), ppid: Number(fields[1]), start: fields.slice(2, 7).join(' '), command: fields.slice(7).join(' ') };
  } catch { return null; }
}

export function sameProcess(expected, actual = expected && processIdentity(expected.pid)) {
  return !!expected && !!actual && expected.pid === actual.pid && expected.uid === actual.uid && expected.start === actual.start && expected.command === actual.command;
}

export function privateDirectory(dir) {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const stat = fs.lstatSync(dir);
  if (!stat.isDirectory() || stat.uid !== uid() || (stat.mode & 0o077) !== 0) throw new Error(`unsafe private directory: ${dir}`);
}

export function readPrivateJSON(file) {
  const fd = fs.openSync(file, fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW);
  try {
    const stat = fs.fstatSync(fd);
    if (!stat.isFile() || stat.uid !== uid() || (stat.mode & 0o777) !== 0o600) throw new Error(`unsafe state file: ${file}`);
    return JSON.parse(fs.readFileSync(fd, 'utf8'));
  } finally { fs.closeSync(fd); }
}

function publish(file, value) {
  const temporary = `${file}.${randomUUID()}.new`;
  try {
    fs.writeFileSync(temporary, JSON.stringify(value), { mode: 0o600, flag: 'wx' });
    fs.renameSync(temporary, file);
  } finally { if (fs.existsSync(temporary)) fs.unlinkSync(temporary); }
}

async function* lines(input) {
  let chunks = [], size = 0;
  for await (const chunk of input) {
    let start = 0;
    while (start < chunk.length) {
      const end = chunk.indexOf(10, start), stop = end < 0 ? chunk.length : end;
      const part = chunk.subarray(start, stop); chunks.push(part); size += part.length;
      if (size > MAX_PENDING_BYTES) throw new Error('JSON-RPC line exceeds 64MiB transport limit');
      if (end >= 0) { yield Buffer.concat(chunks, size).toString('utf8'); chunks = []; size = 0; }
      start = stop + 1;
    }
  }
  if (size) yield Buffer.concat(chunks, size).toString('utf8');
}

async function write(stream, data) {
  if (!stream.write(data)) await once(stream, 'drain');
}

async function passthrough(codexBin, args, first, iterator) {
  const child = spawn(codexBin, args, { env: childEnvironment(), stdio: [iterator ? 'pipe' : 'inherit', 'inherit', 'inherit'] });
  for (const signal of ['SIGTERM', 'SIGINT', 'SIGHUP']) process.on(signal, () => child.kill(signal));
  child.once('error', error => { process.stderr.write(`${error.message}\n`); process.exit(1); });
  child.once('exit', (code, signal) => process.exit(code ?? (signal ? 1 : 0)));
  if (iterator) {
    child.stdin.on('error', () => {});
    try {
      await write(child.stdin, first + '\n');
      for await (const line of iterator) await write(child.stdin, line + '\n');
      child.stdin.end();
    } catch (error) { child.kill('SIGTERM'); throw error; }
  }
}

function childEnvironment(codexHome) {
  const env = { ...process.env };
  // CLI overrides select this bridge only at the Desktop boundary.
  for (const key of ['CODEX_CLI_PATH', 'CODEX_APP_SERVER_WS_URL', 'CODEX_APP_SERVER_USE_LOCAL_DAEMON']) delete env[key];
  if (codexHome) env.CODEX_HOME = codexHome;
  return env;
}

function configurationKey(config, codexBin) {
  return JSON.stringify([codexBin, config.listenURL, config.proxySocket, config.stateDir, config.codexHome]);
}

async function acquireLock(config) {
  const file = path.join(config.stateDir, 'runtime.lock');
  const fd = fs.openSync(file, fs.constants.O_CREAT | fs.constants.O_RDONLY | fs.constants.O_NOFOLLOW, 0o600);
  const stat = fs.fstatSync(fd); fs.closeSync(fd);
  if (!stat.isFile() || stat.uid !== uid() || (stat.mode & 0o777) !== 0o600) throw new Error('unsafe runtime lock file');
  const guard = spawn('/usr/bin/lockf', ['-k', '-s', '-t', '0', file, process.execPath, path.join(import.meta.dirname, 'codex-runtime-guard.mjs')], { stdio: ['pipe', 'pipe', 'inherit'] });
  guard.stdin.on('error', () => {});
  return new Promise((resolve, reject) => {
    guard.once('error', reject);
    guard.once('exit', code => {
      if (code === 75) resolve(null);
      else reject(new Error(`runtime guard exited: ${code}`));
    });
    guard.stdout.once('data', data => {
      if (String(data) !== 'locked\n') { guard.stdin.end(); reject(new Error('invalid runtime guard handshake')); }
      else resolve(guard);
    });
  });
}

export function validateState(state, config, codexBin, desktop) {
  if (state?.endpoint !== config.endpoint || typeof state.generation !== 'string' || !state.generation) throw new Error('invalid shared runtime endpoint or generation');
  if (state?.key !== configurationKey(config, codexBin) || !sameProcess(state.desktop, desktop)) throw new Error('shared runtime belongs to another Desktop instance or configuration');
  const owner = processIdentity(state.owner?.pid), codex = processIdentity(state.codex?.pid);
  if (!sameProcess(state.owner, owner) || owner.ppid !== desktop.pid || !sameProcess(state.codex, codex) || codex.ppid !== owner.pid) throw new Error('shared runtime process ancestry is stale');
  return state;
}

async function portInUse(port) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: '127.0.0.1', port });
    socket.once('connect', () => { socket.destroy(); resolve(true); });
    socket.once('error', error => { socket.destroy(); if (error.code === 'ECONNREFUSED') resolve(false); else reject(error); });
    socket.setTimeout(1000, () => { socket.destroy(); reject(new Error('listener port probe timed out')); });
  });
}

async function startProxy(config) {
  privateDirectory(path.dirname(config.proxySocket));
  try {
    const stat = fs.lstatSync(config.proxySocket);
    if (!stat.isSocket() || stat.uid !== uid()) throw new Error(`refusing to replace proxy path: ${config.proxySocket}`);
    // A live proxy is never taken over, even when runtime metadata is missing.
    const live = await new Promise((resolve, reject) => {
      const probe = net.createConnection(config.proxySocket);
      probe.once('connect', () => { probe.destroy(); resolve(true); });
      probe.once('error', error => { if (error.code === 'ECONNREFUSED' || error.code === 'ENOENT') resolve(false); else reject(error); });
      probe.setTimeout(1000, () => { probe.destroy(); reject(new Error('proxy probe timed out')); });
    });
    if (live) throw new Error('shared Unix proxy is already active');
    fs.unlinkSync(config.proxySocket);
  } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const connections = new Set();
  const server = net.createServer(client => {
    const upstream = net.createConnection({ host: '127.0.0.1', port: config.port });
    connections.add(client); connections.add(upstream);
    client.on('close', () => { connections.delete(client); upstream.destroy(); });
    upstream.on('close', () => { connections.delete(upstream); client.destroy(); });
    client.on('error', () => upstream.destroy()); upstream.on('error', () => client.destroy());
    client.pipe(upstream); upstream.pipe(client);
  });
  server.listen(config.proxySocket); await once(server, 'listening');
  fs.chmodSync(config.proxySocket, 0o600);
  return () => { for (const connection of connections) connection.destroy(); server.close(); };
}

async function ownRuntime(config, codexBin, args, desktop, guard) {
  let child, closeProxy, stopping = false;
  const stateFile = path.join(config.stateDir, 'runtime.json'), generation = randomUUID();
  const shutdown = async code => {
    if (stopping) return; stopping = true;
    closeProxy?.();
    guard.stdin.end();
    // The guard releases flock only after reaping the registered Codex.
    if (guard.exitCode === null) await once(guard, 'exit').catch(() => {});
    process.exit(code);
  };
  for (const signal of ['SIGTERM', 'SIGINT', 'SIGHUP']) process.on(signal, () => { void shutdown(0); });
  try {
    if (await portInUse(config.port)) throw new Error('shared listener port is occupied; refusing to attach');
    child = spawn(codexBin, [...args, '--listen', config.listenURL], { env: childEnvironment(config.codexHome), stdio: ['ignore', 'ignore', 'inherit'] });
    await once(child, 'spawn');
    const codex = processIdentity(child.pid);
    if (!codex) throw new Error('Codex exited before runtime registration');
    const state = { generation, key: configurationKey(config, codexBin), desktop, owner: processIdentity(process.pid), codex, endpoint: config.endpoint };
    guard.stdin.write(JSON.stringify({ ...state, stateFile, proxySocket: config.proxySocket }) + '\n');
    child.on('exit', code => { if (!stopping) { process.stderr.write(`shared Codex exited: ${code}\n`); void shutdown(1); } });
    const deadline = Date.now() + STARTUP_MS;
    let ready = false;
    while (Date.now() < deadline && child.exitCode === null) {
      try { const response = await fetch(`http://127.0.0.1:${config.port}/readyz`, { signal: AbortSignal.timeout(500) }); ready = response.ok; await response.body?.cancel(); } catch { /* startup only */ }
      if (ready) break;
      await delay(50);
    }
    if (!ready) throw new Error('shared listener startup timed out');
    const listeners = execFileSync('/usr/sbin/lsof', ['-nP', '-t', `-iTCP:${config.port}`, '-sTCP:LISTEN'], { encoding: 'utf8' }).trim().split(/\s+/);
    if (listeners.length !== 1 || Number(listeners[0]) !== child.pid) throw new Error('listener is not owned by this Codex');
    closeProxy = await startProxy(config);
    publish(stateFile, state);
    const timer = setInterval(() => { if (!sameProcess(desktop)) void shutdown(0); }, 1000);
    guard.on('exit', () => { if (!stopping) { child.kill('SIGTERM'); process.exit(1); } });
    process.on('exit', () => clearInterval(timer));
    return state;
  } catch (error) {
    process.stderr.write(`mac-fleet shared runtime: ${error.message}\n`);
    if (child && child.exitCode === null) child.kill('SIGTERM');
    await shutdown(1);
  }
}

async function runtime(config, codexBin, args, desktop) {
  privateDirectory(config.stateDir);
  const deadline = Date.now() + STARTUP_MS;
  while (Date.now() < deadline) {
    const guard = await acquireLock(config);
    if (guard) return { state: await ownRuntime(config, codexBin, args, desktop, guard), owner: true };
    try {
      const state = validateState(readPrivateJSON(path.join(config.stateDir, 'runtime.json')), config, codexBin, desktop);
      return { state, owner: false };
    } catch (error) {
      if (error.code !== 'ENOENT') throw error;
    }
    await delay(50);
  }
  throw new Error('shared runtime startup timed out while waiting for its owner');
}

async function relay(endpoint, first, iterator, owner) {
  const socket = new WebSocket(endpoint);
  let connected = false, closed = false, writing = false, pendingBytes = 0;
  const pending = [];
  const close = error => {
    if (closed) return; closed = true;
    if (error) process.stderr.write(`mac-fleet Desktop bridge: ${error.message}\n`);
    pending.length = 0; pendingBytes = 0;
    socket.close(); process.stdin.destroy(); process.stdout.destroy();
    if (!owner) process.exit(error ? 1 : 0);
  };
  const flush = async () => {
    if (writing) return; writing = true;
    try {
      while (pending.length && !closed) {
        const message = pending.shift();
        await write(process.stdout, message); pendingBytes -= Buffer.byteLength(message);
      }
    } catch (error) { close(error); }
    finally { writing = false; }
  };
  process.stdout.on('error', error => close(error));
  socket.addEventListener('message', event => {
    if (closed) return;
    if (typeof event.data !== 'string') { close(new Error('non-text app-server frame')); return; }
    const line = event.data + '\n'; pendingBytes += Buffer.byteLength(line);
    if (pendingBytes > MAX_PENDING_BYTES) { close(new Error('Desktop output backlog exceeds 64MiB')); return; }
    pending.push(line); void flush();
  });
  socket.addEventListener('close', () => close(connected ? new Error('shared listener disconnected; requests were not replayed') : new Error('shared listener connection failed')));
  socket.addEventListener('error', () => close(new Error('shared listener WebSocket error')));
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('shared listener connect timed out')), STARTUP_MS);
    socket.addEventListener('open', () => { clearTimeout(timer); connected = true; resolve(); }, { once: true });
    socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('shared listener connection failed')); }, { once: true });
  });
  const send = async line => {
    const bytes = Buffer.byteLength(line);
    if (bytes > MAX_PENDING_BYTES) throw new Error('JSON-RPC message exceeds 64MiB transport limit');
    while (socket.bufferedAmount + bytes > MAX_PENDING_BYTES && !closed) await delay(10);
    if (closed || socket.readyState !== WebSocket.OPEN) throw new Error('shared listener disconnected');
    socket.send(line);
  };
  try { await send(first); for await (const line of iterator) await send(line); close(); }
  catch (error) { close(error); }
}

export async function runDesktopBridge({ config, codexBin, args, desktop }) {
  if (classifyInvocation(args, desktop?.command, desktop?.command) !== 'inspect') return passthrough(codexBin, args);
  const iterator = lines(process.stdin);
  const first = await iterator.next(); if (first.done) return;
  let message;
  try { message = JSON.parse(first.value); } catch { throw new Error('invalid Desktop initialization JSON'); }
  const kind = classifyHandshake(message);
  if (kind === 'passthrough') return passthrough(codexBin, args, first.value, iterator);
  if (kind !== 'shared') throw new Error('unsupported Desktop initialize profile; update Fleet integration');
  if (!desktop || desktop.uid !== uid() || !sameProcess(desktop)) throw new Error('Desktop process identity is unavailable');
  const validated = validateRuntimeConfig(config);
  const shared = await runtime(validated, codexBin, args, desktop);
  await relay(shared.state.endpoint, first.value, iterator, shared.owner);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const config = readPrivateJSON(process.argv[2]);
    const args = process.argv.slice(3), desktop = processIdentity(process.ppid);
    const desktopCommand = path.join(config.appPath, 'Contents', 'MacOS', 'ChatGPT');
    if (classifyInvocation(args, desktop?.command, desktopCommand) !== 'inspect') await passthrough(process.env.FLEET_CODEX_REAL_BIN, args);
    else await runDesktopBridge({ config, codexBin: process.env.FLEET_CODEX_REAL_BIN, args, desktop });
  } catch (error) { process.stderr.write(`mac-fleet Desktop bridge: ${error.message}\n`); process.exit(1); }
}
