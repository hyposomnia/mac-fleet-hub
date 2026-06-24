#!/bin/bash
# ttyd 的启动命令（配合 ttyd --url-arg）：把浏览器 ?arg=<tmux会话名> 传进来，
# 校验后 attach 到对应 tmux 会话。会话由 fleet-agent 的 /api/open|/api/new 预先创建。
#
# 安全：只允许 ^fleet-[a-z0-9]+$ 的会话名，避免经 url-arg 注入任意 tmux/命令。
set -euo pipefail
TMUX_BIN="$(command -v tmux || echo /opt/homebrew/bin/tmux)"
name="${1:-}"

if [[ ! "$name" =~ ^fleet-[a-z0-9]+$ ]]; then
  echo "mac-fleet-hub: 缺少/非法会话参数，请从控制台打开一个会话。"
  sleep 5
  exit 0
fi
if ! "$TMUX_BIN" has-session -t "$name" 2>/dev/null; then
  echo "mac-fleet-hub: 会话 $name 不存在或已回收，请回控制台重新打开。"
  sleep 5
  exit 0
fi
# -u：强制 tmux 客户端按 UTF-8 处理，避免 launchd 缺省 locale 下吃掉中文输入。
exec "$TMUX_BIN" -u attach -t "$name"
