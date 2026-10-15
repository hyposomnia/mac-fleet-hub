import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const appearanceSource = await readFile(new URL('./device_appearance.js', import.meta.url), 'utf8');
const source = await readFile(new URL('./account.js', import.meta.url), 'utf8').catch(() => '');
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.attributes = {}; this.dataset = {}; this.value = ''; this.checked = false; this.disabled = false; this.textContent = ''; }
  append(...children) { for (const child of children) { this.children.push(child); if (child && typeof child === 'object') child.parentElement = this; } }
  appendChild(child) { this.append(child); return child; }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  insertBefore(child, before) { const old = this.children.indexOf(child); if (old >= 0) this.children.splice(old, 1); const index = this.children.indexOf(before); this.children.splice(index < 0 ? this.children.length : index, 0, child); child.parentElement = this; }
  setAttribute(key, value) { this.attributes[key] = String(value); }
  getAttribute(key) { return this.attributes[key]; }
  focus() { this.focused = true; }
  showModal() { this.open = true; this.modal = true; }
  close() { this.open = false; this.onclose?.(); }
  getBoundingClientRect() { return { top: 0, height: 40 }; }
}
function text(node) { return typeof node === 'string' ? node : (node?.textContent || '') + (node?.children || []).map(text).join(' '); }
function all(node, predicate) { return [node, ...(node.children || []).flatMap((child) => typeof child === 'string' ? [] : all(child, predicate))].filter(predicate); }
function harness({ page = 'auth', path = '/auth', search = '', role = 'user', respond, confirmed = true, origin = 'https://fleet.test', released = true, transformRelease = value => value, embedded = false, devices = [], appearances = {}, withNav = true } = {}) {
  assert.ok(source, 'shared account page implementation is missing');
  const clipboard = [];
  const sandbox = { URL, URLSearchParams, AbortController, setTimeout, clearTimeout, performance,
    navigator: { clipboard: { writeText: async value => { clipboard.push(value); } } },
    fetch: async () => ({ ok: true, json: async () => ({ ok: true }) }) };
  vm.runInNewContext(source, sandbox);
  const content = new Element('main');
  const status = new Element('p');
  const nav = new Element('nav');
  const document = { createElement: (tag) => new Element(tag), querySelector: (selector) => ({ '#page-content': content, '#page-status': status, '#page-nav': withNav ? nav : null })[selector], documentElement: { dataset: {} } };
  sandbox.document = { ...document, createElementNS: (_ns, tag) => new Element(tag) };
  vm.runInContext(appearanceSource, sandbox);
  const calls = [];
  const redirects = [];
  const location = { origin, pathname: path, search, replace: (target) => redirects.push(target) };
  const auth = { user: { id: 'u1', email: 'one@example.com', role, status: 'active' },
    setRecoveryPending: value => { auth.recoveryPending = value; },
    resumeAuth: async () => ({state: location.pathname.endsWith('/setup') ? 'setup' : 'challenge'}),
    me: async () => { calls.push({ url: '/api/auth/me' }); return { user: auth.user, csrf_token: 'token' }; },
    safeNext: (target) => target?.startsWith('/') && !target.startsWith('//') ? target : '/',
    json: async (url, options = {}) => {
      calls.push({ url, ...options });
      if (url === '/enroll/client-release.json') {
        if (!released) throw new Error('not published');
        return transformRelease({ schema: 1, notarization: 'Accepted', bundle_id: 'com.macfleet.fleet-hub', version: '1.0.0', build: 1, minimum_macos: '13.0', architectures: ['arm64','x86_64'],
          assets: { dmg: { path: '/enroll/clients/1/Fleet-Hub.dmg', sha256: 'a'.repeat(64), size: 12345678 }, update: { path: '/enroll/clients/1/Fleet-Hub-update.zip', sha256: 'b'.repeat(64), size: 12345678, ed_signature: Buffer.alloc(64, 1).toString('base64') } } });
      }
      if (respond) return respond(url, options);
      if (url === '/api/devices') return { devices };
      if (url === '/api/settings') return { deviceAppearance: appearances, deviceOrder: devices.map(device => device.id) };
      return { sessions: [], users: [], total: 0, page: 1, page_size: 20 };
    }, logout: async () => { calls.push({ url: '/api/auth/logout', method: 'POST' }); } };
  auth.invalidate = (options) => { redirects.push(`/auth?next=${encodeURIComponent(options?.returnTo || '/')}`); };
  const history = { replaceState: (_, __, target) => {
    const url = new URL(target, location.origin); location.pathname = url.pathname; location.search = url.search;
  } };
  const pages = sandbox.FleetAccountPages.createPages({ document: embedded ? { ...document, querySelector: () => null } : document,
    auth, location, history, confirm: () => confirmed, ...(embedded ? { content, status, nav } : {}) });
  const find = (predicate) => all(content, predicate)[0];
  const form = (name) => find((node) => node.tagName === 'form' && node.attributes['data-form'] === name);
  const submit = async (name, values) => {
    const node = form(name);
    assert.ok(node, `form ${name} missing`);
    for (const [key, value] of Object.entries(values || {})) {
      const input = all(node, (child) => child.name === key)[0];
      assert.ok(input, `field ${key} missing`);
      if (typeof value === 'boolean') input.checked = value;
      else input.value = value;
    }
    await node.onsubmit({ preventDefault() {} });
  };
  const click = async (label) => {
    const button = find((node) => node.tagName === 'button' && text(node) === label);
    assert.ok(button, `button ${label} missing`);
    await button.onclick();
  };
  return { pages, content, status, nav, calls, redirects, location, auth, clipboard, find, form, submit, click, start: () => pages.start(page) };
}

test('registration has exactly three fields, sends exact contract, and requires Authenticator setup', async () => {
  const current = harness({ path: '/auth/register', respond: () => ({ state: 'setup', totp: { secret: 'TEST-ONLY', uri: 'otpauth://totp/test' } }) });
  await current.start();
  assert.deepEqual(all(current.form('register'), (node) => node.tagName === 'input').map((node) => node.name), ['email', 'password', 'confirm_password']);
  const payload = { email: 'one@example.com', password: 'password', confirm_password: 'password' };
  await current.submit('register', payload);
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls[0].body)), payload);
  assert.equal(current.calls[0].url, '/api/auth/register');
  assert.equal(current.location.pathname, '/auth/setup');
  assert.match(text(current.content), /Authenticator/);
  assert.equal(current.find((node) => node.tagName === 'img').src, '/api/auth/qr');
  const dialog = current.find(node => node.tagName === 'dialog');
  assert.equal(dialog.modal, true);
  assert.equal(dialog.attributes.closedby, 'none');
  assert.equal(dialog.attributes['aria-modal'], 'true');
  assert.equal(dialog.attributes['aria-labelledby'], current.find(node => node.tagName === 'h1').id);
  let cancelled = false;
  dialog.oncancel({ preventDefault() { cancelled = true; } });
  assert.equal(cancelled, true);
  assert.equal(dialog.open, true);
  assert.equal(dialog.onclick, undefined, 'backdrop clicks must not dismiss setup');
  assert.equal(current.pages.canLeave(), false);
  assert.equal(current.redirects.length, 0);
});

test('registration modal stays open through verification failures and recovery acknowledgment', async () => {
  const current = harness({ path: '/auth/setup', respond: (_, { body }) => {
    if (body.code !== '123456') throw Error('验证码错误');
    return { user: { id: 'u1' }, recovery_codes: ['TEST-ONLY'] };
  } });
  await current.start();
  const dialog = current.find(node => node.tagName === 'dialog');
  await current.submit('verify', { code: '000000' });
  assert.equal(dialog.open, true);
  assert.ok(current.form('verify'));
  await current.submit('verify', { code: '123456' });
  assert.ok(current.form('recovery-ack'));
  assert.equal(dialog.open, true);
  assert.equal(current.pages.canLeave(), false);
  assert.equal(current.find(node => node.tagName === 'h1').id, dialog.attributes['aria-labelledby']);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.redirects[0], '/');
});

test('account inputs use valid autocomplete and security forms identify the current account without changing payloads', async () => {
  const current = harness({ page: 'account' });
  await current.start();
  const inputs = all(current.content, (node) => node.tagName === 'input');
  assert.ok(inputs.every((input) => input.autocomplete));
  for (const name of ['password', 'totp-start', 'recovery-codes']) {
    const username = all(current.form(name), (node) => node.autocomplete === 'username')[0];
    assert.ok(username, `${name} must identify the current account for password managers`);
    assert.equal(username.value, current.auth.user.email);
    assert.equal(username.hidden, true);
  }
  const payload = { password: 'test-password', code: '123456' };
  await current.submit('totp-start', payload);
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls.at(-1).body)), payload);
});

test('account security sections are expanded cards in settings and disclosures on the standalone page', async () => {
  const titles = ['登录设备', '更改密码', '更换 Authenticator', '重新生成恢复码'];
  for (const embedded of [false, true]) {
    const current = harness({ page: 'account', embedded });
    await current.start();
    const sections = all(current.content, (node) => String(node.className || '').split(/\s+/).includes('account-section'));
    assert.deepEqual(sections.map((node) => text(node.children[0])), titles);
    assert.deepEqual(sections.map((node) => node.tagName), Array(4).fill(embedded ? 'section' : 'details'));
    assert.deepEqual(sections.map((node) => node.children[0].tagName), Array(4).fill(embedded ? 'h2' : 'summary'));
    assert.equal(all(current.content, (node) => node.tagName === 'details').length, embedded ? 0 : 4);
    for (const name of ['password', 'totp-start', 'recovery-codes']) {
      assert.ok(sections.some((section) => all(section, (node) => node === current.form(name)).length));
    }
    if (embedded) {
      const header = current.find((node) => node.className === 'settings-page-header');
      assert.equal(header.children[0].className, 'settings-page-title');
      assert.equal(text(header.children[0]), '账号设置');
    }
    assert.equal(current.find((node) => node.id === 'devices'), undefined);
    assert.match(text(sections[3]), /旧恢复码将立即失效/);
  }
});

test('controls keep accent focus cues without yellow highlight borders', async () => {
  const accountCSS = await readFile(new URL('./account.css', import.meta.url), 'utf8');
  const styleCSS = await readFile(new URL('./style.css', import.meta.url), 'utf8');
  assert.doesNotMatch(accountCSS, /\.account-form input:focus-visible,\s*\.account-button/);
  assert.match(accountCSS, /\.account-form input:not\(\[type="checkbox"\]\):focus-visible\s*\{\s*outline:\s*none;\s*box-shadow:\s*none;/);
  assert.match(accountCSS, /\.account-button:focus-visible,\s*\.account-page a:focus-visible\s*\{\s*outline:\s*2px solid[^}]*box-shadow:\s*none;/);
  assert.match(accountCSS, /\.account-disclosure > summary:focus-visible\s*\{\s*outline:\s*2px solid/);
  const globalFocus = styleCSS.slice(styleCSS.indexOf('\n:focus-visible {') + 1).split('}')[0];
  assert.match(globalFocus, /box-shadow:\s*none;/);
  assert.doesNotMatch(globalFocus, /highlight/);
  const emptyComposerInput = styleCSS.slice(styleCSS.indexOf('.empty-chat-compose textarea')).split('}')[0];
  assert.match(emptyComposerInput, /outline:\s*none;/);
  assert.match(emptyComposerInput, /box-shadow:\s*none;/);
  assert.match(styleCSS, /\.empty-chat-context select\s*\{[^}]*background:\s*color-mix\(in srgb,\s*var\(--chat-text\) 4%,\s*transparent\);/);
  for (const selector of ['.input:focus', '.session-searchbar:focus-within', '.chat-request-input:focus', '#cmd-input:focus', '.file-searchbar:focus-within']) {
    const declaration = styleCSS.slice(styleCSS.indexOf(selector)).split('}')[0];
    assert.match(declaration, /outline:\s*none;/);
  }
});

test('account login metadata formats Unix timestamps and does not show unknown dates as epoch', async () => {
  const timestamp = 1800000000;
  const current = harness({ page: 'account', respond: () =>
    ({ sessions: [{ id: 'session', created_at: timestamp, last_seen: timestamp, expires_at: timestamp + 2592000 }] }) });
  await current.start();
  assert.match(text(current.content), /2027/);
  assert.doesNotMatch(text(current.content), /1800000000|1802592000|1970/);
  assert.match(text(current.content), /—/);
});

test('password login challenge never opens dashboard before OTP verification', async () => {
  const current = harness({ respond: (url) => url.endsWith('/login') ? { state: 'challenge' } : { user: { id: 'u1' } } });
  await current.start();
  await current.submit('login', { email: 'one@example.com', password: 'password' });
  assert.equal(current.redirects.length, 0);
  assert.equal(current.location.pathname, '/auth/verify');
  await current.submit('verify', { code: '123456' });
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls[1].body)), { code: '123456' });
  assert.equal(current.redirects[0], '/');
});

test('setup verification shows recovery codes only in DOM and requires acknowledgment, preserving enrollment next', async () => {
  const current = harness({ path: '/auth/setup', search: '?next=%2Fenroll%2Fconfirm%3Fcode%3Dabc', respond: () => ({ user: { id: 'u1' }, recovery_codes: ['TEST-CODE'] }) });
  await current.start();
  await current.submit('verify', { code: '123456' });
  assert.match(text(current.content), /TEST-CODE/);
  await current.submit('recovery-ack');
  assert.equal(current.redirects.length, 0);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.redirects[0], '/enroll/confirm?code=abc');
  assert.doesNotMatch(source, /(?:localStorage|sessionStorage)\.(?:setItem|getItem)/);
});

test('recovery sends email, password and recovery_code then forces new Authenticator setup', async () => {
  const current = harness({ path: '/auth/recovery', respond: () => ({ state: 'setup', totp: { secret: 'TEST-SECRET' } }) });
  await current.start();
  const payload = { email: 'one@example.com', password: 'password', recovery_code: 'TEST-CODE' };
  await current.submit('recover', payload);
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls[0].body)), payload);
  assert.equal(current.calls[0].url, '/api/auth/recover');
  assert.ok(current.form('verify'));
});

test('form errors are inline and re-enable submission; mismatched passwords never hit transport', async () => {
  const current = harness({ path: '/auth/register', respond: () => { throw new Error('Too many attempts'); } });
  await current.start();
  await current.submit('register', { email: 'one@example.com', password: 'password', confirm_password: 'different' });
  assert.equal(current.calls.length, 0);
  await current.submit('register', { confirm_password: 'password' });
  assert.match(text(current.content), /Too many attempts/);
  assert.equal(current.find((node) => node.tagName === 'button' && node.type === 'submit').disabled, false);
});

test('account authenticates first and sends password and recovery rotation contracts', async () => {
  const current = harness({ page: 'account', path: '/account', respond: (url) => url.endsWith('recovery-codes') ? { recovery_codes: ['NEW-CODE'] } : { sessions: [{ id: 's1', current: true }] } });
  await current.start();
  assert.equal(current.calls[0].url, '/api/auth/me');
  assert.ok(!current.calls.some(call => call.url === '/api/devices'));
  const password = { current_password: 'old', password: 'new-password', confirm_password: 'new-password', code: '123456' };
  await current.submit('password', password);
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls.find((call) => call.url.endsWith('/password')).body)), password);
  await current.submit('recovery-codes', { password: 'new-password', code: '123456' });
  assert.match(text(current.content), /NEW-CODE/);
});

test('account TOTP rotation uses start then confirm and displays new recovery codes', async () => {
  const current = harness({ page: 'account', respond: (url) => url.endsWith('/start') ? { secret: 'ROTATED' } : url.endsWith('/confirm') ? { recovery_codes: ['ROTATED-CODE'] } : { devices: [], sessions: [] } });
  await current.start();
  await current.submit('totp-start', { password: 'password', code: '123456' });
  assert.equal(current.calls.at(-1).url, '/api/auth/totp/start');
  assert.match(text(current.content), /ROTATED/);
  await current.submit('totp-confirm', { code: '654321' });
  assert.equal(current.calls.at(-1).url, '/api/auth/totp/confirm');
  assert.match(text(current.content), /ROTATED-CODE/);
});

test('device removal and other session revocation require confirmation and use writes', async () => {
  const devicePage = harness({ page: 'add-device', devices: [{ id: 'm1', name: 'My Mac' }] });
  await devicePage.start();
  await devicePage.click('移除设备');
  assert.ok(devicePage.calls.some((call) => call.url === '/api/devices/m1' && call.method === 'DELETE'));
  const current = harness({ page: 'account', respond: () => ({ sessions: [] }) });
  await current.start();
  await current.click('退出其他设备的登录');
  assert.ok(current.calls.some((call) => call.url === '/api/auth/logout-others' && call.method === 'POST'));
  const cancelled = harness({ page: 'account', confirmed: false });
  await cancelled.start();
  await cancelled.click('退出其他设备的登录');
  assert.equal(cancelled.calls.filter((call) => call.method).length, 0);
});

test('admin denies ordinary users without querying admin APIs', async () => {
  const current = harness({ page: 'admin', path: '/admin' });
  await current.start();
  assert.match(text(current.content), /管理员/);
  assert.equal(current.calls.length, 1);
});

test('admin shows counts, searches and paginates metadata, with no content endpoints or injected markup', async () => {
  const current = harness({ page: 'admin', role: 'admin', respond: () => ({ users: [{ id: 'u2', email: '<img onerror=evil>', online: false, device_count: 0 }], total: 25, page: 1, page_size: 20 }) });
  await current.start();
  assert.match(text(current.content), /25/);
  assert.match(text(current.content), /<img onerror=evil>/);
  assert.equal(current.find((node) => node.tagName === 'img'), undefined);
  await current.submit('admin-users-search', { search: 'a+b@example.com' });
  assert.ok(current.calls.some((call) => call.url === '/api/admin/users?search=a%2Bb%40example.com&page=1'));
  await current.click('用户下一页');
  assert.ok(current.calls.some((call) => call.url.endsWith('&page=2')));
  assert.ok(current.calls.every((call) => !/\/m\d+\/|chat|files|messages/.test(call.url)));
});

test('ADM-1/3/5 admin lists presence and device counts without frontend navigation or management actions', async () => {
  const current = harness({ page: 'admin', role: 'admin', respond: () => ({ users: [
    { id: 2, email: 'two@example.com', online: true, device_count: 3 },
    { id: 3, email: 'three@example.com', online: false, device_count: 0 },
  ], total: 2, page: 1, page_size: 20 }) });
  await current.start();
  assert.deepEqual(current.nav.children, []);
  const rows = all(current.content, node => node.tagName === 'tr' && text(node).includes('@example.com'));
  assert.match(text(rows[0]), /two@example.com.*在线.*3/);
  assert.match(text(rows[1]), /three@example.com.*离线.*0/);
  assert.ok(current.calls.every(call => call.url === '/api/auth/me' || call.url.startsWith('/api/admin/users?')));
  assert.equal(all(current.content, node => node.tagName === 'a').length, 0);
  const standalone = harness({ page: 'admin', role: 'admin', withNav: false });
  await standalone.start();
});

test('ADM-4/5 admin drawer shows only selected user devices and paginated event metadata', async () => {
  const current = harness({ page: 'admin', role: 'admin', respond: (url) => url.startsWith('/api/admin/users/u2') ? {
    user: { id: 'u2', email: 'two@example.com', online: true, device_count: 2 },
    devices: [{ id: 'm1', name: 'Online Mac', status: 'active', online: true },
      { id: 'm2', name: 'Offline Mac', status: 'unavailable', online: true },
      { id: 'm3', name: 'Revoked Mac', status: 'revoked', online: true }],
    events: [{ action: url.includes('event_page=2') ? 'older_event' : 'login', created_at: '2026-09-30', message: 'PRIVATE-CONTENT' }],
    event_page: url.includes('event_page=2') ? 2 : 1, event_total: 51, event_page_size: 50,
  } : { users: [{ id: 'u2', email: 'two@example.com', online: true, device_count: 2 }], total: 1, page: 1, page_size: 20 } });
  await current.start();
  await current.click('two@example.com');
  const drawer = current.find(node => node.tagName === 'dialog');
  assert.equal(drawer.open, true);
  assert.equal(drawer.attributes['aria-modal'], 'true');
  assert.ok(all(drawer, node => node.id === drawer.attributes['aria-labelledby']).length);
  assert.match(text(drawer), /Online Mac.*在线/);
  assert.match(text(drawer), /Offline Mac.*离线/);
  assert.match(text(drawer), /login/);
  assert.doesNotMatch(text(drawer), /PRIVATE-CONTENT|Revoked Mac|停用|启用|撤销|移除/);
  await current.click('事件下一页');
  assert.ok(current.calls.some(call => call.url === '/api/admin/users/u2?event_page=2'));
  assert.match(text(drawer), /older_event/);
  assert.equal(current.find(node => node.tagName === 'button' && text(node) === '事件下一页').disabled, true);
  assert.ok(current.calls.every(call => !call.method));
  await current.click('关闭');
  assert.equal(drawer.open, false);
  assert.equal(current.find(node => node.tagName === 'button' && text(node) === 'two@example.com').focused, true);
});

test('ADM-4 closing the drawer discards delayed responses, including a prior request for the same user', async () => {
  const pending = [];
  const current = harness({ page: 'admin', role: 'admin', respond: url => url === '/api/admin/users/u2' ?
    new Promise(resolve => pending.push(resolve)) : { users: [{ id: 'u2', email: 'two@example.com', online: false, device_count: 0 }], total: 1, page: 1, page_size: 20 } });
  await current.start();
  const first = current.click('two@example.com');
  const drawer = current.find(node => node.tagName === 'dialog');
  assert.equal(drawer.open, true);
  drawer.close();
  const second = current.click('two@example.com');
  pending[1]({ user: { id: 'u2', email: 'two@example.com' }, devices: [], events: [{ action: 'latest_event' }], event_total: 1, event_page: 1, event_page_size: 50 });
  await second;
  pending[0]({ user: { id: 'u2', email: 'stale@example.com' }, devices: [], events: [], event_total: 0, event_page: 1, event_page_size: 50 });
  await first;
  assert.match(text(drawer), /latest_event/);
  assert.doesNotMatch(text(drawer), /stale@example.com/);
  assert.match(text(drawer), /暂无设备/);
});

test('ADM-4 drawer failures stay open and a retry recovers', async () => {
  let attempt = 0;
  const current = harness({ page: 'admin', role: 'admin', respond: url => {
    if (url === '/api/admin/users/u2') {
      if (++attempt === 1) throw Error('服务暂时不可用');
      return { user: { id: 'u2', email: 'two@example.com' }, devices: [], events: [], event_total: 0, event_page: 1, event_page_size: 50 };
    }
    return { users: [{ id: 'u2', email: 'two@example.com', online: false, device_count: 0 }], total: 1, page: 1, page_size: 20 };
  } });
  await current.start();
  await current.click('two@example.com');
  const drawer = current.find(node => node.tagName === 'dialog');
  assert.match(text(drawer), /服务暂时不可用/);
  assert.equal(drawer.open, true);
  await current.click('重试');
  assert.match(text(drawer), /暂无设备/);
  assert.doesNotMatch(text(drawer), /服务暂时不可用/);
});

test('enrollment reads return query, authenticates, and confirms only after explicit click', async () => {
  const current = harness({ page: 'enrollment', path: '/enroll/confirm', search: '?code=one%2Btwo', respond: () => ({ name: 'My private Mac', state: 'pending' }) });
  await current.start();
  assert.deepEqual(current.calls.map((call) => call.url), ['/api/auth/me', '/api/enrollment/preview?code=one%2Btwo']);
  assert.match(text(current.content), /My private Mac/);
  await current.submit('enrollment-confirm');
  assert.equal(current.calls[2].url, '/api/enrollment/confirm');
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls[2].body)), { code: 'one+two' });
  assert.match(text(current.content), /已确认/);
  const missing = harness({ page: 'enrollment' });
  await missing.start();
  assert.equal(missing.form('enrollment-confirm'), undefined);
});

test('native OAuth consent uses the authenticated account, no pairing code, and a loopback callback', async () => {
  const current = harness({ page: 'enrollment', path: '/oauth/consent', search: '?request=opaque-request', respond: (url) =>
    url.startsWith('/api/oauth/preview') ? { name: 'My Mac', owner_email: 'one@example.com', redirect_uri: 'http://127.0.0.1:51234/oauth/callback' } :
      { redirect_uri: 'http://127.0.0.1:51234/oauth/callback?state=state&code=authorization-code' } });
  await current.start();
  assert.match(text(current.content), /授权/);
  assert.doesNotMatch(text(current.content), /配对码/);
  assert.equal(current.calls[1].url, '/api/oauth/preview?request=opaque-request');
  await current.submit('oauth-consent');
  assert.deepEqual(JSON.parse(JSON.stringify(current.calls[2].body)), { request_id: 'opaque-request', action: 'approve' });
  assert.deepEqual(current.redirects, ['http://127.0.0.1:51234/oauth/callback?state=state&code=authorization-code']);
});

test('admin pagination disables both boundaries after a click rather than re-enabling them', async () => {
  const current = harness({ page: 'admin', role: 'admin', respond: (url) => {
    if (!url.includes('/users?')) return { devices: [], users: [], total: 0 };
    const page = Number(new URL(url, 'https://fleet.test').searchParams.get('page'));
    return { users: Array.from({ length: page === 1 ? 2 : 1 }, (_, index) => ({ id: `u${page}-${index}`, email: `user${page}-${index}@example.com` })), total: 3, page, page_size: 2 };
  } });
  await current.start();
  await current.click('用户下一页');
  assert.equal(current.find((node) => node.tagName === 'button' && text(node) === '用户下一页').disabled, true);
  await current.click('用户上一页');
  assert.equal(current.find((node) => node.tagName === 'button' && text(node) === '用户上一页').disabled, true);
});

test('recovery acknowledgment and TOTP pages prevent duplicate confirmation while the mutation is pending', async () => {
  let finish;
  const current = harness({ path: '/auth/verify', respond: () => new Promise((resolve) => { finish = resolve; }) });
  await current.start();
  const first = current.submit('verify', { code: '123456' });
  await current.submit('verify', { code: '123456' });
  assert.equal(current.calls.length, 1);
  finish({ user: { id: 'u1' } });
  await first;
});

test('TOTP rotation retains the current session and preserves recovery codes until acknowledged', async () => {
  const current = harness({ page: 'account', respond: (url) => url.endsWith('/start') ? { secret: 'NEW' } : url.endsWith('/confirm') ? { recovery_codes: ['SAVE-ME'], login_required: false } : { devices: [], sessions: [] } });
  await current.start();
  await current.submit('totp-start', { password: 'password', code: '123456' });
  await current.submit('totp-confirm', { code: '654321' });
  assert.match(text(current.content), /SAVE-ME/);
  assert.equal(current.auth.recoveryPending, true);
  assert.equal(current.redirects.length, 0);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.auth.recoveryPending, false);
  assert.equal(current.redirects.length, 0);
});

test('add device offers a native DMG and explains fresh browser OAuth and background disk authorization', async () => {
  const current = harness({ page: 'add-device' });
  await current.start();
  assert.doesNotMatch(text(current.content), /bootstrap\.sh|curl -fsSL/);
  const add = current.find((node) => node.id === 'add-device');
  const downloads = all(add, (node) => node.tagName === 'a' && node.download);
  assert.deepEqual(downloads.map((node) => node.href), ['/enroll/clients/1/Fleet-Hub.dmg']);
  assert.equal(downloads[0].download, 'Fleet-Agent-Settings.dmg');
  assert.equal(text(downloads[0]), '下载 Fleet Agent Settings');
  assert.match(text(add), /11\.8 MB/);
  assert.doesNotMatch(text(add), /fleet-agent login|Homebrew|tar -|回到终端|输入 y/);
  assert.match(text(add), /https:\/\/fleet.test/);
  assert.match(text(add), /应用.*服务网页地址/);
  assert.match(text(add), /浏览器.*登录.*Authenticator/);
  assert.match(text(add), /关联账号.*打开网页授权/);
  assert.match(text(add), /授权.*自动接入/);
  assert.doesNotMatch(text(add), /配对码|确认接入|保存并连接/);
  assert.doesNotMatch(text(add), /fleet-agent 发起|Developer ID|Apple 公证|本服务管理员|文件本身的权限仍然生效/);
  assert.match(text(add), /完全磁盘访问/);
  assert.match(text(add), /Fleet Agent.app/);
  assert.match(text(add), /内置 Fleet Agent.*无需另行下载/);
  assert.match(text(add), /安装并启动/);
  assert.match(text(add), /页面中.*拖入.*打开开关/);
  assert.doesNotMatch(text(add), /开启 Fleet Hub\.app/);
  assert.match(text(add), /关闭.*后台.*运行/);
  assert.match(text(add), /检查更新/);
  assert.ok(!current.calls.some((call) => call.url === '/api/enrollment/start'), 'web page must not initiate a client grant');
  assert.equal(current.form('enrollment-code'), undefined);
});

test('device page shows the ordered device manager before installation instructions', async () => {
  const current = harness({ page: 'add-device', devices: [
    { id: 'm2', name: 'Studio', ip: '100.64.0.22', online: true },
    { id: 'm1', name: 'Laptop', ip: '100.64.0.11', online: false },
  ], appearances: { m2: { icon: 'laptop', color: 'teal' } } });
  await current.start();
  const devices = current.find(node => node.id === 'devices');
  const install = current.find(node => node.id === 'add-device');
  assert.ok(devices && install);
  assert.ok(current.content.children.indexOf(devices) < current.content.children.indexOf(install));
  const rows = all(devices, node => node.className === 'account-device-row');
  assert.deepEqual(rows.map(row => row.dataset.deviceId), ['m2', 'm1']);
  assert.match(text(rows[0]), /Studio.*100\.64\.0\.22.*在线/);
  assert.match(text(rows[1]), /Laptop.*100\.64\.0\.11.*离线/);
  assert.ok(rows.every(row => row.draggable === true));
});

test('device editor saves the existing name and appearance contracts', async () => {
  const current = harness({ page: 'add-device', devices: [{ id: 'm2', name: 'Studio', ip: '100.64.0.22', online: true }],
    appearances: { m2: { icon: 'monitor', color: 'steel' } } });
  await current.start();
  const row = current.find(node => node.className === 'account-device-row');
  const edit = all(row, node => node.tagName === 'button' && node.textContent === '编辑')[0];
  await edit.onclick();
  const name = all(row, node => node.name === 'device-name')[0];
  name.value = 'Design Mac';
  const icon = all(row, node => node.dataset?.deviceIcon === 'laptop')[0];
  icon.onclick();
  const color = all(row, node => node.dataset?.deviceColorChoice === 'teal')[0];
  color.onclick();
  const save = all(row, node => node.tagName === 'button' && node.textContent === '保存')[0];
  await save.onclick();
  const nameCall = current.calls.find(call => call.url === '/api/names' && call.method === 'POST');
  const appearanceCall = current.calls.find(call => call.url === '/api/settings' && call.method === 'PATCH' && call.body?.appearance);
  assert.deepEqual(JSON.parse(JSON.stringify(nameCall.body)), { id: 'm2', name: 'Design Mac' });
  assert.deepEqual(JSON.parse(JSON.stringify(appearanceCall.body)), { id: 'm2', appearance: { icon: 'laptop', color: 'teal' } });
});

test('dragging devices persists their order', async () => {
  const current = harness({ page: 'add-device', devices: [{ id: 'm1', name: 'One' }, { id: 'm2', name: 'Two' }] });
  await current.start();
  const list = current.find(node => node.className === 'account-device-list');
  const [first, second] = list.children;
  second.ondragstart({ dataTransfer: { effectAllowed: '', setData() {} } });
  first.ondragover({ preventDefault() {}, clientY: 0 });
  let dropAllowed = false;
  const dataTransfer = { dropEffect: '' };
  list.ondragover({ preventDefault() { dropAllowed = true; }, dataTransfer });
  assert.equal(dropAllowed, true);
  assert.equal(dataTransfer.dropEffect, 'move');
  await list.ondrop({ preventDefault() {} });
  assert.deepEqual(list.children.map(row => row.dataset.deviceId), ['m2', 'm1']);
  const orderCall = current.calls.find(call => call.url === '/api/settings' && call.method === 'PATCH' && call.body?.order);
  assert.deepEqual(JSON.parse(JSON.stringify(orderCall.body)), { order: ['m2', 'm1'] });
});

test('canceling a device drag restores the saved order without persisting', async () => {
  const current = harness({ page: 'add-device', devices: [{ id: 'm1', name: 'One' }, { id: 'm2', name: 'Two' }] });
  await current.start();
  const list = current.find(node => node.className === 'account-device-list');
  const [first, second] = list.children;
  second.ondragstart({ dataTransfer: { effectAllowed: '', setData() {} } });
  first.ondragover({ preventDefault() {}, clientY: 0 });
  second.ondragend();
  assert.deepEqual(list.children.map(row => row.dataset.deviceId), ['m1', 'm2']);
  assert.ok(!current.calls.some(call => call.url === '/api/settings' && call.method === 'PATCH' && call.body?.order));
});

test('login and device setup omit redundant introductory copy without dropping account identity or security reminders', async () => {
  const login = harness();
  await login.start();
  assert.doesNotMatch(text(login.content), /连接和管理你的设备/);
  const addition = harness({ page: 'add-device', embedded: true });
  await addition.start();
  assert.doesNotMatch(text(addition.content), /安装 Fleet Hub，使用当前账号关联这台 Mac|授权请求由/);
  assert.match(text(addition.content), /Fleet Agent Settings.*检查更新/);
  assert.doesNotMatch(text(addition.content), /Fleet Hub.*无需磁盘权限/);
  const account = harness({ page: 'account', embedded: true });
  await account.start();
  assert.match(text(account.content), /one@example.com/);
  assert.match(text(account.content), /旧恢复码.*失效/);
});

test('unpublished clients do not produce fake download links', async () => {
  const current = harness({ page: 'add-device', released: false });
  await current.start();
  const add = current.find((node) => node.id === 'add-device');
  assert.equal(all(add, (node) => node.tagName === 'a' && node.download).length, 0);
  assert.match(text(add), /尚未发布/);
  assert.equal(current.form('enrollment-code'), undefined);
});

test('invalid or cross-origin native releases never expose download links', async () => {
  for (const mutate of [
    release => { release.assets.dmg.path = 'https://other.test/Fleet-Hub.dmg'; },
    release => { release.notarization = 'In Progress'; },
    release => { release.assets.update.ed_signature = 'fake'; },
    release => { release.assets.dmg.sha256 = ''; },
    release => { release.bundle_id = 'other.application'; },
  ]) {
    const current = harness({ page: 'add-device', transformRelease: release => { mutate(release); return release; } });
    await current.start();
    const add = current.find(node => node.id === 'add-device');
    assert.equal(all(add, node => node.tagName === 'a' && node.download).length, 0);
  }
});

test('native installation instructions use the current service origin', async () => {
  for (const origin of ['https://other.example.com:8443', 'https://192.0.2.10:9443']) {
    const current = harness({ page: 'add-device', origin });
    await current.start();
    const add = current.find((node) => node.id === 'add-device');
    assert.ok(text(add).includes(origin));
    assert.equal(all(add, (node) => node.tagName === 'pre').length, 0);
    assert.ok(all(add, (node) => node.tagName === 'a' && node.href === origin).length);
    const copy = all(add, (node) => node.tagName === 'button' && node.attributes['aria-label'] === '复制服务器地址');
    assert.equal(copy.length, 1);
    assert.equal(copy[0].textContent, '');
    await copy[0].onclick();
    assert.equal(current.clipboard.at(-1), origin);
    assert.doesNotMatch(source, /10\.17\.74\.92|7443/);
  }
});

test('null and empty login recovery codes complete verification without an empty code screen', async () => {
  for (const recovery_codes of [null, []]) {
    const current = harness({ path: '/auth/verify', respond: () => ({ user: { id: 'u1' }, recovery_codes }) });
    await current.start();
    await current.submit('verify', { code: '123456' });
    assert.equal(current.redirects[0], '/');
    assert.equal(current.form('recovery-ack'), undefined);
  }
});

test('auth links switch between login and registration without losing the authorization destination', async () => {
  const current = harness({ path: '/auth', search: '?next=%2Fenroll%2Fconfirm%3Fcode%3DABC', respond: () => ({ state: 'challenge' }) });
  await current.start();
  const links = all(current.content, (node) => node.tagName === 'a');
  assert.equal(links.filter((node) => node.attributes['aria-current'] === 'page').length, 0);
  assert.ok(links.some((node) => node.href.startsWith('/auth/register')));
  assert.ok(links.every((node) => node.href.includes('next=%2Fenroll%2Fconfirm%3Fcode%3DABC')));
  await current.submit('login', { email: 'one@example.com', password: 'password' });
  assert.equal(all(current.content, (node) => node.attributes['aria-current'] === 'page').length, 0);
});

test('verification steps do not offer navigation that leaves the active flow', async () => {
  for (const step of ['setup', 'verify']) {
    const current = harness({ path: `/auth/${step}` });
    await current.start();
    assert.equal(all(current.content, (node) => node.tagName === 'a').length, 0);
    assert.equal(current.nav.children.length, 0);
  }
});

test('registration links back to login and preserves next', async () => {
  const current = harness({ path: '/auth/register', search: '?next=%2Foauth%2Fauthorize' });
  await current.start();
  assert.ok(all(current.content, node => node.tagName === 'a').some(node => node.href === '/auth?next=%2Foauth%2Fauthorize'));
});

test('standalone account navigation switches the add-device hash without a reload', async () => {
  const current = harness({ page: 'account', path: '/account' });
  await current.start();
  const add = all(current.nav, node => node.href === '/account#add-device')[0];
  assert.equal(typeof add.onclick, 'function');
  await add.onclick({ preventDefault() {} });
  assert.ok(current.find(node => node.id === 'add-device'));
  assert.equal(current.form('password'), undefined);
  assert.equal(current.redirects.length, 0);
});

test('embedded account and add-device pages reuse security logic but stay separate', async () => {
  const current = harness({ page: 'account', embedded: true });
  await current.start();
  assert.ok(current.form('password'));
  assert.equal(current.find(node => node.id === 'add-device'), undefined);
  assert.ok(!current.calls.some(call => call.url === '/enroll/client-release.json'));
  await current.pages.start('add-device');
  assert.ok(current.find(node => node.id === 'add-device'));
  assert.equal(current.form('password'), undefined);
});

test('embedded security flow cannot be silently discarded until recovery codes are acknowledged', async () => {
  const current = harness({ page: 'account', embedded: true, respond: url => url.endsWith('recovery-codes') ? { recovery_codes: ['NEW-CODE'] } : { devices: [], sessions: [] } });
  await current.start();
  assert.equal(current.pages.canLeave(), true);
  await current.submit('recovery-codes', { password: 'new-password', code: '123456' });
  assert.equal(current.pages.canLeave(), false);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.pages.canLeave(), true);
});

test('recovery acknowledgment renders its checkbox before the label and still requires explicit consent', async () => {
  const current = harness({ path: '/auth/verify', respond: () => ({ user: { id: 'u1' }, recovery_codes: ['TEST-ONLY'] }) });
  await current.start();
  await current.submit('verify', { code: '123456' });
  const label = all(current.form('recovery-ack'), (node) => node.tagName === 'label')[0];
  assert.equal(label.children[0].type, 'checkbox');
  assert.equal(label.textContent, '');
  assert.equal(text(label.children[1]), '我已安全保存恢复码');
  await current.submit('recovery-ack');
  assert.equal(current.redirects.length, 0);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.redirects[0], '/');
});

test('private navigation keeps account selection without exposing an admin entry', async () => {
  for (const page of ['account', 'admin']) {
    const current = harness({ page, path: `/${page}`, role: 'admin' });
    await current.start();
    const selected = all(current.nav, (node) => node.attributes['aria-current'] === 'page');
    assert.equal(selected.length, page === 'account' ? 1 : 0);
    if (page === 'account') assert.equal(selected[0].href, '/account');
    assert.equal(all(current.nav, node => node.href === '/admin').length, 0);
    assert.equal(all(current.content, node => node.href === '/admin').length, 0);
  }
  const current = harness({ page: 'account', path: '/account' });
  await current.start();
  assert.equal(all(current.nav, (node) => node.href === '/admin').length, 0);
});

test('device editor supports case-preserving text icons and rejects invalid text', async () => {
  const current = harness({ page: 'add-device', devices: [{ id: 'm2', name: 'Air' }],
    appearances: { m2: { icon: 'text', text: 'Air', color: 'rose' } } });
  await current.start();
  const row = current.find(node => node.className === 'account-device-row');
  const find = predicate => all(row, predicate)[0];
  find(n => n.textContent === '编辑').onclick();
  const input = find(n => n.name === 'device-icon-text');
  assert.ok(input);
  assert.equal(input.value, 'Air');
  const save = find(n => n.textContent === '保存');
  input.value = '五'; input.oninput();
  assert.equal(save.disabled, true);
  await save.onclick();
  assert.equal(current.calls.filter(c => c.method === 'PATCH').length, 0);
  find(n => n.dataset?.deviceColorChoice === 'teal').onclick();
  assert.equal(input.value, '五');
  assert.equal(save.disabled, true);
  input.value = 'mB4'; input.oninput();
  assert.equal(save.disabled, false);
  await save.onclick();
  const call = current.calls.find(c => c.method === 'PATCH' && c.body?.appearance);
  assert.deepEqual(JSON.parse(JSON.stringify(call.body)), {id:'m2', appearance:{icon:'text', text:'mB4', color:'teal'}});
  find(n => n.textContent === '编辑').onclick();
  find(n => n.dataset?.deviceIcon === 'laptop').onclick();
  find(n => n.dataset?.deviceIcon === 'text').onclick();
  assert.equal(input.value, 'A');
});

test('account logout is a text action that uses the existing logout flow', async () => {
  const current = harness({ page:'account', role:'admin' });
  await current.start();
  const logout = current.find(n => n.textContent === '退出登录');
  assert.equal(logout.className, 'account-logout-link');
  await logout.onclick();
  assert.ok(current.calls.some(c => c.url === '/api/auth/logout'));
});

test('temporarily missing enrolled nodes show reconnection rather than revoked or installing', async () => {
 const current=harness({page:'add-device',devices:[{id:'m7',name:'Air',status:'unavailable',online:false}]});await current.start();
 assert.match(text(current.content),/正在重新连接/);
});
