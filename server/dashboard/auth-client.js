'use strict';

(function (root) {
  const PRIVATE_KEYS = ['fleet-session-read-v2', 'fleet-show-archived-sessions', 'fleet-ui-state-v1', 'fleet-pool'];
  const PENDING_PATHS = new Set(['/api/auth/register', '/api/auth/login', '/api/auth/verify', '/api/auth/recover']);

  function createClient(environment) {
    let user = null;
    let csrfToken = '';
    let expired = false;
    let generation = 0;
    const origin = environment.location.origin;
    const sessionError = () => Object.assign(new Error('登录已失效，请重新登录。'), { status: 401 });
    const prefix = (identity) => `fleet-user:${encodeURIComponent(identity)}:`;

    function safeNext(target) {
      try {
        const decoded = decodeURIComponent(target || '/');
        if (!decoded.startsWith('/') || decoded.startsWith('//') || /[\\\r\n]/.test(decoded)) return '/';
        const url = new URL(target || '/', origin);
        if (url.origin !== origin || /^\/auth(?:\/|$)/.test(url.pathname)) return '/';
        return url.pathname + url.search + url.hash;
      } catch (_) { return '/'; }
    }

    function clearPrivate(identity = user?.id) {
      for (const name of ['localStorage', 'sessionStorage']) {
        try {
          const storage = environment[name];
          const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index));
          for (const key of keys) {
            if (PRIVATE_KEYS.includes(key) || (identity != null && key?.startsWith(prefix(identity)))) storage.removeItem(key);
          }
        } catch (_) {}
      }
    }

    function invalidate({ broadcast = true, returnTo } = {}) {
      if (expired) return;
      expired = true;
      generation++;
      clearPrivate();
      user = null;
      csrfToken = '';
      environment.onUnauthorized?.();
      if (broadcast) broadcastChange();
      const next = safeNext(returnTo ?? (environment.location.pathname + environment.location.search));
      environment.location.replace(`/auth${next === '/' ? '' : `?next=${encodeURIComponent(next)}`}`);
    }

    function broadcastChange() {
      try { environment.localStorage.setItem('fleet-auth-event', `${Date.now()}:${Math.random()}`); } catch (_) {}
    }

    async function request(input, options = {}) {
      const url = new URL(typeof input === 'string' ? input : input.url, origin);
      if (url.origin !== origin) throw new Error('请求必须同源（same origin）。');
      const method = (options.method || input.method || 'GET').toUpperCase();
      const pending = PENDING_PATHS.has(url.pathname);
      const headers = new Headers(options.headers || input.headers);
      if (expired) throw sessionError();
      if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && !pending) {
        if (!user || !csrfToken) throw sessionError();
        headers.set('X-CSRF-Token', csrfToken);
      }
      const started = generation;
      const response = await environment.fetch(input, { ...options, method, headers,
        credentials: 'same-origin', cache: 'no-store' });
      if (started !== generation) throw sessionError();
      if (response.status === 401 && !pending) {
        invalidate();
        throw sessionError();
      }
      return response;
    }

    async function json(url, options = {}) {
      const headers = new Headers(options.headers);
      let body = options.body;
      if (body != null && typeof body === 'object') {
        headers.set('Content-Type', 'application/json');
        body = JSON.stringify(body);
      }
      const started = generation;
      const response = await request(url, { ...options, headers, body });
      const data = response.status === 204 ? null : await response.json().catch(() => null);
      if (started !== generation) throw sessionError();
      if (!response.ok) {
        const message = data?.message || data?.error?.message || (typeof data?.error === 'string' ? data.error : `请求失败（${response.status}）。`);
        throw Object.assign(new Error(message), { status: response.status });
      }
      if (url === '/api/auth/verify' && data?.user) {
        clearPrivate();
        user = data.user;
        csrfToken = '';
        broadcastChange();
      }
      return data;
    }

    async function me() {
      const data = await json('/api/auth/me');
      if (!data?.user?.id || !data.csrf_token) throw new Error('登录信息不完整，请重新登录。');
      if (user && user.id !== data.user.id) {
        clearPrivate();
        generation++;
      }
      user = data.user;
      csrfToken = data.csrf_token;
      clearPrivate(null);
      return data;
    }

    async function logout() {
      await json('/api/auth/logout', { method: 'POST' });
      invalidate({ returnTo: '/' });
    }

    function storageKey(key) {
      if (!user || expired) throw sessionError();
      return prefix(user.id) + key;
    }

    return { fetch: request, json, me, logout, invalidate, clearPrivate, storageKey, safeNext,
      get user() { return user; }, get csrfToken() { return csrfToken; } };
  }

  root.FleetAuth = { createClient };
  if (root.document && root.location) {
    if (root.document.documentElement.classList.contains('account-root')) {
      let theme;
      try { theme = root.localStorage.getItem('fleet-theme'); } catch (_) {}
      if (!['light', 'dark'].includes(theme)) theme = root.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
      root.document.documentElement.dataset.theme = theme;
      root.document.querySelector('meta[name="theme-color"]').content = theme === 'light' ? '#f6f7f9' : '#090c12';
    }
    const nativeFetch = root.fetch.bind(root);
    const client = createClient({ fetch: nativeFetch, location: root.location,
      get localStorage() { return root.localStorage; }, get sessionStorage() { return root.sessionStorage; },
      onUnauthorized: () => {
        root.document.documentElement.dataset.auth = 'expired';
        root.document.querySelectorAll('#app, #preview-page, #page-content').forEach((node) => { node.hidden = true; });
        root.document.dispatchEvent(new Event('fleet:auth-lost'));
      } });
    root.FleetAuth = Object.assign(client, { createClient });
    root.fetch = client.fetch;
    root.addEventListener('storage', (event) => {
      if (event.key === 'fleet-auth-event' && client.user) client.invalidate({ broadcast: false });
    });
    root.addEventListener('pageshow', (event) => {
      if (event.persisted) {
        root.document.documentElement.dataset.auth = 'checking';
        client.me().then(() => { root.document.documentElement.dataset.auth = 'ready'; }).catch(() => {});
      }
    });
  }
})(globalThis);
