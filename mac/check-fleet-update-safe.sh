#!/usr/bin/env bash
# An agent-only update restarts fleet-agent, not the shared Codex app-server.
# Permit Desktop-owned turns; reject Fleet-owned turns or in-flight queue work.
set -euo pipefail

codex_home="${FLEET_CODEX_HOME:-$HOME/.codex}"
queue_file="${FLEET_CHAT_QUEUE_FILE:-$HOME/.macfleet/chat-queue.json}"
agent_url="${FLEET_AGENT_URL:-}"
[[ -n "$agent_url" ]] || { echo 'missing FLEET_AGENT_URL' >&2; exit 1; }
command -v jq >/dev/null || { echo 'jq required for update guard' >&2; exit 1; }

# A restart while the queue worker is delivering can make the result uncertain.
if [[ -f "$queue_file" ]]; then
  if ! jq -e '.items | type == "array"' "$queue_file" >/dev/null; then
    echo 'unreadable Fleet chat queue' >&2
    exit 75
  fi
  if jq -e '.items[] | select(.status == "steering" or .status == "taking_over" or .status == "sending" or .status == "recovering" or .status == "takeover_check")' "$queue_file" >/dev/null; then
    echo 'fleet_queue_in_flight' >&2
    exit 75
  fi
fi

shopt -s nullglob
for lock_path in "$codex_home"/thread-writer-locks/*.lock; do
  /usr/sbin/lsof -t -- "$lock_path" >/dev/null 2>&1 || continue
  session_id="${lock_path##*/}"
  session_id="${session_id%.lock}"
  rollout="$(find "$codex_home/sessions" -type f -name "*${session_id}*.jsonl" -print -quit 2>/dev/null || true)"
  if [[ -n "$rollout" ]]; then
    marker="$(tail -n 2000 "$rollout" | grep -Eo '"type":"(task_started|task_complete|turn_aborted)"' | tail -n1 || true)"
    if [[ "$marker" == '"type":"task_complete"' || "$marker" == '"type":"turn_aborted"' ]]; then
      continue
    fi
  fi
  snapshot="$(curl -fsS --max-time 5 --get --data-urlencode 'assistant=codex' --data-urlencode "sessionId=$session_id" "$agent_url/api/chat/queue")" || {
    echo "fleet_turn_unknown session=$session_id (agent unavailable)" >&2
    exit 75
  }
  if ! printf '%s' "$snapshot" | jq -e '(.turnPhase == "idle" and ((.turnOwner // "") != "fleet")) or (.turnPhase == "running" and .turnOwner == "desktop" and ([.items[]? | select(.status == "steering" or .status == "taking_over" or .status == "sending" or .status == "recovering" or .status == "takeover_check")] | length == 0))' >/dev/null; then
    echo "fleet_turn_active_or_unknown session=$session_id" >&2
    exit 75
  fi
done

echo 'fleet_update_safe'
