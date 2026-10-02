#!/usr/bin/env bash
set -euo pipefail
ORIGIN="${FLEET_ORIGIN:-}"
if [[ -z "$ORIGIN" ]]; then read -r -p '服务网页地址（例如 https://fleet.example.com）> ' ORIGIN < /dev/tty; fi
[[ "$ORIGIN" =~ ^https://[^/]+/?$ ]] || { echo '请输入 HTTPS 服务网页地址。' >&2; exit 1; }
ORIGIN="${ORIGIN%/}"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
echo '下载客户端安装包；登录与设备确认将在浏览器中进行。'
curl --proto '=https' --tlsv1.2 -fsS --max-time 300 "$ORIGIN/enroll/mac-bundle.tar.gz" -o "$WORK/mac-bundle.tar.gz"
tar xzf "$WORK/mac-bundle.tar.gz" -C "$WORK"
[[ -f "$WORK/mac/install.sh" ]] || { echo '客户端包不完整。' >&2; exit 1; }
FLEET_ORIGIN="$ORIGIN" bash "$WORK/mac/install.sh"
