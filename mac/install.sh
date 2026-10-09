#!/usr/bin/env bash
# mac-fleet-hub —— Mac 端一键安装。
#
# 把全过程串起来：装 Tailscale → 起系统守护进程 → 入网 Headscale（这几步需 sudo，会提示输密码）
#                 → 装 ttyd/tmux/filebrowser/fleet-agent + 起 3 个常驻服务（无需 sudo）。
#
# 用法（任选其一）：
#   bash mac/install.sh                         # 手动模式：交互询问（服务器地址 / 第几台 / AUTHKEY；正常应走 bootstrap 自动分配编号）
#   LOGIN_SERVER=https://你的网关:8443 MAC_INDEX=2 AUTHKEY=hskey-... bash mac/install.sh
#   bash mac/install.sh 2 hskey-...             # 位置参数：MAC_INDEX AUTHKEY（仍会问服务器地址）
#
# 可选 env：FLEET_UPDATE_BASE=https://<网关>/enroll/dist —— 写进 ~/.zshrc，使 `fleet-agent update`
#   自更新开箱即用（bootstrap.sh 会自动注入；手动装可自带，或指向你 fork 的 dist 目录）。
#
# 不修改系统「远程登录/屏幕共享」开关（mac↔mac 的 SSH/VNC 请自行在系统设置开启）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/tailscale-utils.sh"
LOGIN_SERVER="${LOGIN_SERVER:-}"   # Headscale 控制面（网关）地址；留空则交互询问（bootstrap.sh 会自动注入）
MAC_INDEX="${MAC_INDEX:-${1:-}}"
AUTHKEY="${AUTHKEY:-${2:-}}"

bold() { printf "\033[1m%s\033[0m\n" "$1"; }

bold "== mac-fleet-hub 一键安装 =="

# --- 0. 前置：Homebrew ---
if ! command -v brew >/dev/null 2>&1; then
  echo "未找到 Homebrew。请先安装：https://brew.sh ，再重跑本脚本。" >&2
  exit 1
fi

# --- 1. 收集参数 ---
# MAC_INDEX 通常由 bootstrap.sh 从网关自动分配并经 env 传入（此时不会提问）；
# 仅手动直跑本脚本（无网关协调）才需手填。
while ! [[ "${MAC_INDEX}" =~ ^[1-9][0-9]?$ ]]; do
  read -r -p "这台是第几台 Mac？(通常由 bootstrap 自动分配，手动安装才需填，如 1/2/3 …) > " MAC_INDEX
done
if [[ -z "${LOGIN_SERVER}" ]]; then
  echo "网关的 Headscale 控制面地址（默认监听 8443；网关若用高位端口/ISP 封 443，则填对外端口，如 https://fleet.example.com:28443）。"
  while ! [[ "${LOGIN_SERVER}" =~ ^https?:// ]]; do
    read -r -p "粘贴控制面地址 (https://你的网关:8443) > " LOGIN_SERVER
  done
fi
if [[ -z "${AUTHKEY}" ]]; then
  echo "需要 Headscale 预授权密钥（在网关用 'headscale preauthkeys create -u 1 --reusable --tags tag:fleet-mac' 生成）。"
  read -r -p "粘贴 AUTHKEY (hskey-...) > " AUTHKEY
fi
echo "  目标：mac${MAC_INDEX} · 控制面 ${LOGIN_SERVER}"

# --- 2. Tailscale 客户端 ---
if ! command -v tailscale >/dev/null 2>&1 && [[ ! -x /opt/homebrew/bin/tailscale && ! -x /usr/local/bin/tailscale ]]; then
  bold "[1/4] 安装 Tailscale 客户端"
  brew install tailscale
else
  echo "[1/4] Tailscale 已安装"
fi
TS_BIN="$(command -v tailscale || echo "$(brew --prefix)/bin/tailscale")"

# --- 3. 系统守护进程（需 sudo）---
if ! pgrep -x tailscaled >/dev/null 2>&1; then
  bold "[2/4] 安装并启动 tailscaled 系统守护进程（需要 sudo 密码）"
  sudo "$TS_BIN"d install-system-daemon || sudo tailscaled install-system-daemon
  sleep 2
else
  echo "[2/4] tailscaled 已在运行"
fi

# --- 4. 入网 Headscale（需 sudo）---
TARGET_CONTROL="$(fleet_normalize_control_url "$LOGIN_SERVER")"
JOIN_FLEET=1
if fleet_tailscale_connected "$TS_BIN"; then
  CURRENT_CONTROL="$(fleet_normalize_control_url "$(fleet_tailscale_control_url "$TS_BIN")")"
  CURRENT_HOSTNAME="$(fleet_tailscale_hostname "$TS_BIN")"
  if [[ -n "$CURRENT_CONTROL" && "$CURRENT_CONTROL" == "$TARGET_CONTROL" ]]; then
    JOIN_FLEET=0
    if [[ "$CURRENT_HOSTNAME" =~ ^mac([1-9][0-9]*)$ ]]; then
      EXISTING_INDEX="${BASH_REMATCH[1]}"
      if [[ "$EXISTING_INDEX" != "$MAC_INDEX" ]]; then
        echo "[3/4] 已作为 mac${EXISTING_INDEX} 加入目标 mesh；复用现有编号（忽略新分配的 mac${MAC_INDEX}）"
        MAC_INDEX="$EXISTING_INDEX"
      else
        echo "[3/4] 已在目标 mesh 中（$("$TS_BIN" ip -4 | head -n1)），跳过入网"
      fi
    else
      bold "[3/4] 已在目标 mesh，修正节点名为 mac${MAC_INDEX}（需要 sudo 密码）"
      sudo "$TS_BIN" set --hostname="mac${MAC_INDEX}" --accept-dns=false
    fi
  elif [[ "${FLEET_REPLACE_TAILNET:-0}" == "1" ]]; then
    bold "[3/4] 退出当前 Tailscale 网络并切换到 Fleet mesh（需要 sudo 密码）"
    sudo "$TS_BIN" logout
  else
    echo "检测到当前 Tailscale 控制面不是目标 Headscale：${CURRENT_CONTROL:-无法识别}" >&2
    echo "为避免覆盖现有网络，安装已停止。确认要切换后设置 FLEET_REPLACE_TAILNET=1 重跑。" >&2
    exit 1
  fi
fi
if [[ "$JOIN_FLEET" == "1" ]]; then
  bold "[3/4] 入网 Headscale（需要 sudo 密码）"
  sudo "$TS_BIN" up --login-server="$LOGIN_SERVER" --authkey="$AUTHKEY" --hostname="mac${MAC_INDEX}" --accept-dns=false
  sleep 3
fi
FINAL_CONTROL="$(fleet_normalize_control_url "$(fleet_tailscale_control_url "$TS_BIN")")"
[[ "$FINAL_CONTROL" == "$TARGET_CONTROL" ]] || {
  echo "入网失败：当前控制面 ${FINAL_CONTROL:-无法识别}，目标为 ${TARGET_CONTROL}。" >&2
  exit 1
}
TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -n1 || true)"
[[ -n "$TS_IP" ]] || { echo "入网失败：拿不到 mesh IP。检查 AUTHKEY / 网络后重试。" >&2; exit 1; }

# --- 5. 配服务（无需 sudo）---
bold "[4/4] 安装服务（ttyd / filebrowser / fleet-agent）"
MAC_INDEX="$MAC_INDEX" FLEET_UPDATE_BASE="${FLEET_UPDATE_BASE:-}" bash "$SCRIPT_DIR/setup-mac.sh"

cat <<EOF

🎉 mac${MAC_INDEX} 安装完成，mesh IP = ${TS_IP}

下一步（在网关）把它接进反代：
  ssh <你的网关>                              # 例：ssh -p <ssh端口> youruser@your-gateway
  cd ~/mac-fleet-hub
  # 把本机 mesh IP ${TS_IP} 填到 server/.env 的 MAC_IPS（空格分隔，按 m1 m2 … 顺序；
  # 这是第 ${MAC_INDEX} 台 → 放在第 ${MAC_INDEX} 个位置）
  sudo bash scripts/setup-server.sh        # 重渲染并 reload nginx（幂等）

然后浏览器开 https://<你的子域>/ → 选 Mac ${MAC_INDEX} → 续接会话。
（如需 mac↔mac 的 SSH/VNC，请自行在「系统设置 > 通用 > 共享」开启。）
EOF
