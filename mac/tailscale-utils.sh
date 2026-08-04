#!/usr/bin/env bash

# Shared Tailscale state helpers for the Mac installer and Linux gateway setup.
# `tailscale status` alone only proves that some tailnet is active; ControlURL
# identifies whether it is the Fleet Headscale control plane.
fleet_normalize_control_url() {
  local value="${1:-}"
  while [[ "$value" == */ ]]; do value="${value%/}"; done
  printf '%s' "$value"
}

fleet_tailscale_control_url() {
  "$1" debug prefs 2>/dev/null \
    | sed -n 's/^[[:space:]]*"ControlURL":[[:space:]]*"\([^"]*\)".*$/\1/p' \
    | head -n1 || true
}

fleet_tailscale_hostname() {
  "$1" debug prefs 2>/dev/null \
    | sed -n 's/^[[:space:]]*"Hostname":[[:space:]]*"\([^"]*\)".*$/\1/p' \
    | head -n1 || true
}

fleet_tailscale_connected() {
  "$1" status >/dev/null 2>&1 && "$1" ip -4 >/dev/null 2>&1
}
