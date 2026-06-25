#!/usr/bin/env bash
# mac-fleet-hub 自助安装（免 clone）。在新 Mac 上一行运行：
#   curl -fsSL https://<网关域名:端口>/enroll/bootstrap.sh | bash
#
# 流程：问域名/第几台/验证码 → 经网关 TOTP 校验换取一次性入网密钥 → 下载安装包 → 安装并入网。
set -euo pipefail

bold() { printf "\033[1m%s\033[0m\n" "$1"; }
bold "== mac-fleet-hub 自助安装 =="

# 允许通过环境变量预填（curl|bash 时也可：DOMAIN=.. IDX=.. CODE=.. bash <(curl ...)）
DEF_DOMAIN="${DOMAIN:-fleet.example.com}"
read -r -p "网关地址[:端口]（默认 ${DEF_DOMAIN}）> " IN_DOMAIN < /dev/tty || true
DOMAIN="${IN_DOMAIN:-$DEF_DOMAIN}"

IDX="${IDX:-}"
while ! [[ "$IDX" =~ ^[1-9]$ ]]; do
  read -r -p "这是第几台 Mac (1-9) > " IDX < /dev/tty
done

CODE="${CODE:-}"
while ! [[ "$CODE" =~ ^[0-9]{6}$ ]]; do
  read -r -p "Authenticator 入网验证码（6 位）> " CODE < /dev/tty
done

BASE="https://${DOMAIN}/enroll"
echo "→ 校验验证码并申请一次性入网密钥…"
# 不用 -f：让 4xx/5xx 的响应体（含 {"error":...}）也能读到，给出具体提示。
# 把 HTTP 状态码追加在末行，再拆分。
OUT="$(curl -sS -w $'\n%{http_code}' -X POST "${BASE}/join" -H 'content-type: application/json' \
  -d "{\"code\":\"${CODE}\",\"index\":\"${IDX}\"}")" || { echo "✗ 网络不通，请检查域名/端口"; exit 1; }
HTTP="${OUT##*$'\n'}"
RESP="${OUT%$'\n'*}"
if [ "$HTTP" != "200" ]; then
  ERR="$(printf '%s' "$RESP" | sed -n 's/.*"error":"\([^"]*\)".*/\1/p')"
  echo "✗ ${ERR:-请求被拒绝 (HTTP ${HTTP})}"; exit 1
fi

AUTHKEY="$(printf '%s' "$RESP" | sed -n 's/.*"authKey":"\([^"]*\)".*/\1/p')"
LOGIN="$(printf '%s' "$RESP" | sed -n 's/.*"loginServer":"\([^"]*\)".*/\1/p')"
[ -n "$AUTHKEY" ] || { echo "✗ 未取得入网密钥"; exit 1; }
echo "✓ 验证通过，已获一次性入网密钥（mac${IDX}）"

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
echo "→ 下载安装包…"
curl -fsSL "${BASE}/mac-bundle.tar.gz" | tar xz -C "$TMP"
[ -f "$TMP/mac/install.sh" ] || { echo "✗ 安装包损坏"; exit 1; }

echo "→ 开始安装（接下来会提示输入本机 sudo 密码，用于装 tailscaled 与入网）"
# 自更新源 = 本网关的 /enroll/dist（agent `update` 直接从这拉新二进制，用户零配置）。
MAC_INDEX="$IDX" AUTHKEY="$AUTHKEY" LOGIN_SERVER="$LOGIN" \
  FLEET_UPDATE_BASE="https://${DOMAIN}/enroll/dist" \
  bash "$TMP/mac/install.sh"
