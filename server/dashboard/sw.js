// PWA 外壳缓存。终端、API 与用户文件必须实时，明确不进入 Cache Storage。
const CACHE = 'fleet-shell-v197';
const FILE_TYPE_ICONS = [
  'audio', 'c', 'console', 'cpp', 'csharp', 'css', 'dart', 'database', 'docker',
  'document', 'exe', 'font', 'git', 'go', 'html', 'image', 'java', 'javascript',
  'json', 'kotlin', 'lock', 'log', 'lua', 'markdown', 'npm', 'pdf', 'php',
  'powerpoint', 'powershell', 'python', 'r', 'react', 'ruby', 'rust', 'sass',
  'settings', 'svelte', 'swift', 'table', 'toml', 'typescript', 'video', 'vue',
  'word', 'xml', 'yaml', 'zip',
].map((name) => `/icons/file-types/${name}.svg`);
const CODEMIRROR_ASSETS = [
  '/vendor/codemirror/lib/codemirror.css?v=5.65.20',
  '/vendor/codemirror/lib/codemirror.js?v=5.65.20',
  'javascript', 'xml', 'jsx', 'css', 'go', 'python', 'ruby', 'shell', 'yaml', 'toml', 'properties',
].map((name, index) => index < 2 ? name : `/vendor/codemirror/mode/${name}/${name}.js?v=5.65.20`);
const SHELL = [
  '/', '/index.html', '/automation-guide.html', '/theme.js?v=197', '/device_appearance.js?v=191', '/style.css?v=197', '/account.css?v=197', '/auth-client.js?v=197',
  '/vendor/purify.min.js?v=3.2.6', '/vendor/marked.min.js?v=15.0.12',
  ...CODEMIRROR_ASSETS,
  "/fleet_core.js?v=188", "/workspace.js?v=188", "/native_bridge.js?v=188", "/native.css?v=188", "/titanium.css?v=197", "/icons/logo.svg?v=197",
  '/markdown.js?v=197', '/preview.js?v=197', '/chat_model.js?v=197',
  '/account.js?v=197', '/settings_dialog.js?v=197', '/auth_effects.js?v=188',
  '/upload_model.js?v=197', '/sidebar_layout.js?v=188', '/device_hover.js?v=188', '/workspace_tabs.js?v=188', '/compact_composer.js?v=188', '/app.js?v=197',
  '/manifest.webmanifest', '/icons/icon.svg?v=192', '/icons/favicon.svg?v=192', '/icons/icon-180.png?v=192', '/icons/icon-192.png?v=192',
  '/icons/icon-512.png?v=192', '/icons/icon-maskable-512.png?v=192',
  ...FILE_TYPE_ICONS,
];
const SHELL_KEYS = new Set(SHELL);

function isSensitivePath(pathname) {
  return /^\/(?:api|auth|account|admin|enroll|oauth)(?:\/|$)/.test(pathname) ||
    /^\/m\d+(?:\/|$)/.test(pathname) ||
    pathname.startsWith('/files/');
}

async function cacheFresh(cache, request, key = request) {
  const response = await fetch(request, { cache: 'no-cache' });
  if (response && response.ok && !response.redirected) await cache.put(key, response.clone());
  return response;
}

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    // 单个可选图标失败不应让整个 PWA 安装失败。
    await Promise.allSettled(SHELL.map(async (url) => {
      const response = await fetch(url, { cache: 'reload' });
      if (!response.ok || response.redirected) throw new Error(`${url}: ${response.status}`);
      await cache.put(url, response);
    }));
    await self.skipWaiting();
  })());
});

self.addEventListener('activate', (event) => {
  event.waitUntil((async () => {
    const keys = await caches.keys();
    await Promise.all(keys.filter((key) => key !== CACHE).map((key) => caches.delete(key)));
    await self.clients.claim();
  })());
});

self.addEventListener('message', (event) => {
  if (event.data?.type === 'SKIP_WAITING') self.skipWaiting();
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin || isSensitivePath(url.pathname)) return;

  if (request.mode === 'navigate') {
    event.respondWith((async () => {
      const cache = await caches.open(CACHE);
      const navigationKey = url.pathname === '/automation-guide.html' ? '/automation-guide.html' : '/index.html';
      try {
        const response = await fetch(request, { cache: 'no-cache' });
        if (response.ok && !response.redirected) await cache.put(navigationKey, response.clone());
        return response;
      } catch (_) {
        return (await cache.match(navigationKey)) ||
          (await cache.match('/index.html')) ||
          (await cache.match('/')) ||
          new Response('fleet hub 暂时离线', {
            status: 503,
            headers: { 'content-type': 'text/plain; charset=utf-8' },
          });
      }
    })());
    return;
  }

  const key = url.pathname + url.search;
  if (!SHELL_KEYS.has(key) && !SHELL_KEYS.has(url.pathname)) return;
  const refresh = caches.open(CACHE).then((cache) => cacheFresh(cache, request));
  event.waitUntil(refresh.then(() => undefined).catch(() => {}));
  event.respondWith(
    caches.match(request)
      .then((cached) => cached || refresh)
      .catch(() => new Response('', { status: 504, statusText: 'Offline' }))
  );
});
