#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP="$ROOT/scripts/setup-server.sh"
UNIT="$ROOT/server/systemd/fleet-enroll.service"
WORK="$(mktemp -d /tmp/macfleet-multiuser-test.XXXXXX)"
WORK="$(cd "$WORK" && pwd -P)"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "multiuser-server: $*" >&2; exit 1; }
require() { grep -Fq -- "$2" "$1" || fail "$1 lacks $2"; }
reject() { if grep -Eq -- "$2" "$1"; then fail "$1 contains obsolete/unsafe configuration: $2"; fi; }

require "$UNIT" 'User=fleet-enroll'
require "$UNIT" 'Group=fleet-enroll'
require "$UNIT" 'UMask=0077'
require "$UNIT" 'ProtectSystem=strict'
require "$UNIT" 'ReadWritePaths=/var/lib/fleet-enroll'
reject "$SETUP" 'AUTHELIA_|ENROLL_SECRET_FILE|ENROLL_MAC_IPS|MAC_LOCATIONS|show-uri'
reject "$SETUP" 'enable .*fleet-nodes'
for setting in ENROLL_LISTEN FLEET_ORIGIN FLEET_STATE_DIR FLEET_STATIC_DIR FLEET_KEY_FILE \
  FLEET_HEADSCALE_URL FLEET_HEADSCALE_API_KEY_FILE FLEET_GATEWAY_IP ENROLL_LOGIN_SERVER \
  ENROLL_AGENT_PORT ENROLL_TTYD_PORT ENROLL_FB_PORT; do
  require "$SETUP" "$setting="
done
require "$SETUP" 'headscale apikeys create'
require "$SETUP" 'FLEET_REPLACE_TAILNET'
require "$SETUP" 'tailscale ip -4'
require "$SETUP" 'nginx -t'
require "$SETUP" 'rollback_nginx'
require "$SETUP" 'go build'
require "$SETUP" 'admin <email>'
require "$SETUP" 'migrate <email>'
require "$ROOT/server/headscale/config.yaml.example" 'mode: database'
reject "$ROOT/server/headscale/config.yaml.example" 'mode: file|path: /etc/headscale/acl'
node -e 'const fs=require("fs"); const p=JSON.parse(fs.readFileSync(process.argv[1])); if (!Array.isArray(p.acls)||p.acls.length||p.grants?.length) process.exit(1)' \
  "$ROOT/server/headscale/acl.hujson" || fail 'initial Headscale policy must deny all access'
require "$SETUP" 'headscale policy set'
require "$SETUP" '支持设备授权的 agent'

mkdir -p "$WORK/key-check/etc" "$WORK/key-check/state" "$WORK/key-check/scratch"
printf 'existing encrypted database\n' > "$WORK/key-check/state/fleet.sqlite"
awk '/^if \[\[ ! -e \/etc\/fleet-enroll\/encryption.key/ { inside=1 } inside {print} /^chmod 0600 \/etc\/fleet-enroll\/encryption.key/ {exit}' "$SETUP" \
  | sed -e "s|/etc/fleet-enroll|$WORK/key-check/etc|g" -e "s|/var/lib/fleet-enroll|$WORK/key-check/state|g" \
  > "$WORK/key-check/body.sh"
[[ -s "$WORK/key-check/body.sh" ]] || fail 'could not extract the real deployment key initialization'
cat > "$WORK/key-check/check.sh" <<'SH'
set -euo pipefail
fail() { echo "$*" >&2; exit 1; }
install() { command install -m 0600 "${@: -2}"; }
chown() { :; }
source "$KEY_BODY"
SH
if WORK="$WORK/key-check/scratch" KEY_BODY="$WORK/key-check/body.sh" \
  bash "$WORK/key-check/check.sh" > "$WORK/key-check/output" 2>&1; then
  fail 'deployment generated a replacement key for an existing database'
fi
[[ ! -e "$WORK/key-check/etc/encryption.key" ]] || fail 'deployment wrote a replacement encryption key'
require "$WORK/key-check/output" 'existing database'

mkdir -p "$WORK/snapshot/fs/etc/nginx" "$WORK/snapshot/fs/etc/systemd/system" "$WORK/snapshot/backup"
printf 'old site\n' > "$WORK/snapshot/fs/etc/nginx/fleet-site.conf"
printf 'old map\n' > "$WORK/snapshot/fs/etc/nginx/fleet-map.conf"
printf 'old hosts\n' > "$WORK/snapshot/fs/etc/hosts"
printf 'old DERP service\n' > "$WORK/snapshot/fs/etc/systemd/system/fleet-derp-redirect.service"
awk '/^for path in \/etc\/fleet-enroll/ {inside=1} inside {print} inside && /^done$/ {exit}' "$SETUP" \
  | sed -e "s|/etc/|$WORK/snapshot/fs/etc/|g" -e "s|/var/|$WORK/snapshot/fs/var/|g" -e "s|/usr/|$WORK/snapshot/fs/usr/|g" \
  > "$WORK/snapshot/body.sh"
BACKUP="$WORK/snapshot/backup" NGINX_SITE="$WORK/snapshot/fs/etc/nginx/fleet-site.conf" \
  NGINX_MAP="$WORK/snapshot/fs/etc/nginx/fleet-map.conf" bash "$WORK/snapshot/body.sh"
for resource in etc/hosts etc/nginx/fleet-site.conf etc/nginx/fleet-map.conf etc/systemd/system/fleet-derp-redirect.service; do
  cmp "$WORK/snapshot/fs/$resource" "$WORK/snapshot/backup$WORK/snapshot/fs/$resource" >/dev/null 2>&1 \
    || fail "recovery snapshot missing original ${resource}"
done

mkdir -p "$WORK/nat/etc/systemd/system"
awk '/^if \[\[ "\$HEADSCALE_PUBLIC_PORT" !=/ {inside=1} inside {print} inside && /^fi$/ {exit}' "$SETUP" \
  | sed "s|/etc/systemd/system|$WORK/nat/etc/systemd/system|g" > "$WORK/nat/body.sh"
cat > "$WORK/nat/check.sh" <<'SH'
set -euo pipefail
systemctl() { printf '%s\n' "$*" >> "$SERVICE_LOG"; }
source "$NAT_BODY"
SH
HEADSCALE_PUBLIC_PORT=28443 HEADSCALE_LISTEN_PORT=8443 SRV="$ROOT/server" \
  SERVICE_LOG="$WORK/nat/services.log" NAT_BODY="$WORK/nat/body.sh" bash "$WORK/nat/check.sh" > "$WORK/nat/output"
require "$WORK/nat/services.log" 'restart fleet-derp-redirect.service'
require "$WORK/nat/etc/systemd/system/fleet-derp-redirect.service" '28443'

mkdir -p "$WORK/bin"
cat > "$WORK/bin/go" <<'SH'
#!/usr/bin/env bash
set -eu
[[ "$1" == build && "$2" == -o ]] || exit 1
cat > "$3" <<'SERVER'
#!/usr/bin/env bash
set -eu
[[ "$ENROLL_LISTEN" == 127.0.0.1:7099 && "$FLEET_ORIGIN" == http://127.0.0.1:7099 ]]
[[ "$FLEET_STATE_DIR" == "$TEST_WORK/local/state" && "$FLEET_KEY_FILE" == "$TEST_WORK/local/encryption.key" ]]
[[ "$FLEET_STATIC_DIR" == "$TEST_ROOT/server/dashboard" ]]
[[ -z "${FLEET_HEADSCALE_URL:-}" && -z "${FLEET_HEADSCALE_API_KEY_FILE:-}" && -z "${FLEET_GATEWAY_IP:-}" ]]
printf 'local isolation passed\n'
SERVER
chmod +x "$3"
SH
chmod +x "$WORK/bin/go"
TEST_WORK="$WORK" TEST_ROOT="$ROOT" PATH="$WORK/bin:$PATH" FLEET_STATE_DIR=/var/lib/fleet-enroll \
  FLEET_KEY_FILE=/etc/fleet-enroll/encryption.key FLEET_HEADSCALE_URL=https://production.example.test \
  FLEET_HEADSCALE_API_KEY_FILE=/etc/fleet-enroll/headscale-api.key FLEET_GATEWAY_IP=100.64.0.1 \
  bash "$ROOT/scripts/run-local-server.sh" "$WORK/local" > "$WORK/local.out" 2>&1 \
  || { cat "$WORK/local.out" >&2; fail 'local server inherited production configuration'; }
require "$WORK/local.out" 'local isolation passed'
[[ "$(wc -c < "$WORK/local/encryption.key" | tr -d ' ')" == 32 ]] || fail 'wrong local encryption key length'
if PATH="$WORK/bin:$PATH" bash "$ROOT/scripts/run-local-server.sh" /var/lib/fleet-enroll > "$WORK/unsafe.out" 2>&1; then
  fail 'local runner accepted a production state directory'
fi

mkdir -p "$WORK/local-missing-key/state"
printf 'existing encrypted database\n' > "$WORK/local-missing-key/state/fleet.sqlite"
if PATH="$WORK/bin:$PATH" bash "$ROOT/scripts/run-local-server.sh" "$WORK/local-missing-key" > "$WORK/missing-key.out" 2>&1; then
  fail 'local runner accepted an existing database without its encryption key'
fi
[[ ! -e "$WORK/local-missing-key/encryption.key" ]] || fail 'local runner generated a replacement key for an existing database'
require "$WORK/missing-key.out" 'existing database'
echo 'multiuser-server tests passed'
