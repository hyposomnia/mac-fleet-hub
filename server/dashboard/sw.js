// PWA 外壳缓存。终端、API 与用户文件必须实时，明确不进入 Cache Storage。
const CACHE = 'fleet-shell-v51';
const SHELL = [
  '/', '/index.html', '/style.css?v=51',
  '/vendor/purify.min.js?v=3.2.6', '/vendor/marked.min.js?v=15.0.12',
  '/markdown.js?v=51', '/preview.js?v=51', '/chat_model.js?v=51', '/app.js?v=51',
  '/manifest.webmanifest', '/icons/icon.svg', '/icons/icon-180.png', '/icons/icon-192.png',
  '/icons/icon-512.png', '/icons/icon-maskable-512.png',
];
const SHELL_KEYS = new Set(SHELL);

function isSensitivePath(pathname) {
  return pathname.startsWith('/api/') ||
    /^\/m\d+(?:\/|$)/.test(pathname) ||
    pathname.startsWith('/auth/') ||
    pathname.startsWith('/enroll/') ||
    pathname.startsWith('/files/');
}

async function cacheFresh(cache, request, key = request) {
  const response = await fetch(request, { cache: 'no-cache' });
  if (response && response.ok) await cache.put(key, response.clone());
  return response;
}

self.addEventListener('install', (event) => {
  event.waitUntil((async () => {
    const cache = await caches.open(CACHE);
    // 单个可选图标失败不应让整个 PWA 安装失败。
    await Promise.allSettled(SHELL.map(async (url) => {
      const response = await fetch(url, { cache: 'reload' });
      if (!response.ok) throw new Error(`${url}: ${response.status}`);
      await cache.put(url, response);
    }));
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
      try {
        const response = await fetch(request, { cache: 'no-cache' });
        if (response.ok) await cache.put('/index.html', response.clone());
        return response;
      } catch (_) {
        return (await cache.match('/index.html')) ||
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
