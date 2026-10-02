#!/bin/sh
# mac/codex-desktop-env.sh —— 把 shared app-server 地址写进 GUI 域（风险点 R1/R5）。
#
# 关键约束（2026-09-30 事故）：GUI 域变量会被此后每一个 GUI 进程继承，一旦指向一个
# 没人监听的端口，ChatGPT 启动就会直接失败（connect ECONNREFUSED 127.0.0.1:47682）。
# 因此这里坚持 fail-open：
#   1. 注入前先探活 http://127.0.0.1:<port>/readyz（登录时最多等 FLEET_DESKTOP_ENV_WAIT_SEC 秒，
#      避免 app-server 还在启动就被判失败）；
#   2. 探活失败 → **不注入**，并主动清除残留变量，让桌面端回退到自带 Codex。
set -eu

mode=${1:-clear}
endpoint=${2:-}

readyz_url() {
  # 从 ws://127.0.0.1:<port>[/path] 提取端口，拼出就绪探针地址。
  port=$(printf '%s' "$1" | sed -n 's#^ws://\(127\.0\.0\.1\|localhost\|\[::1\]\):\([0-9]\{1,5\}\).*#\2#p')
  [ -n "$port" ] || return 1
  printf 'http://127.0.0.1:%s/readyz' "$port"
}

endpoint_ready_once() {
  url=$(readyz_url "$1") || return 1
  command -v curl >/dev/null 2>&1 || return 1
  curl -fsS --max-time 1 -o /dev/null "$url" >/dev/null 2>&1
}

endpoint_ready() {
  # FLEET_DESKTOP_ENV_PROBE=0 可显式关闭探活（测试/离线排障用）。
  [ "${FLEET_DESKTOP_ENV_PROBE:-1}" = "1" ] || return 0
  if endpoint_ready_once "$1"; then
    return 0
  fi
  wait_seconds=${FLEET_DESKTOP_ENV_WAIT_SEC:-30}
  i=0
  while [ "$i" -lt "$wait_seconds" ]; do
    sleep 1
    i=$((i + 1))
    if endpoint_ready_once "$1"; then
      return 0
    fi
  done
  return 1
}

case "$mode" in
  shared)
    case "$endpoint" in
      ws://127.0.0.1:*|ws://localhost:*|ws://\[::1\]:*) ;;
      *) echo "invalid Codex Desktop shared endpoint: $endpoint" >&2; exit 64 ;;
    esac
    /bin/launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 2>/dev/null || true
    if endpoint_ready "$endpoint"; then
      echo "shared app-server 已就绪，注入 CODEX_APP_SERVER_WS_URL=${endpoint}"
      exec /bin/launchctl setenv CODEX_APP_SERVER_WS_URL "$endpoint"
    fi
    echo "shared app-server 未就绪（${endpoint}）；按 fail-open 跳过注入并清除残留变量，桌面端将回退自带 Codex。" >&2
    exec /bin/launchctl unsetenv CODEX_APP_SERVER_WS_URL
    ;;
  clear)
    /bin/launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 2>/dev/null || true
    exec /bin/launchctl unsetenv CODEX_APP_SERVER_WS_URL
    ;;
  *)
    echo "usage: codex-desktop-env.sh shared <ws-url> | clear" >&2
    exit 64
    ;;
esac
