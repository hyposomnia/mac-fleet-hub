#!/usr/bin/env bash
# Old launch definitions forward into main's keeper without another supervisor.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LAUNCHER="$ROOT/mac/codex-keeper-launch.sh"
work="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-keeper.XXXXXX")"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin"
printf '#!/bin/bash\nexit 0\n' > "$work/codex"
chmod +x "$work/codex"
cat > "$work/bin/launchctl" <<'EOF'
#!/bin/bash
printf '%s\n' "$*" >> "$TEST_CTL_LOG"
EOF
cat > "$work/resolver.sh" <<'EOF'
#!/bin/bash
case "${1:-}" in
 --keeper-node) printf '%s\n' "$TEST_NODE" ;;
 *) [[ "${TEST_RESOLVE_FAIL:-0}" != 1 ]] || exit 64
    printf '%s\n' "$TEST_CODEX" ;;
esac
EOF
cat > "$work/node" <<'EOF'
#!/bin/bash
printf '%s\n' "$1" "$FLEET_CODEX_BIN" "$FLEET_CODEX_APPSERVER_LISTEN" >> "$TEST_NODE_LOG"
exit "${TEST_KEEPER_EXIT:-0}"
EOF
chmod +x "$work/bin/launchctl" "$work/node" "$work/resolver.sh"
: > "$work/control.log"
common=("PATH=$work/bin:$PATH" "FLEET_CODEX_RESOLVER=$work/resolver.sh"
 "FLEET_CODEX_KEEPER_NODE=$work/node" "FLEET_CODEX_KEEPER_SCRIPT=$ROOT/mac/codex-shared-app-server.mjs"
 "FLEET_CODEX_APPSERVER_LISTEN=ws://127.0.0.1:47999" "FLEET_LOG_DIR=$work/logs" "FLEET_STATE_DIR=$work/state"
 "FLEET_CODEX_KEEPER_MAX_ATTEMPTS=1" "FLEET_CODEX_KEEPER_BACKOFF=0" "FLEET_CODEX_KEEPER_READY_TIMEOUT=1"
 "FLEET_CODEX_KEEPER_LOG=$work/logs/keeper.log"
 "TEST_NODE=$work/node" "TEST_CODEX=$work/codex" "TEST_NODE_LOG=$work/node.log" "TEST_CTL_LOG=$work/control.log")
set +e
env "${common[@]}" TEST_KEEPER_EXIT=37 bash "$LAUNCHER" > "$work/output" 2>&1
code=$?
set -e
[[ "$code" == 37 ]] || { echo "legacy launcher must return keeper exit 37; got $code" >&2; exit 1; }
[[ "$(sed -n '1p' "$work/node.log")" == "$ROOT/mac/codex-shared-app-server.mjs" ]]
[[ "$(sed -n '2p' "$work/node.log")" == "$work/codex" ]]
[[ "$(sed -n '3p' "$work/node.log")" == 'ws://127.0.0.1:47999' ]]
[[ "$(wc -l < "$work/node.log" | tr -d ' ')" == 3 ]]
[[ ! -s "$work/control.log" ]]
rm "$work/node.log"
set +e
env "${common[@]}" TEST_RESOLVE_FAIL=1 bash "$LAUNCHER" > "$work/output" 2>&1
code=$?
set -e
[[ "$code" == 64 ]] || { echo "resolver failure must be returned; got $code" >&2; exit 1; }
[[ ! -f "$work/node.log" && ! -s "$work/control.log" ]]
echo 'codex-keeper-launch compatibility tests passed'
