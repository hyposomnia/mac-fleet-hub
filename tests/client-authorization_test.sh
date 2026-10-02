#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "client-authorization: $*" >&2; exit 1; }
for service in ttyd filebrowser; do
  grep -Fq '<string>127.0.0.1</string>' "$ROOT/mac/com.macfleet.${service}.plist" || fail "${service} must use loopback"
done
grep -Fq 'FLEET_BINDING_FILE' "$ROOT/mac/com.macfleet.fleet-agent.plist" || fail 'missing private binding path'
if grep -Eq 'AUTHKEY|MAC_IPS|这台是第几台|Authenticator 入网' "$ROOT/mac/install.sh" "$ROOT/server/enroll/bootstrap.sh"; then fail 'obsolete shared enrollment'; fi
mkdir -p "$WORK/bin"
cat > "$WORK/bin/tailscale" <<'SH'
#!/usr/bin/env bash
if [[ "$1" == status ]]; then printf '{"BackendState":"Running","Self":{"HostName":"mac1"}}';
elif [[ "$1" == ip ]]; then printf '100.64.0.9';
elif [[ "$1" == debug ]]; then printf '  "ControlURL": "https://old.example.com"\n';
else printf '%s\n' "$*" >> "$CALLS"; fi
SH
cat > "$WORK/bin/sudo" <<'SH'
#!/usr/bin/env bash
exec "$@"
SH
chmod +x "$WORK/bin/"*
printf 'private-key' > "$WORK/key"; chmod 0600 "$WORK/key"
export PATH="$WORK/bin:$PATH" CALLS="$WORK/calls" LOGIN_SERVER=https://new.example.com MAC_INDEX=101 FLEET_AUTHKEY_FILE="$WORK/key"
if bash "$ROOT/mac/join-device.sh" > "$WORK/output" 2>&1; then fail 'changed tailnet without consent'; fi
[[ ! -e "$CALLS" ]] || fail 'unsafe network mutation before consent'
FLEET_REPLACE_TAILNET=true bash "$ROOT/mac/join-device.sh" > "$WORK/output" 2>&1 || { cat "$WORK/output"; fail 'join failed'; }
grep -Fq -- '--force-reauth' "$CALLS" || fail 'new binding reused existing node'
grep -Fq -- '--hostname=mac101' "$CALLS" || fail 'lost server assigned index'
grep -Fq -- "--auth-key=file:$WORK/key" "$CALLS" || fail 'key must be passed by private file'
if grep -Fq 'private-key' "$CALLS" "$WORK/output"; then fail 'key leaked'; fi
echo 'client-authorization tests passed'
