#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ $# -le 1 ]] || { echo 'usage: bash scripts/run-local-server.sh [private-temporary-directory]' >&2; exit 1; }
command -v go >/dev/null || { echo 'Go is required to build the local server.' >&2; exit 1; }
WORK="${1:-}"
if [[ -z "$WORK" ]]; then
  WORK="$(mktemp -d /tmp/macfleet-multiuser-local.XXXXXX)"
else
  case "$WORK" in
    /tmp/*|/private/tmp/*) ;;
    *) echo 'Local state must be in a private directory under /tmp or /private/tmp.' >&2; exit 1 ;;
  esac
  [[ ! -L "$WORK" ]] || { echo 'Refusing a symlink state directory.' >&2; exit 1; }
  if [[ -e "$WORK" ]]; then
    [[ -d "$WORK" && -O "$WORK" ]] || { echo 'State directory must belong to the current user.' >&2; exit 1; }
    WORK="$(cd "$WORK" && pwd -P)"
  else
    PARENT="$(cd "$(dirname "$WORK")" && pwd -P)"
    case "$PARENT" in
      /tmp|/tmp/*|/private/tmp|/private/tmp/*) ;;
      *) echo 'Resolved local state parent is outside /tmp.' >&2; exit 1 ;;
    esac
    WORK="$PARENT/$(basename "$WORK")"
    mkdir -m 0700 "$WORK"
  fi
fi
WORK="$(cd "$WORK" && pwd -P)"
case "$WORK" in
  /tmp/*|/private/tmp/*) ;;
  *) echo 'Resolved local state directory is outside /tmp.' >&2; exit 1 ;;
esac
chmod 0700 "$WORK"
for entry in state encryption.key fleet-enroll; do
  [[ ! -L "$WORK/$entry" ]] || { echo 'Refusing a symlink inside the local state directory.' >&2; exit 1; }
done
mkdir -p "$WORK/state"
chmod 0700 "$WORK/state"
if [[ ! -e "$WORK/encryption.key" ]]; then
  [[ ! -s "$WORK/state/fleet.sqlite" && ! -s "$WORK/state/fleet.sqlite-wal" ]] \
    || { echo 'Encryption key missing for existing database; restore its original key before starting.' >&2; exit 1; }
  openssl rand 32 > "$WORK/encryption.key"
fi
[[ -f "$WORK/encryption.key" && "$(wc -c < "$WORK/encryption.key" | tr -d ' ')" == 32 ]] \
  || { echo 'Existing local encryption key must contain exactly 32 bytes.' >&2; exit 1; }
chmod 0600 "$WORK/encryption.key"
(
  cd "$ROOT/server/enroll"
  go build -o "$WORK/fleet-enroll" .
)

export ENROLL_LISTEN=127.0.0.1:7099
export FLEET_ORIGIN=http://127.0.0.1:7099
export FLEET_STATE_DIR="$WORK/state"
export FLEET_STATIC_DIR="$ROOT/server/dashboard"
export FLEET_KEY_FILE="$WORK/encryption.key"
export FLEET_HEADSCALE_URL="${LOCAL_HEADSCALE_URL:-}"
export FLEET_HEADSCALE_API_KEY_FILE="${LOCAL_HEADSCALE_API_KEY_FILE:-}"
export FLEET_GATEWAY_IP="${LOCAL_GATEWAY_IP:-}"
export FLEET_MESH_PROXY="${LOCAL_MESH_PROXY:-}"
export ENROLL_LOGIN_SERVER="${LOCAL_LOGIN_SERVER:-}"
export ENROLL_AGENT_PORT="${LOCAL_AGENT_PORT:-7682}"
export ENROLL_TTYD_PORT="${LOCAL_TTYD_PORT:-7681}"
export ENROLL_FB_PORT="${LOCAL_FB_PORT:-8080}"
unset ENROLL_SECRET_FILE ENROLL_HS_USER ENROLL_NAMES_FILE ENROLL_SETTINGS_FILE \
  ENROLL_ACCESS_KEY_FILE ENROLL_MESSAGE_JOBS_FILE ENROLL_MAC_IPS
printf 'Local server: %s\nPrivate state: %s\n' "$FLEET_ORIGIN" "$WORK"
printf 'Headscale is enabled only via LOCAL_HEADSCALE_* and LOCAL_GATEWAY_IP.\n'
exec "$WORK/fleet-enroll"
