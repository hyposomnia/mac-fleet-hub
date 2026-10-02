#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ "$(uname -s)" == Darwin ]] || { echo '仅支持 macOS 客户端。' >&2; exit 1; }
command -v brew >/dev/null || { echo '请先安装 Homebrew：https://brew.sh' >&2; exit 1; }
ORIGIN="${FLEET_ORIGIN:-${1:-}}"
if [[ -z "$ORIGIN" ]]; then read -r -p '服务网页地址（例如 https://fleet.example.com）> ' ORIGIN < /dev/tty; fi
[[ "$ORIGIN" =~ ^https://[^/]+/?$ ]] || { echo '请输入 HTTPS 服务网页地址，不是控制面地址。' >&2; exit 1; }
ORIGIN="${ORIGIN%/}"
ARCH="$(uname -m)"; [[ "$ARCH" == arm64 ]] && AB=arm64 || AB=amd64
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
echo '下载正式签名客户端并验证授权协议…'
BUNDLED_AGENT="$SCRIPT_DIR/fleet-agent/dist/fleet-agent-darwin-${AB}"
if [[ -f "$BUNDLED_AGENT" && ! -L "$BUNDLED_AGENT" ]]; then
  cp "$BUNDLED_AGENT" "$WORK/fleet-agent"
else
  curl --proto '=https' --tlsv1.2 -fsS --max-time 300 "$ORIGIN/enroll/dist/fleet-agent-darwin-${AB}" -o "$WORK/fleet-agent"
fi
chmod 0700 "$WORK/fleet-agent"
codesign --verify --strict "$WORK/fleet-agent"
SIGNATURE="$(codesign -d --verbose=4 "$WORK/fleet-agent" 2>&1)"
grep -q '^Authority=Developer ID Application:' <<< "$SIGNATURE" || { echo '拒绝非 Developer ID 客户端。' >&2; exit 1; }
grep -qx 'Identifier=com.macfleet.fleet-agent' <<< "$SIGNATURE" || { echo '客户端签名身份错误。' >&2; exit 1; }
CAPABILITIES="$("$WORK/fleet-agent" capabilities)" || { echo '服务器尚未发布支持逐用户授权的签名客户端，请先完成正式发布。' >&2; exit 1; }
grep -Eq '"device_authorization"[[:space:]]*:[[:space:]]*1' <<< "$CAPABILITIES" || { echo '客户端不支持设备授权，停止安装。' >&2; exit 1; }
if ! command -v tailscale >/dev/null; then brew install tailscale; fi
TS_BIN="$(command -v tailscale)"
if ! pgrep -x tailscaled >/dev/null; then
  echo '将安装并启动 tailscaled 系统守护进程，需要 sudo 密码。'
  sudo "${TS_BIN}d" install-system-daemon
fi
SUPPORT="$HOME/.macfleet/support"
mkdir -p "$HOME/.macfleet"
[[ ! -L "$HOME/.macfleet" ]] || { echo '私有状态目录不能是符号链接。' >&2; exit 1; }
chmod 0700 "$HOME/.macfleet"
[[ ! -L "$SUPPORT" ]] || { echo '支持文件目录不能是符号链接。' >&2; exit 1; }
mkdir -p "$SUPPORT/fleet-agent/dist" "$HOME/.local/bin"
chmod 0700 "$SUPPORT"
echo '将更新客户端安装支持文件；配对时会提示入网与服务安装操作。'
if [[ "$SCRIPT_DIR" != "$SUPPORT" ]]; then
  cp "$SCRIPT_DIR/"*.sh "$SCRIPT_DIR/"*.plist "$SCRIPT_DIR/"*.mjs "$SUPPORT/"
  cp "$SCRIPT_DIR/fleet-agent/fleet-attach.sh" "$SUPPORT/fleet-agent/"
fi
install -m 0755 "$WORK/fleet-agent" "$SUPPORT/fleet-agent/dist/fleet-agent-darwin-${AB}"
LOGIN_ARGS=(login --configure)
[[ "${FLEET_REPLACE_TAILNET:-0}" == 1 ]] && LOGIN_ARGS+=(--replace-tailnet)
LOGIN_ARGS+=("$ORIGIN")
"$SUPPORT/fleet-agent/dist/fleet-agent-darwin-${AB}" "${LOGIN_ARGS[@]}"
