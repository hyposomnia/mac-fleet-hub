#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SITE="$ROOT/server/nginx/fleet.conf"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/macfleet-nginx.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
fail() { echo "nginx-config: $*" >&2; exit 1; }
block_of() {
  awk -v want="$1" 'index($0,want) { inside=1 } inside { print } inside && /^[[:space:]]*}[[:space:]]*$/ { exit }' "$2"
}

if grep -Eq 'auth_request|9091|__MAC_LOCATIONS__|__MAC_IP__|root /var/www/fleet|try_files' "$SITE"; then
  fail 'private requests must reach the unified server without static or legacy auth bypasses'
fi
private="$(block_of 'location / {' "$SITE")"
for directive in 'proxy_pass http://127.0.0.1:7090;' 'client_max_body_size 513m;' \
  'proxy_set_header Upgrade $http_upgrade;' 'proxy_set_header Connection $fleet_conn_upgrade;' \
  'proxy_set_header Authorization $http_authorization;' 'proxy_set_header Host $http_host;' \
  'proxy_set_header X-Forwarded-For $remote_addr;' 'proxy_buffering off;'; do
  [[ "$private" == *"$directive"* ]] || fail "unified proxy lacks ${directive}"
done
for route in '/enroll/confirm' '/enroll/confirm/' '/enroll/agent-config' '/enroll/join'; do
  block_of "location = ${route} {" "$SITE" | grep -q 'proxy_pass http://127.0.0.1:7090;' \
    || fail "${route} must preserve its URI and reach the unified server"
done
distribution="$(block_of 'location ^~ /enroll/ {' "$SITE")"
[[ "$distribution" == *'alias /var/www/fleet-enroll/;'* && "$distribution" == *'autoindex off;'* ]] \
  || fail 'public bootstrap/bundle/dist distribution must remain available without listing'

FLEET_HOST=fleet.example.test SSL_CERT=/test/fullchain.pem SSL_KEY=/test/key.pem \
  bash "$ROOT/scripts/setup-server.sh" --render-nginx "$WORK/render" > "$WORK/render.log"
! grep -Eq '__[A-Z_]+__' "$WORK/render/mac-fleet-hub.conf" || fail 'unrendered placeholders'
grep -q 'server_name fleet.example.test;' "$WORK/render/mac-fleet-hub.conf" || fail 'wrong host'
grep -q 'ssl_certificate_key /test/key.pem;' "$WORK/render/mac-fleet-hub.conf" || fail 'wrong certificate'
grep -q 'map $http_upgrade $fleet_conn_upgrade' "$WORK/render/fleet-map.conf" || fail 'missing upgrade map'
if FLEET_HOST='bad;host' SSL_CERT=/test/fullchain.pem SSL_KEY=/test/key.pem \
  bash "$ROOT/scripts/setup-server.sh" --render-nginx "$WORK/invalid" > "$WORK/invalid.log" 2>&1; then
  fail 'nginx directive injection accepted'
fi

awk '/^rollback_nginx\(\)/ {inside=1} inside {print} inside && /^}$/ {exit}' "$ROOT/scripts/setup-server.sh" > "$WORK/rollback-function.sh"
awk '/^echo .*Replace nginx site, validate/ {inside=1} inside && /^echo .*Enable and start/ {exit} inside {print}' \
  "$ROOT/scripts/setup-server.sh" > "$WORK/activate.sh"
[[ -s "$WORK/rollback-function.sh" && -s "$WORK/activate.sh" ]] || fail 'could not extract actual nginx activation'
cat > "$WORK/check-rollback.sh" <<'SH'
set -euo pipefail
fail() { echo "$*" >&2; exit 1; }
nginx() { [[ "$SCENARIO" != syntax ]]; }
systemctl() {
  count=0
  [[ ! -f "$WORK/reloads" ]] || count="$(cat "$WORK/reloads")"
  count=$((count + 1))
  printf '%s' "$count" > "$WORK/reloads"
  [[ "$SCENARIO" != reload || "$count" != 1 ]]
}
source "$ROLLBACK_FUNCTION"
source "$ACTIVATE_BODY"
SH
for scenario in syntax reload success; do
  fixture="$WORK/$scenario"
  mkdir -p "$fixture/nginx"
  cp "$WORK/render/mac-fleet-hub.conf" "$fixture/nginx/mac-fleet-hub.conf"
  cp "$WORK/render/fleet-map.conf" "$fixture/nginx/fleet-map.conf"
  printf 'original site\n' > "$fixture/site.backup"
  printf 'original map\n' > "$fixture/map.backup"
  cp "$fixture/site.backup" "$fixture/site"
  cp "$fixture/map.backup" "$fixture/map"
  status=0
  SCENARIO="$scenario" WORK="$fixture" NGINX_SITE="$fixture/site" NGINX_MAP="$fixture/map" \
    SITE_EXISTED=1 MAP_EXISTED=1 ROLLBACK_FUNCTION="$WORK/rollback-function.sh" ACTIVATE_BODY="$WORK/activate.sh" \
    bash "$WORK/check-rollback.sh" > "$fixture/output" 2>&1 || status=$?
  if [[ "$scenario" == success ]]; then
    [[ "$status" == 0 ]] || fail 'nginx activation failed on valid configuration'
    cmp "$fixture/site" "$fixture/nginx/mac-fleet-hub.conf" || fail 'candidate site was not activated'
  else
    [[ "$status" != 0 ]] || fail "${scenario} failure was hidden"
    cmp "$fixture/site" "$fixture/site.backup" || fail "${scenario} failed to restore site"
    cmp "$fixture/map" "$fixture/map.backup" || fail "${scenario} failed to restore map"
    if [[ "$scenario" == reload ]]; then
      [[ "$(cat "$fixture/reloads")" == 2 ]] || fail 'restored configuration was not reloaded'
    fi
  fi
done

echo 'nginx-config tests passed'
