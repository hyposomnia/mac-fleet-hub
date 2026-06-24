'use strict';

// ============================================================
//  配置 —— 加 / 改一台 Mac 只动这里
// ============================================================
const BASE = '';   // 挂在子域根路径（mfh.example.com）；若改回子路径部署，改这里（如 '/fleet'）
const MACS = [
  { id: 'm1', name: 'Mac 1' },
  { id: 'm2', name: 'Mac 2' },
  { id: 'm3', name: 'Mac 3' },
];

// ============================================================
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => [...r.querySelectorAll(s)];
const apiBase = (id) => `${BASE}/${id}`;

// 安全 DOM 构造助手（不使用 innerHTML，文本走 textContent 天然转义）
function h(tag, props, ...kids) {
  const e = document.createElement(tag);
  if (props) for (const k in props) {
    const v = props[k];
    if (v == null) continue;
    if (k === 'class') e.className = v;
    else if (k === 'text') e.textContent = v;
    else if (k === 'dataset') Object.assign(e.dataset, v);
    else if (k.startsWith('on') && typeof v === 'function') e[k.toLowerCase()] = v;
    else e.setAttribute(k, v);
  }
  for (const c of kids.flat()) {
    if (c == null || c === false) continue;
    e.append(c.nodeType ? c : document.createTextNode(String(c)));
  }
  return e;
}
function clear(el) { el.replaceChildren(); }

const state = {
  macId: null,
  mode: 'claude',        // claude | files
  scope: 'active',       // active | all
  termSid: null,         // 当前终端 tmux 会话名（watch / reload 用）
  activeSessionId: null, // 当前打开的 claude sessionId（高亮）
  curTitle: '',          // 当前会话标题
  nodes: {},             // id -> online
  watchTimer: null,
};

// ---------- 工具 ----------
const isMobile = () => matchMedia('(max-width: 860px)').matches;
function relTime(ms) {
  const d = Date.now() - ms;
  if (d < 60e3) return '刚刚';
  if (d < 3600e3) return Math.round(d / 60e3) + ' 分钟前';
  if (d < 86400e3) return Math.round(d / 3600e3) + ' 小时前';
  return Math.round(d / 86400e3) + ' 天前';
}
function projName(cwd) { return cwd ? cwd.split('/').filter(Boolean).pop() : '(未知项目)'; }
function projDir(cwd) { const p = (cwd || '').split('/'); return p.slice(0, -1).join('/').replace(/^\/Users\/[^/]+/, '~'); }
function macName(id) { return MACS.find((m) => m.id === id)?.name || id; }
async function api(id, path, opts) {
  const r = await fetch(`${apiBase(id)}/api/${path}`, opts);
  if (!r.ok) throw new Error(`${path}: ${r.status}`);
  return r.json();
}

// ============================================================
//  主题
// ============================================================
function applyTheme(t) {
  document.documentElement.setAttribute('data-theme', t);
  try { localStorage.setItem('fleet-theme', t); } catch (_) {}
}
function initTheme() {
  let t;
  try { t = localStorage.getItem('fleet-theme'); } catch (_) {}
  if (!t) t = matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  applyTheme(t);
}
function toggleTheme() {
  applyTheme(document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light');
}

// ============================================================
//  栏 1：主机栏
// ============================================================
function renderHosts() {
  const nav = $('#host-list');
  clear(nav);
  for (const m of MACS) {
    const online = state.nodes[m.id];
    const info = h('span', { class: 'host-info', title: '详情 / 代理', text: 'ⓘ' });
    info.onclick = (e) => { e.stopPropagation(); openHostModal(m.id); };
    const el = h('button', { class: 'host' + (m.id === state.macId ? ' active' : ''), dataset: { mac: m.id } },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      h('span', { class: 'host-name', text: m.name }),
      info,
    );
    el.onclick = () => { selectMac(m.id); closeDrawers(); };
    nav.appendChild(el);
  }
}
function selectMac(id) {
  state.macId = id;
  $('#m-host-name').textContent = macName(id);
  renderHosts();
  if (state.mode === 'files') loadFiles();
  else loadSessions();
}

// ============================================================
//  在线状态
// ============================================================
async function refreshNodes() {
  try {
    const r = await fetch(`${BASE}/api/nodes.json`, { cache: 'no-store' });
    if (!r.ok) return;
    const list = await r.json();
    const map = {};
    for (const n of (Array.isArray(list) ? list : (list.nodes || []))) {
      const name = (n.givenName || n.name || '').toLowerCase();
      MACS.forEach((m, i) => {
        if (name === m.id || name === `mac${i + 1}`) map[m.id] = n.online === true || n.online === 'true';
      });
    }
    state.nodes = map;
    renderHosts();
  } catch (_) {}
}

// ============================================================
//  模式切换（会话 / 文件）
// ============================================================
function setMode(mode) {
  state.mode = mode;
  $('#app').dataset.mode = mode;
  $$('.mode-btn').forEach((b) => b.classList.toggle('active', b.dataset.mode === mode));
  $('#m-mode').textContent = mode === 'files' ? '▤' : '▦';
  if (mode === 'files') loadFiles();
  else { loadSessions(); restoreTermOrEmpty(); }
}

// ============================================================
//  栏 2：会话列表
// ============================================================
async function loadSessions() {
  if (state.mode !== 'claude' || !state.macId) return;
  const wrap = $('#session-groups');
  clear(wrap); wrap.appendChild(h('div', { class: 'empty', text: '加载中…' }));
  let data;
  try { data = await api(state.macId, `sessions?scope=${state.scope}`); }
  catch (e) {
    clear(wrap);
    wrap.appendChild(h('div', { class: 'empty' }, `连不上 ${macName(state.macId)}`, h('br'), h('small', { text: e.message })));
    return;
  }

  const sessions = data.sessions || [];
  $('#cnt-active').textContent = sessions.filter((s) => s.live).length;
  $('#cnt-all').textContent = data.total ?? sessions.length;

  const groups = {};
  for (const s of sessions) (groups[s.cwd] ||= []).push(s);
  const ordered = Object.entries(groups).map(([cwd, arr]) => {
    arr.sort((a, b) => (b.live - a.live) || (b.mtime - a.mtime));
    return { cwd, arr, last: Math.max(...arr.map((s) => s.mtime)) };
  }).sort((a, b) => b.last - a.last);

  clear(wrap);
  if (!ordered.length) { wrap.appendChild(h('div', { class: 'empty', text: '没有会话' })); return; }
  for (const g of ordered) {
    wrap.appendChild(h('div', { class: 'grp-head' },
      h('span', { text: '▸' }),
      h('span', { class: 'gname', text: projName(g.cwd) }),
      h('span', { class: 'path', text: projDir(g.cwd) }),
      h('span', { class: 'count', text: String(g.arr.length) }),
    ));
    for (const s of g.arr) {
      const title = h('div', { class: 'title' }, s.title || '(无标题)', s.live && h('span', { class: 'badge', text: '桌面使用中' }));
      const meta = (s.gitBranch ? s.gitBranch + ' · ' : '') + relTime(s.mtime);
      const el = h('button', { class: 'sess' + (s.sessionId === state.activeSessionId ? ' active' : ''), dataset: { sid: s.sessionId } },
        h('div', { class: 'body' }, title, h('div', { class: 'meta', text: meta })),
        h('span', { class: 'chev', text: '›' }),
      );
      el.onclick = () => openSession(s.sessionId, s.title);
      wrap.appendChild(el);
    }
  }
}

// ============================================================
//  打开 / 新建会话 → 终端 iframe
// ============================================================
async function openSession(sessionId, title) {
  try {
    const r = await api(state.macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sessionId }),
    });
    state.activeSessionId = sessionId;
    enterTerm(r.url, r.sid, title || '会话');
    $$('.sess').forEach((b) => b.classList.toggle('active', b.dataset.sid === sessionId));
  } catch (e) { alert('打开失败：' + e.message); }
}
async function newSessionIn(cwd) {
  closeModal('projects-modal');
  try {
    const r = await api(state.macId, 'new', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ cwd }),
    });
    state.activeSessionId = null;
    enterTerm(r.url, r.sid, '新会话 · ' + projName(cwd));
  } catch (e) { alert('新建失败：' + e.message); }
}
function enterTerm(url, sid, title) {
  state.termSid = sid || null;
  state.curTitle = title || '会话';
  $('#frame').src = url;
  $('#empty-state').style.display = 'none';
  $('#win-title').textContent = title;
  $('#m-session-name').textContent = title;
  $('#reload-btn-top').hidden = false;
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = !isMobile();
  closeDrawers();
  startWatch();
}
function restoreTermOrEmpty() {
  if (state.termSid) { $('#empty-state').style.display = 'none'; $('#mobile-input').hidden = !isMobile(); }
  else { $('#frame').removeAttribute('src'); $('#empty-state').style.display = 'flex'; $('#mobile-input').hidden = true; }
  $('#win-title').textContent = state.curTitle || '选择一个会话';
}

// ============================================================
//  文件浏览器
// ============================================================
function loadFiles() {
  if (!state.macId) return;
  stopWatch(); hideBanner();
  $('#frame').src = `${apiBase(state.macId)}/files/`;
  $('#empty-state').style.display = 'none';
  $('#win-title').textContent = '文件 · ' + macName(state.macId);
  $('#m-session-name').textContent = '文件';
  $('#reload-btn-top').hidden = true;
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = true;
  closeDrawers();
}

// ============================================================
//  Desktop→ttyd 变更检测
// ============================================================
function startWatch() {
  stopWatch(); hideBanner();
  if (!state.termSid) return;
  state.watchTimer = setInterval(async () => {
    try {
      const r = await api(state.macId, `watch?sid=${encodeURIComponent(state.termSid)}`);
      if (r.external) showBanner();
    } catch (_) {}
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
  try { $('#frame').contentWindow.location.reload(); } catch (_) { const f = $('#frame'); f.src = f.src; }
}

// ============================================================
//  新建会话：项目目录
// ============================================================
async function showProjects() {
  openModal('projects-modal');
  const list = $('#project-list');
  clear(list); list.appendChild(h('div', { class: 'empty', text: '加载中…' }));
  try {
    const data = await api(state.macId, 'projects');
    const ps = data.projects || [];
    clear(list);
    if (!ps.length) { list.appendChild(h('div', { class: 'empty', text: '没有已知项目目录' })); return; }
    for (const p of ps) {
      const el = h('button', { class: 'sess' },
        h('div', { class: 'body' },
          h('div', { class: 'title', text: projName(p.cwd) }),
          h('div', { class: 'meta', text: projDir(p.cwd) + ' · ' + p.count + ' 个会话' }),
        ),
        h('span', { class: 'chev', text: '＋' }),
      );
      el.onclick = () => newSessionIn(p.cwd);
      list.appendChild(el);
    }
  } catch (e) { clear(list); list.appendChild(h('div', { class: 'empty', text: '加载失败：' + e.message })); }
}

// ============================================================
//  主机弹窗（IP / 代理）
// ============================================================
let hostModalMac = null;
async function openHostModal(id) {
  hostModalMac = id;
  $('#hm-title').textContent = macName(id);
  const online = state.nodes[id];
  $('#hm-dot').className = 'dot ' + (online ? 'on' : 'off');
  $('#hm-state').textContent = online ? '在线' : '离线';
  $('#hm-ip').textContent = '加载中…';
  $('#hm-http').value = ''; $('#hm-https').value = ''; $('#hm-proxy-on').checked = false;
  openModal('host-modal');
  try {
    const info = await api(id, 'info');
    $('#hm-ip').textContent = info.meshIP || '—';
    const p = info.proxy || {};
    $('#hm-http').value = p.http || '';
    $('#hm-https').value = p.https || '';
    $('#hm-proxy-on').checked = !!p.enabled;
  } catch (e) { $('#hm-ip').textContent = '连不上（' + e.message + '）'; }
}
async function saveHostProxy() {
  if (!hostModalMac) return;
  const body = {
    enabled: $('#hm-proxy-on').checked,
    http: $('#hm-http').value.trim(),
    https: $('#hm-https').value.trim(),
  };
  const btn = $('#hm-save'); btn.disabled = true; btn.textContent = '保存中…';
  try {
    await api(hostModalMac, 'proxy', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
    closeModal('host-modal');
  } catch (e) { alert('保存失败：' + e.message); }
  finally { btn.disabled = false; btn.textContent = '保存'; }
}

// ============================================================
//  弹窗 / 抽屉
// ============================================================
function openModal(id) { $('#' + id).hidden = false; }
function closeModal(id) { $('#' + id).hidden = true; }
function openDrawer(which) { closeDrawers(); $('#' + which).classList.add('open'); $('#scrim').hidden = false; }
function closeDrawers() { $('#rail').classList.remove('open'); $('#sessions-col').classList.remove('open'); $('#scrim').hidden = true; }

// ============================================================
//  移动端输入辅助 → 注入 ttyd（best-effort）
// ============================================================
let ctrlHeld = false;
function sendToTerm(text, key) {
  const win = $('#frame').contentWindow;
  try {
    const t = win && win.term;
    if (t && typeof t.paste === 'function' && text) { t.focus(); t.paste(text); return true; }
  } catch (_) {}
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
    sendToTerm(null, k);
    if (ctrlHeld) { ctrlHeld = false; $$('.keybar button').forEach((x) => x.classList.remove('held')); }
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
  initTheme();
  renderHosts();
  refreshNodes(); setInterval(refreshNodes, 30000);
  wireMobileInput();

  $('#theme-btn').onclick = toggleTheme;
  $$('.mode-btn').forEach((b) => b.onclick = () => setMode(b.dataset.mode));
  $$('.seg-btn').forEach((b) => b.onclick = () => {
    state.scope = b.dataset.scope;
    $$('.seg-btn').forEach((x) => x.classList.toggle('active', x === b));
    loadSessions();
  });
  $('#refresh-btn').onclick = loadSessions;
  $('#new-session').onclick = showProjects;
  $('#reload-btn').onclick = doReload;
  $('#reload-btn-top').onclick = doReload;
  $('#reload-dismiss').onclick = hideBanner;
  $('#reconnect-btn').onclick = () => { try { $('#frame').contentWindow.location.reload(); } catch (_) {} };
  $('#fullscreen-btn').onclick = () => $('#frame-wrap').requestFullscreen?.();

  $('#m-host').onclick = () => openDrawer('rail');
  $('#m-session').onclick = () => openDrawer('sessions-col');
  $('#m-mode').onclick = () => setMode(state.mode === 'files' ? 'claude' : 'files');
  $('#scrim').onclick = closeDrawers;

  $$('[data-close]').forEach((b) => b.onclick = () => closeModal(b.dataset.close));
  $$('.modal').forEach((m) => m.addEventListener('click', (e) => { if (e.target === m) closeModal(m.id); }));
  $('#hm-save').onclick = saveHostProxy;

  setMode('claude');
  selectMac(MACS[0].id);
  restoreTermOrEmpty();

  if ('serviceWorker' in navigator) navigator.serviceWorker.register(`${BASE}/sw.js`).catch(() => {});
}
document.addEventListener('DOMContentLoaded', init);
