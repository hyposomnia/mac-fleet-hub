#!/bin/bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="${2:-$SCRIPT_DIR/codex-desktop.json}"
# clear before first installation has no owned environment to restore.
if [[ "${1:-clear}" == clear && ! -e "$CONFIG" ]]; then exit 0; fi
source "$SCRIPT_DIR/codex-bin-resolve.sh"
export FLEET_CODEX_DESKTOP_APP_PATH
FLEET_CODEX_DESKTOP_APP_PATH="$(/usr/bin/plutil -extract appPath raw -o - "$CONFIG")"
node_bin="$(fleet_resolve_keeper_node)"
exec "$node_bin" "$SCRIPT_DIR/codex-desktop-env.mjs" "$CONFIG" "${1:-clear}" "${3:-}"
