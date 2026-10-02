#!/bin/bash
# mac/codex-keeper-launch.sh —— shared app-server 的启动监督包装（风险点 R4）。
#
# 由 launchd 调用（plist 的 ProgramArguments 指向本脚本）。它替代「让 launchd 直接
# 拉起 keeper」的做法，负责：
#   1. 每次启动都重新解析 Codex 可执行文件（调 codex-bin-resolve.sh）→ App 更新后自愈（R2）
#   2. 就绪探针：启动后轮询 http://127.0.0.1:<port>/readyz，未就绪不算成功
#   3. 退避重试（2s → 10s → 30s）与熔断：连续失败到上限就写入失败态并**成功退出**，
#      配合 plist 的 KeepAlive.SuccessfulExit=false，彻底停止无限崩溃重启（R4）
#   4. 有界、带时间戳的结构化日志与状态文件，让 `fleet-agent doctor` 一眼看到原因（R4/R9）
#
# 关键设计：launchd 的 StandardOut/Err 只在进程启动时打开一次，包装器自己滚动日志
# （LOG_FILE）并把子进程输出重定向进去，避免出现「3.2 MB 全是同一行、无时间戳」的现场。
set -uo pipefail

APP_PATH="${FLEET_CODEX_DESKTOP_APP_PATH:-/Applications/ChatGPT.app}"
RESOLVER="${FLEET_CODEX_RESOLVER:-}"
KEEPER_NODE="${FLEET_CODEX_KEEPER_NODE:-}"
KEEPER_SCRIPT="${FLEET_CODEX_KEEPER_SCRIPT:-}"
LISTEN_URL="${FLEET_CODEX_APPSERVER_LISTEN:-}"
LOG_DIR="${FLEET_LOG_DIR:-$HOME/Library/Logs/macfleet}"
STATE_DIR="${FLEET_STATE_DIR:-$HOME/Library/Application Support/macfleet/state}"
LOG_FILE="${FLEET_CODEX_KEEPER_LOG:-$LOG_DIR/codex-app-server.log}"
STATE_FILE="$STATE_DIR/app-server.json"
ATTEMPT_FILE="$STATE_DIR/app-server-attempts"
LABEL_MAC="${FLEET_CODEX_APPSERVER_LABEL:-com.macfleet.codex-app-server}"
DESKTOP_ENV_VAR="CODEX_APP_SERVER_WS_URL"

MAX_ATTEMPTS="${FLEET_CODEX_KEEPER_MAX_ATTEMPTS:-4}"
READY_TIMEOUT="${FLEET_CODEX_KEEPER_READY_TIMEOUT:-20}"
HEALTHY_SECONDS="${FLEET_CODEX_KEEPER_HEALTHY_SECONDS:-120}"
LOG_MAX_BYTES="${FLEET_CODEX_KEEPER_LOG_MAX_BYTES:-2097152}"
BACKOFF_SECONDS=(2 10 30)
# 测试/特殊场景可覆盖退避序列（空格分隔）。
if [[ -n "${FLEET_CODEX_KEEPER_BACKOFF:-}" ]]; then
  read -r -a BACKOFF_SECONDS <<< "${FLEET_CODEX_KEEPER_BACKOFF}"
fi

child=""
log() { printf '%s %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"; }

json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\t'/\\t}"
  printf '%s' "$s"
}

write_state() { # state last_error cleared_desktop_env
  local state="$1" last_error="$2" cleared="$3"
  mkdir -p "$STATE_DIR" 2>/dev/null || true
  local tmp="$STATE_FILE.$$"
  printf '{"state":"%s","lastError":"%s","updatedAt":"%s","clearedDesktopEnv":%s,"codexBin":"%s","listen":"%s"}\n' \
    "$(json_escape "$state")" "$(json_escape "$last_error")" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$cleared" \
    "$(json_escape "${resolved_bin:-}")" "$(json_escape "$LISTEN_URL")" > "$tmp" 2>/dev/null || return 0
  mv -f "$tmp" "$STATE_FILE" 2>/dev/null || true
}

rotate_log() {
  [[ -f "$LOG_FILE" ]] || return 0
  local size
  size="$(/usr/bin/stat -f '%z' "$LOG_FILE" 2>/dev/null || echo 0)"
  [[ "$size" =~ ^[0-9]+$ ]] || return 0
  if (( size > LOG_MAX_BYTES )); then
    mv -f "$LOG_FILE" "$LOG_FILE.1" 2>/dev/null || true
  fi
}

listen_port() {
  local url="${LISTEN_URL:-}"
  if [[ "$url" =~ :([0-9]{1,5})(/|$) ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
  fi
}

probe_ready() {
  local port
  port="$(listen_port)"
  [[ -n "$port" ]] || return 1
  if command -v curl >/dev/null 2>&1; then
    curl -fsS --max-time 1 -o /dev/null "http://127.0.0.1:${port}/readyz" >/dev/null 2>&1
    return $?
  fi
  /usr/bin/nc -z 127.0.0.1 "$port" >/dev/null 2>&1
}

# fail-open（R5）：shared app-server 起不来时，必须把 GUI 域里指向它的
# CODEX_APP_SERVER_WS_URL 摘掉，否则桌面端会被劫持到一个死端口并直接启动失败。
clear_desktop_env() {
  launchctl unsetenv "$DESKTOP_ENV_VAR" >/dev/null 2>&1 || true
  if [[ "$(launchctl getenv "$DESKTOP_ENV_VAR" 2>/dev/null || true)" == "" ]]; then
    printf '1'
  else
    printf '0'
  fi
}

circuit_open() { # reason
  local reason="$1" cleared
  rm -f "$ATTEMPT_FILE" 2>/dev/null || true
  cleared="$(clear_desktop_env)"
  write_state "failed" "$reason" "$cleared"
  {
    log "已熔断：连续启动失败达到上限（${MAX_ATTEMPTS} 次），停止自动重启。"
    log "原因：${reason}"
    if [[ "$cleared" == "1" ]]; then
      log "已摘除 GUI 域 ${DESKTOP_ENV_VAR}（fail-open）：ChatGPT 将回退到自带 Codex，不再因死端口启动失败。"
    else
      log "警告：未能摘除 GUI 域 ${DESKTOP_ENV_VAR}，ChatGPT 仍可能被指向死端口。"
    fi
    log "修复后手动恢复： launchctl kickstart -k gui/$(id -u)/${LABEL_MAC}"
    log "排查入口： fleet-agent doctor"
  } >> "$LOG_FILE" 2>&1
  # 成功退出：plist 的 KeepAlive 只在非 0 退出时才重启。
  exit 0
}

cleanup() {
  if [[ -n "$child" ]]; then
    kill -TERM "$child" 2>/dev/null || true
  fi
  log "收到停止信号，退出（不触发自动重启）" >> "$LOG_FILE" 2>&1
  exit 0
}
trap cleanup TERM INT HUP

mkdir -p "$LOG_DIR" "$STATE_DIR" 2>/dev/null || true
rotate_log

{
  log "启动监督包装：app=${APP_PATH} listen=${LISTEN_URL:-未配置}"
} >> "$LOG_FILE" 2>&1

if [[ -z "$KEEPER_NODE" || ! -x "$KEEPER_NODE" || -z "$KEEPER_SCRIPT" || ! -f "$KEEPER_SCRIPT" ]]; then
  reason="keeper 不可用（node=${KEEPER_NODE:-未配置} script=${KEEPER_SCRIPT:-未配置}）"
  cleared="$(clear_desktop_env)"
  write_state "failed" "$reason" "$cleared"
  log "$reason" >> "$LOG_FILE" 2>&1
  log "这是配置性错误，重试不会自愈；已停止自动重启。请重跑安装：bash mac/setup-mac.sh" >> "$LOG_FILE" 2>&1
  exit 0
fi

if [[ -z "$RESOLVER" || ! -f "$RESOLVER" ]]; then
  reason="缺少 codex 路径解析器（FLEET_CODEX_RESOLVER=${RESOLVER:-未配置}）"
  cleared="$(clear_desktop_env)"
  write_state "failed" "$reason" "$cleared"
  log "$reason" >> "$LOG_FILE" 2>&1
  exit 0
fi

attempt=0
if [[ -f "$ATTEMPT_FILE" ]]; then
  attempt="$(cat "$ATTEMPT_FILE" 2>/dev/null || echo 0)"
  [[ "$attempt" =~ ^[0-9]+$ ]] || attempt=0
fi

resolved_bin=""
while :; do
  rotate_log

  # 1) 每次启动都重新解析 codex 路径 —— App 自动更新后无需人工介入即可自愈。
  if ! line="$(FLEET_CODEX_DESKTOP_APP_PATH="$APP_PATH" bash "$RESOLVER" 2>/dev/null)"; then
    detail="$(FLEET_CODEX_DESKTOP_APP_PATH="$APP_PATH" bash "$RESOLVER" --explain 2>&1 || true)"
    attempt=$((attempt + 1))
    write_state "failed" "$detail" "false"
    {
      log "启动失败（第 ${attempt}/${MAX_ATTEMPTS} 次）：无法解析 Codex 可执行文件"
      printf '%s\n' "$detail" | sed 's/^/    /'
    } >> "$LOG_FILE" 2>&1
    (( attempt >= MAX_ATTEMPTS )) && circuit_open "无法解析 Codex 可执行文件（App 布局变化或 App 缺失）"
    printf '%s\n' "$attempt" > "$ATTEMPT_FILE" 2>/dev/null || true
    sleep "${BACKOFF_SECONDS[$((attempt - 1))]:-30}"
    continue
  fi
  resolved_bin="${line%%$'\t'*}"
  resolved_source="${line##*$'\t'}"

  # 2) 启动 keeper 并做就绪探针（解析结果显式传给子进程，keeper 不再依赖 plist 里的旧值）
  log "启动 keeper（codex=${resolved_bin} 来源=${resolved_source}）" >> "$LOG_FILE" 2>&1
  started_at="$(date +%s)"
  FLEET_CODEX_BIN="$resolved_bin" FLEET_CODEX_RESOLVER="$RESOLVER" "$KEEPER_NODE" "$KEEPER_SCRIPT" >> "$LOG_FILE" 2>&1 &
  child=$!
  ready=0
  for ((i = 0; i < READY_TIMEOUT; i++)); do
    if ! kill -0 "$child" 2>/dev/null; then
      break
    fi
    if probe_ready; then
      ready=1
      break
    fi
    sleep 1
  done
  if (( ready == 1 )); then
    attempt=0
    rm -f "$ATTEMPT_FILE" 2>/dev/null || true
    write_state "ok" "" "false"
    log "已就绪：${LISTEN_URL}" >> "$LOG_FILE" 2>&1
  else
    log "启动后 ${READY_TIMEOUT}s 内未就绪：${LISTEN_URL:-未配置}" >> "$LOG_FILE" 2>&1
  fi

  wait "$child"
  rc=$?
  child=""
  ran=$(( $(date +%s) - started_at ))
  log "keeper 退出 rc=${rc}（运行 ${ran}s）" >> "$LOG_FILE" 2>&1

  # 正常退出且已就绪 → 直接重启，不计失败（等价于旧的 KeepAlive=true 行为）。
  if (( rc == 0 && ready == 1 )); then
    if (( ran >= HEALTHY_SECONDS )); then
      attempt=0
      rm -f "$ATTEMPT_FILE" 2>/dev/null || true
    fi
    sleep 1
    continue
  fi

  attempt=$((attempt + 1))
  reason="keeper 连续启动失败（第 ${attempt}/${MAX_ATTEMPTS} 次，rc=${rc}，就绪=${ready}）"
  write_state "failed" "$reason" "false"
  (( attempt >= MAX_ATTEMPTS )) && circuit_open "$reason"
  printf '%s\n' "$attempt" > "$ATTEMPT_FILE" 2>/dev/null || true
  log "退避 ${BACKOFF_SECONDS[$((attempt - 1))]:-30}s 后重试" >> "$LOG_FILE" 2>&1
  sleep "${BACKOFF_SECONDS[$((attempt - 1))]:-30}"
done
