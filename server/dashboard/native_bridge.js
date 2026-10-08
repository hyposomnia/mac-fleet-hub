'use strict';

(function (root) {
  if (root.__fleetNativeVersion !== 1 || !root.webkit?.messageHandlers?.fleet || root !== root.top ||
      !['/', '/index.html'].includes(location.pathname)) return;
  const post = (value) => root.webkit.messageHandlers.fleet.postMessage(value);
  if (!root.FleetWorkspace) { post({ type: 'unsupported' }); return; }
  document.documentElement.dataset.native = 'ios';
  const css = document.createElement('link');
  css.rel = 'stylesheet';
  css.href = '/native.css?v=186';
  document.head.append(css);
  let stop = null;
  let busy = false;
  const start = () => {
    if (stop) return;
    stop = FleetWorkspace.subscribe((payload) => post({ type: 'snapshot', payload }));
  };
  root.FleetNative = Object.freeze({
    async dispatch(message) {
      if (busy) { post({ type: 'result', id: message.id, error: '操作正在进行，请稍候' }); return; }
      busy = true;
      try {
        const result = await FleetWorkspace.execute(message, { allowTerminal: true });
        post({ type: 'result', id: message.id, ...result });
      } catch (error) { post({ type: 'result', id: message.id, error: error.message }); }
      finally { busy = false; }
    },
    refresh() {
      FleetWorkspace.execute({ command: 'refresh' }, { allowTerminal: true }).catch(() => {});
    },
  });
  addEventListener('pagehide', () => { stop?.(); stop = null; });
  addEventListener('pageshow', start);
  document.addEventListener('fleet:auth-lost', () => FleetWorkspace.publish());
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start, { once: true });
  else start();
})(globalThis);
