#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/mac/check-fleet-update-safe.sh"
tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-update-guard.XXXXXX")"
trap 'python3 -c "import shutil,sys; shutil.rmtree(sys.argv[1])" "$tmpdir"' EXIT
mkdir -p "$tmpdir/thread-writer-locks" "$tmpdir/sessions/2026/09/29" "$tmpdir/bin"
cat > "$tmpdir/bin/curl" <<'MOCK'
#!/usr/bin/env bash
if [[ "${FAKE_CURL_FAIL:-0}" == 1 ]]; then exit 22; fi
printf '{"turnPhase":"%s","turnOwner":"%s","items":[]}\n' "${FAKE_TURN_PHASE:-running}" "${FAKE_TURN_OWNER:-desktop}"
MOCK
chmod 700 "$tmpdir/bin/curl"
export PATH="$tmpdir/bin:$PATH" FLEET_CODEX_HOME="$tmpdir" FLEET_CHAT_QUEUE_FILE="$tmpdir/queue.json" FLEET_AGENT_URL=http://127.0.0.1:7682
printf '{"items":[]}\n' > "$FLEET_CHAT_QUEUE_FILE"
sid='01a00000-0000-7000-8000-000000000001'
lock="$tmpdir/thread-writer-locks/$sid.lock"
rollout="$tmpdir/sessions/2026/09/29/rollout-$sid.jsonl"
exec 9>"$lock"
printf '%s\n' '{"type":"event_msg","payload":{"type":"task_started"}}' > "$rollout"
bash "$CHECK" | grep -qx fleet_update_safe

set +e
FAKE_TURN_OWNER=fleet bash "$CHECK" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == 75 ]] || { echo "Fleet-owned turn exit=$code want 75" >&2; exit 1; }

printf '{"items":[{"status":"sending"}]}\n' > "$FLEET_CHAT_QUEUE_FILE"
set +e
bash "$CHECK" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == 75 ]] || { echo "queue sending exit=$code want 75" >&2; exit 1; }
printf '{"items":[]}\n' > "$FLEET_CHAT_QUEUE_FILE"

set +e
FAKE_CURL_FAIL=1 bash "$CHECK" >/dev/null 2>&1
code=$?
set -e
[[ "$code" == 75 ]] || { echo "agent unavailable exit=$code want 75" >&2; exit 1; }

printf '%s\n' '{"type":"event_msg","payload":{"type":"task_complete"}}' >> "$rollout"
FAKE_CURL_FAIL=1 bash "$CHECK" | grep -qx fleet_update_safe

echo "check-fleet-update-safe tests passed"
