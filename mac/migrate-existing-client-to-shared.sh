#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FLEET_PLIST="$HOME/Library/LaunchAgents/com.macfleet.fleet-agent.plist"
APPSERVER_PLIST="$HOME/Library/LaunchAgents/com.macfleet.codex-app-server.plist"
DESKTOP_APP="/Applications/ChatGPT.app"
SHARED_PROXY_SOCK="${FLEET_CODEX_APPSERVER_SOCK:-$HOME/.macfleet/codex-app-server.sock}"
SHARED_WS_URL="${FLEET_CODEX_DESKTOP_WS_URL:-ws://127.0.0.1:47682/rpc}"
SHARED_PORT="${SHARED_WS_URL#ws://127.0.0.1:}"
SHARED_PORT="${SHARED_PORT%%/*}"

die() { echo "shared migration failed: $*" >&2; exit 1; }
plist_env() {
  /usr/bin/plutil -extract "EnvironmentVariables.$1" raw -o - "$FLEET_PLIST" 2>/dev/null || true
}
json_field() {
  local json="$1" field="$2"
  printf '%s' "$json" | /usr/bin/plutil -extract "$field" raw -o - - 2>/dev/null || true
}

[[ -f "$FLEET_PLIST" ]] || die "missing installed fleet-agent plist"
[[ -d "$DESKTOP_APP" ]] || die "missing $DESKTOP_APP"
[[ -n "${FLEET_UPDATE_BASE:-}" ]] || die "FLEET_UPDATE_BASE is required"
[[ "$SHARED_PROXY_SOCK" == /* ]] || die "Fleet shared proxy socket must be absolute"
[[ "$SHARED_WS_URL" =~ ^ws://127\.0\.0\.1:[0-9]{1,5}(/[^[:space:]?#]*)?$ ]] \
  || die "Desktop shared endpoint must use 127.0.0.1"

MAC_INDEX="$(plist_env FLEET_MAC_INDEX)"
FB_ROOT="$(plist_env FLEET_FILE_ROOT)"
CODEX_HOME_DIR="$(plist_env FLEET_CODEX_HOME)"
[[ -n "$MAC_INDEX" ]] || die "missing FLEET_MAC_INDEX"
[[ -n "$FB_ROOT" ]] || FB_ROOT="$HOME"
[[ -n "$CODEX_HOME_DIR" ]] || CODEX_HOME_DIR="$HOME/.codex"

echo "确认目标机没有正在运行的 Codex turn（连续两次）..."
FLEET_CODEX_HOME="$CODEX_HOME_DIR" bash "$SCRIPT_DIR/check-codex-idle.sh"
sleep 2
FLEET_CODEX_HOME="$CODEX_HOME_DIR" bash "$SCRIPT_DIR/check-codex-idle.sh"

stamp="$(date +%Y%m%d%H%M%S)"
backup_dir="$HOME/.macfleet/migration-backups/release-to-shared-ws.${stamp}.$$"
install -d -m 0700 "$backup_dir"
for existing in "$HOME/.local/bin/fleet-agent" "$FLEET_PLIST" "$APPSERVER_PLIST"; do
  [[ -f "$existing" ]] && cp -p "$existing" "$backup_dir/$(basename "$existing")"
done
filebrowser_copy=""
if [[ -x "$HOME/.local/bin/filebrowser" ]]; then
  filebrowser_copy="$backup_dir/filebrowser"
  cp -p "$HOME/.local/bin/filebrowser" "$filebrowser_copy"
fi

echo "运行同一发布提交内的 shared 安装迁移 ..."
setup_env=(
  "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
  "MAC_INDEX=$MAC_INDEX"
  "FB_ROOT=$FB_ROOT"
  "FLEET_UPDATE_BASE=$FLEET_UPDATE_BASE"
  "FLEET_CODEX_HOME=$CODEX_HOME_DIR"
  "FLEET_CODEX_APPSERVER_MODE=shared"
  "FLEET_CODEX_APPSERVER_SOCK=$SHARED_PROXY_SOCK"
  "FLEET_CODEX_DESKTOP_WS_URL=$SHARED_WS_URL"
  "FLEET_CODEX_DESKTOP_SHARED_DAEMON=1"
)
if [[ -n "$filebrowser_copy" ]]; then
  setup_env+=("FILEBROWSER_BIN=$filebrowser_copy")
fi
env "${setup_env[@]}" bash "$SCRIPT_DIR/setup-mac.sh"

ip="$(/opt/homebrew/bin/tailscale ip -4 | head -n1)"
[[ -n "$ip" ]] || die "missing mesh IP after setup"
curl -fsS --max-time 3 "http://127.0.0.1:${SHARED_PORT}/readyz" >/dev/null \
  || die "shared app-server is not ready"
curl -fsS --max-time 3 "http://${ip}:7682/api/health" | grep -qx ok \
  || die "fleet-agent is not healthy"

echo "确认 turn 仍为空闲，然后确定性重启 ChatGPT Desktop ..."
FLEET_CODEX_HOME="$CODEX_HOME_DIR" bash "$SCRIPT_DIR/check-codex-idle.sh"
/usr/bin/osascript -e 'tell application "ChatGPT" to quit' >/dev/null 2>&1 || true
for _ in {1..30}; do
  if ! /bin/ps -axo command= | grep -Fxq "$DESKTOP_APP/Contents/MacOS/ChatGPT"; then
    break
  fi
  sleep 1
done
if /bin/ps -axo command= | grep -Fxq "$DESKTOP_APP/Contents/MacOS/ChatGPT"; then
  die "ChatGPT did not quit cleanly; refusing to force-kill it"
fi
/usr/bin/open --env "CODEX_APP_SERVER_WS_URL=$SHARED_WS_URL" -a "$DESKTOP_APP"

desktop_pid=""
for _ in {1..30}; do
  desktop_pid="$(/bin/ps -axo pid=,command= | awk -v cmd="$DESKTOP_APP/Contents/MacOS/ChatGPT" '$2 == cmd && NF == 2 {print $1; exit}')"
  if [[ -n "$desktop_pid" ]] && /usr/sbin/lsof -n -P -a -p "$desktop_pid" -iTCP:"$SHARED_PORT" 2>/dev/null | grep -q ESTABLISHED; then
    break
  fi
  sleep 1
done
[[ -n "$desktop_pid" ]] || die "ChatGPT did not restart"
/usr/sbin/lsof -n -P -a -p "$desktop_pid" -iTCP:"$SHARED_PORT" 2>/dev/null | grep -q ESTABLISHED \
  || die "ChatGPT did not connect to shared app-server"
if /bin/ps -axo ppid=,command= | awk -v pid="$desktop_pid" '$1 == pid && /codex .*app-server/ {found=1} END {exit !found}'; then
  die "ChatGPT still spawned a private app-server"
fi

# A read-only skills request forces fleet-agent to initialize its own WS client.
curl -fsS --max-time 30 -H 'Content-Type: application/json' \
  -d "{\"assistant\":\"codex\",\"cwd\":\"$HOME\"}" \
  "http://${ip}:7682/api/chat/skills" >/dev/null

server_pid="$(/usr/sbin/lsof -n -P -t -iTCP:"$SHARED_PORT" -sTCP:LISTEN 2>/dev/null | head -n1)"
agent_pid="$(launchctl print "gui/$(id -u)/com.macfleet.fleet-agent" | awk '/pid =/{print $3; exit}')"
[[ -n "$server_pid" && -n "$agent_pid" ]] || die "missing shared server or fleet-agent PID"
/usr/sbin/lsof -n -P -a -p "$agent_pid" -U 2>/dev/null | grep -q -- '->' \
  || die "Fleet PID is not connected to the shared Unix proxy"
listener="$(/usr/sbin/lsof -n -P -a -p "$server_pid" -iTCP:"$SHARED_PORT" -sTCP:LISTEN 2>/dev/null)"
printf '%s\n' "$listener" | grep -q "127.0.0.1:${SHARED_PORT} (LISTEN)" \
  || die "shared listener is not loopback-only"

# Resume one existing idle thread and prove its physical writer is the same
# shared server PID, not a private Desktop/Fleet process.
sessions="$(curl -fsS --max-time 10 "http://${ip}:7682/api/sessions?assistant=codex&scope=active")"
probe_session="$(json_field "$sessions" sessions.0.sessionId)"
if [[ -n "$probe_session" ]]; then
  curl -fsS --max-time 20 -H 'Content-Type: application/json' \
    -d "{\"assistant\":\"codex\",\"sessionId\":\"$probe_session\"}" \
    "http://${ip}:7682/api/chat/resume" >/dev/null
  lock_path="$CODEX_HOME_DIR/thread-writer-locks/$probe_session.lock"
  lock_owner="$(/usr/sbin/lsof -t -- "$lock_path" 2>/dev/null | sort -u | tr '\n' ' ' | xargs)"
  [[ "$lock_owner" == "$server_pid" ]] || die "thread writer PID is $lock_owner, expected shared server $server_pid"
fi

desktop_mcp="$DESKTOP_APP/Contents/Resources/plugins/openai-bundled/plugins/codex-app-tools/desktop-mcp.json"
if [[ -f "$desktop_mcp" ]]; then
  app_tools_pid=""
  for _ in {1..45}; do
    app_tools_pid="$(pgrep -P "$server_pid" -f 'codex-app-tools/server.mjs|server.mjs' | head -n1 || true)"
    [[ -n "$app_tools_pid" ]] && break
    sleep 1
  done
  [[ -n "$app_tools_pid" ]] || die "Codex App tools MCP did not become ready"
fi

codesign --verify --deep --strict "$HOME/.local/bin/fleet-agent"
echo "shared_uat_ok host=$(hostname) server_pid=$server_pid desktop_pid=$desktop_pid agent_pid=$agent_pid writer_pid=${lock_owner:-none} app_tools_pid=${app_tools_pid:-unsupported} backup=$backup_dir"
