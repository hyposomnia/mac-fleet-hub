#!/bin/bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG="$SCRIPT_DIR/codex-desktop.json"
source "$SCRIPT_DIR/codex-bin-resolve.sh"
export FLEET_CODEX_DESKTOP_APP_PATH
FLEET_CODEX_DESKTOP_APP_PATH="$(/usr/bin/plutil -extract appPath raw -o - "$CONFIG")"
# Only App-bundled executables preserve the native browser's signing contract.
entry="$(fleet_codex_manifest_entrypoint "$FLEET_CODEX_DESKTOP_APP_PATH" || true)"
FLEET_CODEX_REAL_BIN=""
requirement='=anchor apple generic and certificate leaf[subject.OU] = "2DC432GLL2"'
for candidate in \
  "${FLEET_CODEX_DESKTOP_APP_PATH}/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex" \
  "${FLEET_CODEX_DESKTOP_APP_PATH}/Contents/Resources/codex-cli/${entry:-missing}" \
  "${FLEET_CODEX_DESKTOP_APP_PATH}/Contents/Resources/codex-cli/bin/codex" \
  "${FLEET_CODEX_DESKTOP_APP_PATH}/Contents/Resources/codex"; do
  if resolved="$(fleet_codex_bin_usable "$candidate")" && /usr/bin/codesign --verify --strict -R "$requirement" "$resolved" 2>/dev/null; then
    FLEET_CODEX_REAL_BIN="$resolved"
    break
  fi
done
[[ -n "$FLEET_CODEX_REAL_BIN" ]] || { echo "App bundled Codex is unavailable" >&2; exit 1; }
node_bin="$(fleet_resolve_keeper_node)"
/usr/bin/codesign --verify --strict -R "$requirement" "$FLEET_CODEX_REAL_BIN"
/usr/bin/codesign --verify --strict -R "$requirement" "$node_bin"
export FLEET_CODEX_REAL_BIN
exec "$node_bin" "$SCRIPT_DIR/codex-desktop-bridge.mjs" "$CONFIG" "$@"
