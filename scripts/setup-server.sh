#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRV="$ROOT/server"
source "$ROOT/mac/tailscale-utils.sh"
fail() { echo "✗ $*" >&2; exit 1; }
url_base() { [[ "$2" == 443 ]] && printf 'https://%s' "$1" || printf 'https://%s:%s' "$1" "$2"; }
validate_host() {
  [[ "$FLEET_HOST" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]] || fail 'FLEET_HOST must be a DNS hostname.'
}
validate_path() {
  [[ "$1" =~ ^/[A-Za-z0-9._/-]+$ && "$1" != *'/../'* ]] || fail 'Configuration paths must be absolute without whitespace or directive characters.'
}
render_nginx() {
  validate_host
  validate_path "$SSL_CERT"
  validate_path "$SSL_KEY"
  install -d -m 0700 "$1"
  sed -e "s|__FLEET_HOST__|${FLEET_HOST}|g" \
    -e "s|__SSL_CERT__|${SSL_CERT}|g" -e "s|__SSL_KEY__|${SSL_KEY}|g" \
    "$SRV/nginx/fleet.conf" > "$1/mac-fleet-hub.conf"
  printf 'map $http_upgrade $fleet_conn_upgrade { default upgrade; "" close; }\n' > "$1/fleet-map.conf"
}

if [[ "${1:-}" == --render-nginx ]]; then
  [[ $# == 2 ]] || fail 'usage: setup-server.sh --render-nginx <output-directory>'
  : "${FLEET_HOST:?FLEET_HOST is required}"
  : "${SSL_CERT:?SSL_CERT is required}"
  : "${SSL_KEY:?SSL_KEY is required}"
  render_nginx "$2"
  exit 0
fi
[[ $# == 0 ]] || fail 'usage: setup-server.sh [--render-nginx <output-directory>]'
[[ "$(uname -s)" == Linux ]] || fail 'This installer runs on the Linux gateway.'
[[ $EUID == 0 ]] || fail 'Run this installer with sudo.'

if [[ -f "$SRV/.env" ]]; then set -a; source "$SRV/.env"; set +a; fi
if [[ -t 0 ]]; then
  if [[ -z "${DOMAIN:-}" ]]; then read -r -p '根域名 > ' DOMAIN < /dev/tty; fi
  if [[ -z "${FLEET_HOST:-}" ]]; then
    read -r -p "服务子域 [fleet.${DOMAIN}] > " FLEET_HOST < /dev/tty
    FLEET_HOST="${FLEET_HOST:-fleet.${DOMAIN}}"
  fi
  if [[ -z "${SSL_CERT:-}" ]]; then read -r -p '证书 fullchain 绝对路径 > ' SSL_CERT < /dev/tty; fi
  if [[ -z "${SSL_KEY:-}" ]]; then read -r -p '证书私钥绝对路径 > ' SSL_KEY < /dev/tty; fi
fi
: "${DOMAIN:?Set DOMAIN in server/.env or the environment}"
: "${FLEET_HOST:?Set FLEET_HOST}"
: "${SSL_CERT:?Set SSL_CERT}"
: "${SSL_KEY:?Set SSL_KEY}"
validate_host
validate_path "$SSL_CERT"
validate_path "$SSL_KEY"
NGINX_SITE="${NGINX_SITE:-/etc/nginx/sites-enabled/mac-fleet-hub.conf}"
validate_path "$NGINX_SITE"
GATEWAY_PORT="${GATEWAY_PORT:-443}"
HEADSCALE_PUBLIC_PORT="${HEADSCALE_PUBLIC_PORT:-8443}"
HEADSCALE_LISTEN_PORT="${HEADSCALE_LISTEN_PORT:-8443}"
ENROLL_AGENT_PORT="${ENROLL_AGENT_PORT:-${AGENT_PORT:-7682}}"
ENROLL_TTYD_PORT="${ENROLL_TTYD_PORT:-${TTYD_PORT:-7681}}"
ENROLL_FB_PORT="${ENROLL_FB_PORT:-${FB_PORT:-8080}}"
for port in "$GATEWAY_PORT" "$HEADSCALE_PUBLIC_PORT" "$HEADSCALE_LISTEN_PORT" \
  "$ENROLL_AGENT_PORT" "$ENROLL_TTYD_PORT" "$ENROLL_FB_PORT"; do
  [[ "$port" =~ ^[1-9][0-9]{0,4}$ ]] && (( port <= 65535 )) || fail 'Ports must be integers from 1 to 65535.'
done
[[ "$HEADSCALE_LISTEN_PORT" != 443 && "$HEADSCALE_LISTEN_PORT" != 7090 ]] || fail 'Headscale listen port conflicts with nginx or Fleet.'
WEB_BASE="$(url_base "$FLEET_HOST" "$GATEWAY_PORT")"
HS_BASE="$(url_base "$FLEET_HOST" "$HEADSCALE_PUBLIC_PORT")"
HS_API_BASE="$(url_base "$FLEET_HOST" "$HEADSCALE_LISTEN_PORT")"

echo '==> Preflight: prerequisites, existing mesh, source validation'
if [[ ( -s /var/lib/fleet-enroll/fleet.sqlite || -s /var/lib/fleet-enroll/fleet.sqlite-wal ) && ! -s /etc/fleet-enroll/encryption.key ]]; then
  fail 'Encryption key missing for existing database; restore its original key before deployment.'
fi
for dependency in nginx curl openssl jq timeout getent systemctl useradd install file; do
  command -v "$dependency" >/dev/null || fail "Missing ${dependency}; install it before retrying."
done
[[ -s "$SSL_CERT" && -s "$SSL_KEY" ]] || fail 'TLS fullchain and private key must exist.'
openssl x509 -in "$SSL_CERT" -noout -checkend 0 >/dev/null || fail 'TLS certificate is invalid or expired.'
getent hosts "$FLEET_HOST" >/dev/null || fail 'FLEET_HOST must resolve before deployment.'
grep -Eq 'include[[:space:]]+[^;]*sites-enabled/\*' /etc/nginx/nginx.conf \
  || fail 'nginx.conf must include sites-enabled/*.'
nginx -t
if grep -Rqs 'snippets/fleet.conf' /etc/nginx/sites-enabled /etc/nginx/sites-available; then
  fail 'Remove the legacy snippets/fleet.conf include from existing nginx sites before retrying.'
fi
if command -v tailscale >/dev/null && fleet_tailscale_connected tailscale; then
  CURRENT_CONTROL="$(fleet_normalize_control_url "$(fleet_tailscale_control_url tailscale)")"
  if [[ "$CURRENT_CONTROL" != "$(fleet_normalize_control_url "$HS_BASE")" && "${FLEET_REPLACE_TAILNET:-0}" != 1 ]]; then
    fail 'Gateway is connected to another tailnet. Set FLEET_REPLACE_TAILNET=1 only when replacement is authorized.'
  fi
fi
if command -v go >/dev/null; then
  bash "$ROOT/scripts/verify.sh"
else
  [[ -n "${FLEET_ENROLL_BINARY:-}" ]] || fail 'Go is required, or explicitly supply a trusted, verified Linux FLEET_ENROLL_BINARY.'
  echo 'Using an explicit prebuilt Linux binary; project verification must already have passed on its build machine.'
fi
case "$(uname -m)" in
  x86_64) ARCH=amd64; ELF_ARCH='x86-64' ;;
  aarch64|arm64) ARCH=arm64; ELF_ARCH='aarch64|ARM aarch64' ;;
  *) fail 'Only amd64 and arm64 gateways are supported.' ;;
esac

WORK="$(mktemp -d /tmp/macfleet-server.XXXXXX)"
CANDIDATE_PID=''
HEADSCALE_INITIALIZING=0
cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$CANDIDATE_PID" ]]; then kill "$CANDIDATE_PID" 2>/dev/null || true; wait "$CANDIDATE_PID" 2>/dev/null || true; fi
  if [[ "$status" != 0 && "$HEADSCALE_INITIALIZING" == 1 ]]; then
    systemctl stop headscale || true
    echo 'Headscale initialization failed; it remains stopped to prevent an uninitialized permissive policy.' >&2
  fi
  if [[ "$status" != 0 && -n "${BACKUP:-}" ]]; then
    printf 'Deployment stopped. Private recovery snapshot: %s\n' "$BACKUP" >&2
  fi
  rm -rf -- "$WORK"
  exit "$status"
}
trap cleanup EXIT
if [[ -n "${FLEET_ENROLL_BINARY:-}" ]]; then
  [[ -f "$FLEET_ENROLL_BINARY" ]] || fail 'FLEET_ENROLL_BINARY does not exist.'
  install -m 0755 "$FLEET_ENROLL_BINARY" "$WORK/fleet-enroll"
else
  (cd "$SRV/enroll" && go build -trimpath -o "$WORK/fleet-enroll" .)
fi
file "$WORK/fleet-enroll" | grep -Eq "ELF .*($ELF_ARCH)" || fail 'Candidate must be a Linux binary matching this gateway architecture.'
install -d -m 0700 "$WORK/probe"
openssl rand 32 > "$WORK/probe/encryption.key"
env -i PATH="$PATH" HOME="$WORK/probe" \
  ENROLL_LISTEN=127.0.0.1:7098 FLEET_ORIGIN=http://127.0.0.1:7098 \
  FLEET_STATE_DIR="$WORK/probe/state" FLEET_KEY_FILE="$WORK/probe/encryption.key" \
  FLEET_STATIC_DIR="$SRV/dashboard" \
  "$WORK/fleet-enroll" > "$WORK/probe/server.log" 2>&1 &
CANDIDATE_PID=$!
PROBE_READY=0
for attempt in $(seq 1 30); do
  kill -0 "$CANDIDATE_PID" 2>/dev/null || break
  if curl -fsS --connect-timeout 1 --max-time 2 http://127.0.0.1:7098/healthz >/dev/null 2>&1; then PROBE_READY=1; break; fi
  sleep 1
done
[[ "$PROBE_READY" == 1 ]] || fail 'Candidate failed the isolated unified-server health probe.'
[[ "$(curl -sS --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' http://127.0.0.1:7098/api/auth/me)" == 401 ]] \
  || fail 'Candidate does not enforce unified authentication.'
[[ "$(curl -sS --connect-timeout 2 --max-time 5 -o /dev/null -w '%{http_code}' -X POST http://127.0.0.1:7098/join)" == 410 ]] \
  || fail 'Candidate still accepts the legacy enrollment protocol.'
kill "$CANDIDATE_PID"
wait "$CANDIDATE_PID" 2>/dev/null || true
CANDIDATE_PID=''
render_nginx "$WORK/nginx"
sed -e "s|{{HS_BASE}}|${HS_BASE}|g" \
  -e "s|{{HEADSCALE_LISTEN_PORT}}|${HEADSCALE_LISTEN_PORT}|g" \
  -e "s|{{SSL_CERT}}|${SSL_CERT}|g" -e "s|{{SSL_KEY}}|${SSL_KEY}|g" \
  "$SRV/headscale/config.yaml.example" > "$WORK/headscale.yaml"

echo '==> Install missing Headscale and Tailscale prerequisites'
if ! command -v headscale >/dev/null; then
  command -v dpkg >/dev/null || fail 'Install Headscale manually on a non-Debian gateway.'
  HS_DEB="${HEADSCALE_DEB:-}"
  if [[ -z "$HS_DEB" ]]; then
    HS_VERSION="${HEADSCALE_VERSION:-0.26.1}"
    [[ "$HS_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail 'Invalid HEADSCALE_VERSION.'
    HS_DEB="$WORK/headscale.deb"
    curl -fsSL --connect-timeout 10 --max-time 300 \
      -o "$HS_DEB" "https://github.com/juanfont/headscale/releases/download/v${HS_VERSION}/headscale_${HS_VERSION}_linux_${ARCH}.deb"
  fi
  [[ -s "$HS_DEB" ]] || fail 'Headscale installation package is missing.'
  dpkg -i "$HS_DEB" || { apt-get -fy install; command -v headscale >/dev/null || fail 'Headscale installation failed.'; }
fi
if ! command -v tailscale >/dev/null; then
  curl -fsSL --connect-timeout 10 --max-time 60 https://tailscale.com/install.sh -o "$WORK/install-tailscale.sh"
  sh "$WORK/install-tailscale.sh"
fi
command -v tailscale >/dev/null || fail 'Tailscale installation failed.'

echo '==> Stop Fleet and Headscale, snapshot existing state, replace service configuration'
mkdir -p /var/backups
BACKUP="$(mktemp -d /var/backups/mac-fleet-hub.XXXXXX)"
chmod 0700 "$BACKUP"
NGINX_MAP=/etc/nginx/conf.d/fleet-map.conf
for service in headscale fleet-enroll nginx fleet-nodes.timer fleet-derp-redirect.service; do
  systemctl is-enabled "$service" > "$BACKUP/$service.enabled" 2>&1 || true
  systemctl is-active "$service" > "$BACKUP/$service.active" 2>&1 || true
done
for service in fleet-enroll.service headscale.service; do
  if systemctl cat "$service" >/dev/null 2>&1; then systemctl stop "$service"; fi
done
if systemctl cat fleet-nodes.timer >/dev/null 2>&1; then
  systemctl disable --now fleet-nodes.timer
fi
for path in /etc/fleet-enroll /var/lib/fleet-enroll /etc/headscale /var/lib/headscale \
  /usr/local/bin/fleet-enroll /etc/systemd/system/fleet-enroll.service /etc/systemd/system/headscale.service \
  "$NGINX_SITE" "$NGINX_MAP" /etc/hosts /etc/systemd/system/fleet-derp-redirect.service \
  /var/www/fleet /var/www/fleet-enroll/bootstrap.sh; do
  if [[ -e "$path" || -L "$path" ]]; then
    install -d -m 0700 "$BACKUP$(dirname "$path")"
    cp -a "$path" "$BACKUP$path"
  else
    printf '%s\n' "$path" >> "$BACKUP/missing.paths"
  fi
done
if [[ -L "$NGINX_SITE" ]]; then
  SITE_TARGET="$(readlink -f "$NGINX_SITE")"
  [[ -f "$SITE_TARGET" ]] || fail 'nginx site symlink must resolve to a regular file.'
  install -d -m 0700 "$BACKUP$(dirname "$SITE_TARGET")"
  cp -a "$SITE_TARGET" "$BACKUP$SITE_TARGET"
fi
id fleet-enroll >/dev/null 2>&1 || useradd --system --user-group --home-dir /var/lib/fleet-enroll --shell /usr/sbin/nologin fleet-enroll
[[ "$(id -u fleet-enroll)" != 0 ]] || fail 'fleet-enroll must be a nonroot account.'
install -d -o root -g fleet-enroll -m 0750 /etc/fleet-enroll
install -d -o fleet-enroll -g fleet-enroll -m 0700 /var/lib/fleet-enroll
chown -R fleet-enroll:fleet-enroll /var/lib/fleet-enroll
find /var/lib/fleet-enroll -type d -exec chmod 0700 {} +
find /var/lib/fleet-enroll -type f -exec chmod 0600 {} +
if [[ ! -e /etc/fleet-enroll/encryption.key ]]; then
  [[ ! -s /var/lib/fleet-enroll/fleet.sqlite && ! -s /var/lib/fleet-enroll/fleet.sqlite-wal ]] \
    || fail 'Encryption key missing for existing database; restore its original key before deployment.'
  [[ ! -L /etc/fleet-enroll/encryption.key ]] || fail 'Encryption key must not be a symlink.'
  openssl rand 32 > "$WORK/encryption.key"
  install -o fleet-enroll -g fleet-enroll -m 0600 "$WORK/encryption.key" /etc/fleet-enroll/encryption.key
fi
[[ -f /etc/fleet-enroll/encryption.key && ! -L /etc/fleet-enroll/encryption.key ]] \
  || fail 'Encryption key must be a regular file.'
[[ "$(wc -c < /etc/fleet-enroll/encryption.key)" -eq 32 ]] || fail 'Encryption key must contain exactly 32 bytes; refusing to replace it.'
chown fleet-enroll:fleet-enroll /etc/fleet-enroll/encryption.key
chmod 0600 /etc/fleet-enroll/encryption.key
install -m 0755 "$WORK/fleet-enroll" /usr/local/bin/fleet-enroll
mkdir -p /etc/headscale /var/lib/headscale
install -m 0644 "$SRV/systemd/headscale.service" /etc/systemd/system/headscale.service
install -m 0644 "$SRV/systemd/fleet-enroll.service" /etc/systemd/system/fleet-enroll.service
install -m 0600 "$SRV/headscale/acl.hujson" /etc/headscale/fleet-initial-policy.hujson
sed "s|listen_addr: 0.0.0.0:${HEADSCALE_LISTEN_PORT}|listen_addr: 127.0.0.1:${HEADSCALE_LISTEN_PORT}|" \
  "$WORK/headscale.yaml" > /etc/headscale/config.yaml
systemctl daemon-reload

echo '==> Start Headscale on loopback only, initialize database policy to deny all'
HEADSCALE_INITIALIZING=1
systemctl start headscale
HS_READY=0
for attempt in $(seq 1 30); do
  if timeout 5 headscale policy set --file /etc/headscale/fleet-initial-policy.hujson \
    > "$WORK/headscale-policy.log" 2>&1; then HS_READY=1; break; fi
  sleep 1
done
[[ "$HS_READY" == 1 ]] || fail 'Headscale failed to accept its database policy.'
if [[ ! -s /etc/fleet-enroll/headscale-api.key ]]; then
  timeout 20 headscale apikeys create --expiration 8760h > "$WORK/headscale-api.key" 2> "$WORK/headscale-apikey.log" \
    || fail 'Unable to create the Headscale API key.'
  [[ -s "$WORK/headscale-api.key" ]] || fail 'Headscale returned an empty API key.'
  install -o fleet-enroll -g fleet-enroll -m 0600 "$WORK/headscale-api.key" /etc/fleet-enroll/headscale-api.key
fi
[[ -f /etc/fleet-enroll/headscale-api.key && ! -L /etc/fleet-enroll/headscale-api.key ]] \
  || fail 'Headscale API key must be a regular file.'
chown fleet-enroll:fleet-enroll /etc/fleet-enroll/headscale-api.key
chmod 0600 /etc/fleet-enroll/headscale-api.key
install -m 0644 "$WORK/headscale.yaml" /etc/headscale/config.yaml
echo '==> Restart Headscale with public TLS listener and initialized deny policy'
systemctl enable headscale >/dev/null
systemctl restart headscale
HEADSCALE_INITIALIZING=0

if ! getent hosts "$FLEET_HOST" | awk '{print $1}' | grep -qx 127.0.0.1; then
  printf '127.0.0.1 %s\n' "$FLEET_HOST" >> /etc/hosts
fi
if [[ "$HEADSCALE_PUBLIC_PORT" != "$HEADSCALE_LISTEN_PORT" ]]; then
  echo '==> Replace and restart the DERP NAT loopback redirect'
  sed -e "s/__HS_PUBLIC_PORT__/${HEADSCALE_PUBLIC_PORT}/g" \
    -e "s/__HS_LISTEN_PORT__/${HEADSCALE_LISTEN_PORT}/g" \
    "$SRV/systemd/fleet-derp-redirect.service" > /etc/systemd/system/fleet-derp-redirect.service
  systemctl daemon-reload
  systemctl enable fleet-derp-redirect.service
  systemctl restart fleet-derp-redirect.service
else
  echo '==> Disable obsolete DERP NAT loopback redirect'
  systemctl disable --now fleet-derp-redirect.service 2>/dev/null || true
fi

echo '==> Verify gateway mesh without replacing another tailnet unless explicitly allowed'
GW_TARGET_CONTROL="$(fleet_normalize_control_url "$HS_BASE")"
GW_JOIN=1
if fleet_tailscale_connected tailscale; then
  GW_CURRENT_CONTROL="$(fleet_normalize_control_url "$(fleet_tailscale_control_url tailscale)")"
  if [[ "$GW_CURRENT_CONTROL" == "$GW_TARGET_CONTROL" ]]; then
    GW_JOIN=0
  elif [[ "${FLEET_REPLACE_TAILNET:-0}" == 1 ]]; then
    echo '==> Explicit tailnet replacement: log out the gateway'
    tailscale logout
  else
    fail 'Gateway is connected to another tailnet; replacement requires FLEET_REPLACE_TAILNET=1.'
  fi
fi
if [[ "$GW_JOIN" == 1 ]]; then
  timeout 20 headscale users list --name fleet-gateway -o json > "$WORK/gateway-users.json"
  GW_UID="$(jq -r 'if length == 1 and .[0].name == "fleet-gateway" then .[0].id else empty end' "$WORK/gateway-users.json")"
  if [[ -z "$GW_UID" ]]; then
    [[ "$(jq 'length' "$WORK/gateway-users.json")" == 0 ]] || fail 'Gateway user lookup is ambiguous.'
    timeout 20 headscale users create fleet-gateway > "$WORK/gateway-user.log" 2>&1
    timeout 20 headscale users list --name fleet-gateway -o json > "$WORK/gateway-users.json"
    GW_UID="$(jq -r 'if length == 1 and .[0].name == "fleet-gateway" then .[0].id else empty end' "$WORK/gateway-users.json")"
  fi
  [[ "$GW_UID" =~ ^[1-9][0-9]*$ ]] || fail 'Unable to resolve the dedicated gateway Headscale user.'
  timeout 20 headscale preauthkeys create -u "$GW_UID" --expiration 1h > "$WORK/gateway-auth.key" 2> "$WORK/gateway-key.log"
  timeout 90 tailscale up --login-server="$HS_BASE" --auth-key="file:$WORK/gateway-auth.key" \
    --hostname=gateway --accept-dns=false > "$WORK/gateway-join.log" 2>&1 || fail 'Gateway enrollment failed.'
fi
[[ "$(fleet_normalize_control_url "$(fleet_tailscale_control_url tailscale)")" == "$GW_TARGET_CONTROL" ]] \
  || fail 'Gateway control-plane verification failed.'
FLEET_GATEWAY_IP="$(tailscale ip -4)"
[[ "$FLEET_GATEWAY_IP" =~ ^100\.(6[4-9]|[7-9][0-9]|1[01][0-9]|12[0-7])\.([0-9]{1,3})\.([0-9]{1,3})$ ]] \
  || fail 'Gateway must have an actual IPv4 address in the Headscale mesh.'

cat > /etc/fleet-enroll/env <<EOF
ENROLL_LISTEN=127.0.0.1:7090
FLEET_ORIGIN=${WEB_BASE}
FLEET_STATE_DIR=/var/lib/fleet-enroll
FLEET_STATIC_DIR=/var/www/fleet
FLEET_KEY_FILE=/etc/fleet-enroll/encryption.key
FLEET_HEADSCALE_URL=${HS_API_BASE}
FLEET_HEADSCALE_API_KEY_FILE=/etc/fleet-enroll/headscale-api.key
FLEET_GATEWAY_IP=${FLEET_GATEWAY_IP}
ENROLL_LOGIN_SERVER=${HS_BASE}
ENROLL_AGENT_PORT=${ENROLL_AGENT_PORT}
ENROLL_TTYD_PORT=${ENROLL_TTYD_PORT}
ENROLL_FB_PORT=${ENROLL_FB_PORT}
ENROLL_MESSAGE_CONCURRENCY=4
EOF
chmod 0600 /etc/fleet-enroll/env
install -d -m 0755 /var/www/fleet /var/www/fleet-enroll
cp -r "$SRV/dashboard/." /var/www/fleet/
find /var/www/fleet -type d -exec chmod 0755 {} +
find /var/www/fleet -type f -exec chmod 0644 {} +

echo '==> Update browser-pairing installer scripts; preserve signed agent distribution'
install -m 0644 "$SRV/enroll/bootstrap.sh" /var/www/fleet-enroll/bootstrap.sh
tar -czf "$WORK/mac-bundle.tar.gz" -C "$ROOT" --exclude='mac/fleet-agent/.git' mac
install -m 0644 "$WORK/mac-bundle.tar.gz" /var/www/fleet-enroll/mac-bundle.tar.gz

SITE_EXISTED=0
MAP_EXISTED=0
install -d "$(dirname "$NGINX_SITE")"
if [[ -e "$NGINX_SITE" ]]; then cp -p "$NGINX_SITE" "$WORK/site.backup"; SITE_EXISTED=1; fi
if [[ -e "$NGINX_MAP" ]]; then cp -p "$NGINX_MAP" "$WORK/map.backup"; MAP_EXISTED=1; fi
rollback_nginx() {
  if [[ "$SITE_EXISTED" == 1 ]]; then cp -p "$WORK/site.backup" "$NGINX_SITE"; else rm -f "$NGINX_SITE"; fi
  if [[ "$MAP_EXISTED" == 1 ]]; then cp -p "$WORK/map.backup" "$NGINX_MAP"; else rm -f "$NGINX_MAP"; fi
}
echo '==> Replace nginx site, validate it and reload; restore previous files on failure'
if ! install -m 0644 "$WORK/nginx/mac-fleet-hub.conf" "$NGINX_SITE" \
  || ! install -m 0644 "$WORK/nginx/fleet-map.conf" "$NGINX_MAP" || ! nginx -t; then
  rollback_nginx
  fail 'nginx configuration rejected; previous site and map restored.'
fi
if ! systemctl reload nginx; then
  rollback_nginx
  nginx -t && systemctl reload nginx || true
  fail 'nginx reload failed; previous configuration restored.'
fi
echo '==> Enable and start the unprivileged unified Fleet service'
systemctl enable fleet-enroll >/dev/null
systemctl restart fleet-enroll
READY=0
for attempt in $(seq 1 30); do
  if curl -fsS --connect-timeout 2 --max-time 5 http://127.0.0.1:7090/readyz >/dev/null 2>&1; then READY=1; break; fi
  sleep 1
done
[[ "$READY" == 1 ]] || fail 'Fleet readiness failed; inspect journalctl -u fleet-enroll.'
systemctl is-active headscale fleet-enroll nginx
curl -fsS --connect-timeout 3 --max-time 10 http://127.0.0.1:7090/healthz
echo
curl -fsS --connect-timeout 3 --max-time 10 http://127.0.0.1:7090/readyz
echo
curl -fsSI --connect-timeout 3 --max-time 10 --resolve "${FLEET_HOST}:443:127.0.0.1" "https://${FLEET_HOST}/auth"
systemctl status headscale fleet-enroll nginx --no-pager

cat <<EOF

Server deployed: ${WEB_BASE}
State snapshot: ${BACKUP}
Register and complete TOTP setup before promoting an existing user.
Administrator command: fleet-enroll admin <email>
Legacy import command: fleet-enroll migrate <email>
Run these commands with the service environment and as fleet-enroll, while the service is stopped.
No account or legacy device owner is assigned automatically.
客户端使用浏览器确认关联；请先通过正式签名公证流程发布支持设备授权的 agent。
Installers require a formally signed and notarized agent with device_authorization capability.
Existing signed /enroll/dist was preserved. Old agents fail the authorization capability check.
EOF
