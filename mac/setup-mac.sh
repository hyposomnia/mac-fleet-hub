#!/usr/bin/env bash
# 在每台 Mac 上运行：安装并启动 ttyd(→tmux→claude) 与 Filebrowser，仅绑 Tailscale 内网 IP。
# 前置：已装并登录 Tailscale（tailscale status 能看到本机与服务器）。
#
# 用法：bash mac/setup-mac.sh
set -euo pipefail

TTYD_PORT="${TTYD_PORT:-7681}"
FB_PORT="${FB_PORT:-8080}"
FB_ROOT="${FB_ROOT:-$HOME}"                 # Filebrowser 根目录，按需收窄
FB_DB="$HOME/.macfleet-filebrowser.db"

# --- 0. 定位 Homebrew ---
if command -v brew >/dev/null 2>&1; then
  BREW_PREFIX="$(brew --prefix)"
else
  echo "未找到 Homebrew，请先安装：https://brew.sh" >&2; exit 1
fi

# --- 1. 取本机 Tailscale IP ---
TS_BIN="$(command -v tailscale || echo /Applications/Tailscale.app/Contents/MacOS/Tailscale)"
TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -n1 || true)"
if [[ -z "${TS_IP}" ]]; then
  echo "拿不到 Tailscale IP，请确认 Tailscale 已登录并连接。" >&2; exit 1
fi
echo "本机 Tailscale IP: $TS_IP"

# --- 2. 安装依赖 ---
echo "安装 ttyd tmux filebrowser syncthing ..."
brew install ttyd tmux filebrowser syncthing 2>/dev/null || true

# --- 3. 生成并安装 launchd 服务 ---
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LA="$HOME/Library/LaunchAgents"
mkdir -p "$LA"

render() { # src dst
  sed -e "s#__BREW_PREFIX__#${BREW_PREFIX}#g" \
      -e "s#__TS_IP__#${TS_IP}#g" \
      -e "s#__PORT__#${PORT}#g" \
      -e "s#__ROOT__#${FB_ROOT}#g" \
      -e "s#__DB__#${FB_DB}#g" \
      "$1" > "$2"
}

PORT="$TTYD_PORT" render "$SCRIPT_DIR/com.macfleet.ttyd.plist"        "$LA/com.macfleet.ttyd.plist"
PORT="$FB_PORT"   render "$SCRIPT_DIR/com.macfleet.filebrowser.plist" "$LA/com.macfleet.filebrowser.plist"

for svc in com.macfleet.ttyd com.macfleet.filebrowser; do
  launchctl unload "$LA/$svc.plist" 2>/dev/null || true
  launchctl load  "$LA/$svc.plist"
  echo "已加载服务: $svc"
done

cat <<EOF

✅ 完成。本机服务（仅 Tailscale 内网可达）：
   网页终端  http://${TS_IP}:${TTYD_PORT}     (进入后是 tmux 会话，可运行 claude)
   文件管理  http://${TS_IP}:${FB_PORT}        (默认 admin/admin，首登请改密码)

下一步：
  1) 启动 Syncthing(brew services start syncthing)，把 ~/Shared 加入三台同步组。
  2) 在服务器侧填好该 Mac 的 IP(${TS_IP})到 server/.env，部署 Caddy+Authelia。
日志：/tmp/macfleet-ttyd.{log,err}  /tmp/macfleet-filebrowser.{log,err}
EOF
