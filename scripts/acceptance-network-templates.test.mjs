import test from 'node:test';
import assert from 'node:assert/strict';
import { acceptanceNetworkTemplates } from './acceptance-network-templates.mjs';

test('independent acceptance templates derive addresses from configured origin', () => {
  const nginx = 'server {\n        location / { proxy_pass http://127.0.0.1:7098; }\n}';
  for (const origin of ['https://192.0.2.1:7443', 'https://192.0.2.2:9443']) {
    const templates = acceptanceNetworkTemplates(origin, nginx);
    assert.ok(templates.headscale.includes(JSON.stringify(origin)));
    assert.match(templates.headscale, /100\.96\.0\.0\/16/);
    assert.match(templates.nginx, /\(bootstrap/);
    assert.match(templates.nginx, /client-current\/\$1/);
    assert.match(templates.nginx, /127\.0\.0\.1:17090/);
    assert.match(templates.nginx, /127\.0\.0\.1:7098/);
    assert.ok(!templates.nginx.includes('proxy_pass http://100.'));
  }
  assert.throws(() => acceptanceNetworkTemplates('http://192.0.2.1:7443', nginx));
  assert.throws(() => acceptanceNetworkTemplates('https://192.0.2.1:7443', 'location / { proxy_pass http://127.0.0.1:7090; }'));
});
