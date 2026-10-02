#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/tailscale-utils.sh"
: "${LOGIN_SERVER:?}" "${MAC_INDEX:?}" "${FLEET_AUTHKEY_FILE:?}"
[[ "$MAC_INDEX" =~ ^[1-9][0-9]*$ ]] || { echo '设备编号不合法。' >&2; exit 1; }
[[ -f "$FLEET_AUTHKEY_FILE" && ! -L "$FLEET_AUTHKEY_FILE" ]] || { echo '缺少私有入网密钥文件。' >&2; exit 1; }
TS_BIN="$(command -v tailscale)"
TARGET="$(fleet_normalize_control_url "$LOGIN_SERVER")"
JOIN_ARGS=(up --force-reauth)
if fleet_tailscale_connected "$TS_BIN"; then
  CURRENT="$(fleet_normalize_control_url "$(fleet_tailscale_control_url "$TS_BIN")")"
  if [[ "$CURRENT" != "$TARGET" ]]; then
    if [[ "${FLEET_REPLACE_TAILNET:-false}" != true ]]; then
      echo '当前连接其他控制面；不会自动切换。确认后使用 login --replace-tailnet。' >&2
      exit 1
    fi
    echo '将切换到新 Tailscale 配置，保留旧配置供 switch 回滚，需要 sudo 密码。'
    JOIN_ARGS=(login)
  fi
fi
echo '将使用本次账号授权重新认证 mesh，需要 sudo 密码。'
sudo "$TS_BIN" "${JOIN_ARGS[@]}" --login-server="$LOGIN_SERVER" \
  --auth-key="file:$FLEET_AUTHKEY_FILE" --hostname="mac${MAC_INDEX}" --accept-dns=false
