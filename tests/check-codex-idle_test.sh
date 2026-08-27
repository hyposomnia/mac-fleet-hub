#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/mac/check-codex-idle.sh"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-codex-idle.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT
mkdir -p "$tmpdir/thread-writer-locks" "$tmpdir/sessions/2026/08/27"
session_id="01a00000-0000-7000-8000-000000000001"
lock="$tmpdir/thread-writer-locks/$session_id.lock"
rollout="$tmpdir/sessions/2026/08/27/rollout-$session_id.jsonl"

exec 9>"$lock"
printf '%s\n' \
  '{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}' \
  > "$rollout"
set +e
FLEET_CODEX_HOME="$tmpdir" bash "$CHECK" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == "75" ]] || { echo "active turn exit=$code want 75" >&2; exit 1; }

printf '%s\n' \
  '{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}' \
  >> "$rollout"
FLEET_CODEX_HOME="$tmpdir" bash "$CHECK" | grep -qx codex_turns_idle

printf '%s\n' \
  '{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-2"}}' \
  '{"type":"event_msg","payload":{"type":"turn_aborted","turn_id":"turn-2"}}' \
  >> "$rollout"
FLEET_CODEX_HOME="$tmpdir" bash "$CHECK" | grep -qx codex_turns_idle

echo "check-codex-idle tests passed"
