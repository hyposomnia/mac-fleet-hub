(function (host) {
  'use strict';
  const labels = { account: '账号设置', 'add-device': '添加设备', automation: '自动化', sessions: '会话设置' };
  function create({ document, overlay, panels, buttons, title, status, confirm, load }) {
    let page = null;
    let generation = 0;
    let flow = null;
    let trigger = null;
    let baseline = new Map();
    let inertSiblings = [];
    const panelFor = selected => panels.find(panel => panel.dataset.settingsPanel === selected);
    const controls = () => [...(panelFor(page)?.querySelectorAll('input, textarea, select') || [])]
      .filter(control => !control.readOnly && !control.disabled && !control.closest?.('[hidden]'));
    const snapshot = () => { baseline = new Map(controls().map(control => [control, [control.value, control.checked]])); };
    function canLeave() {
      if (flow?.canLeave && !flow.canLeave()) {
        status.textContent = '请先完成验证并保存恢复码，再离开此页。';
        return false;
      }
      const dirty = controls().some(control => {
        const original = baseline.get(control) || [control.defaultValue || '', control.defaultChecked || false];
        return control.value !== original[0] || Boolean(control.checked) !== Boolean(original[1]);
      });
      return !dirty || confirm('离开此页并放弃未提交的设置？');
    }
    function clearPrivate() {
      for (const panel of panels) {
        if (['account', 'add-device'].includes(panel.dataset.settingsPanel)) panel.replaceChildren();
      }
      flow = null;
    }
    function reset() {
      generation++;
      overlay.hidden = true;
      panels.forEach(panel => { panel.hidden = true; });
      clearPrivate();
      baseline.clear();
      status.textContent = '';
      page = null;
      for (const [element, previous] of inertSiblings) element.inert = previous;
      inertSiblings = [];
    }
    function close() {
      if (overlay.hidden) return true;
      if (!canLeave()) return false;
      reset();
      trigger?.focus();
      return true;
    }
    async function open(selected, source) {
      if (!Object.hasOwn(labels, selected)) return false;
      if (!overlay.hidden && page === selected) return true;
      if (!overlay.hidden && !canLeave()) return false;
      if (overlay.hidden) {
        trigger = source || document.activeElement;
        inertSiblings = [...(overlay.parentElement?.children || [])].filter(element => element !== overlay).map(element => [element, element.inert]);
        inertSiblings.forEach(([element]) => { element.inert = true; });
      }
      const revision = ++generation;
      clearPrivate();
      page = selected;
      title.textContent = labels[selected];
      status.textContent = '正在加载…';
      overlay.hidden = false;
      panels.forEach(panel => { panel.hidden = panel.dataset.settingsPanel !== selected; });
      buttons.forEach(button => {
        const active = button.dataset.settingsPage === selected;
        button.setAttribute('aria-selected', String(active));
        button.tabIndex = active ? 0 : -1;
      });
      const button = buttons.find(button => button.dataset.settingsPage === selected);
      button?.focus();
      const container = document.createElement('div');
      try {
        const loaded = await load(selected, container);
        if (revision !== generation || overlay.hidden) return false;
        flow = loaded;
        if (['account', 'add-device'].includes(selected)) panelFor(selected).replaceChildren(container);
        status.textContent = '';
        snapshot();
      } catch (error) {
        if (revision === generation && !overlay.hidden) status.textContent = error.message;
      }
      return true;
    }
    function keydown(event) {
      if (overlay.hidden) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopImmediatePropagation();
        close();
      } else if (event.key === 'Tab') {
        const focusable = [...overlay.querySelectorAll('button, a[href], input, select, textarea, [tabindex="0"]')]
          .filter(element => !element.disabled && !element.closest?.('[hidden]') && element.getClientRects?.().length);
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (!first || event.shiftKey && document.activeElement === first || !event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          (event.shiftKey ? last : first)?.focus();
        }
        event.stopImmediatePropagation();
      } else if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key) && buttons.includes(document.activeElement)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        const direction = ['ArrowLeft', 'ArrowUp'].includes(event.key) ? -1 : 1;
        const index = (buttons.indexOf(document.activeElement) + direction + buttons.length) % buttons.length;
        void open(buttons[index].dataset.settingsPage);
      }
    }
    document.addEventListener('keydown', keydown, true);
    buttons.forEach(button => { button.onclick = () => open(button.dataset.settingsPage); });
    return { open, close, reset, markSaved: snapshot, keydown, get page() { return page; } };
  }
  host.FleetSettingsDialog = { create };
})(globalThis);
