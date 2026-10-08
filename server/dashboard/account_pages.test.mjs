import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./account.js', import.meta.url), 'utf8').catch(() => '');
class Element {
  constructor(tag) { this.tagName = tag; this.children = []; this.attributes = {}; this.value = ''; this.checked = false; this.disabled = false; this.textContent = ''; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(key, value) { this.attributes[key] = String(value); }
  focus() { this.focused = true; }
}
function text(node) { return typeof node === 'string' ? node : (node?.textContent || '') + (node?.children || []).map(text).join(' '); }
function all(node, predicate) { return [node, ...(node.children || []).flatMap((child) => typeof child === 'string' ? [] : all(child, predicate))].filter(predicate); }
function harness({ page = 'auth', path = '/auth', search = '', role = 'user', respond, confirmed = true, origin = 'https://fleet.test', released = true, transformRelease = value => value, embedded = false } = {}) {
  assert.ok(source, 'shared account page implementation is missing');
  const sandbox = { URL, URLSearchParams };
  vm.runInNewContext(source, sandbox);
  const content = new Element('main');
  const status = new Element('p');
  const nav = new Element('nav');
  const document = { createElement: (tag) => new Element(tag), querySelector: (selector) => ({ '#page-content': content, '#page-status': status, '#page-nav': nav })[selector], documentElement: { dataset: {} } };
  const calls = [];
  const redirects = [];
  const location = { origin, pathname: path, search, replace: (target) => redirects.push(target) };
  const auth = { user: { id: 'u1', email: 'one@example.com', role, status: 'active' },
    me: async () => { calls.push({ url: '/api/auth/me' }); return { user: auth.user, csrf_token: 'token' }; },
    safeNext: (target) => target?.startsWith('/') && !target.startsWith('//') ? target : '/',
    json: async (url, options = {}) => {
      calls.push({ url, ...options });
      if (url === '/enroll/client-release.json') {
        if (!released) throw new Error('not published');
        return transformRelease({ schema: 1, notarization: 'Accepted', bundle_id: 'com.macfleet.fleet-hub', version: '1.0.0', build: 1, minimum_macos: '13.0', architectures: ['arm64','x86_64'],
          assets: { dmg: { path: '/enroll/clients/1/Fleet-Hub.dmg', sha256: 'a'.repeat(64), size: 12345678 }, update: { path: '/enroll/clients/1/Fleet-Hub-update.zip', sha256: 'b'.repeat(64), size: 12345678, ed_signature: Buffer.alloc(64, 1).toString('base64') } } });
      }
      return respond ? respond(url, options) : { sessions: [], devices: [], users: [], total: 0, page: 1 };
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
  return { pages, content, status, nav, calls, redirects, location, auth, find, form, submit, click, start: () => pages.start(page) };
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
  assert.equal(current.redirects.length, 0);
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

test('account metadata formats Unix timestamps and does not show unknown dates as epoch', async () => {
  const timestamp = 1800000000;
  const current = harness({ page: 'account', respond: (url) => url === '/api/devices'
    ? { devices: [{ id: 'm1', created_at: timestamp, last_seen: 0 }] }
    : { sessions: [{ id: 'session', created_at: timestamp, last_seen: timestamp, expires_at: timestamp + 2592000 }] } });
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

test('account authenticates first, lists owned metadata, and sends password and recovery rotation contracts', async () => {
  const current = harness({ page: 'account', path: '/account', respond: (url) => url.endsWith('recovery-codes') ? { recovery_codes: ['NEW-CODE'] } : { devices: [{ id: 'm1', name: 'My Mac', online: true }], sessions: [{ id: 's1', current: true }] } });
  await current.start();
  assert.equal(current.calls[0].url, '/api/auth/me');
  assert.match(text(current.content), /My Mac/);
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

test('account device removal and other session revocation require confirmation and use writes', async () => {
  const current = harness({ page: 'account', respond: () => ({ devices: [{ id: 'm1', name: 'My Mac' }], sessions: [] }) });
  await current.start();
  await current.click('移除 My Mac');
  assert.ok(current.calls.some((call) => call.url === '/api/devices/m1' && call.method === 'DELETE'));
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
  const current = harness({ page: 'admin', role: 'admin', respond: (url) => url.endsWith('overview') ? { users: 25, devices: 4, online: 2 } : { users: [{ id: 'u2', email: '<img onerror=evil>', status: 'active' }], devices: [], total: 25, page: 1 } });
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

test('admin detail exposes only metadata and actions send exact status/revocation/device paths', async () => {
  const current = harness({ page: 'admin', role: 'admin', respond: (url) => url === '/api/admin/users/u2' ? { user: { id: 'u2', email: 'two@example.com', status: 'active' }, devices: [{ id: 'm1', name: 'Mac' }], events: [{ action: 'login', created_at: '2026-09-30', message: 'PRIVATE-CONTENT' }] } : { users: [{ id: 'u2', email: 'two@example.com', status: 'active' }], devices: [{ id: 'm1', name: 'Mac' }], total: 1, page: 1 } });
  await current.start();
  await current.click('查看 two@example.com');
  assert.match(text(current.content), /login/);
  assert.doesNotMatch(text(current.content), /PRIVATE-CONTENT/);
  await current.click('停用用户');
  const update = current.calls.find((call) => call.method === 'PATCH');
  assert.equal(update.url, '/api/admin/users/u2');
  assert.deepEqual(JSON.parse(JSON.stringify(update.body)), { status: 'disabled' });
  await current.click('撤销用户登录');
  assert.ok(current.calls.some((call) => call.url.endsWith('/u2/revoke-sessions') && call.method === 'POST'));
  await current.click('移除 Mac');
  assert.ok(current.calls.some((call) => call.url === '/api/admin/devices/m1' && call.method === 'DELETE'));
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
    return { users: Array.from({ length: page === 1 ? 2 : 1 }, (_, index) => ({ id: `u${page}-${index}`, email: `user${page}-${index}@example.com` })), total: 3, page };
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

test('security changes that revoke the current session preserve recovery codes until acknowledged', async () => {
  const current = harness({ page: 'account', respond: (url) => url.endsWith('/start') ? { secret: 'NEW' } : url.endsWith('/confirm') ? { recovery_codes: ['SAVE-ME'], login_required: true } : { devices: [], sessions: [] } });
  await current.start();
  await current.submit('totp-start', { password: 'password', code: '123456' });
  await current.submit('totp-confirm', { code: '654321' });
  assert.match(text(current.content), /SAVE-ME/);
  assert.equal(current.redirects.length, 0);
  await current.submit('recovery-ack', { saved: true });
  assert.equal(current.redirects[0], '/auth?next=%2Faccount');
});

test('add device offers a native DMG and explains fresh browser OAuth and background disk authorization', async () => {
  const current = harness({ page: 'add-device' });
  await current.start();
  assert.doesNotMatch(text(current.content), /bootstrap\.sh|curl -fsSL/);
  const add = current.find((node) => node.id === 'add-device');
  const downloads = all(add, (node) => node.tagName === 'a' && node.download);
  assert.deepEqual(downloads.map((node) => node.href), ['/enroll/clients/1/Fleet-Hub.dmg']);
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
  assert.match(text(add), /浮动引导窗.*拖入.*打开开关/);
  assert.doesNotMatch(text(add), /开启 Fleet Hub\.app/);
  assert.match(text(add), /关闭.*后台.*运行/);
  assert.match(text(add), /检查更新/);
  assert.ok(!current.calls.some((call) => call.url === '/api/enrollment/start'), 'web page must not initiate a client grant');
  assert.equal(current.form('enrollment-code'), undefined);
});

test('login and device setup omit redundant introductory copy without dropping account identity or security reminders', async () => {
  const login = harness();
  await login.start();
  assert.doesNotMatch(text(login.content), /连接和管理你的设备/);
  const addition = harness({ page: 'add-device', embedded: true });
  await addition.start();
  assert.doesNotMatch(text(addition.content), /安装 Fleet Hub，使用当前账号关联这台 Mac|授权请求由/);
  assert.match(text(addition.content), /Fleet Hub.*检查更新/);
  assert.match(text(addition.content), /Fleet Hub.*无需磁盘权限/);
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
    assert.ok(all(add, (node) => node.tagName === 'button' && node.textContent === '复制服务器地址').length);
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

test('private navigation identifies only the current page and keeps administrator visibility scoped', async () => {
  for (const page of ['account', 'admin']) {
    const current = harness({ page, path: `/${page}`, role: 'admin' });
    await current.start();
    const selected = all(current.nav, (node) => node.attributes['aria-current'] === 'page');
    assert.equal(selected.length, 1);
    assert.equal(selected[0].href, `/${page}`);
  }
  const current = harness({ page: 'account', path: '/account' });
  await current.start();
  assert.equal(all(current.nav, (node) => node.href === '/admin').length, 0);
});
