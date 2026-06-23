'use strict';

// ============================================================
//  配置 —— 加 / 改一台 Mac 只动这里
// ============================================================
const BASE = '/fleet';
const MACS = [
  { id: 'm1', name: 'Mac 1' },
  { id: 'm2', name: 'Mac 2' },
  { id: 'm3', name: 'Mac 3' },
];

// ============================================================
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const apiBase = (id) => `${BASE}/${id}`;

const state = {
  macId: null,
  scope: 'active',     // active | all
  tab: 'sessions',     // sessions | files | devices
  termSid: null,       // 当前打开终端的 tmux 会话名（用于 watch / reload）
  nodes: {},           // name -> online
  watchTimer: null,
};

// ---------- 工具 ----------
function relTime(ms) {
  const d = Date.now() - ms;
  if (d < 60e3) return '刚刚';
  if (d < 3600e3) return Math.round(d / 60e3) + ' 分钟前';
  if (d < 86400e3) return Math.round(d / 3600e3) + ' 小时前';
  return Math.round(d / 86400e3) + ' 天前';
}
function projName(cwd) { return cwd ? cwd.split('/').filter(Boolean).pop() : '(未知项目)'; }
function projDir(cwd) { const p = (cwd || '').split('/'); return p.slice(0, -1).join('/').replace(/^\/Users\/[^/]+/, '~'); }
async function api(id, path, opts) {
  const r = await fetch(`${apiBase(id)}/api/${path}`, opts);
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json();
}

// ============================================================
//  侧栏 / 设备列表
// ============================================================
function renderMacList() {
  const nav = $('#mac-list');
  nav.innerHTML = '';
  for (const m of MACS) {
    const g = document.createElement('div');
    g.className = 'mac-group';
    const online = state.nodes[m.id];
    g.innerHTML = `
      <div class="mac-head">
        <span class="dot ${online ? 'on' : 'off'}"></span>${m.name}
        <span class="state">${online ? '在线' : '离线'}</span>
      </div>
      <button class="mac-item" data-mac="${m.id}" data-go="sessions">▤ 会话</button>
      <button class="mac-item" data-mac="${m.id}" data-go="files">▦ 文件</button>`;
    nav.appendChild(g);
  }
  $$('.mac-item', nav).forEach((b) => b.onclick = () => {
    selectMac(b.dataset.mac);
    b.dataset.go === 'files' ? showFiles() : showTab('sessions');
    closeSidebar();
  });
  highlightActive();
}
function highlightActive() {
  $$('.mac-item').forEach((b) =>
    b.classList.toggle('active', b.dataset.mac === state.macId && b.dataset.go === state.tab));
}

// ============================================================
//  节点在线状态（绿/灰点）
// ============================================================
async function refreshNodes() {
  try {
    const r = await fetch(`${BASE}/api/nodes.json`, { cache: 'no-store' });
    if (!r.ok) return;
    const list = await r.json();
    const map = {};
    // headscale json: 节点有 name(主机名 mac1/2/3) 与 online 字段
    for (const n of (Array.isArray(list) ? list : (list.nodes || []))) {
      const name = (n.givenName || n.name || '').toLowerCase();
      const online = n.online === true || n.online === 'true';
      MACS.forEach((m, i) => {
        if (name === m.id || name === `mac${i + 1}` || name === m.name.toLowerCase().replace(' ', ''))
          map[m.id] = online;
      });
    }
    state.nodes = map;
    renderMacList();
  } catch (_) { /* 静默 */ }
}

// ============================================================
//  选 Mac + 会话列表
// ============================================================
function selectMac(id) { state.macId = id; $('#crumb').textContent = MACS.find((m) => m.id === id)?.name || ''; }

async function loadSessions() {
  if (!state.macId) return;
  const wrap = $('#session-groups');
  wrap.innerHTML = '<div class="empty">加载中…</div>';
  let data;
  try { data = await api(state.macId, `sessions?scope=${state.scope}`); }
  catch (e) { wrap.innerHTML = `<div class="empty">连不上 ${state.macId}（${e.message}）</div>`; return; }

  const sessions = data.sessions || [];
  $('#cnt-active').textContent = sessions.filter((s) => s.live).length;
  $('#cnt-all').textContent = data.total ?? sessions.length;

  // 按项目(cwd)分组
  const groups = {};
  for (const s of sessions) (groups[s.cwd] ||= []).push(s);
  // 组内按时间倒序；组按"最近活动"倒序
  const ordered = Object.entries(groups).map(([cwd, arr]) => {
    arr.sort((a, b) => (b.live - a.live) || (b.mtime - a.mtime));
    return { cwd, arr, last: Math.max(...arr.map((s) => s.mtime)) };
  }).sort((a, b) => b.last - a.last);

  if (!ordered.length) { wrap.innerHTML = '<div class="empty">没有会话</div>'; return; }
  wrap.innerHTML = '';
  for (const g of ordered) {
    const head = document.createElement('div');
    head.className = 'grp-head';
    head.innerHTML = `▸ ${projName(g.cwd)} <span class="path">${projDir(g.cwd)}</span><span class="count">${g.arr.length}</span>`;
    wrap.appendChild(head);
    for (const s of g.arr) {
      const el = document.createElement('button');
      el.className = 'sess';
      el.innerHTML = `
        <div class="body">
          <div class="title">${esc(s.title || '(无标题)')}${s.live ? '<span class="badge">桌面使用中</span>' : ''}</div>
          <div class="meta">${esc(s.gitBranch || '')}${s.gitBranch ? ' · ' : ''}${relTime(s.mtime)}</div>
        </div><span class="chev">›</span>`;
      el.onclick = () => openSession(s.sessionId);
      wrap.appendChild(el);
    }
  }
}
function esc(s) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML; }

// ============================================================
//  打开 / 新建会话 → iframe 终端
// ============================================================
async function openSession(sessionId) {
  try {
    const r = await api(state.macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sessionId }),
    });
    enterTerm(r.url, r.sid);
  } catch (e) { alert('打开失败：' + e.message); }
}
async function newSessionIn(cwd) {
  try {
    const r = await api(state.macId, 'new', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ cwd }),
    });
    enterTerm(r.url, r.sid);
  } catch (e) { alert('新建失败：' + e.message); }
}
function enterTerm(url, sid) {
  state.termSid = sid || null;
  $('#frame').src = url;
  showView('frame-view');
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = !isMobile();
  startWatch();
}

// ============================================================
//  Desktop→ttyd 变更检测：轮询 agent /api/watch，弹横幅
// ============================================================
function startWatch() {
  stopWatch();
  hideBanner();
  if (!state.termSid) return;
  state.watchTimer = setInterval(async () => {
    try {
      const r = await api(state.macId, `watch?sid=${encodeURIComponent(state.termSid)}`);
      if (r.external) showBanner();
    } catch (_) { /* 忽略 */ }
  }, 5000);
}
function stopWatch() { if (state.watchTimer) { clearInterval(state.watchTimer); state.watchTimer = null; } }
function showBanner() { $('#reload-banner').hidden = false; }
function hideBanner() { $('#reload-banner').hidden = true; }
async function doReload() {
  if (!state.termSid) return;
  try { await api(state.macId, 'reload', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ sid: state.termSid }) }); }
  catch (_) {}
  hideBanner();
  $('#frame').contentWindow.location.reload();  // 重连 ttyd → attach 到已 kill+resume 的窗口
}

// ============================================================
//  文件视图
// ============================================================
function showFiles() {
  if (!state.macId) return;
  state.tab = 'files';
  state.termSid = null; stopWatch(); hideBanner();
  $('#frame').src = `${apiBase(state.macId)}/files/`;
  showView('frame-view');
  $('#reconnect-btn').hidden = true; $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = true;
  setTabActive('files'); highlightActive();
}

// ============================================================
//  新建会话：项目目录列表
// ============================================================
async function showProjects() {
  if (!state.macId) return;
  showView('projects-view');
  const list = $('#project-list');
  list.innerHTML = '<div class="empty">加载中…</div>';
  try {
    const data = await api(state.macId, 'projects');
    const ps = data.projects || [];
    if (!ps.length) { list.innerHTML = '<div class="empty">没有已知项目目录</div>'; return; }
    list.innerHTML = '';
    for (const p of ps) {
      const el = document.createElement('button');
      el.className = 'sess';
      el.innerHTML = `<div class="body"><div class="title">${esc(projName(p.cwd))}</div>
        <div class="meta">${esc(projDir(p.cwd))} · ${p.count} 个会话</div></div><span class="chev">＋</span>`;
      el.onclick = () => newSessionIn(p.cwd);
      list.appendChild(el);
    }
  } catch (e) { list.innerHTML = `<div class="empty">加载失败：${e.message}</div>`; }
}

// ============================================================
//  视图 / Tab 切换
// ============================================================
function showView(id) { $$('.view').forEach((v) => v.hidden = v.id !== id); }
function showTab(tab) {
  state.tab = tab; setTabActive(tab); highlightActive();
  if (tab === 'sessions') { showView('sessions-view'); loadSessions(); }
  else if (tab === 'files') showFiles();
  else if (tab === 'devices') { showView('sessions-view'); /* 设备=会话列表入口；侧栏即设备 */ openSidebar(); }
}
function setTabActive(tab) { $$('#tabbar .tab').forEach((t) => t.classList.toggle('active', t.dataset.tab === tab)); }

// ---------- 移动端侧栏抽屉 ----------
const isMobile = () => matchMedia('(max-width: 760px)').matches;
function openSidebar() { $('#sidebar').classList.add('open'); }
function closeSidebar() { $('#sidebar').classList.remove('open'); }

// ============================================================
//  移动端：辅助按键条 + 输入框 → 注入 ttyd（best-effort）
// ============================================================
let ctrlHeld = false;
function sendToTerm(text, key) {
  const win = $('#frame').contentWindow;
  // ttyd 把 xterm 实例挂在 window.term（多数版本如此）；优先用它的 _core 写入。
  try {
    const t = win && win.term;
    if (t && typeof t.paste === 'function' && text) { t.focus(); t.paste(text); return true; }
    if (t && t._core && key) { /* 交给下面的键事件兜底 */ }
  } catch (_) { /* 跨源/未就绪 */ }
  // 兜底：向 iframe 的 textarea 派发键盘事件
  try {
    const doc = win.document;
    const ta = doc.querySelector('textarea') || doc.activeElement;
    if (ta) {
      ta.focus();
      if (text) { ta.value = text; ta.dispatchEvent(new InputEvent('input', { data: text, bubbles: true })); }
      if (key) ta.dispatchEvent(new KeyboardEvent('keydown', { key, ctrlKey: ctrlHeld, bubbles: true }));
      return true;
    }
  } catch (_) {}
  return false;
}
function wireMobileInput() {
  $$('.keybar button').forEach((b) => b.onclick = () => {
    const k = b.dataset.key;
    if (k === 'Control') { ctrlHeld = !ctrlHeld; b.classList.toggle('held', ctrlHeld); return; }
    sendToTerm(null, k); if (ctrlHeld) { ctrlHeld = false; $$('.keybar button').forEach((x) => x.classList.remove('held')); }
  });
  $('#send-btn').onclick = () => {
    const inp = $('#cmd-input');
    if (inp.value) { sendToTerm(inp.value + '\n'); inp.value = ''; }
  };
  $('#cmd-input').addEventListener('keydown', (e) => { if (e.key === 'Enter') $('#send-btn').click(); });
}

// ============================================================
//  初始化
// ============================================================
function init() {
  renderMacList();
  refreshNodes(); setInterval(refreshNodes, 30000);
  wireMobileInput();

  $('#menu-btn').onclick = () => $('#sidebar').classList.toggle('open');
  $$('.seg-btn').forEach((b) => b.onclick = () => {
    state.scope = b.dataset.scope;
    $$('.seg-btn').forEach((x) => x.classList.toggle('active', x === b));
    loadSessions();
  });
  $$('#tabbar .tab').forEach((t) => t.onclick = () => showTab(t.dataset.tab));
  $('#new-session').onclick = showProjects;
  $('#proj-back').onclick = () => showTab('sessions');
  $('#reload-btn').onclick = doReload;
  $('#reload-dismiss').onclick = hideBanner;
  $('#reconnect-btn').onclick = () => { const f = $('#frame'); f.contentWindow.location.reload(); };
  $('#fullscreen-btn').onclick = () => $('#frame').requestFullscreen?.();

  // 默认选第一台、展示会话
  selectMac(MACS[0].id);
  showTab('sessions');

  if ('serviceWorker' in navigator) navigator.serviceWorker.register(`${BASE}/sw.js`).catch(() => {});
}
document.addEventListener('DOMContentLoaded', init);
