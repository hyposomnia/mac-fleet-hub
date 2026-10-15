'use strict';

(function (root) {
  if (root !== root.top || root.__fleetNativeVersion !== 1 || !root.webkit?.messageHandlers?.fleet ||
      !['/', '/index.html'].includes(location.pathname) || root.FleetNativeAppearance) return;
  const names = ['bg', 'surface', 'surface-1', 'surface-2', 'surface-hover', 'text', 'text-1', 'text-2', 'text-3',
    'accent', 'accent-1', 'accent-text', 'accent-contrast', 'highlight', 'online', 'offline', 'wait', 'danger',
    'session-list-bg', 'session-detail-bg'];
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 1;
  const context = canvas.getContext('2d', { willReadFrequently: true });
  if (!context) return;
  const probe = document.createElement('span');
  probe.setAttribute('aria-hidden', 'true');
  probe.style.cssText = 'position:fixed;width:0;height:0;overflow:hidden;pointer-events:none;visibility:hidden';
  document.documentElement.appendChild(probe);
  let previous = '';
  function publish(force = false) {
    const computed = getComputedStyle(document.documentElement);
    const tokens = {};
    for (const name of names) {
      const value = computed.getPropertyValue('--' + name).trim();
      if (!value || !CSS.supports('color', value)) continue;
      context.clearRect(0, 0, 1, 1);
      probe.style.color = 'var(--' + name + ')';
      context.fillStyle = getComputedStyle(probe).color;
      context.fillRect(0, 0, 1, 1);
      const pixel = context.getImageData(0, 0, 1, 1).data;
      if (pixel[3] !== 255) continue;
      tokens[name] = '#' + Array.from(pixel.slice(0, 3), channel => channel.toString(16).padStart(2, '0')).join('').toUpperCase();
    }
    const signature = JSON.stringify(tokens);
    if (!Object.keys(tokens).length || (!force && signature === previous)) return;
    previous = signature;
    root.webkit.messageHandlers.fleet.postMessage({ type: 'appearance', tokens });
  }
  root.FleetNativeAppearance = Object.freeze({ publish: () => publish(true) });
  new MutationObserver(() => publish()).observe(document.documentElement, {
    attributes: true, attributeFilter: ['style', 'data-theme'],
  });
  addEventListener('pageshow', () => publish(true));
  publish(true);
})(globalThis);
