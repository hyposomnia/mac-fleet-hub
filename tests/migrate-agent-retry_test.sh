#!/usr/bin/env bash
# 回归守卫：mac/migrate-existing-client-to-shared.sh 的 agent API 重试助手。
#
# 背景：本脚本在 UAT 前刚停掉旧 app-server 并重开 Desktop，fleet-agent 的 WS 客户端
# 正在这个窗口里重建，此时 agent 对 chat/sessions 类接口会回 503
# （appserver_unavailable / appserver_recovered / agent_restarting），都是稍后重试
# 即可的瞬时态。2026-09-16 发布时，其中一个探针（/api/sessions）既没重试也没吞
# stderr，在 `set -e` 下命令替换赋值失败**静默退出**——日志里只剩一行
# `curl: (22) The requested URL returned error: 503`，脚本整轮重试、每次再杀一遍
# Desktop，永远收敛不了。
#
# 本测试从真实脚本里抽出该助手（按函数名到行首 `}`），配桩 curl 验证：
# 一次成功 / 503 后转好 / 真错误立刻停 / 连不上重试到上限 / 超时可区分 / 超时值透传。
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
ROOT="$(pwd)"
SCRIPT="$ROOT/mac/migrate-existing-client-to-shared.sh"

[[ -f "$SCRIPT" ]] || { echo "缺少 $SCRIPT" >&2; exit 1; }

TMP="$(mktemp -d)"
cleanup() { rm -rf -- "$TMP"; }
trap cleanup EXIT
mkdir -p "$TMP/bin"

# 抽出助手本体：从 `agent_api_retry() {` 到下一个行首 `}`。
sed -n '/^agent_api_retry() {/,/^}/p' "$SCRIPT" > "$TMP/helper.sh"
[[ -s "$TMP/helper.sh" ]] || { echo "没能从 $SCRIPT 抽出 agent_api_retry（函数被改名或改写？）" >&2; exit 1; }
tail -n1 "$TMP/helper.sh" | grep -qx '}' \
  || { echo "抽出的助手没有正常结束（行首 } 缺失）" >&2; exit 1; }

cat > "$TMP/bin/curl" <<'CURL_STUB'
#!/usr/bin/env bash
# 桩 curl：把参数记到 ${ARGS_LOG}，按 ${MODE} 按次序吐「响应体\nHTTP码」；次数记在 ${COUNTER}。
printf '%s\n' "$*" >> "$ARGS_LOG"
n=$(cat "$COUNTER" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$COUNTER"
case "$MODE" in
  ok)         printf '{"skills":[]}\n200' ;;
  503then200) if [[ $n -le 1 ]]; then printf '{"error":{"code":"appserver_recovered"}}\n503'; else printf '{"skills":[]}\n200'; fi ;;
  always503)  printf '{"error":{"code":"appserver_recovered"}}\n503' ;;
  400)        printf '{"error":{"code":"bad_request"}}\n400' ;;
  noreply)    exit 7 ;;
  timeout)    exit 28 ;;
esac
CURL_STUB
chmod +x "$TMP/bin/curl"

cat > "$TMP/runner.sh" <<'RUNNER'
#!/usr/bin/env bash
set -euo pipefail
RUNNER
cat "$TMP/helper.sh" >> "$TMP/runner.sh"
printf 'agent_api_retry "$1" "$2" "http://127.0.0.1:7682/api/chat/skills"\n' >> "$TMP/runner.sh"

fails=0
check() { # mode attempts timeout want_rc want_calls want_stderr label
  local mode="$1" attempts="$2" timeout="$3" want_rc="$4" want_calls="$5" want_stderr="$6" label="$7"
  : > "$TMP/args.log"
  echo 0 > "$TMP/counter"
  local rc=0 out
  if out="$(COUNTER="$TMP/counter" ARGS_LOG="$TMP/args.log" MODE="$mode" PATH="$TMP/bin:$PATH" \
      /bin/bash "$TMP/runner.sh" "$attempts" "$timeout" 2>&1)"; then rc=0; else rc=$?; fi
  local calls; calls="$(cat "$TMP/counter")"
  if [[ "$rc" == "$want_rc" && "$calls" == "$want_calls" ]] \
     && { [[ -z "$want_stderr" ]] || grep -q "$want_stderr" <<<"$out"; }; then
    echo "✔ ${label}（exit=${rc} 调用=${calls}）"
  else
    echo "✗ ${label}：期望 exit=${want_rc} 调用=${want_calls} 含'${want_stderr}'，实得 exit=${rc} 调用=${calls}" >&2
    echo "$out" | sed 's/^/    /' >&2
    fails=1
  fi
}

# 一次成功：不重试。
check ok         3 30 0 1 "" "200 直接通过"
# 关键回归：503 是瞬时态，必须重试到成功，而不是判死。
check 503then200 3 30 0 2 "" "503 后转 200 自愈"
# 真错误不空转重试。
check 400        3 30 1 1 "http=400" "400 立刻停且带码与响应体"
# 连不上（curl 非零退出、无输出）同样按瞬时态重试，直到上限。
check noreply    2 30 1 2 "curl_rc=7" "连不上重试到上限后失败"
# 一直 503：重试到上限，并把 503 与响应体打出来供排障。
check always503  2 30 1 2 "http=503" "一直 503 重试到上限"
# 超时必须与「连接被拒」可区分：两者都是 http=000，只有 curl_rc 分得开。
# （2026-09-16 探针超时被误判成 agent 连不上，就是缺这个 rc。）
check timeout    2 30 1 2 "curl_rc=28" "超时给 rc=28 而非无响应"

# 超时值必须真透传给 curl：统一用死一个过小的 --max-time 会把 chat/resume
# （实测 17.6s）每次都切断，且表现为 000，极难定位。
echo 0 > "$TMP/counter"; : > "$TMP/args.log"
COUNTER="$TMP/counter" ARGS_LOG="$TMP/args.log" MODE=ok PATH="$TMP/bin:$PATH" \
  /bin/bash "$TMP/runner.sh" 3 17 >/dev/null 2>&1 || true
if grep -q -- '--max-time 17' "$TMP/args.log"; then
  echo "✔ 单次超时按参数透传（--max-time 17）"
else
  echo "✗ 单次超时没有透传：$(cat "$TMP/args.log")" >&2
  fails=1
fi

[[ "$fails" == 0 ]] || exit 1
echo "migrate-agent-retry tests passed"
