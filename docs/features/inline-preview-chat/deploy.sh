#!/usr/bin/env bash
set -euo pipefail
umask 077
release=b701026
webroot=/var/www/fleet
backup_dir=/var/backups/mac-fleet-hub
stage=$(mktemp -d /tmp/fleet-web-v183.XXXXXX)
tar -xzf /tmp/fleet-web-b701026.tgz -C "$stage"
test ! -e "$stage/api"
(cd "$stage" && sha256sum -c /tmp/fleet-web-b701026.sha256 > /tmp/fleet-web-v183-stage-check.txt)
jq -e 'length > 0' "$webroot/api/nodes.json" >/dev/null
(cd "$webroot" && sha256sum -c /tmp/fleet-web-v182-base.sha256 > /tmp/fleet-web-v183-base-check.txt)
backup="$backup_dir/dashboard-before-v183-$(date -u +%Y%m%dT%H%M%SZ).tgz"
install -d -m 0700 "$backup_dir"
tar -C "$webroot" --exclude='./api' -czf "$backup" .
chmod 0600 "$backup"
printf 'backup=%s\n' "$backup"
rollback() {
 local rc=$?
 trap - ERR
 printf 'deploy_failed=%s; restoring=%s\n' "$rc" "$backup" >&2
 tar -xzf "$backup" -C "$webroot"
 (cd "$webroot" && sha256sum -c /tmp/fleet-web-v182-base.sha256 > /tmp/fleet-web-v183-rollback-check.txt)
 printf 'rollback_static_sha256_matches=%s\n' "$(wc -l < /tmp/fleet-web-v183-rollback-check.txt)" >&2
 exit "$rc"
}
trap rollback ERR
# Publish dependencies first, then the entry document and service worker.
while IFS= read -r -d '' item; do
 relative=${item#"$stage/"}
 case "$relative" in index.html|sw.js) continue;; esac
 install -D -o www-data -g www-data -m 0644 "$item" "$webroot/$relative"
done < <(find "$stage" -type f -print0)
find "$stage" -type d -print0 | while IFS= read -r -d '' item; do
 relative=${item#"$stage"}
 chmod 0755 "$webroot$relative"
 chown www-data:www-data "$webroot$relative"
done
install -o www-data -g www-data -m 0644 "$stage/index.html" "$webroot/index.html"
install -o www-data -g www-data -m 0644 "$stage/sw.js" "$webroot/sw.js"
(cd "$webroot" && sha256sum -c /tmp/fleet-web-b701026.sha256 > /tmp/fleet-web-v183-live-check.txt)
printf 'static_sha256_matches=%s\n' "$(wc -l < /tmp/fleet-web-v183-live-check.txt)"
jq -e 'length > 0' "$webroot/api/nodes.json" >/dev/null
printf 'runtime_nodes_present\n'
for service in nginx fleet-enroll headscale fleet-nodes.timer; do
 printf '%s=' "$service"
 systemctl is-active "$service"
done
head -2 "$webroot/sw.js"
printf '%s\n' "$release" > "$backup_dir/dashboard-release-commit"
chmod 0600 "$backup_dir/dashboard-release-commit"
printf 'release=%s\n' "$release"
trap - ERR
