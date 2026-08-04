#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT/mac/tailscale-utils.sh"

mock_tailscale() {
  case "$*" in
    "debug prefs")
      cat <<'EOF'
{
  "ControlURL": "https://fleet.example.com:8443/",
  "Hostname": "mac7"
}
EOF
      ;;
    "status"|"ip -4") return 0 ;;
    *) return 1 ;;
  esac
}

[[ "$(fleet_normalize_control_url 'https://fleet.example.com:8443///')" == "https://fleet.example.com:8443" ]]
[[ "$(fleet_tailscale_control_url mock_tailscale)" == "https://fleet.example.com:8443/" ]]
[[ "$(fleet_tailscale_hostname mock_tailscale)" == "mac7" ]]
fleet_tailscale_connected mock_tailscale

mock_disconnected_tailscale() {
  [[ "$*" == "debug prefs" ]] && return 0
  return 1
}

if fleet_tailscale_connected mock_disconnected_tailscale; then
  echo "disconnected mock was reported as connected" >&2
  exit 1
fi
