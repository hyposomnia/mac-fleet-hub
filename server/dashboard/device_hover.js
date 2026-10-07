/* Collapsed device rows expand in place above neighbouring content. */
(function (root) {
  'use strict';
  function init({onSelect = () => {}, onSettings = () => {}} = {}) {
    const doc = root.document, app = doc.querySelector('#app'), nav = doc.querySelector('#host-list');
    if (!app || !nav || nav.dataset.deviceHoverReady) return;
    nav.dataset.deviceHoverReady = 'true';
    const media = root.matchMedia('(min-width: 861px) and (hover: hover) and (pointer: fine)');
    let current, closeTimer;
    function close(immediate = true) {
      root.clearTimeout(closeTimer);
      const entry = current;
      if (!entry) return;
      entry.closing = true;
      entry.panel.dataset.open = 'false';
      if (immediate) { entry.panel.remove(); current = undefined; }
      else closeTimer = root.setTimeout(() => {
        if (current !== entry) return;
        entry.panel.remove(); current = undefined;
      }, 240);
    }
    function eligible() { return media.matches && app.dataset.railCollapsed === 'true'; }
    function show(source) {
      if (!eligible() || !source || !/^m\d+$/.test(source.dataset.mac || '')) return;
      if (current?.source === source) {
        root.clearTimeout(closeTimer); current.closing = false; current.panel.dataset.open = 'true'; return;
      }
      close();
      const rect = source.getBoundingClientRect();
      const railWidth = parseFloat(root.getComputedStyle(doc.querySelector('#rail')).getPropertyValue('--rail-w')) || 260;
      const width = Math.max(rect.width, Math.min(railWidth - 24, root.innerWidth - rect.left - 12));
      const panel = doc.createElement('div'); panel.className = 'device-hover-card';
      panel.dataset.open = 'false';
      panel.style.left = `${rect.left}px`; panel.style.top = `${rect.top}px`;
      panel.style.setProperty('--device-hover-collapsed-width', `${rect.width}px`);
      panel.style.setProperty('--device-hover-width', `${width}px`);
      const row = source.cloneNode(true);
      row.className += ' device-hover-row'; row.removeAttribute('title'); row.setAttribute('type', 'button');
      row.querySelector('.i')?.remove();
      row.addEventListener('click', () => { close(); onSelect(source.dataset.mac); });
      const settings = doc.createElement('button'); settings.className = 'device-hover-settings'; settings.textContent = 'ⓘ';
      settings.setAttribute('type', 'button');
      settings.setAttribute('aria-label', `${source.getAttribute('aria-label')} 设置`);
      settings.addEventListener('click', () => { close(); onSettings(source.dataset.mac); });
      panel.append(row, settings); doc.body.append(panel);
      const entry = {source, panel, mode: app.dataset.mode}; current = entry;
      panel.addEventListener('pointerenter', () => {
        root.clearTimeout(closeTimer); entry.closing = false; panel.dataset.open = 'true';
      });
      panel.addEventListener('pointerleave', event => {
        if (!source.contains(event.relatedTarget)) close(false);
      });
      // Separate frames preserve the collapsed width as the animation's origin.
      root.requestAnimationFrame(() => root.requestAnimationFrame(() => {
        if (current === entry && !entry.closing && eligible()) panel.dataset.open = 'true';
      }));
    }
    nav.addEventListener('pointerover', event => {
      if (event.pointerType === 'touch') return;
      const source = event.target.closest('.host[data-mac]');
      if (source && nav.contains(source)) show(source);
    });
    nav.addEventListener('pointerout', event => {
      if (!current || current.source.contains(event.relatedTarget) || current.panel.contains(event.relatedTarget)) return;
      close(false);
    });
    nav.addEventListener('focusin', event => show(event.target.closest('.host[data-mac]')));
    doc.addEventListener('focusin', event => {
      if (current && !current.source.contains(event.target) && !current.panel.contains(event.target)) close();
    });
    doc.addEventListener('pointerdown', event => {
      if (current && !current.source.contains(event.target) && !current.panel.contains(event.target)) close();
    });
    doc.addEventListener('keydown', event => { if (event.key === 'Escape') close(); });
    doc.addEventListener('scroll', () => close(), true);
    root.addEventListener('resize', () => close());
    media.addEventListener('change', () => close());
    if (root.MutationObserver) {
      const observer = new root.MutationObserver(() => {
        if (current && (!eligible() || !current.source.isConnected || app.dataset.mode !== current.mode)) close();
      });
      observer.observe(app, {attributes: true, attributeFilter: ['data-rail-collapsed', 'data-mode']});
      observer.observe(nav, {childList: true});
    }
    return {close};
  }
  root.FleetDeviceHover = {init};
})(globalThis);
