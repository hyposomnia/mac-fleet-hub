import test from 'node:test';
import assert from 'node:assert/strict';
import { acceptanceNetworkTemplates, addNativeDownloadLocations } from './acceptance-network-templates.mjs';

test('native download upgrade preserves existing routes and is idempotent', () => {
  const original = 'server {\n        location /derp { proxy_pass http://127.0.0.1:17090; }\n        location / { proxy_pass http://127.0.0.1:7098; }\n}';
  const updated = addNativeDownloadLocations(original, '/opt/fleet-review');
  assert.ok(updated.includes('alias /opt/fleet-review/client-native-current/$1'));
  assert.ok(updated.includes('alias /opt/fleet-review/client-native-releases/$1/$2'));
  assert.equal(updated.match(/location \/derp/g).length, 1);
  assert.equal(addNativeDownloadLocations(updated, '/opt/fleet-review'), updated);
  assert.throws(() => addNativeDownloadLocations(original, '/opt/bad;command'));
});

test('independent acceptance templates derive addresses from configured origin', () => {
  const nginx = 'server {\n        location / { proxy_pass http://127.0.0.1:7098; }\n}';
  for (const origin of ['https://192.0.2.1:7443', 'https://192.0.2.2:9443']) {
    const templates = acceptanceNetworkTemplates(origin, nginx);
    assert.ok(templates.headscale.includes(JSON.stringify(origin)));
    assert.match(templates.headscale, /100\.96\.0\.0\/16/);
    assert.match(templates.nginx, /\(bootstrap/);
    assert.match(templates.nginx, /client-current\/\$1/);
    assert.ok(templates.nginx.includes('client-release\\.json'));
    assert.ok(templates.nginx.includes('appcast\\.xml'));
    assert.ok(templates.nginx.includes('client-native-releases/$1/$2'));
    assert.match(templates.nginx, /127\.0\.0\.1:17090/);
    assert.match(templates.nginx, /127\.0\.0\.1:7098/);
    assert.ok(!templates.nginx.includes('proxy_pass http://100.'));
  }
  assert.throws(() => acceptanceNetworkTemplates('http://192.0.2.1:7443', nginx));
  assert.throws(() => acceptanceNetworkTemplates('https://192.0.2.1:7443', 'location / { proxy_pass http://127.0.0.1:7090; }'));
});
