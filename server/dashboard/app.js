'use strict';

// ============================================================
//  配置
// ============================================================
const BASE = '';   // 挂在子域根路径（如 fleet.example.com）；若改回子路径部署，改这里（如 '/fleet'）
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

// 终止图标：实心圆角方块（stop）。用 SVG 而非 Unicode ⏹——后者在 iOS 上会渲染成彩色 emoji。
function svgStop() {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'currentColor');
  const r = document.createElementNS(NS, 'rect');
  for (const [k, v] of [['x', '7'], ['y', '7'], ['width', '10'], ['height', '10'], ['rx', '3']]) r.setAttribute(k, v);
  svg.appendChild(r);
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
  pool: [],              // 终端 iframe 池：每个打开的会话一个常驻 iframe（见「终端 iframe 池」段）
  current: null,         // 当前显示的池条目（null = 空态 / 文件模式）
  settings: null,        // dashboard 偏好（窗口上限/回滚行数，网关存；GET /api/settings）
};

// 偏好默认（拉取失败/未设时回退，与 server/enroll defaultSettings 对齐）
const SETTINGS_DEFAULT = { desktopMaxWindows: 10, desktopScrollback: 5000, mobileMaxWindows: 4, mobileScrollback: 5000 };

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
function projFull(cwd) { return (cwd || '(未知路径)').replace(/^\/Users\/[^/]+/, '~'); }
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
  applyTermTheme(); // 终端(iframe 内 xterm)跟随切换
}

// ttyd 把 xterm 实例挂在 iframe 的 window.term 上。这里按 data-theme 给它换肤，
// 配色取自 style.css 的设计 token，让网页终端与 dashboard 深/浅色统一。
const XTERM_THEME = {
  dark: {
    background: '#090c12', foreground: '#e9eef5',
    cursor: '#6e8bff', cursorAccent: '#090c12', selectionBackground: 'rgba(110,139,255,.28)',
    black: '#2b3240', brightBlack: '#6b7585',
    red: '#ff6b6b', brightRed: '#ff8f8f',
    green: '#46d39a', brightGreen: '#6ee3b4',
    yellow: '#d08a45', brightYellow: '#e8a868',
    blue: '#6e8bff', brightBlue: '#93a9ff',
    magenta: '#b18bff', brightMagenta: '#c9adff',
    cyan: '#5cc8d8', brightCyan: '#82dbe8',
    white: '#aab4c4', brightWhite: '#e9eef5',
  },
  light: {
    background: '#f6f7f9', foreground: '#141821',
    cursor: '#3f5cff', cursorAccent: '#f6f7f9', selectionBackground: 'rgba(63,92,255,.16)',
    black: '#2c333f', brightBlack: '#828c9d',
    red: '#dc3b3b', brightRed: '#b32d2d',
    green: '#12a567', brightGreen: '#0c8a55',
    yellow: '#9c6321', brightYellow: '#b5762b',
    blue: '#3f5cff', brightBlue: '#2f49e6',
    magenta: '#7c4ddb', brightMagenta: '#6a3fc9',
    cyan: '#1f8fa6', brightCyan: '#157e94',
    white: '#e2e6ec', brightWhite: '#ffffff',
  },
};
// 切主题时给池里所有已就绪的终端换肤（新加载的终端在 hookTerm 里首次套用）。
function applyTermTheme() {
  const mode = document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
  for (const e of state.pool) {
    try {
      const t = e.iframe.contentWindow.term;
      if (t && t.options) t.options.theme = XTERM_THEME[mode];
    } catch (_) {}
  }
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
  // 切主机：终端回空态（不自动复用上一台的窗口）。池条目按 macId 保留、仍占 pty；
  // 选中本台某个已开会话会瞬时显示（poolFind 按 macId 匹配）。
  $('#app').classList.remove('term-open');
  state.selectedSid = null;
  renderHosts();
  closeMenus();
  if (state.mode === 'files') loadFiles();
  else { loadSessions(); showEmpty(); }
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
//  dashboard 偏好（终端窗口上限 / 回滚行数，gateway 存，所有浏览器共享）
// ============================================================
async function refreshSettings() {
  try {
    const r = await fetch(`${BASE}/api/settings`, { cache: 'no-store' });
    if (r.ok) { state.settings = { ...SETTINGS_DEFAULT, ...(await r.json()) }; return; }
  } catch (_) {}
  if (!state.settings) state.settings = { ...SETTINGS_DEFAULT }; // 拉取失败：用默认，不阻塞
}
function openSettings() {
  const s = state.settings || SETTINGS_DEFAULT;
  $('#st-dmax').value = s.desktopMaxWindows;
  $('#st-dscroll').value = s.desktopScrollback;
  $('#st-mmax').value = s.mobileMaxWindows;
  $('#st-mscroll').value = s.mobileScrollback;
  openOverlay('settings-modal');
}
async function saveSettings() {
  const body = {
    desktopMaxWindows: parseInt($('#st-dmax').value, 10) || 0,
    desktopScrollback: parseInt($('#st-dscroll').value, 10) || 0,
    mobileMaxWindows: parseInt($('#st-mmax').value, 10) || 0,
    mobileScrollback: parseInt($('#st-mscroll').value, 10) || 0,
  };
  try {
    const r = await fetch(`${BASE}/api/settings`, {
      method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(body),
    });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    state.settings = { ...SETTINGS_DEFAULT, ...(await r.json()) }; // 服务端 normalize 后的真实值
    closeOverlay('settings-modal');
    toast('设置已保存', 'ok');
    poolEvict();              // 上限调小 → 立即按新上限释放多余窗口
    applyScrollbackToPool();  // 回滚行数即时作用到已开终端
  } catch (e) { toast('保存失败：' + e.message, 'err'); }
}
// 把当前回滚行数应用到池里所有已就绪终端（保存设置后即时生效）。
function applyScrollbackToPool() {
  const n = poolScrollback();
  for (const e of state.pool) {
    try { const t = e.iframe.contentWindow.term; if (t && t.options) t.options.scrollback = n; } catch (_) {}
  }
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
      // 路径不再行内展示（反正显示不全）：改为软底「/」chip，hover 即时弹完整路径（CSS tooltip，不走有延迟的原生 title）
      h('span', { class: 'gpath badge', dataset: { path: projFull(g.cwd) } }, '/'),
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

// 会话行：
// 已在池中的会话：点行即瞬时切换（selectSes 内 poolShow），无展开按钮。
// 不在池中的会话：点行仅选中并展开「连接 / ⚠ Bypass连接」（须显式选权限模式）。
// 开了 pty 的会话另显「终止 ⏹」（与是否在池无关）。
function sessionRow(s) {
  const sid = s.sessionId;
  const inPool = !!poolFind(state.macId, sid);
  const stop = s.pty && h('span', { class: 'stopbtn', title: '终止进程（会话保留）',
    onclick: (e) => { e.stopPropagation(); termSes(sid, s.title); } }, svgStop());
  const top = h('div', { class: 'ses-top' },
    // 行首点位恒定留出（标题统一对齐）：默认透明占位，仅「等待你回复/选择」(s.waiting) 显棕色点
    h('span', { class: 'dot' + (s.waiting ? ' wait' : ''), title: s.waiting ? '等待你的回复 / 选择' : null }),
    h('span', { class: 't', text: s.title || '(无标题)' }),
    // 紧凑化：不再单起一行显示分支/路径，仅在同行标题后跟相对时间
    h('span', { class: 'ses-time', text: relTime(s.mtime) }),
    stop,
  );
  // 已在池中的会话点行即瞬时切换，不需要按钮；不在池中的才展开「连接 / Bypass」。
  const acts = inPool ? null : h('div', { class: 'ses-acts' },
    h('button', { class: 'btn sm accent', onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, false); } },
      h('span', { class: 'gi', text: '→' }), '连接'),
    h('button', { class: 'btn sm danger', title: 'claude --dangerously-skip-permissions（跳过工具权限确认）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, true); } }, '⚠ Bypass连接'));
  const row = h('div', {
    class: 'ses' + (s.pty ? ' conn' : '') + (sid === state.selectedSid ? ' sel' : ''),
    dataset: { sid },
  }, top, acts);
  row.onclick = () => selectSes(sid); // 已在池 → poolShow 瞬时切换；否则仅高亮 + 展开按钮
  return row;
}

function selectSes(sid) {
  state.selectedSid = sid;
  $$('.ses').forEach((el) => el.classList.toggle('sel', el.dataset.sid === sid));
  // 已在池中的会话：选中即瞬时切换，不必再点「进入连接」
  const e = poolFind(state.macId, sid);
  if (e) poolShow(e);
}

// ============================================================
//  终端 iframe 池
//  每个打开过的会话一个常驻 iframe，全尺寸叠放在 #frames 里，靠 .show 显隐切换
//  （visibility 而非 display:none——后者会把 iframe 尺寸塌成 0、ttyd 把 pty resize 成
//   0×0、Claude TUI 排版炸掉）。池内会话全程保持实时、不掉线；切换 = 改 class，瞬时。
//  超上限按「最后收到输出时间」LRU 释放（排除当前窗口）：关 iframe → WS 断 →
//   tmux detach → 释放 1 个 pty；后台 Claude 进程不受影响，再点回来重新 attach。
// ============================================================
const curFrame = () => (state.current ? state.current.iframe : $('#frame'));
function poolMax() { const s = state.settings || SETTINGS_DEFAULT; return isMobile() ? s.mobileMaxWindows : s.desktopMaxWindows; }
function poolScrollback() { const s = state.settings || SETTINGS_DEFAULT; return isMobile() ? s.mobileScrollback : s.desktopScrollback; }
function poolFind(macId, sessionId) {
  if (!sessionId) return null;
  return state.pool.find((e) => e.macId === macId && e.sessionId === sessionId) || null;
}

// xterm 就绪后（ttyd 异步初始化，轮询等它出现）：套主题 + 设回滚行数 + 包 term.write
// 记「最后收到输出时间」（LRU 释放依据）。
function hookTerm(entry, retries = 30) {
  let term;
  try { term = entry.iframe.contentWindow.term; } catch (_) { return; }
  if (!term || !term.options) { if (retries > 0) setTimeout(() => hookTerm(entry, retries - 1), 150); return; }
  const mode = document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
  try { term.options.theme = XTERM_THEME[mode]; } catch (_) {}
  try { term.options.scrollback = poolScrollback(); } catch (_) {}
  if (!term.__fleetHooked) {
    term.__fleetHooked = true;
    const orig = term.write.bind(term);
    term.write = (...a) => { entry.lastOutput = Date.now(); return orig(...a); };
  }
}

// 关掉一个池条目（释放其 pty）。后台 tmux/Claude 不受影响。
function poolDrop(entry) {
  const i = state.pool.indexOf(entry);
  if (i >= 0) state.pool.splice(i, 1);
  try { entry.iframe.remove(); } catch (_) {}
  if (state.current === entry) state.current = null;
}

// 超上限释放：非当前窗口里「最后收到输出时间」最早的先释放。
function poolEvict() {
  const max = poolMax();
  while (state.pool.length > max) {
    let victim = null;
    for (const e of state.pool) {
      if (e === state.current) continue;
      if (!victim || e.lastOutput < victim.lastOutput) victim = e;
    }
    if (!victim) break; // 只剩当前窗口，不再释放
    poolDrop(victim);
  }
}

// 显示某个池条目（隐藏文件 iframe 与其余终端，仅它 .show）。
function poolShow(entry) {
  state.current = entry;
  // 同步老字段，watch / reload / resize / 移动输入坞复用
  state.termSid = entry.sid; state.termUrl = entry.url; state.termSessionId = entry.sessionId;
  state.curTitle = entry.title; state.curCwd = entry.cwd; state.bypass = entry.bypass;
  $('#frame').classList.remove('show');
  for (const e of state.pool) e.iframe.classList.toggle('show', e === entry);
  $('#empty-state').hidden = true;
  $('#reconnect-btn').hidden = false;
  $('#fullscreen-btn').hidden = false;
  $('#mobile-input').hidden = !isMobile();
  renderTermHead();
  if (isMobile()) $('#app').classList.add('term-open');
  closeMenus();
  startWatch();
}

// 空态：隐藏所有 iframe，复位头部。
function showEmpty() {
  state.current = null;
  state.termSid = state.termUrl = state.termSessionId = null;
  for (const e of state.pool) e.iframe.classList.remove('show');
  $('#frame').classList.remove('show');
  $('#empty-state').hidden = false;
  $('#reconnect-btn').hidden = true;
  $('#fullscreen-btn').hidden = true;
  $('#mobile-input').hidden = true;
  stopWatch(); hideBanner();
  const tt = $('#win-title'); clear(tt); tt.append(h('span', { class: 'ttl', text: '选择一个会话' }));
  $('#win-meta').textContent = '选中会话后点「连接」打开终端';
}

// 新建一个池条目（新 iframe）并显示，随后按上限 LRU 回收。
function poolAdd(macId, sessionId, sid, url, title, cwd, bypass) {
  const iframe = document.createElement('iframe');
  iframe.className = 'term-frame';
  iframe.title = 'window';
  iframe.setAttribute('allow', 'clipboard-read; clipboard-write');
  const entry = { macId, sessionId: sessionId || null, sid, url, title: title || '会话', cwd: cwd || '', bypass: !!bypass, iframe, lastOutput: Date.now() };
  iframe.addEventListener('load', () => hookTerm(entry)); // 每次加载/重连后套主题+回滚+记输出
  $('#frames').appendChild(iframe);
  iframe.src = url;
  state.pool.push(entry);
  poolShow(entry);
  poolEvict();
  return entry;
}

// ============================================================
//  连接 / 新建 → 终端 iframe（F1：bypass）
// ============================================================
async function connect(sessionId, title, cwd, bypass) {
  selectSes(sessionId); // 已在池则 selectSes 已瞬时切过去；这里再确保权限模式一致
  const exist = poolFind(state.macId, sessionId);
  if (exist && exist.bypass === !!bypass) { poolShow(exist); return; } // 池内：瞬时切回，不重连
  if (exist) poolDrop(exist); // 权限模式变了（普通↔bypass）→ 丢弃旧窗口重开
  try {
    const r = await api(state.macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ sessionId, bypass: !!bypass }),
    });
    state.selectedSid = sessionId;
    poolAdd(state.macId, sessionId, r.sid, r.url, title || '会话', cwd, !!r.bypass);
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
    poolAdd(state.macId, null, r.sid, r.url, '新会话 · ' + projName(cwd), cwd, !!r.bypass);
  }).catch((e) => toast('新建失败：' + e.message, 'err'));
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

// 切回会话模式：当前池条目仍在则显示，否则空态。
function restoreTermOrEmpty() {
  if (state.current && state.pool.includes(state.current)) poolShow(state.current);
  else showEmpty();
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
  state.current = null;                                  // 文件模式：脱离终端池（reconnect/reload 转而作用于 #frame）
  for (const e of state.pool) e.iframe.classList.remove('show');
  $('#frame').src = `${apiBase(state.macId)}/files/`;
  $('#frame').classList.add('show');
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
  const f = curFrame();
  try { f.contentWindow.location.reload(); } catch (_) { f.src = f.src; }
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
    // 终止后把该会话从池里移除（进程已结束，留着 iframe 只会停在 [exited]/Press ⏎ to Reconnect）。
    const ent = poolFind(state.macId, sid);
    if (ent) {
      const wasCurrent = ent === state.current;
      poolDrop(ent);
      if (wasCurrent) { $('#app').classList.remove('term-open'); restoreTermOrEmpty(); }
    }
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
  const win = curFrame().contentWindow;
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
  refreshSettings();
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
  $('#reconnect-btn').onclick = () => { const f = curFrame(); try { f.contentWindow.location.reload(); } catch (_) { f.src = f.src; } };
  $('#fullscreen-btn').onclick = () => $('.win-body').requestFullscreen?.();

  // 用户菜单（主题切换已收进菜单内 data-act="theme"，不再单独占一行）
  $('#user-btn').onclick = (e) => toggleMenu('usermenu', e);
  $('#m-menu-btn').onclick = (e) => toggleMenu('m-menu', e);
  $$('#usermenu button, #m-menu button').forEach((b) => {
    if (!b.dataset.act) return;
    b.onclick = () => {
      closeMenus();
      if (b.dataset.act === 'theme') toggleTheme();
      else if (b.dataset.act === 'settings') openSettings();
      else if (b.dataset.act === 'logout') doLogout();
    };
  });
  $('#st-save').onclick = saveSettings;
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
