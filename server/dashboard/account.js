'use strict';

(function (root) {
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
      const clientAssets = ['mac-bundle.tar.gz', 'dist/fleet-agent-darwin-arm64', 'dist/fleet-agent-darwin-amd64'];
      let clientPublished = false;
      try {
        const release = await auth.json('/enroll/release.json');
        clientPublished = release.schema === 1 && release.notarization === 'Accepted' && release.device_authorization === 1 &&
          clientAssets.every((asset) => /^[a-f0-9]{64}$/.test(release.assets?.[asset]?.sha256 || ''));
      } catch (_) {}
      heading('账户', `${auth.user.email} · ${auth.user.role} · ${auth.user.status}`);
      const deviceSection = section('我的设备');
      deviceSection.id = 'devices';
      devices(deviceData.devices, deviceSection, false, showAccount);
      const add = section('添加设备');
      add.id = 'add-device';
      add.append(node('p', '下载并在要添加的 Mac 上运行客户端，授权请求由 fleet-agent 发起。邮箱、密码和 Authenticator 验证码只在浏览器输入。'));
      const downloads = node('div', '', { className: 'account-downloads' });
      for (const [label, href, filename] of [
        ['下载 fleet-agent 安装包（推荐）', '/enroll/mac-bundle.tar.gz', 'mac-bundle.tar.gz'],
        ['Apple Silicon · arm64', '/enroll/dist/fleet-agent-darwin-arm64', 'fleet-agent-darwin-arm64'],
        ['Intel · amd64', '/enroll/dist/fleet-agent-darwin-amd64', 'fleet-agent-darwin-amd64'],
      ]) {
        downloads.append(node('a', label, { href, download: filename, className: 'account-button' }));
      }
      if (clientPublished) add.append(downloads);
      else add.append(node('p', '本服务尚未发布经过签名公证的可用客户端安装包，暂不提供下载。', { className: 'account-error' }));
      add.append(node('p', '首次安装请使用完整安装包，它会下载并验证正式签名客户端、准备支持文件，再调用 fleet-agent login。裸二进制仅用于已安装支持文件的设备。需要 macOS 和 Homebrew。', { className: 'account-muted' }));
      const steps = node('ol', '', { className: 'account-steps' });
      for (const description of [
        '下载完整安装包，在终端解压并运行安装器；输入本服务网页地址（含 https:// 和实际端口）。已安装的客户端直接运行 fleet-agent login。',
        'fleet-agent 自动唤起浏览器，登录并完成 Authenticator 验证，核对当前账号、设备名称与配对码后确认关联。',
        '回到终端，核对服务、归属账号和设备编号，输入 y 确认接入；按终端提示完成本机安装，看到“已关联”后才表示流程完成。',
      ]) steps.append(node('li', description));
      add.append(steps);
      add.append(node('p', `本服务网页地址：${serviceOrigin}`));
      const shellOrigin = "'" + serviceOrigin.replace(/'/g, "'\\''") + "'";
      if (clientPublished) add.append(node('pre', `FLEET_ORIGIN=${shellOrigin}\ncd "$HOME/Downloads"\ntar -xzf mac-bundle.tar.gz\nFLEET_ORIGIN="$FLEET_ORIGIN" bash mac/install.sh`, { className: 'account-command' }));
      add.append(node('p', '已安装客户端：运行以下命令，在终端填写上方服务网页地址。'));
      add.append(node('pre', 'fleet-agent login', { className: 'account-command' }));
      add.append(node('p', '只使用本服务发布的 Developer ID 签名并经 Apple 公证的客户端。若下载返回 404，或安装器提示不支持设备授权，说明该服务尚未发布可用的新客户端；不要回退旧版或关闭 TLS / 签名校验。', { className: 'account-muted' }));
      add.append(node('p', '浏览器未自动打开时，可在这里输入 fleet-agent 已生成的配对码，手动打开确认页。此处不发起新的设备授权。'));
      form('enrollment-code', [field('enrollment_code', '设备配对码')], '打开设备确认', ({ enrollment_code }) => {
        const code = enrollment_code.trim();
        if (!code) throw new Error('请输入设备配对码。');
        location.replace(`/enroll/confirm?code=${encodeURIComponent(code)}`);
      }, add);
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

  root.FleetAccountPages = { createPages };
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
