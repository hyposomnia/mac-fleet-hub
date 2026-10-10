#!/usr/bin/env bash
# R4 回归测试：shared app-server 监督包装必须
#   1. 正常路径：就绪探针通过 → 写 ok 状态、清空失败计数；
#   2. 失败路径：连续失败到上限 → 熔断（以 0 退出，launchd 不再拉起）+ 摘除 GUI 域变量 + 有界日志。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LAUNCHER="$ROOT/mac/codex-keeper-launch.sh"
RESOLVER="$ROOT/mac/codex-bin-resolve.sh"

fail() {
  echo "codex-keeper-launch test failed: $*" >&2
  exit 1
}

tmp="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/macfleet-keeper.XXXXXX")" && pwd -P)"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/bin"
# 假 launchctl：记录调用，getenv 从 store 读（unsetenv 会删掉对应行）。
cat > "$tmp/bin/launchctl" <<'EOF'
#!/bin/bash
echo "launchctl $*" >> "${FAKE_CTL_LOG:-/dev/null}"
cmd="${1:-}"; shift || true
case "$cmd" in
  getenv) [[ -f "${FAKE_CTL_STORE:-/dev/null}" ]] && sed -n "s/^$1=//p" "$FAKE_CTL_STORE" | head -1 ;;
  unsetenv) if [[ -f "${FAKE_CTL_STORE:-/dev/null}" ]]; then grep -v "^$1=" "$FAKE_CTL_STORE" > "$FAKE_CTL_STORE.tmp" || true; mv "$FAKE_CTL_STORE.tmp" "$FAKE_CTL_STORE"; fi ;;
  setenv) printf '%s=%s\n' "$1" "$2" >> "${FAKE_CTL_STORE:-/dev/null}" ;;
esac
exit 0
EOF
# 假 curl：ready 标记文件存在即视为 /readyz 通过。
cat > "$tmp/bin/curl" <<'EOF'
#!/bin/bash
[[ -f "${FAKE_READY_FLAG:-/nonexistent}" ]]
EOF
chmod +x "$tmp/bin/launchctl" "$tmp/bin/curl"

export FAKE_CTL_LOG="$tmp/ctl.log"
export FAKE_CTL_STORE="$tmp/ctl.store"
export FAKE_READY_FLAG="$tmp/ready.flag"
: > "$FAKE_CTL_LOG"
: > "$FAKE_CTL_STORE"

common_env=(
  "PATH=$tmp/bin:$PATH"
  "FLEET_CODEX_RESOLVER=$RESOLVER"
  "FLEET_CODEX_BIN="
  "FLEET_CODEX_KEEPER_NODE=/bin/bash"
  "FLEET_CODEX_KEEPER_LOG=$tmp/logs/codex-app-server.log"
  "FLEET_CODEX_APPSERVER_LISTEN=ws://127.0.0.1:47999"
  "FLEET_LOG_DIR=$tmp/logs"
  "FLEET_STATE_DIR=$tmp/state"
)

# ---------------- 场景 1：正常就绪 ----------------
good_app="$tmp/good.app"
mkdir -p "$good_app/Contents/Resources/codex-cli/bin"
printf '#!/bin/bash\necho codex-cli 9.9.9\n' > "$good_app/Contents/Resources/codex-cli/bin/codex"
chmod +x "$good_app/Contents/Resources/codex-cli/bin/codex"
printf '{"layoutVersion":1,"entrypoint":"bin/codex"}\n' > "$good_app/Contents/Resources/codex-cli/codex-package.json"
cat > "$tmp/keeper-ok.sh" <<EOF
#!/bin/bash
touch "$FAKE_READY_FLAG"
sleep 30
EOF
chmod +x "$tmp/keeper-ok.sh"

env "${common_env[@]}" \
  FLEET_CODEX_DESKTOP_APP_PATH="$good_app" \
  FLEET_CODEX_KEEPER_SCRIPT="$tmp/keeper-ok.sh" \
  FLEET_CODEX_KEEPER_MAX_ATTEMPTS=2 \
  FLEET_CODEX_KEEPER_BACKOFF="0 0 0" \
  bash "$LAUNCHER" > "$tmp/ok.out" 2>&1 &
launcher_pid=$!
for _ in $(seq 1 20); do
  [[ -f "$tmp/state/app-server.json" ]] && grep -q '"state":"ok"' "$tmp/state/app-server.json" && break
  sleep 0.5
done
grep -q '"state":"ok"' "$tmp/state/app-server.json" \
  || fail "正常路径未写出 ok 状态：$(cat "$tmp/state/app-server.json" 2>/dev/null || echo 缺失)"
grep -q "已就绪" "$tmp/logs/codex-app-server.log" || fail "正常路径缺少就绪日志"
[[ ! -f "$tmp/state/app-server-attempts" ]] || fail "成功后就绪计数文件应被清空"
kill -TERM "$launcher_pid" 2>/dev/null || true
wait "$launcher_pid" 2>/dev/null || true
grep -q '"state":"ok"' "$tmp/state/app-server.json" || fail "停止后状态不应被改成失败"

# ---------------- 场景 2：连续失败 → 熔断 + fail-open ----------------
rm -f "$tmp/state/app-server.json" "$tmp/state/app-server-attempts" "$tmp/logs/codex-app-server.log"
: > "$FAKE_CTL_STORE"
printf 'CODEX_APP_SERVER_WS_URL=ws://127.0.0.1:47999/rpc\n' > "$FAKE_CTL_STORE"
: > "$FAKE_CTL_LOG"
cat > "$tmp/bad-resolver.sh" <<'EOF'
#!/bin/bash
[[ "${1:-}" == "--explain" ]] && { echo "模拟：无法解析 Codex 可执行文件"; exit 1; }
exit 1
EOF
chmod +x "$tmp/bad-resolver.sh"
cat > "$tmp/keeper-forever.sh" <<'EOF'
#!/bin/bash
sleep 30
EOF
chmod +x "$tmp/keeper-forever.sh"

set +e
env "${common_env[@]}" \
  FLEET_CODEX_RESOLVER="$tmp/bad-resolver.sh" \
  FLEET_CODEX_KEEPER_SCRIPT="$tmp/keeper-forever.sh" \
  FLEET_CODEX_KEEPER_MAX_ATTEMPTS=2 \
  FLEET_CODEX_KEEPER_BACKOFF="0 0 0" \
  bash "$LAUNCHER" > "$tmp/circuit.out" 2>&1
circuit_rc=$?
set -e
[[ "$circuit_rc" -eq 0 ]] || fail "熔断必须以 0 退出（否则 launchd 会继续拉起），实际 rc=$circuit_rc"
grep -q '"state":"failed"' "$tmp/state/app-server.json" || fail "熔断未写出 failed 状态"
grep -q '"clearedDesktopEnv":1' "$tmp/state/app-server.json" || fail "熔断未记录 GUI 域变量已被摘除"
grep -q "launchctl unsetenv CODEX_APP_SERVER_WS_URL" "$FAKE_CTL_LOG" || fail "熔断未摘除 GUI 域 CODEX_APP_SERVER_WS_URL"
grep -q "已熔断" "$tmp/logs/codex-app-server.log" || fail "熔断日志缺失"
[[ ! -f "$tmp/state/app-server-attempts" ]] || fail "熔断后计数文件应被清空"
# 日志必须带时间戳且有界（不再出现 3.2 MB 无时间戳同一行）
log_lines="$(wc -l < "$tmp/logs/codex-app-server.log" | tr -d ' ')"
(( log_lines < 40 )) || fail "熔断日志行数异常（${log_lines}），疑似无限重试"
head -1 "$tmp/logs/codex-app-server.log" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' \
  || fail "日志缺少时间戳前缀"

# ---------------- 场景 3：keeper 配置缺失 → 立即失败态（不做无意义重试） ----------------
rm -f "$tmp/state/app-server.json"
set +e
env "${common_env[@]}" \
  FLEET_CODEX_KEEPER_NODE="" \
  FLEET_CODEX_KEEPER_SCRIPT="$tmp/keeper-forever.sh" \
  FLEET_CODEX_KEEPER_MAX_ATTEMPTS=4 \
  FLEET_CODEX_KEEPER_BACKOFF="0 0 0" \
  bash "$LAUNCHER" > "$tmp/nokeeper.out" 2>&1
nokeeper_rc=$?
set -e
[[ "$nokeeper_rc" -eq 0 ]] || fail "配置性错误应以 0 退出，实际 rc=$nokeeper_rc"
grep -q '"state":"failed"' "$tmp/state/app-server.json" || fail "keeper 缺失未写 failed 状态"
[[ ! -f "$tmp/state/app-server-attempts" ]] || fail "keeper 缺失不应累计重试计数"

echo "codex-keeper-launch tests passed"
