#!/usr/bin/env node

import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const appPath = process.env.FLEET_CODEX_DESKTOP_APP_PATH || "/Applications/ChatGPT.app";
const resourcesPath = path.join(appPath, "Contents", "Resources");
const codexBin = process.env.FLEET_CODEX_BIN;
const listenURL = process.env.FLEET_CODEX_APPSERVER_LISTEN;
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

if (!codexBin || !fs.existsSync(codexBin)) fail(`Codex binary is unavailable: ${codexBin || "(empty)"}`);
assertSharedListenURL(listenURL);
try {
  refreshStablePipe();
} catch (error) {
  process.stderr.write(`mac-fleet shared app-server: initial app-tools discovery failed: ${error.message}\n`);
}

const args = ["-c", "features.code_mode_host=true"];
const mcpOverride = appToolsOverride();
if (mcpOverride) args.push("-c", mcpOverride);
args.push("app-server", "--analytics-default-enabled", "--listen", listenURL);

const child = spawn(codexBin, args, {
  env: {
    ...process.env,
    CODEX_HOME: codexHome,
    CODEX_INTERNAL_ORIGINATOR_OVERRIDE: "Codex Desktop",
  },
  stdio: ["ignore", "inherit", "inherit"],
});

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
  if (signal) process.stderr.write(`mac-fleet shared app-server: Codex exited on ${signal}\n`);
  process.exit(code ?? 1);
});
