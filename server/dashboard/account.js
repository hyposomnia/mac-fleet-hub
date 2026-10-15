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
  function createPages({ document, auth, location, history, confirm, content: suppliedContent, status: suppliedStatus, nav: suppliedNav, onDevicesChanged }) {
    let content = suppliedContent || document.querySelector('#page-content');
    const status = suppliedStatus || document.querySelector('#page-status');
    const nav = suppliedNav || document.querySelector('#page-nav');
    let flowLocked = false;
    let submittingForms = 0;
    let securityDialog = null;
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
      const heading = node('h1', title, { className: suppliedContent && ['账号设置', '设备'].includes(title) ? 'settings-primary-title' : '' });
      if (securityDialog) heading.id = 'auth-security-title';
      content.replaceChildren(heading);
      if (description) content.append(node('p', description, { className: 'account-muted' }));
      status.textContent = '';
    }
    function section(title, parent = content, collapsible = false) {
      const element = node(collapsible ? 'details' : 'section', '', { className: collapsible ? 'account-section account-disclosure' : 'account-section' });
      element.append(node(collapsible ? 'summary' : 'h2', title));
      parent.append(element);
      if (collapsible) {
        const body = node('div', '', { className: 'account-disclosure-body' });
        element.append(body);
        return body;
      }
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
        submittingForms++;
        submit.disabled = true;
        error.textContent = '';
        const values = Object.fromEntries([...inputs].map(([key, input]) => [key, input.type === 'checkbox' ? input.checked : input.value]));
        try {
          if (values.confirm_password != null && values.password !== values.confirm_password) throw new Error('两次输入的密码不一致。');
          await action(values);
        } catch (failure) { error.textContent = failure.message; }
        finally { submittingForms--; submitting = false; submit.disabled = false; }
      };
      parent.append(element);
      return { element, inputs };
    }

    function post(path, body) { return auth.json(path, { method: 'POST', body }); }
    function authURL(step) { return `/auth${step === 'login' ? '' : `/${step}`}${next === '/' ? '' : `?next=${encodeURIComponent(next)}`}`; }
    function authLinks(step) {
      const links = node('div', '', { className: 'account-links' });
      if (step === 'register') links.append(node('span', '已有账号？'), link('登录', authURL('login')));
      else if (step === 'login') links.append(node('span', '还没有账号？'), link('注册', authURL('register')), link('使用恢复码', authURL('recovery')));
      else links.append(link('返回登录', authURL('login')));
      content.append(links);
    }
    function showCodes(codes, done) {
      flowLocked = true;
      auth.setRecoveryPending?.(true);
      heading('保存恢复码', '每个恢复码只能使用一次。请离线保存；离开此页后无法再次查看这批恢复码。');
      content.append(node('pre', codes.join('\n'), { className: 'account-codes' }));
      form('recovery-ack', [field('saved', '我已安全保存恢复码', 'checkbox')], '继续', async ({ saved }) => {
        if (!saved) throw new Error('请先保存恢复码并勾选确认。');
        flowLocked = false;
        auth.setRecoveryPending?.(false);
        content.replaceChildren();
        await done();
      });
    }
    function showTOTP(totp, name, submit) {
      flowLocked = true;
      content.append(node('p', '用 Authenticator 扫码，再输入六位验证码。'));
      const image = node('img', '', { src: '/api/auth/qr', alt: 'Authenticator 设置二维码', className: 'account-qr' });
      image.onerror = () => { status.textContent = '二维码不可用或设置已过期，请重新开始登录或更换 Authenticator。'; };
      content.append(image);
      if (totp?.secret) {
        content.append(node('p', '无法扫码时，手动输入密钥：'), node('code', totp.secret, { className: 'account-secret' }));
      }
      const verification = form(name, [otpField()], '验证并完成设置', submit);
      if (securityDialog) verification.inputs.get('code').focus();
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
    function openSecurityDialog() {
      if (suppliedContent || securityDialog) return;
      securityDialog = node('dialog', '', { className: 'account-security-dialog' });
      securityDialog.setAttribute('aria-labelledby', 'auth-security-title');
      securityDialog.setAttribute('aria-modal', 'true');
      securityDialog.setAttribute('closedby', 'none');
      securityDialog.oncancel = event => event.preventDefault();
      const body = node('div', '', { className: 'account-security-content' });
      securityDialog.append(body, status);
      content.replaceChildren(securityDialog);
      content = body;
      securityDialog.showModal();
    }
    function showAuth(step, totp) {
      history.replaceState(null, '', authURL(step));
      if (step === 'setup') {
        openSecurityDialog();
        heading('设置 Authenticator');
        showTOTP(totp, 'verify', async (values) => completeAuthentication(await post('/api/auth/verify', values)));
      } else if (step === 'verify') {
        heading('两步验证', '输入 Authenticator 中的六位验证码。');
        form('verify', [otpField()], '登录', async (values) => completeAuthentication(await post('/api/auth/verify', values)));
      } else if (step === 'register') {
        heading('创建账户', '注册后设置 Authenticator。');
        form('register', [emailField(), passwordField('password', '密码', 'new-password'), passwordField('confirm_password', '确认密码', 'new-password')], '注册', async (values) => authTransition(await post('/api/auth/register', values)));
      } else if (step === 'recovery') {
        heading('恢复账户', '使用密码和一个未使用的恢复码，随后重新设置 Authenticator。');
        form('recover', [emailField(), passwordField(), field('recovery_code', '恢复码')], '恢复并重新设置', async (values) => authTransition(await post('/api/auth/recover', values)));
      } else {
        heading('登录 Fleet');
        form('login', [emailField(), passwordField()], '继续', async (values) => authTransition(await post('/api/auth/login', values)));
      }
      if (['login', 'register', 'recovery'].includes(step)) authLinks(step);
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
      ['状态', (device) => device.status === 'unavailable' ? '正在重新连接' : device.status], ['在线', (device) => device.online ? '在线' : '离线'],
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

    function deviceAppearance(value) {
      return root.FleetDeviceAppearance?.normalize(value) || { icon: 'monitor', color: 'steel' };
    }
    function deviceIcon(value) {
      return root.FleetDeviceAppearance?.createIcon(value) || node('span', '设备', { className: 'device-icon' });
    }
    async function probeDevice(id, target) {
      if (!root.fetch || !root.AbortController) return;
      target.textContent = '探测中';
      target.className = 'account-device-latency pending';
      const controller = new root.AbortController();
      const timer = root.setTimeout(() => controller.abort(), 5000);
      const started = root.performance?.now?.() ?? Date.now();
      try {
        const response = await root.fetch(`/${id}/api/health?probe=${Date.now()}`, { cache: 'no-store', signal: controller.signal });
        const elapsed = Math.max(1, Math.round((root.performance?.now?.() ?? Date.now()) - started));
        target.textContent = response.ok ? `${elapsed}ms` : '失败';
        target.className = `account-device-latency ${response.ok ? (elapsed <= 80 ? 'good' : elapsed <= 200 ? 'warn' : 'bad') : 'bad'}`;
      } catch (_) {
        target.textContent = '超时';
        target.className = 'account-device-latency bad';
      } finally { root.clearTimeout(timer); }
    }
    function renderDeviceList(records, appearances, parent) {
      if (!records.length) {
        parent.append(node('p', '暂无设备。请按下方说明添加第一台 Mac。', { className: 'account-muted' }));
        return;
      }
      parent.append(node('p', '拖动设备可调整会话窗口中的显示顺序。', { className: 'account-muted account-device-order-hint' }));
      const list = node('div', '', { className: 'account-device-list' });
      let dragged = null;
      let orderBeforeDrag = [];
      function restoreOrder() {
        const rows = new Map([...list.children].map(row => [row.dataset.deviceId, row]));
        for (const id of orderBeforeDrag) if (rows.has(id)) list.insertBefore(rows.get(id), null);
      }
      async function saveOrder() {
        const order = [...list.children].map(row => row.dataset.deviceId);
        try {
          await auth.json('/api/settings', { method: 'PATCH', body: { order } });
          status.textContent = '设备顺序已保存。';
          await onDevicesChanged?.();
        } catch (error) {
          restoreOrder();
          status.textContent = error.message;
        }
      }
      list.ondragover = event => {
        if (!dragged) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = 'move';
      };
      list.ondrop = async event => {
        event.preventDefault();
        if (!dragged) return;
        const saving = saveOrder();
        dragged = null;
        await saving;
        orderBeforeDrag = [];
      };
      for (const device of records) {
        if (!/^m[1-9]\d*$/.test(device.id || '')) continue;
        let savedAppearance = deviceAppearance(appearances?.[device.id]);
        let draft = { ...savedAppearance };
        const row = node('article', '', { className: 'account-device-row', draggable: true });
        row.dataset.deviceId = device.id;
        const handle = node('span', '', { className: 'account-device-drag', title: '拖动排序' });
        handle.setAttribute('aria-hidden', 'true');
        const iconSlot = node('span', '', { className: 'account-device-icon' });
        const name = node('strong', device.name || device.id, { className: 'account-device-name' });
        const state = node('span', device.status === 'unavailable' ? '正在重新连接' : device.online ? '在线' : '离线', { className: `account-device-state ${device.online ? 'online' : ''}` });
        const ip = node('span', device.ip || '—', { className: 'account-device-ip' });
        const details = node('div', '', { className: 'account-device-details' });
        details.append(name, node('span', ' · ', { className: 'account-device-separator' }), ip, node('span', ' · ', { className: 'account-device-separator' }), state);
        const identity = node('div', '', { className: 'account-device-identity' });
        identity.append(iconSlot, details);
        const latency = node('button', '—', { type: 'button', className: 'account-device-latency', title: '重新探测延迟' });
        latency.onclick = () => probeDevice(device.id, latency);
        const edit = node('button', '编辑', { type: 'button', className: 'account-button account-device-edit' });
        const summary = node('div', '', { className: 'account-device-summary' });
        summary.append(handle, identity, latency, edit);
        const editor = node('div', '', { className: 'account-device-editor', hidden: true });
        const nameLabel = node('label', '设备名称', { className: 'account-device-name-field' });
        const nameInput = node('input', '', { name: 'device-name', type: 'text', value: device.name || '', maxLength: 40, autocomplete: 'off' });
        nameLabel.append(nameInput);
        const iconChoices = node('div', '', { className: 'device-icon-choices account-device-icon-choices' });
        iconChoices.setAttribute('role', 'group');
        iconChoices.setAttribute('aria-label', '设备图标');
        const colorChoices = node('div', '', { className: 'device-color-choices account-device-color-choices' });
        colorChoices.setAttribute('role', 'group');
        colorChoices.setAttribute('aria-label', '设备颜色');
        const textField = node('label', '文字图标', { className: 'account-device-name-field' });
        const textInput = node('input', '', { name: 'device-icon-text', type: 'text', maxLength: 4,
          autocomplete: 'off', autocapitalize: 'none', spellcheck: false });
        const textHint = node('span', '1–4 个英文字母或数字，保留输入的大小写。', { className: 'hint' });
        const textError = node('span', '请输入 1–4 个英文字母或数字。', { className: 'device-letter-error', hidden: true });
        textError.setAttribute('role', 'status');
        textField.append(textInput, textHint, textError);
        function validText() { return draft.icon !== 'text' || !!root.FleetDeviceAppearance.letterText(draft.text); }
        function syncText() {
          textField.hidden = draft.icon !== 'text';
          textError.hidden = validText();
          textInput.setAttribute('aria-invalid', String(!validText()));
          save.disabled = !validText();
        }
        textInput.oninput = () => { draft = { ...draft, text: textInput.value }; renderChoices(); };
        function renderChoices() {
          iconChoices.replaceChildren();
          for (const choice of (root.FleetDeviceAppearance?.icons || [])) {
            const option = node('button', '', { type: 'button', className: 'device-icon-choice' });
            option.dataset.deviceIcon = choice.id;
            option.setAttribute('aria-label', choice.label);
            option.setAttribute('aria-pressed', String(draft.icon === choice.id));
            option.onclick = () => { draft = deviceAppearance({ ...draft, icon: choice.id, text: undefined }); renderChoices(); };
            option.append(deviceIcon({ ...draft, icon: choice.id, text: undefined }), node('span', choice.label));
            iconChoices.append(option);
          }
          const textOption = node('button', '', { type: 'button', className: 'device-icon-choice' });
          textOption.dataset.deviceIcon = 'text';
          textOption.setAttribute('aria-label', '文字图标');
          textOption.setAttribute('aria-pressed', String(draft.icon === 'text'));
          textOption.onclick = () => { draft = { ...draft, icon: 'text', text: draft.text || 'A' }; renderChoices(); };
          textOption.append(deviceIcon({ icon: 'text', text: root.FleetDeviceAppearance.letterText(draft.text) || 'A', color: draft.color }), node('span', '文字'));
          iconChoices.append(textOption);
          textInput.value = draft.icon === 'text' ? draft.text || '' : '';
          syncText();
          colorChoices.replaceChildren();
          for (const choice of (root.FleetDeviceAppearance?.colors || [])) {
            const option = node('button', '', { type: 'button', className: 'device-color-choice' });
            option.dataset.deviceColorChoice = choice.id;
            option.setAttribute('aria-label', choice.label);
            option.setAttribute('aria-pressed', String(draft.color === choice.id));
            option.onclick = () => { draft = { ...draft, color: choice.id }; renderChoices(); };
            const swatch = node('span', '', { className: 'device-color-swatch' });
            swatch.dataset.deviceColor = choice.id;
            option.append(swatch);
            colorChoices.append(option);
          }
        }
        const actions = node('div', '', { className: 'account-device-actions' });
        const cancel = node('button', '取消', { type: 'button', className: 'account-button' });
        const remove = node('button', '移除设备', { type: 'button', className: 'account-button danger' });
        const save = node('button', '保存', { type: 'button', className: 'account-button primary' });
        cancel.onclick = () => { draft = { ...savedAppearance }; nameInput.value = device.name || ''; editor.hidden = true; row.draggable = true; };
        edit.onclick = () => { draft = { ...savedAppearance }; renderChoices(); editor.hidden = false; row.draggable = false; nameInput.focus(); };
        save.onclick = async () => {
          if (!validText()) { syncText(); textInput.focus(); return; }
          draft = deviceAppearance(draft);
          save.disabled = true;
          status.textContent = '';
          try {
            if (JSON.stringify(draft) !== JSON.stringify(savedAppearance)) {
              await auth.json('/api/settings', { method: 'PATCH', body: { id: device.id, appearance: draft } });
              savedAppearance = { ...draft };
            }
            const nextName = nameInput.value.trim();
            if (nextName !== (device.name || '')) {
              const names = await auth.json('/api/names', { method: 'POST', body: { id: device.id, name: nextName } });
              device.name = names?.[device.id] || nextName || device.id;
              name.textContent = device.name;
              nameInput.value = device.name;
            }
            iconSlot.replaceChildren(deviceIcon(savedAppearance));
            editor.hidden = true;
            row.draggable = true;
            status.textContent = '设备设置已保存。';
            await onDevicesChanged?.();
          } catch (error) { status.textContent = error.message; }
          finally { save.disabled = false; }
        };
        remove.onclick = async () => {
          if (!confirm(`移除 ${device.name || device.id}？该设备将无法继续通过 Fleet 访问。`)) return;
          remove.disabled = true;
          try {
            await auth.json(`/api/devices/${device.id}`, { method: 'DELETE' });
            await showAddDevice();
            await onDevicesChanged?.();
          } catch (error) { status.textContent = error.message; remove.disabled = false; }
        };
        actions.append(remove, cancel, save);
        editor.append(nameLabel, node('span', '图标', { className: 'account-device-editor-label' }), iconChoices, textField,
          node('span', '颜色', { className: 'account-device-editor-label' }), colorChoices, actions);
        iconSlot.append(deviceIcon(savedAppearance));
        row.append(summary, editor);
        row.ondragstart = event => {
          if (!row.draggable) return;
          dragged = row;
          orderBeforeDrag = [...list.children].map(item => item.dataset.deviceId);
          event.dataTransfer.effectAllowed = 'move';
          event.dataTransfer.setData('text/plain', device.id);
        };
        row.ondragover = event => {
          if (!dragged || dragged === row) return;
          event.preventDefault();
          const box = row.getBoundingClientRect();
          list.insertBefore(dragged, event.clientY > box.top + box.height / 2 ? row.nextElementSibling : row);
        };
        row.ondragend = () => {
          if (!dragged) return;
          restoreOrder();
          dragged = null;
          orderBeforeDrag = [];
        };
        list.append(row);
        void probeDevice(device.id, latency);
      }
      parent.append(list);
    }
    async function showAddDevice() {
      const serviceOrigin = location.origin;
      let release = null;
      try {
        release = nativeClientRelease(await auth.json('/enroll/client-release.json'));
      } catch (_) {}
      const [deviceData, settings] = await Promise.all([auth.json('/api/devices'), auth.json('/api/settings')]);
      heading('设备');
      const deviceSection = section('设备列表');
      deviceSection.id = 'devices';
      renderDeviceList(deviceData.devices || [], settings.deviceAppearance || {}, deviceSection);
      const add = section('新增设备');
      add.id = 'add-device';
      add.append(node('h3', 'macOS 客户端'));
      const downloads = node('div', '', { className: 'account-downloads' });
      if (release) {
        downloads.append(node('a', '下载 Fleet Agent Settings', { href: release.assets.dmg.path, download: 'Fleet-Agent-Settings.dmg', className: 'account-button primary' }));
        add.append(downloads);
        add.append(node('p', `Fleet Agent Settings ${release.version} · macOS ${release.minimum_macos} 及以上 · ${(release.assets.dmg.size / 1048576).toFixed(1)} MB`, { className: 'account-muted' }));
      } else add.append(node('p', 'Fleet Agent Settings 尚未发布，暂不提供下载。', { className: 'account-error' }));
      const steps = node('ol', '', { className: 'account-steps' });
      for (const description of [
        '打开 DMG，将 Fleet Agent Settings 拖入“应用程序”，或点击“安装并启动”。内置 Fleet Agent，无需另行下载。',
        '打开 Fleet Agent Settings，在“关联账号”填写下方服务网页地址，点击“打开网页授权”。',
        '浏览器登录并完成 Authenticator 验证，核对账号与设备名称，点击“授权并连接”；应用会自动接入。',
        '授权过期或取消后，在应用中重新点击“打开网页授权”。',
        '在“磁盘权限”打开系统设置，将页面中的 Fleet Agent.app 图标拖入完全磁盘访问列表并打开开关，返回点击“重启并检查”。',
        '关闭应用窗口后，后台继续运行。',
      ]) steps.append(node('li', description));
      add.append(steps);
      const address = node('p', '', { className: 'account-service-address' });
      const copy = node('button', '', { type: 'button', className: 'account-copy-icon', title: '复制服务器地址' });
      address.append(node('span', '本服务网页地址：'), node('a', serviceOrigin, { href: serviceOrigin }), copy);
      copy.setAttribute('aria-label', '复制服务器地址');
      copy.onclick = async () => {
        try {
          if (!root.navigator?.clipboard?.writeText) throw new Error('浏览器无法复制，请手动复制上方服务网页地址。');
          await root.navigator.clipboard.writeText(serviceOrigin);
          status.textContent = '服务器地址已复制。';
        } catch (error) { status.textContent = error.message; }
      };
      add.append(address);
      add.append(node('p', '更新：Fleet Agent Settings → 关于 → 检查更新。', { className: 'account-muted' }));
    }

    async function showAccount() {
      const sessionData = await auth.json('/api/auth/sessions');
      flowLocked = false;
      heading('账号设置', auth.user.email);
      const sessions = section('登录设备', content, true);
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
      const passwords = section('更改密码', content, true);
      form('password', [passwordField('current_password', '当前密码'), passwordField('password', '新密码', 'new-password'), passwordField('confirm_password', '确认新密码', 'new-password'), otpField()], '保存密码', async (values) => {
        const result = await post('/api/auth/password', values);
        if (result?.login_required) { auth.invalidate({ returnTo: '/account' }); return; }
        await showAccount();
        status.textContent = '密码已更改。';
      }, passwords);
      const totp = section('更换 Authenticator', content, true);
      form('totp-start', [passwordField(), otpField()], '开始更换', async (values) => {
        const result = await post('/api/auth/totp/start', values);
        heading('更换 Authenticator', '扫描新二维码，并使用新的 Authenticator 验证码确认。');
        showTOTP(result, 'totp-confirm', async (verification) => {
          const confirmed = await post('/api/auth/totp/confirm', verification);
          showCodes(confirmed.recovery_codes, showAccount);
        });
      }, totp);
      const recovery = section('重新生成恢复码', content, true);
      recovery.append(node('p', '重新生成后，旧恢复码将立即失效。'));
      form('recovery-codes', [passwordField(), otpField()], '重新生成', async (values) => {
        if (!confirm('重新生成恢复码并使旧恢复码失效？')) return;
        const result = await post('/api/auth/recovery-codes', values);
        showCodes(result.recovery_codes, showAccount);
      }, recovery);
      const logout = button('退出登录', () => auth.logout(), content);
      logout.className = 'account-logout-link';
    }

    async function showAdmin() {
      if (auth.user.role !== 'admin') { heading('需要管理员权限', '请使用管理员账户登录。'); return; }
      const overview = await auth.json('/api/admin/overview');
      heading('管理', `用户 ${overview.users} · 设备 ${overview.devices} · 在线 ${overview.online}`);
      const detail = section('用户详情');
      detail.hidden = true;
      let selectedUser = null;
      async function showUser(identity, eventPage = 1, eventSearch = '') {
        const query = eventPage !== 1 || eventSearch ? `?event_page=${eventPage}&event_search=${encodeURIComponent(eventSearch)}` : '';
        const result = await auth.json(`/api/admin/users/${encodeURIComponent(identity)}${query}`);
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
        form('admin-event-search', [field('search', '搜索事件')], '搜索事件', values => showUser(identity, 1, values.search), events);
        events.append(node('p', `第 ${eventPage} 页 · 共 ${result.event_total ?? result.events.length} 条`));
        metadata(result.events, [['事件', (event) => event.action || event.type], ['时间', (event) => formatTime(event.created_at)],
          ['操作者', (event) => event.actor_id], ['IP', (event) => event.ip]], events);
        button('事件上一页', () => showUser(identity, eventPage - 1, eventSearch), events, false, () => eventPage <= 1);
        button('事件下一页', () => showUser(identity, eventPage + 1, eventSearch), events, false, () => eventPage * (result.event_page_size || 50) >= (result.event_total ?? result.events.length));
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
      heading('授权 Fleet Agent Settings', `关联账号：${auth.user.email}`);
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
      if (flowLocked || submittingForms > 0) throw new Error('请先完成验证并保存恢复码。');
      if (page === 'auth') {
        nav.replaceChildren();
        const step = location.pathname.replace(/\/$/, '').split('/')[2] || 'login';
        if (['verify', 'setup'].includes(step)) {
          const state = await auth.resumeAuth();
          if (state.state === 'full') location.replace(next);
          else authTransition(state);
        } else showAuth(['login', 'register', 'recovery'].includes(step) ? step : 'login');
      } else {
        await auth.me();
        const accountLink = link('账号', '/account', page === 'account');
        const deviceLink = link('设备', '/account#add-device', page === 'add-device');
        for (const [target, destination] of [[accountLink, 'account'], [deviceLink, 'add-device']]) {
          target.onclick = async event => {
            event.preventDefault();
            try {
              await start(destination);
              if (!suppliedContent) history.replaceState(null, '', destination === 'account' ? '/account' : '/account#add-device');
            } catch (error) { status.textContent = error.message; }
          };
        }
        nav.replaceChildren(link('Fleet', '/'), accountLink, deviceLink);
        if (page === 'account') await showAccount();
        else if (page === 'add-device') await showAddDevice();
        else if (page === 'admin') await showAdmin();
        else if (page === 'enrollment') await showEnrollment();
      }
      document.documentElement.dataset.auth = 'ready';
    }
    return { start, canLeave: () => !flowLocked && submittingForms === 0 };
  }

  root.FleetAccountPages = { createPages, nativeClientRelease };
  if (root.document?.body?.dataset.fleetPage) {
    const pages = createPages({ document: root.document, auth: root.FleetAuth, location: root.location,
      history: root.history, confirm: root.confirm.bind(root) });
    const requestedPage = root.document.body.dataset.fleetPage === 'account' && root.location.hash === '#add-device' ? 'add-device' : root.document.body.dataset.fleetPage;
    pages.start(requestedPage).catch((error) => {
      if (error.status === 401) return;
      root.document.querySelector('#page-status').textContent = error.message;
      const retry = root.document.createElement('a');
      retry.href = root.location.href;
      retry.textContent = '重新加载';
      root.document.querySelector('#page-status').append(retry);
    });
  }
})(globalThis);
