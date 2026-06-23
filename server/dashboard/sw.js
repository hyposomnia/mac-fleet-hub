// 极简 service worker —— 只为让 PWA 可安装 + 外壳静态资源离线可用。
// 不缓存 /api/ 与 iframe 内容（终端/文件必须实时）。
const CACHE = 'fleet-shell-v1';
const SHELL = ['/fleet/', '/fleet/index.html', '/fleet/style.css', '/fleet/app.js', '/fleet/manifest.webmanifest'];

self.addEventListener('install', (e) => {
  e.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)).then(() => self.skipWaiting()));
});
self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  // 只对外壳静态资源做 cache-first；其余（api / term / files）一律直连网络。
  if (e.request.method === 'GET' && SHELL.includes(url.pathname)) {
    e.respondWith(caches.match(e.request).then((r) => r || fetch(e.request)));
  }
});
