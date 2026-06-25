#!/usr/bin/env bash
# mac-fleet-hub —— Mac 端一键安装。
#
# 把全过程串起来：装 Tailscale → 起系统守护进程 → 入网 Headscale（这几步需 sudo，会提示输密码）
#                 → 装 ttyd/tmux/filebrowser/fleet-agent + 起 3 个常驻服务（无需 sudo）。
#
# 用法（任选其一）：
#   bash mac/install.sh                         # 全程交互询问
#   MAC_INDEX=2 AUTHKEY=hskey-... bash mac/install.sh
#   bash mac/install.sh 2 hskey-...             # 位置参数：MAC_INDEX AUTHKEY
#
# 不修改系统「远程登录/屏幕共享」开关（mac↔mac 的 SSH/VNC 请自行在系统设置开启）。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOGIN_SERVER="${LOGIN_SERVER:-https://fleet.example.com:8443}"   # Headscale 控制面；请改成你的网关地址（默认监听 8443；ISP 封 443 用高位端口时改成你的对外端口，如 :28443。也可经 enroll/bootstrap.sh 自动注入）
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
while ! [[ "${MAC_INDEX}" =~ ^[1-9]$ ]]; do
  read -r -p "这台是第几台 Mac？(1/2/3 …) > " MAC_INDEX
done
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
if "$TS_BIN" status >/dev/null 2>&1 && "$TS_BIN" ip -4 >/dev/null 2>&1; then
  echo "[3/4] 已在 mesh 中（$("$TS_BIN" ip -4 | head -n1)），跳过入网"
else
  bold "[3/4] 入网 Headscale（需要 sudo 密码）"
  sudo "$TS_BIN" up --login-server="$LOGIN_SERVER" --authkey="$AUTHKEY" --hostname="mac${MAC_INDEX}" --accept-dns=false
  sleep 3
fi
TS_IP="$("$TS_BIN" ip -4 2>/dev/null | head -n1 || true)"
[[ -n "$TS_IP" ]] || { echo "入网失败：拿不到 mesh IP。检查 AUTHKEY / 网络后重试。" >&2; exit 1; }

# --- 5. 配服务（无需 sudo）---
bold "[4/4] 安装服务（ttyd / filebrowser / fleet-agent）"
MAC_INDEX="$MAC_INDEX" bash "$SCRIPT_DIR/setup-mac.sh"

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
