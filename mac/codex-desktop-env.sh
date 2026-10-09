#!/bin/sh
set -eu

mode=${1:-clear}
endpoint=${2:-}

case "$mode" in
  shared)
    case "$endpoint" in
      ws://127.0.0.1:*|ws://localhost:*|ws://\[::1\]:*) ;;
      *) echo "invalid Codex Desktop shared endpoint: $endpoint" >&2; exit 64 ;;
    esac
    /bin/launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 2>/dev/null || true
    exec /bin/launchctl setenv CODEX_APP_SERVER_WS_URL "$endpoint"
    ;;
  clear)
    /bin/launchctl unsetenv CODEX_APP_SERVER_USE_LOCAL_DAEMON 2>/dev/null || true
    exec /bin/launchctl unsetenv CODEX_APP_SERVER_WS_URL
    ;;
  *)
    echo "usage: codex-desktop-env.sh shared <ws-url> | clear" >&2
    exit 64
    ;;
esac
