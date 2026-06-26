#!/usr/bin/env bash
# 在每台 Mac 上运行：入网 Headscale + 起 ttyd / filebrowser / fleet-agent（仅绑 mesh 内网 IP）。
#
# 用法：
#   MAC_INDEX=1 \
#   LOGIN_SERVER=https://fleet.example.com:8443 AUTHKEY=<preauthkey> \
#   bash mac/setup-mac.sh
#
#   - MAC_INDEX 必填(1/2/3/…)：决定终端/文件路径 /m{idx}/...，且要与网关 .env 的 MAC_IPS 第几个对应一致
#   - LOGIN_SERVER/AUTHKEY 选填：给出则自动 tailscale up 入网（Headscale）；
#     省略则假设你已手动入网。
#   - FLEET_UPDATE_BASE 选填：写进 ~/.zshrc，使 `fleet-agent update` 自更新开箱即用。
#   - 不修改系统「远程登录/屏幕共享」开关（mac↔mac 的 SSH/VNC 请自行在系统设置开启）。
set -euo pipefail

MAC_INDEX="${MAC_INDEX:?请设置 MAC_INDEX=1|2|3}"
TTYD_PORT="${TTYD_PORT:-7681}"
FB_PORT="${FB_PORT:-8080}"
AGENT_PORT="${AGENT_PORT:-7682}"
FB_ROOT="${FB_ROOT:-$HOME}"                       # 文件管理根目录 = 整个 home（用户决定）
FB_DB="$HOME/.macfleet-filebrowser.db"
TTYD_BASE="/m${MAC_INDEX}/term"
FB_BASE="/m${MAC_INDEX}/files"
BIN_DIR="$HOME/.local/bin"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# --- 0. Homebrew ---
command -v brew >/dev/null 2>&1 || { echo "未找到 Homebrew，请先装：https://brew.sh" >&2; exit 1; }
BREW_PREFIX="$(brew --prefix)"
CLAUDE_BIN="$(command -v claude || echo "$BREW_PREFIX/bin/claude")"
CODEX_BIN="$(command -v codex || echo "$BREW_PREFIX/bin/codex")"

# --- 1. Tailscale 客户端 + （可选）入网 Headscale ---
TS_BIN="$(command -v tailscale || echo /Applications/Tailscale.app/Contents/MacOS/Tailscale)"
if [[ -n "${LOGIN_SERVER:-}" && -n "${AUTHKEY:-}" ]]; then
  echo "入网 Headscale: $LOGIN_SERVER ..."
  "$TS_BIN" up --login-server="$LOGIN_SERVER" --authkey="$AUTHKEY" --hostname="mac${MAC_INDEX}" --accept-dns=false
fi
TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -n1 || true)"
[[ -n "${TS_IP}" ]] || { echo "拿不到 Tailscale/Headscale IP，请确认已入网。" >&2; exit 1; }
echo "本机 mesh IP: $TS_IP  (mac${MAC_INDEX})"

# --- 2. 依赖 ---
echo "安装 ttyd tmux ..."
brew install ttyd tmux 2>/dev/null || true

# --- 3. 安装 fleet-agent / filebrowser 二进制 + ttyd 附着脚本 ---
mkdir -p "$BIN_DIR"
ARCH="$(uname -m)"; [[ "$ARCH" == "arm64" ]] && AB="arm64" || AB="amd64"
install -m 0755 "$SCRIPT_DIR/fleet-agent/dist/fleet-agent-darwin-${AB}" "$BIN_DIR/fleet-agent"
install -m 0755 "$SCRIPT_DIR/fleet-agent/fleet-attach.sh" "$BIN_DIR/fleet-attach"
AGENT_BIN="$BIN_DIR/fleet-agent"
FLEET_ATTACH="$BIN_DIR/fleet-attach"

# filebrowser：装官方 release 二进制（Homebrew 的 bottle 缺内嵌前端，/files 会空白）。
# 优先级：FILEBROWSER_BIN 指定本地二进制 > 官方 release 下载(校验 sha256) > brew 兜底。
FB_BIN="$BIN_DIR/filebrowser"
FB_VER="${FB_VER:-v2.63.16}"                       # 想换版本：导出 FB_VER 覆盖
fb_brew_fallback() {
  echo "⚠️ $1，回退 brew（若 /files 空白请用 FILEBROWSER_BIN 指定官方二进制）" >&2
  brew install filebrowser 2>/dev/null || true; FB_BIN="$BREW_PREFIX/bin/filebrowser"
}
if [[ -n "${FILEBROWSER_BIN:-}" && -f "${FILEBROWSER_BIN}" ]]; then
  install -m 0755 "${FILEBROWSER_BIN}" "$FB_BIN"
  xattr -dr com.apple.quarantine "$FB_BIN" 2>/dev/null || true
elif command -v curl >/dev/null 2>&1; then
  echo "下载 filebrowser ${FB_VER}（官方 release，darwin-${AB}）..."
  FB_TMP="$(mktemp -d)"; FB_TGZ="darwin-${AB}-filebrowser.tar.gz"
  FB_REL="https://github.com/filebrowser/filebrowser/releases/download/${FB_VER}"
  if curl -fsSL "$FB_REL/$FB_TGZ" -o "$FB_TMP/$FB_TGZ" \
     && curl -fsSL "$FB_REL/filebrowser_${FB_VER#v}_checksums.txt" -o "$FB_TMP/sums.txt"; then
    WANT="$(grep " ${FB_TGZ}\$" "$FB_TMP/sums.txt" | awk '{print $1}')"
    GOT="$(shasum -a 256 "$FB_TMP/$FB_TGZ" | awk '{print $1}')"
    if [[ -n "$WANT" && "$WANT" == "$GOT" ]]; then
      tar -xzf "$FB_TMP/$FB_TGZ" -C "$FB_TMP" filebrowser
      install -m 0755 "$FB_TMP/filebrowser" "$FB_BIN"
      xattr -dr com.apple.quarantine "$FB_BIN" 2>/dev/null || true
    else
      fb_brew_fallback "filebrowser sha256 校验失败 (want=${WANT:-?} got=$GOT)"
    fi
  else
    fb_brew_fallback "filebrowser 下载失败"
  fi
  rm -rf "$FB_TMP"
else
  fb_brew_fallback "无 curl 可用"
fi

# --- 4. filebrowser DB：建用户 + noauth + baseURL（鉴权交给 Headscale ACL）---
# 重跑场景：先卸载已在运行的服务，否则 filebrowser config set 会因 DB 被占而超时。
LA_EARLY="$HOME/Library/LaunchAgents"
for svc in com.macfleet.ttyd com.macfleet.filebrowser com.macfleet.fleet-agent; do
  launchctl unload "$LA_EARLY/$svc.plist" 2>/dev/null || true
done
if [[ ! -f "$FB_DB" ]]; then
  "$FB_BIN" -d "$FB_DB" config init >/dev/null
fi
# noauth 需要一个已存在的用户来自动登录（否则 /api/login 500）；密码随机、不用于登录
"$FB_BIN" -d "$FB_DB" users add admin "$(openssl rand -base64 12)" --perm.admin >/dev/null 2>&1 || true
"$FB_BIN" -d "$FB_DB" config set --auth.method=noauth --baseURL "$FB_BASE" --root "$FB_ROOT" >/dev/null

# --- 5. 渲染并安装 launchd 服务 ---
LA="$HOME/Library/LaunchAgents"; mkdir -p "$LA"
render() { # src dst
  sed -e "s#__BREW_PREFIX__#${BREW_PREFIX}#g" \
      -e "s#__TS_IP__#${TS_IP}#g" \
      -e "s#__PORT__#${PORT:-}#g" \
      -e "s#__ROOT__#${FB_ROOT}#g" \
      -e "s#__DB__#${FB_DB}#g" \
      -e "s#__TTYD_BASE__#${TTYD_BASE}#g" \
      -e "s#__FB_BASE__#${FB_BASE}#g" \
      -e "s#__FB_BIN__#${FB_BIN}#g" \
      -e "s#__FLEET_ATTACH__#${FLEET_ATTACH}#g" \
      -e "s#__AGENT_BIN__#${AGENT_BIN}#g" \
      -e "s#__AGENT_PORT__#${AGENT_PORT}#g" \
      -e "s#__MAC_INDEX__#${MAC_INDEX}#g" \
      -e "s#__CLAUDE_BIN__#${CLAUDE_BIN}#g" \
      -e "s#__CODEX_BIN__#${CODEX_BIN}#g" \
      "$1" > "$2"
}
PORT="$TTYD_PORT" render "$SCRIPT_DIR/com.macfleet.ttyd.plist"        "$LA/com.macfleet.ttyd.plist"
PORT="$FB_PORT"   render "$SCRIPT_DIR/com.macfleet.filebrowser.plist" "$LA/com.macfleet.filebrowser.plist"
                  render "$SCRIPT_DIR/com.macfleet.fleet-agent.plist" "$LA/com.macfleet.fleet-agent.plist"

for svc in com.macfleet.ttyd com.macfleet.filebrowser com.macfleet.fleet-agent; do
  launchctl unload "$LA/$svc.plist" 2>/dev/null || true
  launchctl load  "$LA/$svc.plist"
  echo "已加载服务: $svc"
done

# fleet-agent 自更新源：写进 ~/.zshrc 受管块，使交互式 `fleet-agent update` 开箱即用
# （update 是手动 CLI，读交互 shell 环境变量，不读 launchd plist）。幂等：先删旧块再追加。
if [[ -n "${FLEET_UPDATE_BASE:-}" ]]; then
  ZRC="$HOME/.zshrc"; MB="# >>> mac-fleet-hub >>>"; ME="# <<< mac-fleet-hub <<<"; touch "$ZRC"
  if grep -qF "$MB" "$ZRC"; then
    tmp="$(mktemp)"; awk -v b="$MB" -v e="$ME" '$0==b{skip=1} !skip{print} $0==e{skip=0}' "$ZRC" > "$tmp" && mv "$tmp" "$ZRC"
  fi
  { echo "$MB"; echo "export FLEET_UPDATE_BASE=\"$FLEET_UPDATE_BASE\""; echo "$ME"; } >> "$ZRC"
  echo "已写入 ~/.zshrc：FLEET_UPDATE_BASE=$FLEET_UPDATE_BASE（新开终端后 'fleet-agent update' 即可用）"
fi

cat <<EOF

✅ 完成（mac${MAC_INDEX}，仅 mesh 内网可达）：
   网页终端    http://${TS_IP}:${TTYD_PORT}${TTYD_BASE}   (经 fleet-agent 选会话)
   文件管理    http://${TS_IP}:${FB_PORT}${FB_BASE}        (整个 home, noauth)
   会话服务    http://${TS_IP}:${AGENT_PORT}/api/health

下一步（在网关）：把本机 mesh IP ${TS_IP} 按顺序填到 server/.env 的 MAC_IPS（第 ${MAC_INDEX} 台 = 第 ${MAC_INDEX} 个），再跑 setup-server.sh。
提醒：mac↔mac 的 SSH/VNC 需你自行在「系统设置 > 通用 > 共享」开启（本脚本不动这些开关）。
日志：/tmp/macfleet-ttyd.* /tmp/macfleet-filebrowser.* /tmp/macfleet-agent.*
EOF
