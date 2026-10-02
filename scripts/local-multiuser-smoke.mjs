import assert from 'node:assert/strict';
import { createHmac, randomBytes } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import path from 'node:path';

const base = process.argv[2] || 'http://127.0.0.1:7099';
const work = process.argv[3];
assert(work?.startsWith('/private/tmp/') || work?.startsWith('/tmp/'), 'Provide the private local deployment directory');
assert(new URL(base).hostname === '127.0.0.1', 'Smoke test only runs on loopback');
if (process.argv.includes('--require-network')) {
  const readiness = await fetch(base + '/readyz', { signal: AbortSignal.timeout(10000) });
  assert.equal(readiness.status, 200, 'The real Headscale fixture must be ready for this smoke run');
}
const password = `test-${randomBytes(24).toString('hex')}`;
const suffix = randomBytes(5).toString('hex');

function otp(secret) {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567';
  let bits = '';
  for (const letter of secret) bits += alphabet.indexOf(letter).toString(2).padStart(5, '0');
  const bytes = [];
  for (let offset = 0; offset + 8 <= bits.length; offset += 8) bytes.push(parseInt(bits.slice(offset, offset + 8), 2));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const hash = createHmac('sha1', Buffer.from(bytes)).update(counter).digest();
  const offset = hash.at(-1) & 15;
  return String((hash.readUInt32BE(offset) & 0x7fffffff) % 1000000).padStart(6, '0');
}

class Browser {
  cookie = '';
  csrf = '';
  async request(method, route, body, expected = 200) {
    const response = await fetch(base + route, { method, headers: {
      Origin: base, Cookie: this.cookie, 'X-CSRF-Token': this.csrf, 'Content-Type': 'application/json',
    }, body: body === undefined ? undefined : JSON.stringify(body), redirect: 'manual', signal: AbortSignal.timeout(20000) });
    const cookies = response.headers.getSetCookie();
    if (cookies.length) this.cookie = cookies.at(-1).split(';')[0];
    const raw = await response.text();
    assert.equal(response.status, expected, `${method} ${route}: unexpected HTTP status`);
    const data = response.headers.get('content-type')?.includes('json') ? JSON.parse(raw) : raw;
    return { response, data };
  }
  async register(email) {
    const { data: setup } = await this.request('POST', '/api/auth/register', { email, password, confirm_password: password });
    assert.equal(setup.state, 'setup');
    await this.request('GET', '/api/auth/me', undefined, 401);
    const qr = await fetch(base + '/api/auth/qr', { headers: { Cookie: this.cookie }, signal: AbortSignal.timeout(5000) });
    assert.equal(qr.status, 200);
    assert.equal(qr.headers.get('content-type'), 'image/png');
    assert.equal(Buffer.from(await qr.arrayBuffer()).subarray(1, 4).toString(), 'PNG');
    const { data: verified, response } = await this.request('POST', '/api/auth/verify', { code: otp(setup.totp.secret) });
    assert.equal(verified.recovery_codes.length, 10);
    assert.match(response.headers.getSetCookie()[0], /Max-Age=2592000/);
    assert.match(response.headers.getSetCookie()[0], /HttpOnly/);
    const { data: me } = await this.request('GET', '/api/auth/me');
    this.csrf = me.csrf_token;
    return me.user;
  }
}

const admin = new Browser();
const member = new Browser();
const owner = await admin.register(`local-admin-${suffix}@example.test`);
const user = await member.register(`local-member-${suffix}@example.test`);
assert.equal(owner.role, 'user');
assert.equal(user.role, 'user');
console.log('PASS registration, mandatory Authenticator, real PNG QR, recovery codes, 30-day cookie');

await member.request('GET', '/api/admin/users', undefined, 403);
await member.request('POST', '/api/names', { id: 'm999999', name: 'forged' }, 404);
await member.request('GET', '/m999999/api/info', undefined, 404);
const savedCSRF = member.csrf;
member.csrf = 'forged';
await member.request('POST', '/api/settings', {}, 403);
member.csrf = savedCSRF;
await admin.request('POST', '/api/settings', { chatCacheMaxSessions: 2 });
const { data: preferences } = await member.request('GET', '/api/settings');
assert.equal(preferences.chatCacheMaxSessions, 6);
console.log('PASS ordinary-user admin denial, device authorization, CSRF, user preference isolation');

execFileSync(path.join(work, 'fleet-enroll'), ['admin', owner.email], {
  env: { ...process.env, FLEET_ORIGIN: base, FLEET_STATE_DIR: path.join(work, 'state'),
    FLEET_KEY_FILE: path.join(work, 'encryption.key'), FLEET_HEADSCALE_URL: '' },
  stdio: ['ignore', 'pipe', 'pipe'],
});
const { data: overview } = await admin.request('GET', '/api/admin/overview');
assert(overview.users >= 2);
const { data: detail } = await admin.request('GET', `/api/admin/users/${user.id}`);
assert.equal(detail.user.email, user.email);
assert(!('password' in detail.user));
assert(!('totp' in detail.user));
console.log('PASS explicit local administrator promotion and metadata-only user detail');

const installer = new Browser();
const { data: enrollment } = await installer.request('POST', '/api/enrollment/start', { name: 'Local protocol fixture' });
const credentials = { request_id: enrollment.request_id, claim_token: enrollment.claim_token };
await installer.request('POST', '/api/enrollment/claim', credentials, 202);
const { data: preview } = await member.request('GET', `/api/enrollment/preview?code=${encodeURIComponent(enrollment.code)}`);
assert.equal(preview.name, 'Local protocol fixture');
await member.request('POST', '/api/enrollment/confirm', { code: enrollment.code });
await admin.request('POST', '/api/enrollment/confirm', { code: enrollment.code }, 409);
await installer.request('POST', '/api/enrollment/claim', { ...credentials, claim_token: 'forged' }, 404);
const ready = await fetch(base + '/readyz');
const { data: grant } = await installer.request('POST', '/api/enrollment/claim', credentials, ready.status === 200 ? 200 : 503);
if (ready.status === 200) {
  assert(grant.authKey && grant.index);
  const { data: repeated } = await installer.request('POST', '/api/enrollment/claim', credentials);
  assert.equal(repeated.authKey, grant.authKey);
  assert.equal(repeated.device_token, grant.device_token);
  assert.equal(repeated.proxy_token, grant.proxy_token);
  assert.notEqual(grant.device_token, grant.proxy_token);
  assert.equal(grant.owner_email, user.email);
  for (const [token, expected] of [[grant.device_token, 202], [grant.proxy_token, 401]]) {
    const response = await fetch(base + '/api/device/status', { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(5000) });
    assert.equal(response.status, expected);
  }
  console.log('PASS real Headscale one-use key issuance and idempotent claim');
  console.log('PASS private device/proxy credentials, token direction isolation and installing lease denial');
} else {
  console.log('PASS missing Headscale returns 503 rather than fake successful enrollment');
}
const { data: devices } = await member.request('GET', '/api/devices');
assert.equal(devices.devices.length, 1);
assert(!JSON.stringify(devices).includes(grant.device_token || 'never-present-secret'));
assert(!JSON.stringify(devices).includes(grant.proxy_token || 'never-present-secret'));
const { data: adminDevices } = await admin.request('GET', '/api/devices');
assert.equal(adminDevices.devices.length, 0);
console.log('PASS browser-confirmed owner, claim-token protection, cross-account device isolation');

const { data: key } = await member.request('POST', '/api/automation/access-keys', { name: 'Local key', rpm: 10 }, 201);
assert.equal(typeof key.key, 'string');
const validKeyResponse = await fetch(base + '/api/v1/messages/not-a-message', { headers: { Authorization: `Bearer ${key.key}` } });
assert.equal(validKeyResponse.status, 404);
const { data: ownerKeys } = await admin.request('GET', '/api/automation/access-keys');
assert.equal(ownerKeys.keys.length, 0);
await admin.request('PATCH', `/api/admin/users/${user.id}`, { status: 'disabled' });
await member.request('GET', '/api/auth/me', undefined, 401);
if (grant.device_token) {
  const response = await fetch(base + '/api/device/status', { headers: { Authorization: `Bearer ${grant.device_token}` }, signal: AbortSignal.timeout(5000) });
  assert.equal(response.status, 403);
}
const automation = await fetch(base + '/api/v1/messages', { method: 'POST',
  headers: { Authorization: `Bearer ${key.key}`, 'Content-Type': 'application/json' },
  body: JSON.stringify({ device: 'm1', project: '/', message: 'must not execute' }) });
assert.equal(automation.status, 401);
console.log('PASS isolated automation keys and immediate disabled-user session/API denial');

if (process.argv.includes('--keep-session')) {
  writeFileSync(path.join(work, 'restart-session.json'), JSON.stringify({ cookie: admin.cookie, csrf: admin.csrf, id: owner.id }), { mode: 0o600 });
  console.log('PASS authenticated session saved privately for restart persistence verification');
} else {
  await admin.request('POST', '/api/auth/logout');
  await admin.request('GET', '/api/auth/me', undefined, 401);
}
await installer.request('POST', '/enroll/join', { code: '000000' }, 410);
writeFileSync(path.join(work, 'smoke-summary.json'), JSON.stringify({ checked_at: new Date().toISOString(), admin_email: owner.email, member_id: user.id, passed: true }, null, 2), { mode: 0o600 });
console.log('PASS retired global enrollment path; local multi-user smoke complete');
