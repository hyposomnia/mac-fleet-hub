// 极简 service worker —— 只为让 PWA 可安装 + 外壳静态资源离线可用。
// 不缓存 /api/ 与 iframe 内容（终端/文件必须实时）。
const CACHE = 'fleet-shell-v5';
const SHELL = ['/', '/index.html', '/style.css', '/app.js', '/manifest.webmanifest'];

self.addEventListener('install', (e) => {
  // {cache:'reload'} 绕过浏览器 HTTP 缓存预缓存最新外壳——否则可能把旧版 app.js 存进来，
  // 部署后新 index.html 与旧 app.js 不匹配会直接报错（如引用已删除的 DOM 元素）。
  e.waitUntil(
    caches.open(CACHE)
      .then((c) => Promise.all(SHELL.map((u) => fetch(u, { cache: 'reload' }).then((r) => c.put(u, r)))))
      .then(() => self.skipWaiting())
  );
});
self.addEventListener('activate', (e) => {
  e.waitUntil(caches.keys().then((ks) => Promise.all(ks.filter((k) => k !== CACHE).map((k) => caches.delete(k)))).then(() => self.clients.claim()));
});
self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  // 外壳静态资源：network-first 且强制重新校验（{cache:'no-cache'}）——默认 fetch 会命中浏览器
  // HTTP 缓存，把旧外壳喂回来，造成「部署了新版但客户端仍跑旧 app.js」。失败才回退缓存（离线可用）。
  // 其余（api / term / files）一律直连网络，不缓存。
  if (e.request.method === 'GET' && SHELL.includes(url.pathname)) {
    e.respondWith(
      fetch(e.request, { cache: 'no-cache' }).then((r) => {
        const copy = r.clone();
        caches.open(CACHE).then((c) => c.put(e.request, copy));
        return r;
      }).catch(() => caches.match(e.request))
    );
  }
});
