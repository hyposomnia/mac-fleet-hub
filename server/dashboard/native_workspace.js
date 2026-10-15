'use strict';

(function (root) {
  if (root !== root.top || root.__fleetNativeVersion !== 1 || !root.webkit?.messageHandlers?.fleet ||
      !['/', '/index.html'].includes(location.pathname) || root.FleetNativeUI) return;
  const post = value => root.webkit.messageHandlers.fleet.postMessage(value);
  const style = document.createElement('style');
  style.textContent = `
    html[data-native-overlay="true"], html[data-native-overlay="true"] body,
    html[data-native-overlay="true"] #app { background: transparent !important; }
    html[data-native-overlay="true"] #app > :not(.overlay):not(#toast-wrap) { visibility: hidden !important; }
  `;
  document.head.append(style);
  let previousOverlay;
  function updateOverlay() {
    const visible = [...document.querySelectorAll('#app > .overlay')].some(element => !element.hidden);
    document.documentElement.dataset.nativeOverlay = String(visible);
    if (previousOverlay !== visible) {
      previousOverlay = visible;
      post({ type: 'overlay', visible });
    }
    root.FleetWorkspace?.publish();
  }
  for (const overlay of document.querySelectorAll('#app > .overlay')) {
    new MutationObserver(updateOverlay).observe(overlay, { attributes: true, attributeFilter: ['hidden'] });
  }
  root.FleetNativeUI = Object.freeze({
    async dispatch(message) {
      if (message.assistant === 'claude') {
        post({ type: 'result', id: message.id, error: 'App 暂不支持 Claude' });
        return;
      }
      if (message.command === 'open' && message.terminal === true) {
        post({ type: 'result', id: message.id, error: 'App 不支持主动切换到 ttyd' });
        return;
      }
      if (message.command !== 'settings-page') return root.FleetNative?.dispatch(message);
      try {
        if (!root.FleetWorkspace?.snapshot().ready) throw new Error('请先登录');
        const page = message.page || 'account';
        if (!['account', 'add-device', 'appearance', 'automation', 'sessions'].includes(page)) throw new Error('不支持的设置页面');
        if (typeof root.openUnifiedSettings !== 'function') throw new Error('网关尚未提供统一设置，请更新 dashboard');
        await root.openUnifiedSettings(page, document.getElementById('user-btn'));
        updateOverlay();
        post({ type: 'result', id: message.id, opened: true });
      } catch (error) { post({ type: 'result', id: message.id, error: error.message }); }
    },
  });
  updateOverlay();
})(globalThis);
