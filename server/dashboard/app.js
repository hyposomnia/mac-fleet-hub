'use strict';

// ============================================================
//  配置
// ============================================================
const BASE = '';   // 挂在子域根路径（mfh.example.com）；若改回子路径部署，改这里（如 '/fleet'）
// Mac 列表不再硬编码：从 /api/nodes.json 按入网节点名 mac<N> 动态推导（见 refreshNodes），
// 故没入网的台不会出现空占位。显示名从 /api/names（gateway 存）覆盖默认「Mac N」。
let MACS = [];          // [{id:'m1'}, ...]，按序号排
let macNames = {};      // id -> 自定义显示名

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

// 内联 SVG 图标（SVG 元素须用命名空间 createElementNS，不能走 h() 的 createElement；不用 innerHTML）。
// 目前仅折叠箭头用——Unicode ▾ 太淡，换成描边 chevron 更清晰。
function svgIcon(cls, pathD) {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  for (const [k, v] of [['class', cls], ['viewBox', '0 0 24 24'], ['fill', 'none'],
    ['stroke', 'currentColor'], ['stroke-width', '2.5'], ['stroke-linecap', 'round'], ['stroke-linejoin', 'round']]) {
    svg.setAttribute(k, v);
  }
  const p = document.createElementNS(NS, 'path');
  p.setAttribute('d', pathD);
  svg.appendChild(p);
  return svg;
}

const state = {
  macId: null,
  mode: 'claude',        // claude | files
  scope: 'active',       // active | all
  termSid: null,         // 当前终端 tmux 会话名（watch / reload 用）
  termUrl: null,         // 当前终端 iframe URL（files↔claude 切换后恢复用）
  termSessionId: null,   // 当前终端对应的 claude sessionId（判断「进入连接」是否就是当前终端）
  selectedSid: null,     // 当前选中的 claude sessionId（高亮 + 展开按钮）
  curTitle: '',          // 当前终端标题
  curCwd: '',            // 当前终端会话目录（用于头部 meta）
  bypass: false,         // 当前终端是否 bypass 权限（F1）
  killTarget: null,      // 待终止的 sessionId（二次确认用）
  nodes: {},             // id -> online
  counts: {},            // id -> 活跃会话数（主机栏/主机条展示）
  collapsed: new Set(),  // 已折叠的分组 cwd
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
function macName(id) { return macNames[id] || ('Mac ' + id.slice(1)); }
async function api(id, path, opts) {
  const r = await fetch(`${apiBase(id)}/api/${path}`, opts);
  if (!r.ok) {
    // 后端错误体 {error,message}：优先展示可读 message（如 pty 耗尽），回退状态码
    let msg = `${path}: ${r.status}`;
    try { const j = await r.json(); if (j && j.message) msg = j.message; } catch (_) {}
    throw new Error(msg);
  }
  return r.json();
}

// ============================================================
//  主题（默认跟随系统 prefers-color-scheme，切换后写 localStorage 覆盖）
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
//  toast（状态反馈，取代 alert）
// ============================================================
function toast(msg, kind = 'info') {
  const ic = kind === 'err' ? '⚠' : (kind === 'ok' ? '✓' : 'ℹ');
  const t = h('div', { class: 'toast ' + kind }, h('span', { class: 'ic', text: ic }), h('span', { text: msg }));
  $('#toast-wrap').append(t);
  setTimeout(() => t.remove(), 2800);
}

// ============================================================
//  主机栏（桌面行）/ 主机条（移动 chips）
// ============================================================
function renderHosts() {
  const nav = $('#host-list'); clear(nav);
  nav.append(h('div', { class: 'hd eyebrow', text: '主机' }));
  const chips = $('#host-chips'); clear(chips);
  if (!MACS.length) { nav.append(h('div', { class: 'empty', text: '暂无已入网的 Mac' })); return; }
  for (const m of MACS) {
    const online = state.nodes[m.id];
    const cnt = state.counts[m.id];
    // 桌面行
    const info = h('span', { class: 'i', title: '设置 / 代理', text: 'ⓘ' });
    info.onclick = (e) => { e.stopPropagation(); openHostModal(m.id); };
    const row = h('button', { class: 'host', dataset: { mac: m.id }, 'aria-current': String(m.id === state.macId) },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      h('span', { class: 'nm', text: macName(m.id) }),
      h('span', { class: 'ct tnum', text: online ? (cnt != null ? String(cnt) : '') : '离线' }),
      info,
    );
    row.onclick = () => selectMac(m.id);
    nav.append(row);
    // 移动 chip
    const chip = h('button', { class: 'chip', dataset: { mac: m.id }, 'aria-current': String(m.id === state.macId) },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      macName(m.id),
      online && cnt != null ? h('span', { class: 'ct tnum', text: String(cnt) }) : null,
    );
    chip.onclick = () => selectMac(m.id);
    chips.append(chip);
  }
}

function selectMac(id) {
  state.macId = id;
  // 切主机：放弃上一台的终端视图与选中态（tmux 在那台机上仍持久保留）
  $('#app').classList.remove('term-open');
  state.termSid = null; state.termUrl = null; state.termSessionId = null; state.selectedSid = null;
  renderHosts();
  closeMenus();
  if (state.mode === 'files') loadFiles();
  else { loadSessions(); restoreTermOrEmpty(); }
}

// ============================================================
//  在线状态 + 显示名 + 每主机会话数
// ============================================================
async function refreshNodes() {
  try {
    const r = await fetch(`${BASE}/api/nodes.json`, { cache: 'no-store' });
    if (!r.ok) return;
    const list = await r.json();
    const online = {};
    const ids = [];
    for (const n of (Array.isArray(list) ? list : (list.nodes || []))) {
      // 入网节点名固定为 mac<N>（setup-mac.sh --hostname=mac$MAC_INDEX）；gateway 等非 Mac 节点跳过。
      const mm = String(n.givenName || n.name || '').toLowerCase().match(/^mac(\d+)$/);
      if (!mm) continue;
      const id = 'm' + mm[1];
      if (!ids.includes(id)) ids.push(id);
      online[id] = n.online === true || n.online === 'true';
    }
    ids.sort((a, b) => (+a.slice(1)) - (+b.slice(1)));
    MACS = ids.map((id) => ({ id }));
    state.nodes = online;
    renderHosts();
    if (!state.macId && MACS.length) selectMac(MACS[0].id);
    refreshHostCounts();
  } catch (_) {}
}

// 各在线主机的活跃会话数（主机栏/主机条角标）。失败静默：数字非关键。
async function refreshHostCounts() {
  await Promise.all(MACS.filter((m) => state.nodes[m.id]).map(async (m) => {
    try { const d = await api(m.id, 'sessions?scope=active'); state.counts[m.id] = (d.sessions || []).length; }
    catch (_) {}
  }));
  renderHosts();
}

// Mac 显示名（gateway 存，所有浏览器共享）。失败静默：名字非关键，回退默认「Mac N」。
async function refreshNames() {
  try {
    const r = await fetch(`${BASE}/api/names`, { cache: 'no-store' });
    if (!r.ok) return;
    macNames = (await r.json()) || {};
    renderHosts();
  } catch (_) {}
}

// ============================================================
//  模式切换（Claude会话 / 文件）
// ============================================================
function setMode(mode) {
  state.mode = mode;
  $('#app').dataset.mode = mode;
  if (mode !== 'claude') $('#app').classList.remove('term-open'); // 离开会话模式收起终端 push
  $$('button[data-mode]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.mode === mode)));
  if (mode === 'files') loadFiles();
  else { loadSessions(); restoreTermOrEmpty(); }
}

// ============================================================
//  Claude会话 列表
// ============================================================
async function loadSessions() {
  if (state.mode !== 'claude' || !state.macId) return;
  const wrap = $('#session-groups');
  // 仅列表为空（首次/切主机）才显示骨架；刷新已有内容时保留旧内容直到新数据就绪，避免闪
  if (!wrap.querySelector('.grp, .empty')) {
    clear(wrap);
    for (let i = 0; i < 3; i++) wrap.append(h('div', { class: 'skel-ses' }, h('div', { class: 'skel l1' }), h('div', { class: 'skel l2' })));
  }

  let data;
  try { data = await api(state.macId, `sessions?scope=${state.scope}`); }
  catch (e) {
    clear(wrap);
    wrap.append(h('div', { class: 'empty' }, '连不上 ' + macName(state.macId), h('br'), h('small', { text: e.message })));
    return;
  }

  const sessions = data.sessions || [];
  const activeN = state.scope === 'active' ? sessions.length : sessions.filter((s) => s.live).length;
  $('#cnt-active').textContent = activeN;
  $('#cnt-all').textContent = data.total ?? sessions.length;
  state.counts[state.macId] = activeN;

  const groups = {};
  for (const s of sessions) (groups[s.cwd] ||= []).push(s);
  const ordered = Object.entries(groups).map(([cwd, arr]) => {
    arr.sort((a, b) => (b.live - a.live) || (b.mtime - a.mtime));
    return { cwd, arr, last: Math.max(...arr.map((s) => s.mtime)) };
  }).sort((a, b) => b.last - a.last);

  clear(wrap);
  if (!ordered.length) { wrap.append(h('div', { class: 'empty', text: '没有会话' })); return; }
  for (const g of ordered) {
    const collapsed = state.collapsed.has(g.cwd);
    const head = h('button', { class: 'grp-h' },
      svgIcon('chev', 'M6 9l6 6 6-6'),
      h('span', { class: 'gn', text: projName(g.cwd) }),
      h('span', { class: 'gp', text: projDir(g.cwd) }),
      h('span', { class: 'gc badge', text: String(g.arr.length) }),
    );
    const items = h('div', { class: 'grp-items' }, ...g.arr.map(sessionRow));
    const grp = h('div', { class: 'grp' + (collapsed ? ' collapsed' : '') }, head, items);
    head.onclick = () => {
      grp.classList.toggle('collapsed');
      if (grp.classList.contains('collapsed')) state.collapsed.add(g.cwd);
      else state.collapsed.delete(g.cwd);
    };
    wrap.append(grp);
  }
}

// 会话行：点行 = 仅选中。
// 已开 pty（控制台起过进程）的会话：恒显「终止 ⏹」（不论是否选中），选中后按钮为「进入连接」(回到已有终端，不重连)。
// 未开 pty 的会话：无 ⏹，选中后展开「连接 / ⚠ Bypass连接」。
function sessionRow(s) {
  const sid = s.sessionId;
  const stop = s.pty && h('span', { class: 'stopbtn', title: '终止进程（会话保留）', text: '⏹',
    onclick: (e) => { e.stopPropagation(); termSes(sid, s.title); } });
  const top = h('div', { class: 'ses-top' },
    // 行首点位恒定留出（标题统一对齐）：默认透明占位，仅「等待你回复/选择」(s.waiting) 显棕色点
    h('span', { class: 'dot' + (s.waiting ? ' wait' : ''), title: s.waiting ? '等待你的回复 / 选择' : null }),
    h('span', { class: 't', text: s.title || '(无标题)' }),
    // 紧凑化：不再单起一行显示分支/路径，仅在同行标题后跟相对时间
    h('span', { class: 'ses-time', text: relTime(s.mtime) }),
    stop,
  );
  const acts = s.pty
    ? h('div', { class: 'ses-acts' },
        h('button', { class: 'btn sm accent', title: '回到已有终端（进程已在运行，不重连）',
          onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, false); } },
          h('span', { class: 'gi', text: '→' }), '进入连接'))
    : h('div', { class: 'ses-acts' },
        h('button', { class: 'btn sm accent', onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, false); } },
          h('span', { class: 'gi', text: '→' }), '连接'),
        h('button', { class: 'btn sm danger', title: 'claude --dangerously-skip-permissions（跳过工具权限确认）',
          onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, true); } }, '⚠ Bypass连接'));
  const row = h('div', {
    class: 'ses' + (s.pty ? ' conn' : '') + (sid === state.selectedSid ? ' sel' : ''),
    dataset: { sid },
  }, top, acts);
  row.onclick = () => selectSes(sid);
  return row;
}

function selectSes(sid) {
  state.selectedSid = sid;
  $$('.ses').forEach((el) => el.classList.toggle('sel', el.dataset.sid === sid));
}

// ============================================================
//  连接 / 新建 → 终端 iframe（F1：bypass）
// ============================================================
async function connect(sessionId, title, cwd, bypass) {
  selectSes(sessionId);
  // 「进入连接」：已是当前终端则仅回到该视图，不重连、不重载（tmux 进程一直在）
  if (state.termSessionId === sessionId && state.termSid) {
    if (isMobile()) $('#app').classList.add('term-open');
    return;
  }
  try {
    const r = await api(state.macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sessionId, bypass: !!bypass }),
    });
    state.selectedSid = sessionId;
    state.termSessionId = sessionId;
    enterTerm(r.url, r.sid, title || '会话', cwd, !!r.bypass);
    loadSessions(); // 刷新 pty 标记：该会话现在有进程 → 行变「进入连接」+ 显示 ⏹（无骨架闪）
  } catch (e) { toast('连接失败：' + e.message, 'err'); }
}

function newSessionIn(cwd) {
  closeOverlay('projects-modal');
  api(state.macId, 'new', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ cwd }),
  }).then((r) => {
    state.selectedSid = null;
    state.termSessionId = null;
    enterTerm(r.url, r.sid, '新会话 · ' + projName(cwd), cwd, !!r.bypass);
  }).catch((e) => toast('新建失败：' + e.message, 'err'));
}

function enterTerm(url, sid, title, cwd, bypass) {
  state.termSid = sid || null;
  state.termUrl = url;
  state.curTitle = title || '会话';
  state.curCwd = cwd || '';
  state.bypass = !!bypass;
  $('#frame').src = url;
  $('#empty-state').hidden = true;
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = !isMobile();
  renderTermHead();
  if (isMobile()) $('#app').classList.add('term-open');
  closeMenus();
  startWatch();
}

// 终端头：状态点 + 标题（bypass 时追加「⚠ 跳过权限」徽标）+ 权限模式 meta
function renderTermHead() {
  const tt = $('#win-title'); clear(tt);
  tt.append(h('span', { class: 'dot live' }), h('span', { class: 'ttl', text: state.curTitle }));
  if (state.bypass) tt.append(h('span', { class: 'badge err', text: '⚠ 跳过权限' }));
  $('#win-meta').textContent = macName(state.macId) + ' · '
    + (state.curCwd ? projName(state.curCwd) + ' · ' : '')
    + (state.bypass ? '⚠ 跳过权限模式' : '正常权限');
}

// 切回会话模式：有终端则恢复，无则空态
function restoreTermOrEmpty() {
  if (state.termSid) {
    $('#frame').src = state.termUrl;
    $('#empty-state').hidden = true;
    $('#reconnect-btn').hidden = false;
    $('#fullscreen-btn').hidden = false;
    $('#mobile-input').hidden = !isMobile();
    renderTermHead();
    startWatch();
  } else {
    $('#frame').removeAttribute('src');
    $('#empty-state').hidden = false;
    $('#reconnect-btn').hidden = true;
    $('#fullscreen-btn').hidden = true;
    $('#mobile-input').hidden = true;
    stopWatch(); hideBanner();
    const tt = $('#win-title'); clear(tt); tt.append(h('span', { class: 'ttl', text: '选择一个会话' }));
    $('#win-meta').textContent = '选中会话后点「连接」打开终端';
  }
}

// 移动端从终端「返回」：仅收起 push，不结束进程（tmux 持久）
function backToList() { $('#app').classList.remove('term-open'); }

// ============================================================
//  文件浏览器
// ============================================================
function loadFiles() {
  if (!state.macId) return;
  stopWatch(); hideBanner();
  $('#app').classList.remove('term-open');
  $('#frame').src = `${apiBase(state.macId)}/files/`;
  $('#empty-state').hidden = true;
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = true;
  const tt = $('#win-title'); clear(tt); tt.append(h('span', { class: 'ttl', text: '文件 · ' + macName(state.macId) }));
  $('#win-meta').textContent = macName(state.macId);
  closeMenus();
}

// ============================================================
//  Desktop→ttyd 变更检测（重载条）
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
  if (!state.macId) return;
  openOverlay('projects-modal');
  const list = $('#project-list'); clear(list); list.append(h('div', { class: 'empty', text: '加载中…' }));
  try {
    const data = await api(state.macId, 'projects');
    const ps = data.projects || [];
    clear(list);
    if (!ps.length) { list.append(h('div', { class: 'empty', text: '没有已知项目目录' })); return; }
    for (const p of ps) {
      const el = h('button', { class: 'proj' },
        h('div', { class: 'body' },
          h('div', { class: 'pn', text: projName(p.cwd) }),
          h('div', { class: 'pm', text: projDir(p.cwd) + ' · ' + p.count + ' 个会话' }),
        ),
      );
      el.onclick = () => newSessionIn(p.cwd);
      list.append(el);
    }
  } catch (e) { clear(list); list.append(h('div', { class: 'empty', text: '加载失败：' + e.message })); }
}

// ============================================================
//  终止进程（F2：会话保留，二次确认非原生 confirm）
// ============================================================
function termSes(sessionId, title) {
  state.killTarget = sessionId;
  $('#ck-name').textContent = title || '该会话';
  openOverlay('confirm-kill');
}
async function closeSession() {
  const sid = state.killTarget;
  closeOverlay('confirm-kill');
  if (!sid) return;
  try {
    const r = await api(state.macId, 'close', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sessionId: sid }),
    });
    toast(r.killed ? '已终止该会话进程（会话保留）' : '该会话没有正在运行的控制台进程', r.killed ? 'ok' : 'info');
  } catch (e) { toast('终止失败：' + e.message, 'err'); }
  loadSessions();
  refreshHostCounts();
}

// ============================================================
//  主机设置弹窗（Mesh IP / 代理 / 显示名）
// ============================================================
async function openHostModal(id) {
  state.killTarget = null;
  state.hostModalMac = id;
  $('#hm-title').textContent = macName(id);
  $('#hm-name').value = macNames[id] || '';
  $('#hm-name').placeholder = 'Mac ' + id.slice(1);
  const online = state.nodes[id];
  $('#hm-dot').className = 'dot ' + (online ? 'on' : 'off');
  const st = $('#hm-state'); st.textContent = online ? '在线' : '离线'; st.className = 'badge ' + (online ? 'ok' : '');
  $('#hm-ip').textContent = '加载中…';
  $('#hm-http').value = ''; $('#hm-https').value = ''; $('#hm-proxy-on').checked = false;
  closeMenus();
  openOverlay('host-modal');
  try {
    const info = await api(id, 'info');
    $('#hm-ip').textContent = info.meshIP || '—';
    const p = info.proxy || {};
    $('#hm-http').value = p.http || '';
    $('#hm-https').value = p.https || '';
    $('#hm-proxy-on').checked = !!p.enabled;
  } catch (e) { $('#hm-ip').textContent = '连不上（' + e.message + '）'; }
}

async function saveHost() {
  const id = state.hostModalMac;
  if (!id) return;
  const btn = $('#hm-save'); btn.disabled = true; btn.textContent = '保存中…';

  // 1) 显示名 → gateway（/api/names）。离线也能改名。
  try {
    const r = await fetch(`${BASE}/api/names`, {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id, name: $('#hm-name').value.trim() }),
    });
    if (r.ok) { macNames = (await r.json()) || {}; renderHosts(); $('#hm-title').textContent = macName(id); }
  } catch (_) {}

  // 2) 代理 → 该 Mac（/m{n}/api/proxy）。离线则失败，仅提示，不回滚已存的名字。
  let proxyErr = '';
  try {
    await api(id, 'proxy', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        enabled: $('#hm-proxy-on').checked,
        http: $('#hm-http').value.trim(),
        https: $('#hm-https').value.trim(),
      }),
    });
  } catch (e) { proxyErr = e.message; }

  btn.disabled = false; btn.textContent = '保存';
  if (proxyErr) { toast('显示名已保存；代理未保存（' + macName(id) + ' 可能离线）：' + proxyErr, 'err'); return; }
  closeOverlay('host-modal');
  toast('已保存', 'ok');
}

// ============================================================
//  退出登录（F4：跳 Authelia 退出端点，登出后回登录页）
// ============================================================
function doLogout() {
  closeMenus();
  location.href = `${BASE}/auth/logout?rd=${encodeURIComponent(location.origin + BASE + '/')}`;
}

// ============================================================
//  浮层菜单 / 弹窗
// ============================================================
function openOverlay(id) { $('#' + id).hidden = false; }
function closeOverlay(id) { $('#' + id).hidden = true; }
function closeMenus() { $('#usermenu').hidden = true; $('#m-menu').hidden = true; }
function toggleMenu(id, e) {
  if (e) e.stopPropagation();
  const m = $('#' + id);
  const willOpen = m.hidden;
  closeMenus();
  m.hidden = !willOpen;
}

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
  // Enter 发送、Shift+Enter 换行
  $('#cmd-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); $('#send-btn').click(); }
  });
}

// ============================================================
//  初始化
// ============================================================
function init() {
  initTheme();
  renderHosts();
  refreshNames();
  refreshNodes(); setInterval(refreshNodes, 30000);
  wireMobileInput();

  // 模式 / 范围 / 刷新 / 新建
  // 注意 button[data-mode]：#app 本身带 data-mode（CSS 切栅格用），裸 [data-mode] 会把 #app 也选中，
  // 给根容器挂上 onclick → 点页面任意处都冒泡触发 setMode→loadSessions（每次点击闪一下）。
  $$('button[data-mode]').forEach((b) => b.onclick = () => setMode(b.dataset.mode));
  $$('[data-scope]').forEach((b) => b.onclick = () => {
    state.scope = b.dataset.scope;
    $$('[data-scope]').forEach((x) => x.setAttribute('aria-selected', String(x === b)));
    loadSessions();
  });
  $('#refresh-btn').onclick = () => { loadSessions(); refreshHostCounts(); };
  $('#new-session').onclick = showProjects;

  // 终端窗口
  $('#win-back').onclick = backToList;
  $('#reload-btn').onclick = doReload;
  $('#reload-dismiss').onclick = hideBanner;
  $('#reconnect-btn').onclick = () => { try { $('#frame').contentWindow.location.reload(); } catch (_) { const f = $('#frame'); f.src = f.src; } };
  $('#fullscreen-btn').onclick = () => $('.win-body').requestFullscreen?.();

  // 主题 / 用户菜单
  $('#theme-btn').onclick = toggleTheme;
  $('#user-btn').onclick = (e) => toggleMenu('usermenu', e);
  $('#m-menu-btn').onclick = (e) => toggleMenu('m-menu', e);
  $$('#usermenu button, #m-menu button').forEach((b) => {
    if (!b.dataset.act) return;
    b.onclick = () => { closeMenus(); if (b.dataset.act === 'theme') toggleTheme(); else if (b.dataset.act === 'logout') doLogout(); };
  });
  $('#m-info-btn').onclick = () => { if (state.macId) openHostModal(state.macId); };

  // 弹窗 / 抽屉
  $$('[data-close]').forEach((b) => b.onclick = () => closeOverlay(b.dataset.close));
  $$('.overlay').forEach((o) => o.addEventListener('click', (e) => { if (e.target === o) closeOverlay(o.id); }));
  $('#hm-save').onclick = saveHost;
  $('#hm-copy').onclick = async () => {
    const ip = $('#hm-ip').textContent;
    try { await navigator.clipboard.writeText(ip); toast('已复制 ' + ip, 'ok'); }
    catch (_) { toast('复制失败', 'err'); }
  };
  $('#ck-confirm').onclick = closeSession;

  // 点空白处关菜单
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.menu') && !e.target.closest('#user-btn') && !e.target.closest('#m-menu-btn')) closeMenus();
  });
  // 跨断点时同步移动输入坞可见性
  addEventListener('resize', () => {
    if (state.mode === 'claude' && state.termSid) $('#mobile-input').hidden = !isMobile();
  });
  // 移动端软键盘弹起时把输入坞顶到键盘之上。iOS 键盘不缩布局视口（100dvh/fixed 不变），
  // 用 VisualViewport 算键盘高度 → CSS 变量 --kb，输入坞据此上移（见 style.css #mobile-input transform）。
  if (window.visualViewport) {
    const vv = window.visualViewport;
    const syncKb = () => {
      const kb = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
      document.documentElement.style.setProperty('--kb', kb + 'px');
    };
    vv.addEventListener('resize', syncKb);
    vv.addEventListener('scroll', syncKb);
    syncKb();
  }

  setMode('claude');
  restoreTermOrEmpty();

  if ('serviceWorker' in navigator) navigator.serviceWorker.register(`${BASE}/sw.js`).catch(() => {});
}
document.addEventListener('DOMContentLoaded', init);
