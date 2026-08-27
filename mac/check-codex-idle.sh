#!/usr/bin/env bash
set -euo pipefail

CODEX_HOME_DIR="${FLEET_CODEX_HOME:-$HOME/.codex}"
LOCK_DIR="$CODEX_HOME_DIR/thread-writer-locks"
SESSION_DIR="$CODEX_HOME_DIR/sessions"
active=0

shopt -s nullglob
for lock_path in "$LOCK_DIR"/*.lock; do
  session_id="${lock_path##*/}"
  session_id="${session_id%.lock}"
  /usr/sbin/lsof -t -- "$lock_path" >/dev/null 2>&1 || continue
  rollout="$(find "$SESSION_DIR" -type f -name "*${session_id}*.jsonl" -print -quit 2>/dev/null || true)"
  if [[ -z "$rollout" ]]; then
    echo "active_or_unknown session=${session_id} reason=missing_rollout" >&2
    active=1
    continue
  fi
  marker="$(tail -n 2000 "$rollout" | grep -Eo '"type":"(task_started|task_complete|turn_aborted)"' | tail -n1 || true)"
  if [[ "$marker" == '"type":"task_started"' || -z "$marker" ]]; then
    echo "active_or_unknown session=${session_id} marker=${marker:-none}" >&2
    active=1
  fi
done

if [[ "$active" == "1" ]]; then
  exit 75
fi
echo "codex_turns_idle"
