'use strict';
(function (root) {
  if (root !== root.top || root.__fleetNativeVersion !== 1 || !root.webkit?.messageHandlers?.fleet ||
      !['/', '/index.html'].includes(location.pathname) || root.FleetNativeDialogs) return;
  const post = value => root.webkit.messageHandlers.fleet.postMessage(value);
  const ids = ['device-modal', 'new-project-modal', 'file-name-modal', 'file-path-modal', 'confirm-file-delete', 'confirm-kill',
    'chat-approval-popover', 'chat-options-popover', 'file-settings-menu', 'chat-subagent-drawer'];
  const nodes = new Map();
  let names = new WeakMap();
  let sequence = 0, previous = '', current = null, currentIdentity = '', queued = false;
  const style = document.createElement('style');
  style.textContent = ids.map(id => `html[data-native="ios"] #${id}:not([hidden])`).join(',') +
    ',html[data-native="ios"] .file-row-menu:not([hidden]){opacity:0!important;pointer-events:none!important}' +
    'html[data-native="ios"][data-native-layout="compact"] #app.term-open .win-head{padding-right:14px!important}';
  document.head.append(style);
  if (typeof root.openHostModal === 'function') root.openHostModal = id => {
    if (root.FleetWorkspace?.snapshot().ready) post({ type: 'native-sheet', sheet: 'device', device: id });
  };
  if (typeof root.openUnifiedSettings === 'function') root.openUnifiedSettings = page => {
    if (root.FleetWorkspace?.snapshot().ready) post({ type: 'native-sheet', sheet: 'settings', page });
    return Promise.resolve();
  };
  function key(element) {
    if (!names.has(element)) names.set(element, 'control-' + ++sequence);
    const value = names.get(element); nodes.set(value, element); return value;
  }
  function label(element) {
    return (element.getAttribute('aria-label') || element.getAttribute('title') || element.innerText || '').replace(/\s+/g, ' ').trim();
  }
  function fieldLabel(element) {
    return element.getAttribute('aria-label') || [...(element.labels || [])].map(label).join(' ') || element.placeholder || element.name || '输入';
  }
  function publish() {
    queued = false;
    const snapshot = root.FleetWorkspace?.snapshot();
    if (currentIdentity && currentIdentity !== (snapshot?.identity || '')) {
      names = new WeakMap();
      for (const element of [...ids.map(id => document.getElementById(id)), ...document.querySelectorAll('.file-row-menu')]) {
        if (element) element.hidden = true;
      }
    }
    const active = snapshot?.ready ? [...ids.map(id => document.getElementById(id)), ...document.querySelectorAll('.file-row-menu')].find(element => element && !element.closest('[hidden]')) : null;
    nodes.clear(); current = active;
    currentIdentity = snapshot?.identity || '';
    let payload = null;
    if (active) {
      active.inert = true;
      active.setAttribute('aria-hidden', 'true');
      if (!active.id) active.id = 'native-file-menu-' + ++sequence;
      const items = [], footer = [];
      const selector = 'button,a[href],input,textarea,select,h1,h2,h3,p,[role="status"],[role="alert"]' +
        (active.id === 'chat-subagent-drawer' ? ',pre,li,#chat-subagent-detail-name,#chat-subagent-detail-state,#chat-subagent-detail-meta,.chat-subagent-detail-empty' : '');
      for (const element of active.querySelectorAll(selector)) {
        if (element.closest('[hidden]') || !element.getClientRects().length || element.closest('[data-close]') || element.matches('input[type="hidden"],input[type="file"]')) continue;
        const tag = element.tagName.toLowerCase();
        if (element.parentElement?.closest('button,a[href]')) continue;
        const item = { id: key(element), label: label(element), disabled: !!element.disabled || element.getAttribute('aria-disabled') === 'true', selected: element.getAttribute('aria-pressed') === 'true' || element.getAttribute('aria-selected') === 'true' || element.getAttribute('aria-checked') === 'true' || (element.classList.contains('device-option-main') && element.parentElement?.getAttribute('aria-current') === 'true'), destructive: element.classList.contains('danger') };
        const view = element.getAttribute('data-file-view');
        if (view) item.icon = ({ icons: 'FileIcons', list: 'FileList', columns: 'FileColumns' })[view];
        const group = element.closest('[data-native-menu-group]')?.getAttribute('data-native-menu-group');
        if (group) item.group = group;
        if (['input', 'textarea', 'select'].includes(tag)) {
          item.label = fieldLabel(element); item.value = element.type === 'password' ? '' : element.value;
          item.placeholder = element.placeholder || '';
          item.kind = tag === 'select' ? 'picker' : element.type === 'checkbox' ? 'toggle' : element.type === 'password' ? 'secure' : element.type === 'number' ? 'number' : 'field';
          if (item.kind === 'toggle') item.value = String(element.checked);
          if (item.kind === 'picker') item.options = [...element.options].map(option => ({ value: option.value, label: option.textContent, disabled: option.disabled }));
        } else if (tag === 'button' || tag === 'a') { item.kind = 'button'; if (!item.label) continue; }
        else {
          if (element.querySelector('button,input,select,textarea,a[href],p,pre,li')) continue;
          item.kind = tag.startsWith('h') ? 'heading' : 'text'; if (!item.label) continue;
        }
        (element.closest('.modal-foot') ? footer : items).push(item);
      }
      const heading = active.querySelector('.modal-head,.new-project-title,h1,h2');
      const title = active.getAttribute('aria-label') || (heading ? label(heading).replace(/关闭$/, '').trim() : '') ||
        ({'chat-approval-popover':'会话权限','chat-options-popover':'模型与推理','file-settings-menu':'文件设置','chat-subagent-drawer':'Sub Agent'})[active.id] || (active.classList.contains('file-row-menu') ? '文件操作' : '选择');
      payload = { id: active.id, title, items, footer };
      if (active.id === 'file-settings-menu' || active.classList.contains('file-row-menu')) {
        const trigger = active.id === 'file-settings-menu' ? document.getElementById('file-settings') : active.parentElement?.querySelector('.file-row-menu-trigger');
        const rect = trigger?.getBoundingClientRect?.();
        if (rect && [rect.x, rect.y, rect.width, rect.height].every(Number.isFinite)) {
          payload.anchor = { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
        }
      }
    }
    const signature = JSON.stringify(payload);
    if (signature !== previous) { previous = signature; post({ type: 'native-dialog', payload }); }
  }
  function schedule() {
    if (queued) return; queued = true; queueMicrotask(publish);
  }
  function close() {
    if (!current) return;
    if (current.classList.contains('overlay')) closeOverlay(current.id);
    else if (current.id === 'chat-approval-popover') closeChatApproval();
    else if (current.id === 'chat-options-popover') closeChatOptions();
    else if (current.id === 'file-settings-menu') closeFileSettings();
    else if (current.id === 'chat-subagent-drawer') setChatSubagentPanel(state.chat, 'closed');
    else if (current.classList.contains('file-row-menu')) closeFileMenus();
    publish();
  }
  root.FleetNativeDialogs = Object.freeze({
    act(dialog, id, value) {
      const snapshot = root.FleetWorkspace?.snapshot();
      if (!snapshot?.ready || (snapshot.identity || '') !== currentIdentity || !current || current.id !== dialog || current.closest('[hidden]')) throw new Error('弹层已变化，请重试');
      if (id === '__close') { close(); return; }
      const element = nodes.get(id);
      if (!element || !current.contains(element) || element.closest('[hidden]') || !element.getClientRects().length || element.disabled || element.getAttribute('aria-disabled') === 'true') throw new Error('操作已失效');
      if (value !== null && value !== undefined) {
        if (element.tagName === 'SELECT' && ![...element.options].some(option => option.value === value && !option.disabled)) throw new Error('选项已变化');
        if (element.type === 'checkbox') element.checked = value === 'true';
        else element.value = element.maxLength > 0 ? String(value).slice(0, element.maxLength) : String(value);
        element.dispatchEvent(new Event('input', { bubbles: true }));
        element.dispatchEvent(new Event('change', { bubbles: true }));
      } else {
        if (element.type === 'submit' && element.form && !element.form.checkValidity()) throw new Error('请填写完整且有效的表单');
        element.click();
        if (element.tagName === 'A' && current?.classList.contains('file-row-menu')) closeFileMenus();
      }
      schedule();
    }, publish,
  });
  new MutationObserver(schedule).observe(document.getElementById('app'), { subtree: true, childList: true, characterData: true, attributes: true,
    attributeFilter: ['hidden', 'disabled', 'aria-disabled', 'aria-selected', 'aria-pressed', 'aria-checked', 'aria-current', 'class', 'style', 'value'] });
  document.addEventListener('input', schedule, true);
  document.addEventListener('change', schedule, true);
  document.addEventListener('fleet:auth-lost', schedule);
  publish();
})(globalThis);
