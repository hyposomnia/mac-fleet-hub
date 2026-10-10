#!/bin/bash
# Compatibility for already installed legacy plists; new jobs launch signed Node directly.
# Path resolution is shared with the installer; main's keeper owns the runtime.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
resolver="${FLEET_CODEX_RESOLVER:-$SCRIPT_DIR/codex-bin-resolve.sh}"
keeper="${FLEET_CODEX_KEEPER_SCRIPT:-$SCRIPT_DIR/codex-shared-app-server.mjs}"
FLEET_CODEX_BIN="$(/bin/bash "$resolver")"
export FLEET_CODEX_BIN
node="$(/bin/bash "$resolver" --keeper-node)"
exec "$node" "$keeper"
