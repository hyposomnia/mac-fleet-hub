import { readFileSync, writeFileSync } from 'node:fs';
import { isIP } from 'node:net';
import { pathToFileURL } from 'node:url';

export function addNativeDownloadLocations(nginx, root) {
  if (!/^\/opt\/[a-zA-Z0-9_-]+$/.test(root) || !nginx.includes('        location / {') || !nginx.includes('127.0.0.1:7098')) throw new Error('Invalid independent download deployment');
  if (nginx.includes('/client-native-current/')) {
    if (!nginx.includes(`alias ${root}/client-native-current/$1;`) || !nginx.includes(`alias ${root}/client-native-releases/$1/$2;`)) throw new Error('Existing native download deployment does not match');
    return nginx;
  }
  const locations = `        location ~ ^/enroll/(client-release\\.json|appcast\\.xml)$ {
            alias ${root}/client-native-current/$1;
            add_header Cache-Control "no-cache" always;
            add_header X-Content-Type-Options "nosniff" always;
            types { application/json json; application/xml xml; }
        }
        location ~ ^/enroll/clients/([1-9][0-9]*)/(Fleet-Hub\\.dmg|Fleet-Hub-update\\.zip)$ {
            alias ${root}/client-native-releases/$1/$2;
            default_type application/octet-stream;
            add_header Cache-Control "public, max-age=31536000, immutable";
            add_header X-Content-Type-Options "nosniff" always;
        }
`;
  return nginx.replace('        location / {', locations + '\n        location / {');
}

export function acceptanceNetworkTemplates(origin, nginx) {
  const service = new URL(origin);
  if (service.protocol !== 'https:' || service.origin !== origin || isIP(service.hostname) !== 4) throw new Error('Acceptance entry must be a configured HTTPS IPv4 origin');
  if (!nginx.includes('location / {') || !nginx.includes('127.0.0.1:7098')) throw new Error('Not the independent acceptance nginx configuration');
  const headscale = `server_url: ${JSON.stringify(origin)}
listen_addr: 127.0.0.1:17090
metrics_listen_addr: 127.0.0.1:17092
grpc_listen_addr: 127.0.0.1:17093
grpc_allow_insecure: false
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.96.0.0/16
  v6: fd7a:115c:a1e0:1000::/64
  allocation: sequential
derp:
  server:
    enabled: true
    region_id: 998
    region_code: fleet-uat
    region_name: Fleet Acceptance
    stun_listen_addr: ${JSON.stringify(service.hostname + ':13479')}
    private_key_path: /var/lib/headscale/derp_private.key
    automatically_add_embedded_derp_region: true
  urls: []
  paths: []
  auto_update_enabled: false
disable_check_updates: true
ephemeral_node_inactivity_timeout: 30m
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true
policy:
  mode: database
dns:
  magic_dns: false
  base_domain: fleet-uat.invalid
  override_local_dns: false
log:
  level: warn
unix_socket: /var/lib/headscale/headscale.sock
unix_socket_permission: "0600"
`;
  const downloadLocation = `        location ~ ^/enroll/(bootstrap\\.sh|mac-bundle\\.tar\\.gz|release\\.json|dist/fleet-agent-darwin-(?:arm64|amd64))$ {
            alias /opt/macfleet-saas-uat/client-current/$1;
            add_header Cache-Control "no-cache" always;
            add_header X-Content-Type-Options "nosniff" always;
        }
`;
  const routes = `
        location ~ ^/(?:key|ts2021|machine(?:/.*)?|derp(?:/.*)?|generate_204|bootstrap-dns)$ {
            proxy_pass http://127.0.0.1:17090;
            proxy_http_version 1.1;
            proxy_set_header Host $http_host;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection $fleet_conn_upgrade;
            proxy_buffering off;
            proxy_read_timeout 3600s;
        }
`;
  return { headscale, nginx: addNativeDownloadLocations(nginx.replace('        location / {', downloadLocation + routes + '\n        location / {'), '/opt/macfleet-saas-uat') };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  if (process.argv[2] === '--native-downloads') {
    const [, original, output, root] = process.argv.slice(2);
    writeFileSync(output, addNativeDownloadLocations(readFileSync(original, 'utf8'), root));
    process.exit(0);
  }
  const [origin, original, output] = process.argv.slice(2);
  const templates = acceptanceNetworkTemplates(origin, readFileSync(original, 'utf8'));
  writeFileSync(output + '/config.yaml', templates.headscale);
  writeFileSync(output + '/nginx.conf', templates.nginx);
}
