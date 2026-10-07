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
    const panel = doc.querySelector('#sescol');
    const media = host.matchMedia('(min-width: 861px)');
    let saved = {};
    try { saved = JSON.parse(host.localStorage.getItem(key) || '{}') || {}; } catch (_) {}
    let railCollapsed = saved.railCollapsed === true;
    let sessionsCollapsed = saved.sessionsCollapsed === true;
    function persist() {
      try { host.localStorage.setItem(key, JSON.stringify({railCollapsed, sessionsCollapsed})); } catch (_) {}
    }
    function render() {
      const desktop = media.matches, sessions = app.dataset.mode !== 'files';
      app.dataset.railCollapsed = String(railCollapsed);
      app.dataset.sessionsCollapsed = String(sessionsCollapsed);
      railButton.setAttribute('aria-expanded', String(!railCollapsed));
      railButton.hidden = railCollapsed;
      logoButton.disabled = !railCollapsed;
      logoButton.setAttribute('aria-expanded', String(!railCollapsed));
      logoButton.setAttribute('aria-label', railCollapsed ? '展开设备栏' : 'Fleet Hub');
      logoButton.title = railCollapsed ? '展开设备栏' : 'Fleet Hub';
      sessionButton.setAttribute('aria-expanded', String(!sessionsCollapsed));
      trigger.hidden = !(desktop && sessions && sessionsCollapsed);
      trigger.setAttribute('aria-expanded', String(!sessionsCollapsed));
      panel.inert = desktop && (!sessions || sessionsCollapsed);
    }
    railButton.onclick = () => { railCollapsed = true; persist(); render(); logoButton.focus(); };
    logoButton.onclick = () => { railCollapsed = false; persist(); render(); railButton.focus(); };
    sessionButton.onclick = () => {
      sessionsCollapsed = true; persist(); render(); trigger.focus();
    };
    trigger.onclick = () => {
      if (!media.matches || app.dataset.mode === 'files') return;
      sessionsCollapsed = false; persist(); render(); sessionButton.focus();
    };
    media.addEventListener('change', render);
    host.FleetSidebarLayout.sync = render;
    render();
  }
  host.FleetSidebarLayout = {init, sync: () => {}};
})(globalThis);
