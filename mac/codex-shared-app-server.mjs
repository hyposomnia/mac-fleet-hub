#!/usr/bin/env node

import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const appPath = process.env.FLEET_CODEX_DESKTOP_APP_PATH || "/Applications/ChatGPT.app";
const resourcesPath = path.join(appPath, "Contents", "Resources");
const codexBin = process.env.FLEET_CODEX_BIN;
const listenURL = process.env.FLEET_CODEX_APPSERVER_LISTEN;
const proxySocket = process.env.FLEET_CODEX_APPSERVER_PROXY_SOCK;
const codexHome = process.env.CODEX_HOME || path.join(os.homedir(), ".codex");
const stateDir = path.join(os.homedir(), ".macfleet");
const stablePipe = path.join(stateDir, "codex-app-tools.sock");
const desktopCommand = path.join(appPath, "Contents", "MacOS", "ChatGPT");

function fail(message) {
  process.stderr.write(`mac-fleet shared app-server: ${message}\n`);
  process.exit(1);
}

function assertSharedListenURL(value) {
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    fail(`invalid listener URL: ${value || "(empty)"}`);
  }
  if (parsed.protocol !== "ws:" || parsed.hostname !== "127.0.0.1" || !parsed.port || parsed.pathname !== "/") {
    fail(`listener must be ws://127.0.0.1:<port>: ${value}`);
  }
}

function tomlKey(value) {
  return /^[A-Za-z0-9_-]+$/.test(value) ? value : JSON.stringify(value);
}

function tomlValue(value) {
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "number" || typeof value === "boolean") return String(value);
  if (Array.isArray(value)) return `[${value.map(tomlValue).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.entries(value).map(([key, child]) => `${tomlKey(key)}=${tomlValue(child)}`).join(",")}}`;
  }
  fail("unsupported value in Desktop MCP configuration");
}

function chatGPTPIDs() {
  let output = "";
  try {
    output = execFileSync("/bin/ps", ["-axo", "pid=,uid=,command="], { encoding: "utf8" });
  } catch {
    return [];
  }
  const uid = typeof process.getuid === "function" ? process.getuid() : -1;
  const pids = [];
  for (const line of output.split("\n")) {
    const match = line.trim().match(/^(\d+)\s+(\d+)\s+(.+)$/);
    if (match && Number(match[2]) === uid && match[3] === desktopCommand) pids.push(match[1]);
  }
  return pids;
}

function appToolsPipeCandidates() {
  const uid = typeof process.getuid === "function" ? process.getuid() : -1;
  const candidates = new Map();
  for (const pid of chatGPTPIDs()) {
    let output = "";
    try {
      output = execFileSync("/usr/sbin/lsof", ["-n", "-P", "-a", "-p", pid, "-U", "-Fn"], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"],
      });
    } catch {
      continue;
    }
    for (const line of output.split("\n")) {
      if (!line.startsWith("n/tmp/codex-browser-use/") || !line.endsWith(".sock")) continue;
      const candidate = line.slice(1);
      if (path.dirname(candidate) !== "/tmp/codex-browser-use") continue;
      try {
        const stat = fs.lstatSync(candidate);
        const kindAndPermissions = stat.mode & 0o170777;
        if (stat.uid === uid && kindAndPermissions === 0o140600) {
          candidates.set(candidate, stat.mtimeMs);
        }
      } catch {
        // Socket disappeared while Desktop was restarting; retry next tick.
      }
    }
  }
  return [...candidates.entries()].sort((a, b) => a[1] - b[1] || a[0].localeCompare(b[0]));
}

let linkedPipe = "";
let reloadTimer = null;
let appToolsConfigured = false;
function scheduleMCPReload(attempt = 0) {
  if (!appToolsConfigured) return;
  if (reloadTimer) clearTimeout(reloadTimer);
  reloadTimer = setTimeout(async () => {
    reloadTimer = null;
    try {
      await reloadAppToolsMCP();
    } catch (error) {
      if (attempt < 14) scheduleMCPReload(attempt + 1);
      else process.stderr.write(`mac-fleet shared app-server: App tools reload failed: ${error.message}\n`);
    }
  }, attempt === 0 ? 1500 : 3000);
}

function refreshStablePipe() {
  const candidates = appToolsPipeCandidates();
  if (candidates.length === 0) return;
  const candidate = candidates[0][0];
  if (candidate === linkedPipe) return;
  fs.mkdirSync(stateDir, { mode: 0o700, recursive: true });
  try {
    const existing = fs.lstatSync(stablePipe);
    if (!existing.isSymbolicLink()) {
      process.stderr.write(`mac-fleet shared app-server: refusing to replace non-symlink ${stablePipe}\n`);
      return;
    }
    if (fs.readlinkSync(stablePipe) === candidate) {
      linkedPipe = candidate;
      return;
    }
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  const temporary = `${stablePipe}.${process.pid}.new`;
  try {
    const tempInfo = fs.lstatSync(temporary);
    if (tempInfo.isSymbolicLink()) fs.unlinkSync(temporary);
    else throw new Error(`refusing to replace non-symlink ${temporary}`);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  fs.symlinkSync(candidate, temporary);
  fs.renameSync(temporary, stablePipe);
  linkedPipe = candidate;
  process.stderr.write(`mac-fleet shared app-server: Desktop app-tools pipe ${path.basename(candidate)}\n`);
  scheduleMCPReload();
}

function appToolsOverride() {
  const pluginDir = path.join(resourcesPath, "plugins", "openai-bundled", "plugins", "codex-app-tools");
  const configPath = path.join(pluginDir, "desktop-mcp.json");
  const launcher = path.join(pluginDir, "scripts", "launch_codex_app_tools_mcp");
  const nodePath = path.join(resourcesPath, "cua_node", "bin", "node");
  const serverMJS = path.join(pluginDir, "server.mjs");
  if (![configPath, launcher, nodePath, serverMJS].every((entry) => fs.existsSync(entry))) return "";
  const parsed = JSON.parse(fs.readFileSync(configPath, "utf8"));
  const config = structuredClone(parsed?.mcpServers?.codex_app || {});
  config.command = launcher;
  config.args = ["./server.mjs"];
  config.cwd = pluginDir;
  config.enabled = true;
  config.omit_tools_from = ["deferred", "code_mode"];
  config.env = {
    ...(config.env || {}),
    CODEX_APP_TOOLS_PIPE_PATH: stablePipe,
    CODEX_MCP_NODE_PATH: nodePath,
    CODEX_BROWSER_USE_NODE_PATH: nodePath,
    CODEX_ELECTRON_RESOURCES_PATH: resourcesPath,
  };
  return `mcp_servers.codex_app=${tomlValue(config)}`;
}

function appServerRPC() {
  const endpoint = new URL(listenURL);
  endpoint.pathname = "/rpc";
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(endpoint.href);
    const pending = new Map();
    let nextId = 1;
    const timeout = setTimeout(() => {
      socket.close();
      reject(new Error("app-server RPC connection timed out"));
    }, 5000);
    socket.addEventListener("error", () => reject(new Error("app-server RPC connection failed")), { once: true });
    socket.addEventListener("message", (event) => {
      let message;
      try { message = JSON.parse(String(event.data)); } catch { return; }
      if (message.id == null || !pending.has(message.id)) return;
      const waiter = pending.get(message.id);
      pending.delete(message.id);
      if (message.error) waiter.reject(new Error(message.error.message || "app-server RPC error"));
      else waiter.resolve(message.result);
    });
    socket.addEventListener("open", () => {
      clearTimeout(timeout);
      const call = (method, params) => new Promise((resolveCall, rejectCall) => {
        const id = nextId++;
        pending.set(id, { resolve: resolveCall, reject: rejectCall });
        socket.send(JSON.stringify({ id, method, ...(params === undefined ? {} : { params }) }));
      });
      const notify = (method, params) => socket.send(JSON.stringify({ method, ...(params === undefined ? {} : { params }) }));
      resolve({ socket, call, notify });
    }, { once: true });
  });
}

async function reloadAppToolsMCP() {
  const rpc = await appServerRPC();
  try {
    await rpc.call("initialize", {
      clientInfo: { name: "mac_fleet_keeper", title: "mac-fleet keeper", version: "1" },
      capabilities: { experimentalApi: true },
    });
    rpc.notify("initialized", null);
    await rpc.call("config/mcpServer/reload", null);
    const status = await rpc.call("mcpServerStatus/list", { cursor: null, limit: 100, detail: "full" });
    const appTools = status?.data?.find((entry) => entry.name === "codex_app");
    const toolCount = appTools?.tools ? Object.keys(appTools.tools).length : 0;
    if (toolCount === 0 || (appTools.runtimeStatus && appTools.runtimeStatus !== "connected")) {
      throw new Error(`codex_app status=${appTools?.runtimeStatus || "unknown"} tools=${toolCount}`);
    }
    process.stderr.write(`mac-fleet shared app-server: codex_app ready tools=${toolCount}\n`);
  } finally {
    rpc.socket.close();
  }
}

function startUnixProxy() {
  if (!proxySocket || !path.isAbsolute(proxySocket)) fail(`invalid Unix proxy socket: ${proxySocket || "(empty)"}`);
  fs.mkdirSync(path.dirname(proxySocket), { mode: 0o700, recursive: true });
  try {
    const existing = fs.lstatSync(proxySocket);
    if (!existing.isSocket() && !existing.isSymbolicLink()) fail(`refusing to replace ${proxySocket}`);
    fs.unlinkSync(proxySocket);
  } catch (error) {
    if (error?.code !== "ENOENT") throw error;
  }
  const target = new URL(listenURL);
  const server = net.createServer((client) => {
    let upstream = null;
    let attempts = 0;
    const connect = () => {
      attempts += 1;
      upstream = net.createConnection({ host: "127.0.0.1", port: Number(target.port) });
      upstream.once("connect", () => {
        client.pipe(upstream);
        upstream.pipe(client);
      });
      upstream.once("error", () => {
        upstream.destroy();
        if (!client.destroyed && attempts < 20) setTimeout(connect, 100);
        else client.destroy();
      });
    };
    client.on("error", () => upstream?.destroy());
    connect();
  });
  server.listen(proxySocket, () => fs.chmodSync(proxySocket, 0o600));
  return server;
}

if (!codexBin || !fs.existsSync(codexBin)) fail(`Codex binary is unavailable: ${codexBin || "(empty)"}`);
assertSharedListenURL(listenURL);
try {
  refreshStablePipe();
} catch (error) {
  process.stderr.write(`mac-fleet shared app-server: initial app-tools discovery failed: ${error.message}\n`);
}

const args = ["-c", "features.code_mode_host=true"];
const mcpOverride = appToolsOverride();
if (mcpOverride) {
  appToolsConfigured = true;
  args.push("-c", mcpOverride);
}
args.push("app-server", "--analytics-default-enabled", "--listen", listenURL);

const child = spawn(codexBin, args, {
  env: {
    ...process.env,
    CODEX_HOME: codexHome,
    CODEX_INTERNAL_ORIGINATOR_OVERRIDE: "Codex Desktop",
  },
  stdio: ["ignore", "inherit", "inherit"],
});
const unixProxy = startUnixProxy();

const pipeTimer = setInterval(() => {
  try {
    refreshStablePipe();
  } catch (error) {
    process.stderr.write(`mac-fleet shared app-server: app-tools discovery failed: ${error.message}\n`);
  }
}, 2000);

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => child.kill(signal));
}
child.on("error", (error) => fail(`could not start Codex: ${error.message}`));
child.on("exit", (code, signal) => {
  clearInterval(pipeTimer);
  if (reloadTimer) clearTimeout(reloadTimer);
  unixProxy.close();
  try {
    const existing = fs.lstatSync(proxySocket);
    if (existing.isSocket()) fs.unlinkSync(proxySocket);
  } catch {
    // Already removed by launchd teardown or a replacement keeper.
  }
  if (signal) process.stderr.write(`mac-fleet shared app-server: Codex exited on ${signal}\n`);
  process.exit(code ?? 1);
});
