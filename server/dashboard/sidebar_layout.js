/* Desktop navigation preferences; mobile keeps its existing full-screen flow. */
(function (host) {
  'use strict';
  const key = 'fleet-sidebar-layout-v1';
  function init() {
    const doc = host.document, app = doc.querySelector('#app');
    if (!app || app.dataset.sidebarReady) return;
    app.dataset.sidebarReady = 'true';
    const railButton = doc.querySelector('#rail-collapse');
    const logoButton = doc.querySelector('#rail-expand');
    const sessionButton = doc.querySelector('#sessions-collapse');
    const trigger = doc.querySelector('#sessions-launcher');
    const backdrop = doc.querySelector('#sessions-backdrop');
    const panel = doc.querySelector('#sescol');
    const media = host.matchMedia('(min-width: 861px)');
    let saved = {};
    try { saved = JSON.parse(host.localStorage.getItem(key) || '{}') || {}; } catch (_) {}
    let railCollapsed = saved.railCollapsed === true;
    let sessionsCollapsed = saved.sessionsCollapsed === true;
    let open = false;
    function persist() {
      try { host.localStorage.setItem(key, JSON.stringify({railCollapsed, sessionsCollapsed})); } catch (_) {}
    }
    function render() {
      const desktop = media.matches, sessions = app.dataset.mode !== 'files';
      app.dataset.railCollapsed = String(railCollapsed);
      app.dataset.sessionsCollapsed = String(sessionsCollapsed);
      app.dataset.sessionsOpen = String(open && desktop && sessions);
      railButton.setAttribute('aria-expanded', String(!railCollapsed));
      railButton.hidden = railCollapsed;
      logoButton.disabled = !railCollapsed;
      logoButton.setAttribute('aria-expanded', String(!railCollapsed));
      logoButton.setAttribute('aria-label', railCollapsed ? '展开设备栏' : 'Fleet Hub');
      logoButton.title = railCollapsed ? '展开设备栏' : 'Fleet Hub';
      sessionButton.setAttribute('aria-expanded', String(!sessionsCollapsed));
      sessionButton.setAttribute('aria-pressed', String(!sessionsCollapsed));
      sessionButton.dataset.pinned = String(!sessionsCollapsed);
      sessionButton.setAttribute('aria-label', sessionsCollapsed ? '固定会话列表' : '取消固定会话列表');
      sessionButton.title = sessionsCollapsed ? '固定会话列表' : '取消固定会话列表';
      trigger.hidden = !(desktop && sessions && sessionsCollapsed && !open);
      trigger.setAttribute('aria-expanded', String(open));
      backdrop.hidden = !(desktop && sessions && open);
      panel.inert = desktop && (!sessions || (sessionsCollapsed && !open));
    }
    function close({restoreFocus = false} = {}) {
      if (!open) return false;
      open = false; render();
      if (restoreFocus) trigger.focus();
      return true;
    }
    railButton.onclick = () => { railCollapsed = true; persist(); render(); logoButton.focus(); };
    logoButton.onclick = () => { railCollapsed = false; persist(); render(); railButton.focus(); };
    sessionButton.onclick = () => {
      sessionsCollapsed = !sessionsCollapsed; open = false; persist(); render();
      if (sessionsCollapsed) trigger.focus();
    };
    trigger.onclick = () => {
      if (!media.matches || app.dataset.mode === 'files') return;
      open = true; render(); sessionButton.focus();
    };
    backdrop.onclick = () => close({restoreFocus: true});
    doc.addEventListener('pointerdown', (event) => {
      if (open && !panel.contains(event.target) && event.target !== trigger && !trigger.contains(event.target)) close();
    });
    doc.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && open) { close({restoreFocus: true}); event.preventDefault(); }
    });
    media.addEventListener('change', () => { open = false; render(); });
    host.FleetSidebarLayout.close = close;
    host.FleetSidebarLayout.sync = () => { open = false; render(); };
    render();
  }
  host.FleetSidebarLayout = {init, close: () => false, sync: () => {}};
})(globalThis);
