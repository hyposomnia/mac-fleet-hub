#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP="$ROOT/mac/setup-mac.sh"
APPSERVER_PLIST="$ROOT/mac/com.macfleet.codex-app-server.plist"
SHARED_APPSERVER_PLIST="$ROOT/mac/com.macfleet.codex-shared-app-server.plist"
SHARED_KEEPER="$ROOT/mac/codex-shared-app-server.mjs"
DESKTOP_ENV_PLIST="$ROOT/mac/com.macfleet.codex-desktop-env.plist"
DESKTOP_ENV_HELPER="$ROOT/mac/codex-desktop-env.sh"
MIGRATE="$ROOT/mac/migrate-existing-client-to-shared.sh"
RELEASE="$ROOT/scripts/release-fleet-agent.sh"
CONFIG_DEPLOY="$ROOT/scripts/deploy-shared-config.sh"

fail() {
  echo "setup-mac shared test failed: $*" >&2
  exit 1
}

contains() {
  local file="$1" literal="$2"
  rg -qF -- "$literal" "$file" || fail "$file does not contain: $literal"
}

bash -n "$SETUP"
/usr/bin/plutil -lint "$APPSERVER_PLIST" >/dev/null
/usr/bin/plutil -lint "$SHARED_APPSERVER_PLIST" >/dev/null
/usr/bin/plutil -lint "$DESKTOP_ENV_PLIST" >/dev/null
bash -n "$DESKTOP_ENV_HELPER"
bash -n "$MIGRATE" "$RELEASE" "$CONFIG_DEPLOY"
if [[ -x /Applications/ChatGPT.app/Contents/Resources/cua_node/bin/node ]]; then
  /Applications/ChatGPT.app/Contents/Resources/cua_node/bin/node --check "$SHARED_KEEPER"
fi

contains "$SETUP" 'CODEX_APPSERVER_MODE="${FLEET_CODEX_APPSERVER_MODE:-shared}"'
contains "$SETUP" 'CODEX_DESKTOP_SHARED_DAEMON="${FLEET_CODEX_DESKTOP_SHARED_DAEMON:-1}"'
contains "$SETUP" 'CODEX_APPSERVER_SOCK="${CODEX_APPSERVER_SOCK:-$HOME/.macfleet/codex-app-server.sock}"'
contains "$SETUP" 'CODEX_DESKTOP_WS_URL="${FLEET_CODEX_DESKTOP_WS_URL:-ws://127.0.0.1:47682/rpc}"'
contains "$SETUP" 'CODEX_APPSERVER_LISTEN="ws://127.0.0.1:${CODEX_APPSERVER_PORT}"'
contains "$SETUP" 'launchctl setenv CODEX_APP_SERVER_WS_URL "$CODEX_DESKTOP_WS_URL"'
contains "$SETUP" 'launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON'
contains "$SETUP" '"$CODEX_BIN" app-server daemon stop'
contains "$SETUP" 'render "$SCRIPT_DIR/com.macfleet.codex-shared-app-server.plist"'
contains "$SETUP" 'install -m 0700 "$SCRIPT_DIR/codex-shared-app-server.mjs" "$CODEX_KEEPER_SCRIPT"'
contains "$SETUP" 'launchctl bootstrap "$GUI_DOMAIN" "$LA/com.macfleet.codex-desktop-env.plist"'
contains "$SETUP" 'curl -fsS --max-time 1 "http://127.0.0.1:${CODEX_APPSERVER_PORT}/readyz"'
contains "$SHARED_KEEPER" 'kindAndPermissions === 0o140600'
contains "$SHARED_KEEPER" 'mcp_servers.codex_app='
contains "$MIGRATE" '/usr/bin/open --env "CODEX_APP_SERVER_WS_URL=$SHARED_WS_URL"'
contains "$MIGRATE" 'Fleet PID is not connected to the shared Unix proxy'
contains "$RELEASE" 'migrate-existing-client-to-shared.sh'
contains "$CONFIG_DEPLOY" '保留现有正式 agent 二进制'

shared_install_block="$(awk '
  $0 == "    shared)" { capture=1 }
  capture { print }
  capture && $0 == "    daemon)" { exit }
' "$SETUP")"
[[ "$shared_install_block" != *"refresh_managed_codex_binary"* ]] || fail "shared still copies a stale managed Codex"
[[ "$shared_install_block" != *"bootstrap_legacy_codex_daemon"* ]] || fail "shared still bootstraps the control-socket daemon"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-shared-plist.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT
rendered="$tmpdir/com.macfleet.codex-app-server.plist"
sed -e 's#__CODEX_BIN__#/tmp/codex#g' \
    -e 's#__CODEX_APPSERVER_LISTEN__#ws://127.0.0.1:47682#g' \
    -e 's#__CODEX_APPSERVER_SOCK__#/tmp/codex-shared-proxy.sock#g' \
    -e 's#__CODEX_KEEPER_NODE__#/tmp/openai-node#g' \
    -e 's#__CODEX_KEEPER_SCRIPT__#/tmp/codex-shared-app-server.mjs#g' \
    -e 's#__CODEX_HOME__#/tmp/codex-home#g' \
    -e 's#__BREW_PREFIX__#/opt/homebrew#g' \
    "$SHARED_APPSERVER_PLIST" > "$rendered"
/usr/bin/plutil -lint "$rendered" >/dev/null

[[ "$(/usr/bin/plutil -extract ProgramArguments.0 raw -o - "$rendered")" == "/tmp/openai-node" ]]
[[ "$(/usr/bin/plutil -extract ProgramArguments.1 raw -o - "$rendered")" == "/tmp/codex-shared-app-server.mjs" ]]
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_BIN raw -o - "$rendered")" == "/tmp/codex" ]]
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_APPSERVER_LISTEN raw -o - "$rendered")" == "ws://127.0.0.1:47682" ]]
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_CODEX_APPSERVER_PROXY_SOCK raw -o - "$rendered")" == "/tmp/codex-shared-proxy.sock" ]]

if rg -qF -- '--remote-control' "$APPSERVER_PLIST"; then
  fail "shared LaunchAgent still enables the old remote-control mode"
fi

# fleet-agent plist 的 DSH 占位符必须能被渲染，且默认关闭。
AGENT_PLIST="$ROOT/mac/com.macfleet.fleet-agent.plist"
rendered_agent="$tmpdir/com.macfleet.fleet-agent.plist"
sed -e 's#__BREW_PREFIX__#/opt/homebrew#g' \
    -e 's#__FLEET_CONFIG_URL__##g' \
    -e 's#__TS_IP__#100.64.0.2#g' \
    -e 's#__PORT__#7681#g' \
    -e 's#__ROOT__#/Users/test#g' \
    -e 's#__DB__#/tmp/fb.db#g' \
    -e 's#__TTYD_BASE__#/m1/term#g' \
    -e 's#__FB_BASE__#/m1/files#g' \
    -e 's#__FB_BIN__#/opt/homebrew/bin/filebrowser#g' \
    -e 's#__FLEET_ATTACH__#/tmp/fleet-attach.sh#g' \
    -e 's#__AGENT_BIN__#/tmp/fleet-agent#g' \
    -e 's#__AGENT_PORT__#7682#g' \
    -e 's#__MAC_INDEX__#1#g' \
    -e 's#__CLAUDE_BIN__#/opt/homebrew/bin/claude#g' \
    -e 's#__CODEX_BIN__#/tmp/codex#g' \
    -e 's#__MANAGED_CODEX_BIN__#/tmp/codex#g' \
    -e 's#__CODEX_HOME__#/tmp/.codex#g' \
    -e 's#__CODEX_APPSERVER_MODE__#shared#g' \
    -e 's#__CODEX_APPSERVER_SOCK__#/tmp/codex.sock#g' \
    -e 's#__CODEX_DESKTOP_WS_URL__#ws://127.0.0.1:47682/rpc#g' \
    -e 's#__CODEX_DESKTOP_SHARED_DAEMON__#1#g' \
    -e 's#__DSH_ENABLED__#0#g' \
    -e 's#__DSH_HOME__#/tmp/dsh-home#g' \
    -e 's#__DSH_LOG__#/tmp/harness.log#g' \
    "$AGENT_PLIST" > "$rendered_agent"
/usr/bin/plutil -lint "$rendered_agent" >/dev/null
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_DSH_ENABLED raw -o - "$rendered_agent")" == "0" ]]
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_DSH_HOME raw -o - "$rendered_agent")" == "/tmp/dsh-home" ]]
[[ "$(/usr/bin/plutil -extract EnvironmentVariables.FLEET_DSH_LOG raw -o - "$rendered_agent")" == "/tmp/harness.log" ]]
# 渲染后不能残留任何未替换的占位符，否则 launchd 会把字面量当成配置值。
if rg -qF -- '__DSH_' "$rendered_agent"; then
  fail "fleet-agent plist 渲染后仍残留 DSH 占位符"
fi
contains "$SETUP" '__DSH_ENABLED__'
# DSH 开关必须"sticky"：重渲染 plist 时保留已安装里的值，
# 否则下次发布会把已经开好的机器悄悄关回去。
contains "$SETUP" 'dsh_enabled_default()'
contains "$SETUP" 's#__DSH_ENABLED__#${DSH_ENABLED:-$(dsh_enabled_default)}#g'
contains "$SETUP" '__DSH_HOME__'
contains "$SETUP" '__DSH_LOG__'

echo "setup-mac shared tests passed"
