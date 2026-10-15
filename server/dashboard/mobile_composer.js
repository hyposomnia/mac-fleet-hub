(function (root) {
  'use strict';
  if (root.FleetMobileComposer) {
    root.FleetMobileComposer.init();
    return;
  }
  const doc = root.document;
  if (!doc) return;
  let controller;
  let nativeKeyboardOpen = null;

  function init() {
    if (controller) return controller;
    const input = doc.querySelector('#chat-input');
    const composer = doc.querySelector('#chat-composer');
    if (!input || !composer) return;
    const html = doc.documentElement;
    const pane = doc.querySelector('#chat-pane');
    const viewport = root.visualViewport;
    const media = root.matchMedia?.('(max-width: 860px)');
    let baselineWidth = viewport?.width || root.innerWidth;
    let baselineHeight = viewport?.height || root.innerHeight;
    let keyboardWasOpen = false;
    let dismissed = false;
    let toolPointer = false;
    let pendingResize = false;
    let lastValue = input.value;
    let lastBusinessHeight = input.style.height;
    const mobile = () => media?.matches === true || root.__fleetNativeVersion === 1;
    const focused = () => doc.activeElement === input;
    const setData = (node, key, value) => {
      if (node.dataset[key] !== String(value)) node.dataset[key] = String(value);
    };
    function resize() {
      pendingResize = false;
      lastValue = input.value;
      lastBusinessHeight = input.style.height;
      if (!mobile() || composer.dataset.mobileComposerExpanded !== 'true') return;
      input.style.setProperty('--mobile-composer-input-height', '0px');
      const height = input.value ? Math.min(144, input.scrollHeight) : 44;
      input.style.setProperty('--mobile-composer-input-height', `${Math.max(44, height)}px`);
    }
    function scheduleResize() {
      if (pendingResize) return;
      pendingResize = true;
      (root.requestAnimationFrame || root.setTimeout)(resize);
    }
    function sync() {
      setData(html, 'mobileComposer', mobile());
      if (!mobile()) {
        setData(composer, 'mobileComposerExpanded', false);
        return;
      }
      const width = viewport?.width || root.innerWidth;
      const height = viewport?.height || root.innerHeight;
      if (Math.abs(width - baselineWidth) > 1) {
        baselineHeight = height;
        keyboardWasOpen = false;
      }
      baselineWidth = width;
      baselineHeight = Math.max(baselineHeight, height);
      const keyboardOpen = nativeKeyboardOpen ?? (viewport
        ? (viewport.scale || 1) === 1 && baselineHeight - height > 120
        : html.classList.contains('visual-keyboard-open'));
      if (keyboardWasOpen && !keyboardOpen && focused()) dismissed = true;
      if (!keyboardWasOpen && keyboardOpen && focused()) dismissed = false;
      keyboardWasOpen = keyboardOpen;
      setData(composer, 'mobileComposerExpanded', !doc.hidden && !pane?.hidden &&
        ((focused() && !dismissed) || toolPointer));
      scheduleResize();
    }
    function beginEditing() {
      dismissed = false;
      sync();
    }
    input.addEventListener('focus', beginEditing);
    input.addEventListener('pointerdown', beginEditing);
    input.addEventListener('input', beginEditing);
    input.addEventListener('blur', sync);
    composer.addEventListener('pointerdown', event => {
      if (event.target === input) return;
      toolPointer = true;
    });
    const finishPointer = () => {
      if (!toolPointer) return;
      root.setTimeout(() => { toolPointer = false; sync(); }, 0);
    };
    doc.addEventListener('pointerup', finishPointer);
    doc.addEventListener('pointercancel', finishPointer);
    doc.addEventListener('visibilitychange', sync);
    root.addEventListener?.('pageshow', sync);
    root.addEventListener?.('resize', sync);
    viewport?.addEventListener('resize', sync);
    media?.addEventListener('change', sync);
    if (root.MutationObserver) {
      const observer = new root.MutationObserver(() => {
        if (input.value !== lastValue || input.style.height !== lastBusinessHeight) scheduleResize();
      });
      observer.observe(input, {attributes: true, attributeFilter: ['style']});
      const visibility = new root.MutationObserver(sync);
      visibility.observe(html, {attributes: true, attributeFilter: ['class']});
      if (pane) visibility.observe(pane, {attributes: true, attributeFilter: ['hidden']});
    }
    setData(composer, 'mobileComposerReady', true);
    controller = Object.freeze({sync});
    sync();
    return controller;
  }

  root.FleetMobileComposer = Object.freeze({
    init,
    sync: () => init()?.sync(),
    setKeyboardOpen(value) {
      nativeKeyboardOpen = typeof value === 'boolean' ? value : null;
      init()?.sync();
    },
  });
  if (!init() && doc.readyState === 'loading') doc.addEventListener('DOMContentLoaded', init, {once: true});
})(typeof globalThis !== 'undefined' ? globalThis : window);
