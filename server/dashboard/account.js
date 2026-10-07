'use strict';

(function (root) {
  function nativeClientRelease(release) {
    if (release?.schema !== 1 || release.notarization !== 'Accepted' || release.bundle_id !== 'com.macfleet.fleet-hub' ||
        !Number.isSafeInteger(release.build) || release.build < 1 || !/^\d+\.\d+\.\d+$/.test(release.version || '') ||
        !/^\d+\.\d+(?:\.\d+)?$/.test(release.minimum_macos || '') || !Array.isArray(release.architectures) ||
        !release.architectures.length || release.architectures.some((arch) => !['arm64', 'x86_64'].includes(arch))) return null;
    for (const [name, filename] of [['dmg', 'Fleet-Hub.dmg'], ['update', 'Fleet-Hub-update.zip']]) {
      const asset = release.assets?.[name];
      if (asset?.path !== `/enroll/clients/${release.build}/${filename}` || !/^[a-f0-9]{64}$/.test(asset.sha256 || '') ||
          !Number.isSafeInteger(asset.size) || asset.size < 1) return null;
    }
    if (!/^[A-Za-z0-9+/]{85}[AQgw]==$/.test(release.assets.update.ed_signature || '')) return null;
    return release;
  }
  function createPages({ document, auth, location, history, confirm }) {
    const content = document.querySelector('#page-content');
    const status = document.querySelector('#page-status');
    const nav = document.querySelector('#page-nav');
    const next = auth.safeNext(new URLSearchParams(location.search).get('next'));

    function node(tag, text = '', props = {}) {
      const element = document.createElement(tag);
      element.textContent = text;
      for (const [key, value] of Object.entries(props)) element[key] = value;
      return element;
    }
    function link(label, href, current = false) {
      const element = node('a', label, { href });
      if (current) element.setAttribute('aria-current', 'page');
      return element;
    }
    function heading(title, description) {
      content.replaceChildren(node('h1', title));
      if (description) content.append(node('p', description, { className: 'account-muted' }));
      status.textContent = '';
    }
    function section(title, parent = content) {
      const element = node('section', '', { className: 'account-section' });
      element.append(node('h2', title));
      parent.append(element);
      return element;
    }
    function button(label, action, parent = content, danger = false, disabled = () => false) {
      const element = node('button', label, { type: 'button', className: danger ? 'account-button danger' : 'account-button' });
      element.onclick = async () => {
        if (element.disabled) return;
        element.disabled = true;
        status.textContent = '';
        try { await action(); }
        catch (error) { status.textContent = error.message; }
        finally { element.disabled = disabled(); }
      };
      parent.append(element);
      return element;
    }
    function field(name, label, type = 'text', autocomplete = 'off') {
      return { name, label, type, autocomplete };
    }
    const emailField = () => field('email', '邮箱', 'email', 'username');
    const passwordField = (name = 'password', label = '密码', autocomplete = 'current-password') => field(name, label, 'password', autocomplete);
    const otpField = () => field('code', 'Authenticator 验证码', 'text', 'one-time-code');

    function form(name, fields, label, action, parent = content) {
      const element = node('form', '', { className: 'account-form' });
      element.setAttribute('data-form', name);
      const inputs = new Map();
      if (auth.user?.email && fields.some((config) => config.type === 'password') && !fields.some((config) => config.name === 'email')) {
        element.append(node('input', '', { type: 'text', name: 'username', autocomplete: 'username', value: auth.user.email, hidden: true }));
      }
      for (const config of fields) {
        const wrapper = node('label', config.type === 'checkbox' ? '' : config.label);
        const input = node('input', '', { name: config.name, type: config.type, autocomplete: config.autocomplete, required: true });
        if (config.name === 'code') { input.inputMode = 'numeric'; input.pattern = '[0-9]{6}'; input.maxLength = 6; }
        if (config.name === 'search') input.required = false;
        if (config.type === 'checkbox') input.required = false;
        wrapper.append(input);
        if (config.type === 'checkbox') wrapper.append(node('span', config.label));
        element.append(wrapper);
        inputs.set(config.name, input);
      }
      const error = node('p', '', { className: 'account-error' });
      error.setAttribute('role', 'alert');
      const submit = node('button', label, { type: 'submit', className: 'account-button primary' });
      element.append(error, submit);
      let submitting = false;
      element.onsubmit = async (event) => {
        event.preventDefault();
        if (submitting) return;
        submitting = true;
        submit.disabled = true;
        error.textContent = '';
        const values = Object.fromEntries([...inputs].map(([key, input]) => [key, input.type === 'checkbox' ? input.checked : input.value]));
        try {
          if (values.confirm_password != null && values.password !== values.confirm_password) throw new Error('两次输入的密码不一致。');
          await action(values);
        } catch (failure) { error.textContent = failure.message; }
        finally { submitting = false; submit.disabled = false; }
      };
      parent.append(element);
      return { element, inputs };
    }

    function post(path, body) { return auth.json(path, { method: 'POST', body }); }
    function authURL(step) { return `/auth${step === 'login' ? '' : `/${step}`}${next === '/' ? '' : `?next=${encodeURIComponent(next)}`}`; }
    function authLinks(step) {
      const links = node('div', '', { className: 'account-links' });
      links.append(link('登录', authURL('login'), step === 'login'), link('注册', authURL('register'), step === 'register'), link('使用恢复码', authURL('recovery'), step === 'recovery'));
      content.append(links);
    }
    function showCodes(codes, done) {
      heading('保存恢复码', '每个恢复码只能使用一次。请离线保存；离开此页后无法再次查看这批恢复码。');
      content.append(node('pre', codes.join('\n'), { className: 'account-codes' }));
      form('recovery-ack', [field('saved', '我已安全保存恢复码', 'checkbox')], '继续', async ({ saved }) => {
        if (!saved) throw new Error('请先保存恢复码并勾选确认。');
        content.replaceChildren();
        await done();
      });
    }
    function showTOTP(totp, name, submit) {
      content.append(node('p', '使用 Authenticator（如 Apple 密码、Google Authenticator 或 1Password）扫描二维码，再输入六位验证码。'));
      const image = node('img', '', { src: '/api/auth/qr', alt: 'Authenticator 设置二维码', className: 'account-qr' });
      image.onerror = () => { status.textContent = '二维码不可用或设置已过期，请重新开始登录或更换 Authenticator。'; };
      content.append(image);
      if (totp?.secret) {
        content.append(node('p', '无法扫码时，手动输入密钥：'), node('code', totp.secret, { className: 'account-secret' }));
      }
      form(name, [otpField()], '验证并完成设置', submit);
    }
    async function completeAuthentication(result) {
      if (!result?.user) throw new Error('验证未完成，请重试。');
      if (result.recovery_codes?.length) showCodes(result.recovery_codes, () => location.replace(next));
      else location.replace(next);
    }
    function authTransition(result) {
      if (result?.state === 'setup') showAuth('setup', result.totp);
      else if (result?.state === 'challenge') showAuth('verify');
      else throw new Error('登录流程响应无效，请重试。');
    }
    function showAuth(step, totp) {
      history.replaceState(null, '', authURL(step));
      if (step === 'setup') {
        heading('设置 Authenticator', '所有账户都必须开启两步验证。完成设置后才能访问设备。');
        showTOTP(totp, 'verify', async (values) => completeAuthentication(await post('/api/auth/verify', values)));
      } else if (step === 'verify') {
        heading('两步验证', '输入 Authenticator 中的六位验证码。');
        form('verify', [otpField()], '登录', async (values) => completeAuthentication(await post('/api/auth/verify', values)));
      } else if (step === 'register') {
        heading('创建账户', '使用邮箱注册，下一步设置 Authenticator。');
        form('register', [emailField(), passwordField('password', '密码', 'new-password'), passwordField('confirm_password', '确认密码', 'new-password')], '注册', async (values) => authTransition(await post('/api/auth/register', values)));
      } else if (step === 'recovery') {
        heading('恢复账户', '使用密码和一个未使用的恢复码，随后重新设置 Authenticator。');
        form('recover', [emailField(), passwordField(), field('recovery_code', '恢复码')], '恢复并重新设置', async (values) => authTransition(await post('/api/auth/recover', values)));
      } else {
        heading('登录 Fleet', '登录后连接和管理你的 Mac。');
        form('login', [emailField(), passwordField()], '继续', async (values) => authTransition(await post('/api/auth/login', values)));
      }
      if (step === 'login' || step === 'recovery') authLinks(step);
    }

    function metadata(records, columns, parent, actions) {
      if (!records?.length) { parent.append(node('p', '暂无记录', { className: 'account-muted' })); return; }
      const wrapper = node('div', '', { className: 'account-table-scroll' });
      const table = node('table', '', { className: 'account-table' });
      const head = node('tr');
      for (const [label] of columns) head.append(node('th', label, { scope: 'col' }));
      if (actions) head.append(node('th', '操作', { scope: 'col' }));
      const thead = node('thead');
      thead.append(head);
      const tbody = node('tbody');
      for (const record of records) {
        const row = node('tr');
        for (const [, value] of columns) row.append(node('td', String(value(record) ?? '—')));
        if (actions) { const cell = node('td'); actions(record, cell); row.append(cell); }
        tbody.append(row);
      }
      table.append(thead, tbody);
      wrapper.append(table);
      parent.append(wrapper);
    }
    function formatTime(value) {
      if (value == null || value === '' || value === 0) return '—';
      const milliseconds = typeof value === 'number' ? value * 1000 : Date.parse(value);
      if (!Number.isFinite(milliseconds)) return '—';
      return new Date(milliseconds).toLocaleString('zh-CN', { hour12: false });
    }
    const deviceColumns = [
      ['设备', (device) => device.name || device.id], ['编号', (device) => device.id],
      ['状态', (device) => device.status], ['在线', (device) => device.online ? '在线' : '离线'],
      ['创建时间', (device) => formatTime(device.created_at)], ['最后在线', (device) => formatTime(device.last_seen)], ['版本', (device) => device.version],
    ];
    function devices(records, parent, administrative = false, reload) {
      metadata(records, deviceColumns, parent, (device, cell) => {
        if (!/^m\d+$/.test(device.id)) return;
        button(`移除 ${device.name || device.id}`, async () => {
          if (!confirm(`移除 ${device.name || device.id}？该设备将无法继续通过 Fleet 访问。`)) return;
          await auth.json(`/api/${administrative ? 'admin/' : ''}devices/${device.id}`, { method: 'DELETE' });
          await reload();
        }, cell, true);
      });
    }
    async function showAccount() {
      const [deviceData, sessionData] = await Promise.all([auth.json('/api/devices'), auth.json('/api/auth/sessions')]);
      const serviceOrigin = location.origin;
      let release = null;
      try {
        release = nativeClientRelease(await auth.json('/enroll/client-release.json'));
      } catch (_) {}
      heading('账户', `${auth.user.email} · ${auth.user.role} · ${auth.user.status}`);
      const deviceSection = section('我的设备');
      deviceSection.id = 'devices';
      devices(deviceData.devices, deviceSection, false, showAccount);
      const add = section('添加设备');
      add.id = 'add-device';
      add.append(node('p', '下载并在要添加的 Mac 上运行客户端，授权请求由 fleet-agent 发起。邮箱、密码和 Authenticator 验证码只在浏览器输入。'));
      const downloads = node('div', '', { className: 'account-downloads' });
      if (release) {
        downloads.append(node('a', '下载 macOS 应用', { href: release.assets.dmg.path, download: 'Fleet-Hub.dmg', className: 'account-button primary' }));
        add.append(downloads);
        add.append(node('p', `Fleet Hub ${release.version} · macOS ${release.minimum_macos} 及以上 · ${(release.assets.dmg.size / 1048576).toFixed(1)} MB`, { className: 'account-muted' }));
      } else add.append(node('p', '本服务尚未发布经过签名公证的 Fleet Hub 应用，暂不提供下载。', { className: 'account-error' }));
      const steps = node('ol', '', { className: 'account-steps' });
      for (const description of [
        '下载并打开 DMG，将 Fleet Hub 拖入“应用程序”，也可以双击后选择“安装到应用程序”。',
        '打开 Fleet Hub 应用，在“关联账号”填写下方服务网页地址，点击“打开网页授权”。',
        '浏览器登录并完成 Authenticator 验证，核对账号与设备名称，点击“授权并连接”；应用会自动接入。',
        '授权过期或取消时，回到应用再次点击“打开网页授权”，每次都会创建新的授权请求。',
        '在应用“磁盘权限”中选择后台应用，在系统设置开启独立 Fleet Agent.app 的完全磁盘访问，点击“重启并检查”。Fleet Hub 只负责设置，无需磁盘权限；不要选择安装包中的后台副本或旧命令行 fleet-agent。',
        '确认后台及设备服务正常后，可以关闭应用窗口，后台服务仍会持续运行。',
      ]) steps.append(node('li', description));
      add.append(steps);
      add.append(node('p', `本服务网页地址：${serviceOrigin}`));
      button('复制服务器地址', async () => {
        if (!root.navigator?.clipboard?.writeText) throw new Error('浏览器无法复制，请手动复制上方服务网页地址。');
        await root.navigator.clipboard.writeText(serviceOrigin);
        status.textContent = '服务器地址已复制。';
      }, add);
      add.append(node('p', '更新客户端：打开 Fleet Hub → 检查更新，从已设置的服务器下载并升级整个应用。重启、登录后自动运行和卸载也在应用中管理。', { className: 'account-muted' }));
      add.append(node('p', '客户端经 Developer ID 签名与 Apple 公证。若下载不可用，请联系本服务管理员检查应用发行状态。完全磁盘访问由你在系统设置开启，文件本身的权限仍然生效。', { className: 'account-muted' }));
      const sessions = section('登录设备');
      metadata(sessionData.sessions, [['当前', (session) => session.current ? '此设备' : '其他设备'],
        ['编号', (session) => session.id], ['创建时间', (session) => formatTime(session.created_at)],
        ['最近使用', (session) => formatTime(session.last_seen)], ['到期时间', (session) => formatTime(session.expires_at)],
        ['IP', (session) => session.ip], ['浏览器', (session) => session.user_agent || session.agent]], sessions);
      button('退出其他设备的登录', async () => {
        if (!confirm('退出其他设备的登录？当前登录会保留。')) return;
        await post('/api/auth/logout-others');
        await showAccount();
        status.textContent = '其他登录已退出。';
      }, sessions, true);
      const passwords = section('更改密码');
      form('password', [passwordField('current_password', '当前密码'), passwordField('password', '新密码', 'new-password'), passwordField('confirm_password', '确认新密码', 'new-password'), otpField()], '保存密码', async (values) => {
        const result = await post('/api/auth/password', values);
        if (result?.login_required) { auth.invalidate({ returnTo: '/account' }); return; }
        await showAccount();
        status.textContent = '密码已更改。';
      }, passwords);
      const totp = section('更换 Authenticator');
      form('totp-start', [passwordField(), otpField()], '开始更换', async (values) => {
        const result = await post('/api/auth/totp/start', values);
        heading('更换 Authenticator', '扫描新二维码，并使用新的 Authenticator 验证码确认。');
        showTOTP(result, 'totp-confirm', async (verification) => {
          const confirmed = await post('/api/auth/totp/confirm', verification);
          showCodes(confirmed.recovery_codes, confirmed.login_required ? () => auth.invalidate({ returnTo: '/account' }) : showAccount);
        });
      }, totp);
      const recovery = section('重新生成恢复码');
      recovery.append(node('p', '重新生成后，旧恢复码将立即失效。'));
      form('recovery-codes', [passwordField(), otpField()], '重新生成', async (values) => {
        if (!confirm('重新生成恢复码并使旧恢复码失效？')) return;
        const result = await post('/api/auth/recovery-codes', values);
        showCodes(result.recovery_codes, showAccount);
      }, recovery);
      button('退出登录', () => auth.logout(), content, true);
    }

    async function showAdmin() {
      if (auth.user.role !== 'admin') { heading('需要管理员权限', '请使用管理员账户登录。'); return; }
      const overview = await auth.json('/api/admin/overview');
      heading('管理', `用户 ${overview.users} · 设备 ${overview.devices} · 在线 ${overview.online}`);
      const detail = section('用户详情');
      detail.hidden = true;
      let selectedUser = null;
      async function showUser(identity) {
        const result = await auth.json(`/api/admin/users/${encodeURIComponent(identity)}`);
        if (selectedUser !== identity) return;
        detail.hidden = false;
        detail.replaceChildren(node('h2', '用户详情'));
        metadata([result.user], [['邮箱', (user) => user.email], ['编号', (user) => user.id],
          ['角色', (user) => user.role], ['状态', (user) => user.status], ['创建时间', (user) => formatTime(user.created_at)]], detail);
        button(result.user.status === 'disabled' ? '启用用户' : '停用用户', async () => {
          const enabled = result.user.status === 'disabled';
          if (!confirm(`${enabled ? '启用' : '停用'} ${result.user.email}？`)) return;
          await auth.json(`/api/admin/users/${encodeURIComponent(identity)}`, { method: 'PATCH', body: { status: enabled ? 'active' : 'disabled' } });
          await showUser(identity);
          await loadUsers();
        }, detail, true);
        button('撤销用户登录', async () => {
          if (!confirm(`撤销 ${result.user.email} 的所有登录？`)) return;
          await post(`/api/admin/users/${encodeURIComponent(identity)}/revoke-sessions`);
          status.textContent = '用户登录已撤销。';
        }, detail, true);
        devices(result.devices, detail, true, async () => { await showUser(identity); await loadDevices(); });
        const events = section('事件', detail);
        metadata(result.events, [['事件', (event) => event.action || event.type], ['时间', (event) => formatTime(event.created_at)],
          ['操作者', (event) => event.actor_id], ['IP', (event) => event.ip]], events);
      }
      function list(kind, title) {
        const parent = section(title);
        const results = node('div');
        let search = '';
        let page = 1;
        let pageSize = 0;
        let hasMore = false;
        let sequence = 0;
        const controls = node('div', '', { className: 'account-links' });
        async function load() {
          const started = ++sequence;
          const data = await auth.json(`/api/admin/${kind}?search=${encodeURIComponent(search)}&page=${page}`);
          if (started !== sequence) return;
          if (page === 1) pageSize = data[kind]?.length || 1;
          hasMore = Boolean(data[kind]?.length) && page * pageSize < data.total;
          results.replaceChildren(node('p', `第 ${page} 页 · 共 ${data.total} 条`));
          if (kind === 'users') {
            metadata(data.users, [['邮箱', (user) => user.email], ['编号', (user) => user.id],
              ['角色', (user) => user.role], ['状态', (user) => user.status]], results, (user, cell) => {
              button(`查看 ${user.email}`, () => { selectedUser = user.id; return showUser(user.id); }, cell);
            });
          } else devices(data.devices, results, true, load);
          previous.disabled = page <= 1;
          following.disabled = !hasMore;
        }
        form(`admin-${kind}-search`, [field('search', '搜索')], '搜索', async (values) => { search = values.search; page = 1; await load(); }, parent);
        const previous = button(`${title}上一页`, async () => { page = Math.max(1, page - 1); await load(); }, controls, false, () => page <= 1);
        const following = button(`${title}下一页`, async () => { page++; await load(); }, controls, false, () => !hasMore);
        parent.append(results, controls);
        return load;
      }
      const loadUsers = list('users', '用户');
      const loadDevices = list('devices', '设备');
      await Promise.all([loadUsers(), loadDevices()]);
    }

    async function showEnrollment() {
      if (location.pathname === '/oauth/consent') return showOAuthConsent();
      const code = new URLSearchParams(location.search).get('code');
      heading('确认添加设备', `设备将归属于 ${auth.user.email}。只确认你主动发起的服务器配对请求。`);
      if (!code) { content.append(node('p', '确认链接缺少配对码，请重新打开服务器配对流程生成的链接。')); return; }
      const preview = await auth.json(`/api/enrollment/preview?code=${encodeURIComponent(code)}`);
      content.append(node('p', `关联设备：${preview.name} · 配对码：${code}`));
      form('enrollment-confirm', [], '确认添加设备', async () => {
        await post('/api/enrollment/confirm', { code });
        heading('设备已确认', '请回到 Mac 客户端，客户端将自动领取授权、入网并完成安装。');
        content.append(link('返回 Fleet', '/'), link('我的设备', '/account#devices'));
      });
    }

    async function showOAuthConsent() {
      const requestID = new URLSearchParams(location.search).get('request');
      heading('授权 Fleet Hub', `关联账号：${auth.user.email}`);
      if (!requestID) { content.append(node('p', '请从客户端重新打开网页授权。')); return; }
      const preview = await auth.json(`/api/oauth/preview?request=${encodeURIComponent(requestID)}`);
      content.append(node('p', `设备：${preview.name}`));
      const returnToApp = async (action) => {
        const result = await post('/api/oauth/authorize', { request_id: requestID, action });
        const callback = new URL(result.redirect_uri);
        const expected = new URL(preview.redirect_uri);
        if (callback.protocol !== 'http:' || callback.hostname !== '127.0.0.1' || !callback.port ||
            callback.username || callback.password || callback.hash || callback.pathname !== '/oauth/callback' ||
            callback.origin !== expected.origin || callback.pathname !== expected.pathname ||
            !callback.searchParams.get('state') || (action === 'approve' ? !callback.searchParams.get('code') : callback.searchParams.get('error') !== 'access_denied')) {
          throw new Error('本机回调不合法，请重新发起授权。');
        }
        location.replace(callback.href);
      };
      form('oauth-consent', [], '授权并连接', () => returnToApp('approve'));
      button('取消授权', () => returnToApp('deny'));
    }

    async function start(page) {
      if (page === 'auth') {
        nav.replaceChildren();
        const step = location.pathname.replace(/\/$/, '').split('/')[2] || 'login';
        showAuth(['login', 'register', 'verify', 'setup', 'recovery'].includes(step) ? step : 'login');
      } else {
        await auth.me();
        nav.replaceChildren(link('Fleet', '/'), link('账户', '/account', page === 'account'), link('添加设备', '/account#add-device'));
        if (auth.user.role === 'admin') nav.append(link('管理', '/admin', page === 'admin'));
        if (page === 'account') await showAccount();
        else if (page === 'admin') await showAdmin();
        else if (page === 'enrollment') await showEnrollment();
      }
      document.documentElement.dataset.auth = 'ready';
    }
    return { start };
  }

  root.FleetAccountPages = { createPages, nativeClientRelease };
  if (root.document?.body?.dataset.fleetPage) {
    const pages = createPages({ document: root.document, auth: root.FleetAuth, location: root.location,
      history: root.history, confirm: root.confirm.bind(root) });
    pages.start(root.document.body.dataset.fleetPage).catch((error) => {
      if (error.status === 401) return;
      root.document.querySelector('#page-status').textContent = error.message;
      const retry = root.document.createElement('a');
      retry.href = root.location.href;
      retry.textContent = '重新加载';
      root.document.querySelector('#page-status').append(retry);
    });
  }
})(globalThis);
