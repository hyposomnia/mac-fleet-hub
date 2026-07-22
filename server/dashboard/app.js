'use strict';

// ============================================================
//  配置
// ============================================================
const BASE = '';   // 挂在子域根路径（如 fleet.example.com）；若改回子路径部署，改这里（如 '/fleet'）
// Mac 列表不再硬编码：从 /api/nodes.json 按入网节点名 mac<N> 动态推导（见 refreshNodes），
// 故没入网的台不会出现空占位。显示名从 /api/names（gateway 存）覆盖默认「Mac N」。
let MACS = [];          // [{id:'m1'}, ...]，按序号排
let macNames = {};      // id -> 自定义显示名
let sessionLoadSeq = 0; // 会话列表请求序号：切主机/切筛选时丢弃旧响应，避免慢请求回写旧列表

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

function svgIconParts(cls, parts) {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  for (const [k, v] of [['class', cls], ['viewBox', '0 0 24 24'], ['fill', 'none'],
    ['stroke', 'currentColor'], ['stroke-width', '2'], ['stroke-linecap', 'round'], ['stroke-linejoin', 'round']]) {
    svg.setAttribute(k, v);
  }
  for (const part of parts) {
    const node = document.createElementNS(NS, part.tag || 'path');
    for (const [k, v] of Object.entries(part.attrs || {})) node.setAttribute(k, v);
    svg.appendChild(node);
  }
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
  mode: 'sessions',      // sessions | files
  assistant: 'codex',    // claude | codex
  scope: 'active',       // active | all
  termSid: null,         // 当前终端 tmux 会话名（watch / reload 用）
  termUrl: null,         // 当前终端 iframe URL（files↔sessions 切换后恢复用）
  termSessionId: null,   // 当前终端对应的 sessionId（判断「进入连接」是否就是当前终端）
  selectedSid: null,     // 当前选中的 sessionId（高亮 + 展开按钮）
  curTitle: '',          // 当前终端标题
  curCwd: '',            // 当前终端会话目录（用于头部 meta）
  curMode: 'default',    // 当前终端的权限模式：default | bypass | auto
  killTarget: null,      // 待终止的 sessionId（二次确认用）
  killAssistant: null,   // 待终止会话所属助手
  nodes: {},             // id -> online
  counts: {},            // id -> 活跃会话数（主机栏/主机条展示）
  collapsed: new Set(),  // 已折叠的分组 cwd
  watchTimer: null,
  pool: [],              // 终端 iframe 池：每个打开的会话一个常驻 iframe（见「终端 iframe 池」段）
  current: null,         // 当前显示的池条目（null = 空态 / 文件模式）
  settings: null,        // dashboard 偏好（窗口上限/回滚行数，网关存；GET /api/settings）
  selfDraw: false,       // 实验：Codex 自绘界面（localStorage，默认关）
  chat: null,            // 当前自绘 Codex 会话状态（独立于 ttyd pool）
  chatCache: new Map(),  // key(macId/sessionId) -> 自绘 Codex 会话状态；保持 SSE 连接，切回秒开
};

// 偏好默认（拉取失败/未设时回退，与 server/enroll defaultSettings 对齐）
const SETTINGS_DEFAULT = {
  desktopMaxWindows: 10, desktopScrollback: 5000,
  mobileMaxWindows: 4, mobileScrollback: 5000,
  autoCloseMinutes: 30,
  chatCacheMaxSessions: 6,
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
function projFull(cwd) { return (cwd || '(未知路径)').replace(/^\/Users\/[^/]+/, '~'); }
function macName(id) { return macNames[id] || ('Mac ' + id.slice(1)); }
function assistantLabel(a = state.assistant) { return a === 'codex' ? 'Codex' : 'Claude'; }
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

const SELF_DRAW_KEY = 'fleet-experiment-selfdraw';
let chatIMEComposing = false;
let mobileIMEComposing = false;
function initExperimentFlags() {
  try { state.selfDraw = localStorage.getItem(SELF_DRAW_KEY) === '1'; } catch (_) { state.selfDraw = false; }
  updateExperimentMenus();
}
function updateExperimentMenus() {
  $$('.selfdraw-mark').forEach((el) => el.classList.toggle('on', !!state.selfDraw));
}
function toggleSelfDraw() {
  state.selfDraw = !state.selfDraw;
  try { localStorage.setItem(SELF_DRAW_KEY, state.selfDraw ? '1' : '0'); } catch (_) {}
  updateExperimentMenus();
  if (!state.selfDraw) {
    closeChatPane();
    restoreTermOrEmpty();
  } else if (state.assistant === 'codex' && state.selectedSid) {
    loadSessions();
  }
}
function canSelfDrawChat() { return state.selfDraw && state.assistant === 'codex' && state.mode === 'sessions'; }
function isIMEComposing(e, composingFlag) {
  return !!(composingFlag || e.isComposing || e.keyCode === 229);
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
    // 桌面行
    const info = h('span', { class: 'i', title: '设置 / 代理', text: 'ⓘ' });
    info.onclick = (e) => { e.stopPropagation(); openHostModal(m.id); };
    const row = h('button', { class: 'host', dataset: { mac: m.id }, 'aria-current': String(m.id === state.macId) },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      h('span', { class: 'nm', text: macName(m.id) }),
      // 会话数不再显示；仅离线时标「离线」（在线/离线 dot 已在前面）
      h('span', { class: 'ct', text: online ? '' : '离线' }),
      info,
    );
    row.onclick = () => selectMac(m.id);
    nav.append(row);
    // 移动 chip
    const chip = h('button', { class: 'chip', dataset: { mac: m.id }, 'aria-current': String(m.id === state.macId) },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      macName(m.id),
      online ? null : h('span', { class: 'ct', text: '离线' }), // 会话数不显示，仅离线状态
    );
    chip.onclick = () => selectMac(m.id);
    chips.append(chip);
  }
}

function selectMac(id) {
  if (state.macId === id) return;
  state.macId = id;
  // 切主机：终端回空态（不自动复用上一台的窗口）。池条目按 macId 保留、仍占 pty；
  // 选中本台某个已开会话会瞬时显示（poolFind 按 macId 匹配）。
  $('#app').classList.remove('term-open');
  state.selectedSid = null;
  renderHosts();
  closeMenus();
  if (state.mode === 'files') loadFiles();
  else { loadSessions({ clear: true }); showEmpty(); }
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
    try { const d = await api(m.id, `sessions?assistant=${state.assistant}&scope=active`); state.counts[m.id] = (d.sessions || []).length; }
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
    if (r.ok) { state.settings = { ...SETTINGS_DEFAULT, ...(await r.json()) }; evictChatCache(); return; }
  } catch (_) {}
  if (!state.settings) state.settings = { ...SETTINGS_DEFAULT }; // 拉取失败：用默认，不阻塞
}
function openSettings() {
  const s = state.settings || SETTINGS_DEFAULT;
  $('#st-dmax').value = s.desktopMaxWindows;
  $('#st-dscroll').value = s.desktopScrollback;
  $('#st-mmax').value = s.mobileMaxWindows;
  $('#st-mscroll').value = s.mobileScrollback;
  $('#st-autoclose').value = s.autoCloseMinutes;
  $('#st-chat-cache-max').value = s.chatCacheMaxSessions;
  renderChatCacheStats();
  showSettingsTab('terminal');
  openOverlay('settings-modal');
}
async function saveSettings() {
  const body = {
    desktopMaxWindows: parseInt($('#st-dmax').value, 10) || 0,
    desktopScrollback: parseInt($('#st-dscroll').value, 10) || 0,
    mobileMaxWindows: parseInt($('#st-mmax').value, 10) || 0,
    mobileScrollback: parseInt($('#st-mscroll').value, 10) || 0,
    autoCloseMinutes: parseInt($('#st-autoclose').value, 10) || 0,
    chatCacheMaxSessions: parseInt($('#st-chat-cache-max').value, 10) || 0,
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
    evictChatCache();         // 自绘缓存上限调小 → 立即释放最久未看的连接
    applyScrollbackToPool();  // 回滚行数即时作用到已开终端
  } catch (e) { toast('保存失败：' + e.message, 'err'); }
}

function showSettingsTab(tab) {
  const key = tab === 'chat' ? 'chat' : 'terminal';
  $$('[data-settings-tab]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.settingsTab === key)));
  $$('[data-settings-panel]').forEach((p) => { p.hidden = p.dataset.settingsPanel !== key; });
  if (key === 'chat') renderChatCacheStats();
}

function formatBytes(bytes) {
  const n = Math.max(0, Number(bytes) || 0);
  if (n < 1024) return `${Math.round(n)} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`;
}

function estimateChatBytes(chat) {
  if (!chat) return 0;
  let textBytes = 0;
  try { textBytes = JSON.stringify(chat.model || {}).length * 2; } catch (_) {}
  try { textBytes += JSON.stringify({
    historyCursor: chat.historyCursor, models: chat.models, efforts: chat.efforts, serviceTiers: chat.serviceTiers,
    selectedModel: chat.selectedModel, selectedEffort: chat.selectedEffort, selectedServiceTier: chat.selectedServiceTier,
    approvalMode: chat.approvalMode, draft: chat.draft,
  }).length * 2; } catch (_) {}
  const imageIds = new Set();
  let imageBytes = 0;
  const collectImage = (img) => {
    if (!img) return;
    const id = img.id || img.url || img.previewUrl || img.localId || img.name;
    if (id && imageIds.has(id)) return;
    if (id) imageIds.add(id);
    imageBytes += Number(img.size) || 0;
  };
  for (const att of chat.attachments || []) collectImage(att);
  for (const id of chat.model?.messages || []) {
    const item = chat.model.items?.[id];
    for (const img of item?.images || []) collectImage(img);
  }
  const objectOverhead = 64 * 1024; // EventSource + message index objects 的保守基础开销。
  return Math.round((textBytes * 1.35) + imageBytes + objectOverhead);
}

function chatCacheStats() {
  let bytes = 0;
  for (const chat of state.chatCache.values()) bytes += estimateChatBytes(chat);
  return { count: state.chatCache.size, max: chatCacheMax(), bytes, each: state.chatCache.size ? bytes / state.chatCache.size : 0 };
}

function renderChatCacheStats() {
  const count = $('#st-chat-cache-count');
  if (!count) return;
  const st = chatCacheStats();
  count.textContent = `${st.count} / ${st.max}`;
  $('#st-chat-cache-bytes').textContent = formatBytes(st.bytes);
  $('#st-chat-cache-each').textContent = formatBytes(st.each);
}
// 把当前回滚行数应用到池里所有已就绪终端（保存设置后即时生效）。
function applyScrollbackToPool() {
  const n = poolScrollback();
  for (const e of state.pool) {
    try { const t = e.iframe.contentWindow.term; if (t && t.options) t.options.scrollback = n; } catch (_) {}
  }
}

// ============================================================
//  模式切换（会话 / 文件）
// ============================================================
function setMode(mode) {
  state.mode = mode;
  $('#app').dataset.mode = mode;
  if (mode !== 'sessions') $('#app').classList.remove('term-open'); // 离开会话模式收起终端 push
  $$('button[data-mode]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.mode === mode)));
  if (mode === 'files') loadFiles();
  else { loadSessions(); restoreTermOrEmpty(); }
}

function setAssistant(assistant) {
  state.assistant = assistant === 'codex' ? 'codex' : 'claude';
  state.selectedSid = null;
  closeChatPane();
  $$('[data-assistant]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.assistant === state.assistant)));
  loadSessions({ clear: true });
  refreshHostCounts();
}

// ============================================================
//  会话列表
// ============================================================
function setSessionsLoading(on) {
  const btn = $('#refresh-btn');
  if (!btn) return;
  btn.classList.toggle('loading', on);
  btn.setAttribute('aria-busy', String(on));
}

function renderSessionSkeleton(wrap) {
  clear(wrap);
  for (let i = 0; i < 3; i++) wrap.append(h('div', { class: 'skel-ses' }, h('div', { class: 'skel l1' }), h('div', { class: 'skel l2' })));
}

async function loadSessions(opts = {}) {
  if (state.mode !== 'sessions' || !state.macId) return;
  const wrap = $('#session-groups');
  const req = ++sessionLoadSeq;
  const macId = state.macId;
  const assistant = state.assistant;
  const scope = state.scope;
  const stale = () => req !== sessionLoadSeq || state.mode !== 'sessions' || state.macId !== macId || state.assistant !== assistant || state.scope !== scope;
  setSessionsLoading(true);
  // 切主机/切助手/切范围时立即清空旧列表；普通刷新保留旧内容直到新数据就绪，避免闪。
  if (opts.clear || !wrap.querySelector('.grp, .empty, .skel-ses')) renderSessionSkeleton(wrap);

  let data;
  try { data = await api(macId, `sessions?assistant=${assistant}&scope=${scope}`); }
  catch (e) {
    if (stale()) return;
    clear(wrap);
    wrap.append(h('div', { class: 'empty' }, '连不上 ' + macName(macId), h('br'), h('small', { text: e.message })));
    setSessionsLoading(false);
    return;
  }
  if (stale()) return;
  setSessionsLoading(false);

  const sessions = data.sessions || [];
  for (const s of sessions) updateCachedChatFromSession(macId, s);
  const activeN = scope === 'active' ? sessions.length : sessions.filter((s) => s.live).length;
  state.counts[macId] = activeN;

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

// 软刷新会话列表：定时静默拉取，只就地更新「易变字段」——棕点 waiting / 相对时间 / 计数，
// 不 clear 重建整列表（避免每周期把 hover 的路径 tooltip、冷会话展开按钮闪断）。这是棕点的
// 「退出机制」：用户答完 AskUserQuestion / 授权后 jsonl 末条已变、后端 waiting 转 false，
// 这里把对应行的棕点摘掉，不必等手动刷新。会话集合发生增删（结构变了）才回退全量 loadSessions。
async function refreshSessionsSoft() {
  if (state.mode !== 'sessions' || !state.macId) return;
  const rows = $$('#session-groups .ses');
  if (!rows.length) {
    // 已经渲染成「没有会话」时也要继续探测；否则 agent 修复/会话新建后，
    // 页面会永远停在空态，必须手动点刷新。
    if ($('#session-groups .empty')) loadSessions();
    return; // 首次 / 骨架中 → 交给正在进行的 loadSessions
  }
  let data;
  try { data = await api(state.macId, `sessions?assistant=${state.assistant}&scope=${state.scope}`); }
  catch (_) { return; } // 软刷新失败静默，不打断用户
  const sessions = data.sessions || [];
  const domSids = new Set(rows.map((el) => el.dataset.sid));
  // 会话集合变化（新增 / 消失）→ 结构变了，交给全量重建（含重新分组与排序）
  if (domSids.size !== sessions.length || sessions.some((s) => !domSids.has(s.sessionId))) {
    loadSessions();
    return;
  }
  const bySid = {};
  for (const s of sessions) {
    bySid[s.sessionId] = s;
    updateCachedChatFromSession(state.macId, s);
  }
  for (const el of rows) {
    const s = bySid[el.dataset.sid];
    if (!s) continue;
    const dot = el.querySelector('.dot');
    if (dot) {
      dot.classList.toggle('wait', !!s.waiting);
      dot.title = s.waiting ? '等待你的回复 / 选择' : ''; // 置空 = 移除 tooltip（勿用 null，会渲染成 "null"）
    }
    const tEl = el.querySelector('.ses-time');
    if (tEl) tEl.textContent = relTime(s.mtime);
  }
  const activeN = state.scope === 'active' ? sessions.length : sessions.filter((s) => s.live).length;
  state.counts[state.macId] = activeN;
}

// 会话行：
// 已在池中 / 有运行中进程（行尾绿点）的会话：点行即直接进入——池内 poolShow 瞬时切换，
//   仅有进程未在池时 api open 重新 attach（tmux 复用，权限模式启动时已固定，不再让选）。
// 仅「冷会话」（无进程且未在池）点行才展开「连接 / Bypass / Auto」——那才是真正新起 Claude。
// 开了 pty 的会话另显「终止 ⏹」（与是否在池无关）。
function sessionRow(s) {
  const sid = s.sessionId;
  const assistant = s.assistant || state.assistant;
  const selfDraw = canSelfDrawChat() && assistant === 'codex';
  const inPool = !!poolFind(state.macId, sid, assistant);
  const chatConnected = assistant === 'codex' && isChatConnectionKept(state.macId, sid);
  const live = !!s.pty; // 有运行中进程（行尾绿点）：再连只是重新 attach，不需选权限模式
  const stop = s.pty && h('span', { class: 'stopbtn', title: '终止进程（会话保留）',
    onclick: (e) => { e.stopPropagation(); termSes(sid, s.title); } }, svgStop());
  const top = h('div', { class: 'ses-top' },
    // 行首点位恒定留出（标题统一对齐）：默认透明占位，仅「等待你回复/选择」(s.waiting) 显棕色点
    h('span', { class: 'dot' + (s.waiting ? ' wait' : ''), title: s.waiting ? '等待你的回复 / 选择' : null }),
    h('span', { class: 't', text: s.title || '(无标题)' }),
    // 紧凑化：不再单起一行显示分支/路径，仅在同行标题后跟相对时间
    h('span', { class: 'ses-time', text: relTime(s.mtime) }),
    h('span', { class: 'chat-cache-status', title: '自绘会话保持连接', 'aria-label': '自绘会话保持连接' }),
    stop,
  );
  // 池内 / 有进程的会话点行即直接进入，不需按钮；仅冷会话才展开三种权限模式。
  const acts = (selfDraw || inPool || live) ? null : h('div', { class: 'ses-acts' },
    h('button', { class: 'btn sm accent', title: '普通连接（逐项确认工具权限）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'default'); } },
      h('span', { class: 'gi', text: '→' }), '连接'),
    h('button', { class: 'btn sm danger', title: assistant === 'codex' ? 'codex --dangerously-bypass-approvals-and-sandbox' : 'claude --dangerously-skip-permissions（跳过全部工具权限确认）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'bypass'); } }, 'Bypass'),
    h('button', { class: 'btn sm warn', title: assistant === 'codex' ? 'codex --ask-for-approval never --sandbox workspace-write（自动批准 + 工作区可写沙箱）' : 'claude --permission-mode auto（自动批准 + 后台安全分类器）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'auto'); } }, 'Auto'));
  const row = h('div', {
    class: 'ses' + (s.pty ? ' conn' : '') + (chatConnected ? ' chat-connected' : '') + (sid === state.selectedSid ? ' sel' : ''),
    dataset: { sid },
  }, top, acts);
  // 池内 → poolShow 瞬时切换；有进程未在池 → 直接重新 attach；冷会话 → 仅高亮 + 展开三按钮。
  row.onclick = () => {
    if (selfDraw) { openChatSession(s); return; }
    if (!inPool && live) { connect(sid, s.title, s.cwd, 'default'); return; }
    selectSes(sid);
  };
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
function poolFind(macId, sessionId, assistant = state.assistant) {
  if (!sessionId) return null;
  return state.pool.find((e) => e.macId === macId && e.assistant === assistant && e.sessionId === sessionId) || null;
}

const POOL_SNAP_KEY = 'fleet-pool';
// 把当前池序列化成最小重建标识存 sessionStorage（刷新/崩溃恢复用，关标签即清）。
// 只存重建所需：macId/assistant/sessionId/permMode/title/cwd——sid/url 是 attach 时新生成的，不存。
// 池条目按 (macId,assistant,sessionId) 唯一，故 assistant 必带；cur 同样带 assistant 以精确定位焦点窗口。
function savePoolSnapshot() {
  try {
    const snap = {
      macId: state.macId,
      cur: state.current ? { sessionId: state.current.sessionId, assistant: state.current.assistant } : null,
      items: state.pool
        .filter((e) => e.sessionId) // 无 sessionId 的新建会话不持久化（无法 resume 定位）
        .map((e) => ({ macId: e.macId, assistant: e.assistant, sessionId: e.sessionId, permMode: e.permMode, title: e.title, cwd: e.cwd })),
    };
    sessionStorage.setItem(POOL_SNAP_KEY, JSON.stringify(snap));
  } catch (_) {}
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
  savePoolSnapshot();
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
  closeChatPane();
  state.current = entry;
  // 同步老字段，watch / reload / resize / 移动输入坞复用
  state.assistant = entry.assistant || state.assistant;
  $$('[data-assistant]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.assistant === state.assistant)));
  state.termSid = entry.sid; state.termUrl = entry.url; state.termSessionId = entry.sessionId;
  state.curTitle = entry.title; state.curCwd = entry.cwd; state.curMode = entry.permMode;
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
  closeChatPane();
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
function poolAdd(macId, assistant, sessionId, sid, url, title, cwd, permMode) {
  const iframe = document.createElement('iframe');
  iframe.className = 'term-frame';
  iframe.title = 'window';
  iframe.setAttribute('allow', 'clipboard-read; clipboard-write');
  const entry = { macId, assistant: assistant || 'claude', sessionId: sessionId || null, sid, url, title: title || '会话', cwd: cwd || '', permMode: permMode || 'default', iframe, lastOutput: Date.now() };
  iframe.addEventListener('load', () => hookTerm(entry)); // 每次加载/重连后套主题+回滚+记输出
  $('#frames').appendChild(iframe);
  iframe.src = url;
  state.pool.push(entry);
  poolShow(entry);
  poolEvict();
  savePoolSnapshot();
  return entry;
}

// ============================================================
//  连接 / 新建 → 终端 iframe（权限模式：default / bypass / auto）
// ============================================================
async function connect(sessionId, title, cwd, mode) {
  mode = mode || 'default';
  selectSes(sessionId); // 已在池则 selectSes 已瞬时切过去；这里再确保权限模式一致
  const exist = poolFind(state.macId, sessionId);
  if (exist && exist.permMode === mode) { poolShow(exist); return; } // 池内同模式：瞬时切回，不重连
  if (exist) poolDrop(exist); // 权限模式变了 → 丢弃旧窗口按新模式重开
  try {
    const r = await api(state.macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: state.assistant, sessionId, mode }),
    });
    state.selectedSid = sessionId;
    poolAdd(state.macId, state.assistant, sessionId, r.sid, r.url, title || '会话', cwd, r.mode || mode);
    loadSessions(); // 刷新 pty 标记：该会话现在有进程 → 行变「进入连接」+ 显示 ⏹（无骨架闪）
  } catch (e) { toast('连接失败：' + e.message, 'err'); }
}

function newSessionIn(cwd) {
  closeOverlay('projects-modal');
  api(state.macId, 'new', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ assistant: state.assistant, cwd, mode: 'default' }),
  }).then((r) => {
    state.selectedSid = null;
    poolAdd(state.macId, state.assistant, null, r.sid, r.url, '新' + assistantLabel() + '会话 · ' + projName(cwd), cwd, r.mode || 'default');
  }).catch((e) => toast('新建失败：' + e.message, 'err'));
}

// 终端头：状态点 + 标题（bypass/auto 追加权限徽标）+ 权限模式 meta
const MODE_BADGE = { bypass: { cls: 'err', tt: '⚠ 跳过权限', meta: '⚠ 跳过权限模式' }, auto: { cls: 'warn', tt: '⚡ Auto', meta: '⚡ Auto 自动批准模式' } };
function renderTermHead() {
  const tt = $('#win-title'); clear(tt);
  tt.append(h('span', { class: 'dot live' }), h('span', { class: 'ttl', text: state.curTitle }));
  const mb = MODE_BADGE[state.curMode];
  if (mb) tt.append(h('span', { class: 'badge ' + mb.cls, text: mb.tt }));
  $('#win-meta').textContent = macName(state.macId) + ' · '
    + (state.curCwd ? projName(state.curCwd) + ' · ' : '')
    + (mb ? mb.meta : '正常权限');
}

// 切回会话模式：当前池条目仍在则显示，否则空态。
function restoreTermOrEmpty() {
  if (state.current && state.pool.includes(state.current)) poolShow(state.current);
  else showEmpty();
}

// ============================================================
//  Codex 自绘会话（实验）：独立于 ttyd / iframe pool，仅替换右侧窗口区域
// ============================================================
function chatCacheKey(macId, sessionId) {
  return `${macId || ''}\n${sessionId || ''}`;
}

function saveChatDraft(chat = state.chat) {
  const input = $('#chat-input');
  if (chat && input) chat.draft = input.value || '';
}

function isChatRunning(chat) {
  return !!chat && (chat.model?.phase === 'running' || chat.submitting === true);
}

function enqueueChatFollowup(chat, text, images, id) {
  if (!chat) return null;
  chat.followups = chat.followups || [];
  const item = {
    id: id || `follow-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    text: typeof text === 'string' ? text : '',
    images: Array.isArray(images) ? images.map((image) => ({ ...image })) : [],
  };
  chat.followups.push(item);
  return item;
}

function removeChatFollowup(chat, id) {
  if (!chat?.followups) return null;
  const index = chat.followups.findIndex((item) => item.id === id);
  if (index < 0) return null;
  return chat.followups.splice(index, 1)[0];
}

function disposeChat(chat) {
  if (!chat) return;
  if (chat.events) { try { chat.events.close(); } catch (_) {} }
  chat.events = null;
  if (chat.objectUrls) for (const u of chat.objectUrls) { try { URL.revokeObjectURL(u); } catch (_) {} }
  chat.objectUrls = [];
  syncChatConnectionIndicators();
}

function chatCacheMax() {
  const s = state.settings || SETTINGS_DEFAULT;
  return Math.max(1, parseInt(s.chatCacheMaxSessions, 10) || SETTINGS_DEFAULT.chatCacheMaxSessions);
}

function chatCacheVictim(cache, currentChat) {
  let victimKey = '';
  let victim = null;
  for (const [key, chat] of cache) {
    if (chat === currentChat) continue;
    if (!victim || (Number(chat.updatedAt) || 0) < (Number(victim.updatedAt) || 0)) {
      victimKey = key;
      victim = chat;
    }
  }
  return victim ? { key: victimKey, chat: victim } : null;
}

// 超过上限时，保留当前正在看的会话；其余按最后一次会话更新时间从早到晚释放。
function evictChatCache() {
  while (state.chatCache.size > chatCacheMax()) {
    const victim = chatCacheVictim(state.chatCache, state.chat);
    if (!victim) break;
    disposeChat(victim.chat);
    state.chatCache.delete(victim.key);
  }
  syncChatConnectionIndicators();
  renderChatCacheStats();
}

function updateChatUpdatedAt(chat, value) {
  const updatedAt = Number(value) || 0;
  if (chat && updatedAt > (Number(chat.updatedAt) || 0)) chat.updatedAt = updatedAt;
}

function updateCachedChatFromSession(macId, session) {
  if (!session?.sessionId) return;
  updateChatUpdatedAt(state.chatCache.get(chatCacheKey(macId, session.sessionId)), session.mtime);
}

function isChatConnectionKept(macId, sessionId) {
  const chat = state.chatCache.get(chatCacheKey(macId, sessionId));
  return !!(chat?.events && chat.events.readyState !== EventSource.CLOSED);
}

function syncChatConnectionIndicators() {
  if (state.assistant !== 'codex') return;
  $$('#session-groups .ses').forEach((row) => {
    row.classList.toggle('chat-connected', isChatConnectionKept(state.macId, row.dataset.sid));
  });
}

function closeChatPane({ dispose = false } = {}) {
  const chat = state.chat;
  saveChatDraft(chat);
  if (dispose && chat) {
    disposeChat(chat);
    state.chatCache.delete(chat.cacheKey);
    renderChatCacheStats();
  }
  state.chat = null;
  const pane = $('#chat-pane');
  if (pane) pane.hidden = true;
  closeChatOptions();
}

function showChatPane(title, cwd) {
  state.current = null;
  state.termSid = state.termUrl = null;
  for (const e of state.pool) e.iframe.classList.remove('show');
  $('#frame').classList.remove('show');
  $('#empty-state').hidden = true;
  $('#mobile-input').hidden = true;
  $('#reconnect-btn').hidden = true;
  $('#fullscreen-btn').hidden = false;
  $('#chat-pane').hidden = false;
  const tt = $('#win-title'); clear(tt);
  tt.append(h('span', { class: 'dot live' }), h('span', { class: 'ttl', text: title || 'Codex 会话' }), h('span', { class: 'badge live', text: '自绘' }));
  $('#win-meta').textContent = macName(state.macId) + ' · ' + (cwd ? projName(cwd) + ' · ' : '') + 'Codex Desktop-backed';
  if (isMobile()) $('#app').classList.add('term-open');
}

function chatAtBottom() {
  const sc = $('#chat-scroll');
  return sc.scrollHeight - sc.scrollTop - sc.clientHeight < 64;
}

function currentChatTurn(model) {
  for (let i = (model?.messages?.length || 0) - 1; i >= 0; i -= 1) {
    const id = model.messages[i];
    if (model.items[id]?.type === 'user') return { id, item: model.items[id] };
  }
  return null;
}

function firstChatLine(text) {
  return String(text || '').split(/\r?\n/, 1)[0].trim();
}

function syncChatTurnPin() {
  const sc = $('#chat-scroll');
  const pin = $('#chat-turn-pin');
  const row = sc?.querySelector('.chat-row.current-turn');
  if (!sc || !pin || !row || !$('#chat-turn-pin-text').textContent) {
    if (pin) pin.hidden = true;
    return;
  }
  pin.hidden = row.getBoundingClientRect().bottom > sc.getBoundingClientRect().top;
}

function renderChat({ preserveScroll = false, forceBottom = false } = {}) {
  const chat = state.chat;
  const sc = $('#chat-scroll');
  if (!chat || !sc) return;
  const oldHeight = sc.scrollHeight;
  const oldTop = sc.scrollTop;
  const stick = !preserveScroll && chatAtBottom();
  const stack = h('div', { class: 'chat-stack' });
  if (chat.historyReady && chat.historyLoading) {
    stack.append(h('div', { class: 'chat-history-state', text: '正在加载更早记录…' }));
  }
  if (chat.loading) stack.append(chatRow(h('div', { class: 'chat-card muted', text: '正在连接 Codex app-server…' })));
  const model = chat.model || FleetChatModel.createChatState();
  const currentTurn = currentChatTurn(model);
  for (const id of model.messages) {
    const item = model.items[id];
    if (!item) continue;
    stack.append(renderChatItem(item, id === currentTurn?.id));
  }
  if (model.error) stack.append(renderChatError(model.error));
  clear(sc); sc.append(stack);
  if (preserveScroll) sc.scrollTop = oldTop + (sc.scrollHeight - oldHeight);
  else if (forceBottom || stick) sc.scrollTop = sc.scrollHeight;
  $('#chat-turn-pin-text').textContent = firstChatLine(currentTurn?.item?.text);
  syncChatTurnPin();
  $('#chat-jump').hidden = chatAtBottom();
}

function chatRow(body, cls = '') {
  return h('div', { class: 'chat-row ' + cls }, body);
}

function chatToolStatus(status) {
  const value = String(status || '').toLowerCase();
  if (['inprogress', 'running', 'pending', 'started', 'interacted'].includes(value)) return { key: 'running', label: '运行中' };
  if (['failed', 'errored', 'error', 'interrupted'].includes(value)) return { key: 'failed', label: '失败' };
  if (['completed', 'success', 'succeeded', 'shutdown'].includes(value)) return { key: 'completed', label: '完成' };
  return { key: 'neutral', label: status || '' };
}

function chatToolDuration(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  if (value < 1000) return `${Math.round(value)} ms`;
  if (value < 60000) return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} 秒`;
  const minutes = Math.floor(value / 60000);
  const seconds = Math.round((value % 60000) / 1000);
  return `${minutes} 分 ${seconds} 秒`;
}

function formatChatDate(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  const d = new Date(value);
  if (!Number.isFinite(d.getTime())) return '';
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function formatChatInteger(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return '';
  return Math.round(n).toLocaleString('en-US');
}

function chatUserMetaText(item) {
  const sent = formatChatDate(item.sentAtMs || item.createdAtMs || item.created_at_ms);
  return sent ? `用户：${sent}` : '';
}

function chatAssistantMetaText(item) {
  const chunks = [];
  const identity = [item.model, item.effort].filter(Boolean).join(', ');
  if (identity) chunks.push(identity);
  const inputTokens = formatChatInteger(item.usage?.inputTokens);
  const outputTokens = formatChatInteger(item.usage?.outputTokens);
  if (inputTokens || outputTokens) chunks.push(`in ${inputTokens || '-'} / out ${outputTokens || '-'}`);
  const completed = formatChatDate(item.completedAtMs || item.finishedAtMs || item.finished_at_ms);
  if (completed) chunks.push(completed);
  return chunks.length ? `AI：${chunks.join('  |  ')}` : '';
}

function applyChatMetadataDefaults(chat) {
  const model = chat?.model;
  if (!chat || !model?.items) return;
  for (const id of model.messages || []) {
    const item = model.items[id];
    if (!item || item.type !== 'assistant') continue;
    if (!item.model && chat.selectedModel) item.model = chat.selectedModel;
    if (!item.effort && chat.selectedEffort) item.effort = chat.selectedEffort;
  }
}

function renderChatMessageMeta(text) {
  return text ? h('div', { class: 'chat-msg-meta mono', text }) : null;
}

// Codex 自绘工具行使用项目内 Lucide SVG 副本：
// server/dashboard/icons/codex-tools/*.svg。运行时仍创建内联 SVG，避免额外请求/外部依赖。
function chatToolIcon(kind) {
  if (kind === 'commandExecution') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'm7 11 2-2-2-2' } },
    { tag: 'path', attrs: { d: 'M11 13h4' } },
    { tag: 'rect', attrs: { x: '3', y: '3', width: '18', height: '18', rx: '2', ry: '2' } },
  ]);
  if (kind === 'fileRead') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'M12 7v14' } },
    { tag: 'path', attrs: { d: 'M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z' } },
  ]);
  if (kind === 'fileChange') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'M21.174 6.812a1 1 0 0 0-3.986-3.987L3.842 16.174a2 2 0 0 0-.5.83l-1.321 4.352a.5.5 0 0 0 .623.622l4.353-1.32a2 2 0 0 0 .83-.497z' } },
    { tag: 'path', attrs: { d: 'm15 5 4 4' } },
  ]);
  if (kind === 'webSearch') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'm21 21-4.34-4.34' } },
    { tag: 'circle', attrs: { cx: '11', cy: '11', r: '8' } },
  ]);
  if (kind === 'imageView' || kind === 'imageGeneration') return svgIconParts('ic', [
    { tag: 'rect', attrs: { x: '3', y: '3', width: '18', height: '18', rx: '2', ry: '2' } },
    { tag: 'circle', attrs: { cx: '9', cy: '9', r: '2' } },
    { tag: 'path', attrs: { d: 'm21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21' } },
  ]);
  if (kind === 'collabAgentToolCall' || kind === 'subAgentActivity') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'M12 8V4H8' } },
    { tag: 'rect', attrs: { x: '4', y: '12', width: '8', height: '8', rx: '1' } },
    { tag: 'path', attrs: { d: 'M12 12h4v-2' } },
    { tag: 'path', attrs: { d: 'M16 4h4v4' } },
    { tag: 'path', attrs: { d: 'M16 8h4' } },
    { tag: 'path', attrs: { d: 'M20 12v4h-4' } },
    { tag: 'path', attrs: { d: 'M16 16h4' } },
  ]);
  if (kind === 'sleep') return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z' } },
  ]);
  return svgIconParts('ic', [
    { tag: 'path', attrs: { d: 'M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94z' } },
  ]);
}

function renderChatTool(item) {
  const status = chatToolStatus(item.status);
  const duration = chatToolDuration(item.durationMs);
  const hasBody = Boolean(item.detail || item.output || item.progress || item.meta || item.exitCode !== undefined);
  const isCommand = item.kind === 'commandExecution';
  const verb = status.key === 'running' ? '正在运行' : (status.key === 'failed' ? '运行失败' : '已运行');
  const title = isCommand ? verb : (item.title || '工具调用');
  const summary = isCommand ? (item.summary || item.title || '') : (item.summary || '');
  const header = h('span', { class: 'chat-tool-summary' },
    h('span', { class: 'chat-tool-icon' }, chatToolIcon(item.kind)),
    h('span', { class: 'chat-tool-copy' },
      h('span', { class: 'chat-tool-title', text: title }),
      summary ? h('span', { class: 'chat-tool-subtitle mono', text: summary }) : null),
    h('span', { class: 'chat-tool-aside' },
      duration ? h('span', { class: 'chat-tool-duration', text: duration }) : null,
      status.label ? h('span', { class: `chat-tool-status ${status.key}`, text: status.label }) : null,
      hasBody ? svgIcon('chat-tool-chevron', 'M6 9l6 6 6-6') : null));
  if (!hasBody) return chatRow(h('div', { class: 'chat-tool compact' }, header), 'tool');
  const body = h('div', { class: 'chat-tool-body' },
    item.progress ? h('div', { class: 'chat-tool-progress', text: item.progress }) : null,
    item.meta ? h('div', { class: 'chat-tool-meta mono', text: item.meta }) : null,
    (item.summary || item.output || item.detail) ? h('div', { class: 'chat-tool-section' },
      h('div', { class: 'chat-tool-label', text: isCommand ? 'Shell' : '详情' }),
      h('pre', { text: [
        item.summary ? `$ ${item.summary}` : '',
        item.detail || '',
        item.output || '',
      ].filter(Boolean).join('\n') })) : null,
    item.exitCode !== undefined ? h('div', { class: 'chat-tool-exit mono', text: `退出码 ${item.exitCode}` }) : null);
  return chatRow(h('details', { class: 'chat-tool compact' }, h('summary', {}, header), body), 'tool');
}

function renderChatDiff(item) {
  const files = item.files || [];
  const title = '编辑了文件';
  const header = h('span', { class: 'chat-tool-summary' },
    h('span', { class: 'chat-tool-icon' }, chatToolIcon('fileChange')),
    h('span', { class: 'chat-tool-copy' }, h('span', { class: 'chat-tool-title', text: title })),
    h('span', { class: 'chat-tool-aside' }, files.length ? svgIcon('chat-tool-chevron', 'M6 9l6 6 6-6') : null));
  if (!files.length) return chatRow(h('div', { class: 'chat-tool chat-diff compact' }, header), 'diff');
  return chatRow(h('details', { class: 'chat-tool chat-diff compact' },
    h('summary', {}, header),
    h('div', { class: 'chat-diff-files' }, files.map((file) => h('div', { class: 'chat-diff-file' },
      h('span', { class: 'chat-diff-path mono', text: file.path }),
      h('span', { class: 'chat-diff-stats mono' },
        h('span', { class: 'chat-diff-add', text: `+${file.additions || 0}` }),
        h('span', { class: 'chat-diff-del', text: `-${file.deletions || 0}` })))))),
    'diff');
}

function renderChatItem(item, isCurrentTurn = false) {
  if (item.type === 'user') {
    const parts = [];
    if (item.text) parts.push(h('div', { text: item.text }));
    if (item.images && item.images.length) {
      parts.push(h('div', { class: 'chat-images' }, item.images.map((img) => {
        const src = chatImageSrc(img);
        return src ? h('img', { class: 'chat-img', src, alt: img.name || 'image' }) : h('div', { class: 'chat-img muted', text: img.name || '图片' });
      })));
    }
    const meta = renderChatMessageMeta(chatUserMetaText(item));
    if (meta) parts.push(meta);
    return chatRow(h('div', { class: 'chat-card' }, parts.length ? parts : h('div', { text: '' })), 'user' + (isCurrentTurn ? ' current-turn' : ''));
  }
  if (item.type === 'assistant') {
    return chatRow(h('div', { class: 'chat-card' },
      FleetMarkdown.renderMarkdown(item.text),
      renderChatMessageMeta(chatAssistantMetaText(item))),
    'assistant');
  }
  if (item.type === 'tool') return renderChatTool(item);
  if (item.type === 'approval') return chatRow(h('div', { class: 'chat-approval' },
    h('div', { class: 'chat-approval-h', text: item.status === 'resolved' ? '审批已处理' : (item.kind === 'command' ? '需要批准命令执行' : '需要批准权限 / 文件改动') }),
    h('div', { class: 'chat-approval-body' },
      item.reason ? h('div', { text: item.reason }) : null,
      item.command ? h('code', { text: item.command }) : null,
      item.cwd ? h('div', { class: 'muted mono', text: item.cwd }) : null,
      item.status === 'resolved' ? h('div', { class: 'muted', text: '已发送决定。' }) : h('div', { class: 'chat-approval-actions' },
        h('button', { class: 'btn sm primary', onclick: () => resolveApproval(item.requestId, 'approved') }, '批准'),
        h('button', { class: 'btn sm', onclick: () => resolveApproval(item.requestId, 'denied') }, '拒绝')))), 'approval');
  if (item.type === 'diff') return renderChatDiff(item);
  return chatRow(h('div', { class: 'chat-card muted', text: JSON.stringify(item) }));
}

function chatImageSrc(img) {
  if (!img) return '';
  if (img.previewUrl) return img.previewUrl;
  if (img.url && img.url.startsWith('/api/')) return `${apiBase(state.macId)}${img.url}`;
  return img.url || '';
}

function resizeChatInput() {
  const input = $('#chat-input');
  if (!input) return;
  input.style.height = 'auto';
  input.style.height = Math.min(input.scrollHeight, 180) + 'px';
  input.style.overflowY = input.scrollHeight > 180 ? 'auto' : 'hidden';
}

function updateChatComposerState() {
  const input = $('#chat-input');
  const send = $('#chat-send');
  if (!send) return;
  const attachments = state.chat?.attachments || [];
  const blocked = attachments.some((att) => att.uploading || att.error || !att.id);
  const hasContent = Boolean(input?.value.trim()) || attachments.length > 0;
  const running = isChatRunning(state.chat);
  send.dataset.action = running ? 'interrupt' : 'send';
  send.disabled = !state.chat || (running ? !!state.chat.interrupting : (!hasContent || blocked));
  send.title = running ? '停止生成' : (blocked ? '等待图片上传完成' : '发送');
  send.setAttribute('aria-label', send.title);
}

function renderChatFollowups() {
  const box = $('#chat-followups');
  const chat = state.chat;
  if (!box) return;
  clear(box);
  const items = chat?.followups || [];
  box.hidden = items.length === 0;
  for (const item of items) {
    const label = item.text || `${item.images.length} 张图片`;
    box.append(h('div', { class: 'chat-followup', dataset: { id: item.id } },
      svgIconParts('chat-followup-icon', [
        { tag: 'path', attrs: { d: 'M8 6h11M8 12h11M8 18h7' } },
        { tag: 'circle', attrs: { cx: '3.5', cy: '6', r: '1', fill: 'currentColor', stroke: 'none' } },
        { tag: 'circle', attrs: { cx: '3.5', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
        { tag: 'circle', attrs: { cx: '3.5', cy: '18', r: '1', fill: 'currentColor', stroke: 'none' } },
      ]),
      h('span', { class: 'chat-followup-text', text: label, title: label }),
      item.images.length ? h('span', { class: 'chat-followup-images', text: `+${item.images.length} 图` }) : null,
      h('div', { class: 'chat-followup-actions' },
        h('button', { type: 'button', class: 'chat-followup-guide', title: '引导当前任务', onclick: () => guideChatFollowup(item.id) },
          svgIconParts('ic', [{ tag: 'path', attrs: { d: 'M4 5v6a4 4 0 0 0 4 4h11M15 11l4 4-4 4' } }]), '引导'),
        h('button', { type: 'button', class: 'iconbtn bare', title: '编辑追问', 'aria-label': '编辑追问', onclick: () => editChatFollowup(item.id) },
          svgIconParts('ic', [{ tag: 'path', attrs: { d: 'M12 20h9M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z' } }])),
        h('button', { type: 'button', class: 'iconbtn bare', title: '删除追问', 'aria-label': '删除追问', onclick: () => { removeChatFollowup(chat, item.id); renderChatFollowups(); } },
          svgIconParts('ic', [{ tag: 'path', attrs: { d: 'M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v5M14 11v5' } }])))));
  }
}

function editChatFollowup(id) {
  const chat = state.chat;
  const item = removeChatFollowup(chat, id);
  if (!item) return;
  const input = $('#chat-input');
  const current = input.value.trim();
  input.value = [current, item.text].filter(Boolean).join(current ? '\n' : '');
  chat.draft = input.value;
  chat.attachments = [...(chat.attachments || []), ...item.images.map((image) => ({ ...image }))];
  renderChatFollowups();
  renderChatAttachments();
  resizeChatInput();
  input.focus();
}

function renderChatAttachments() {
  const box = $('#chat-attachments');
  const chat = state.chat;
  if (!box || !chat) { updateChatComposerState(); return; }
  const atts = chat.attachments || [];
  box.hidden = atts.length === 0;
  clear(box);
  for (const att of atts) {
    box.append(h('div', { class: 'chat-att' + (att.error ? ' err' : '') },
      att.previewUrl ? h('img', { src: att.previewUrl, alt: att.name || 'image' }) : null,
      att.error || att.uploading ? h('span', { class: 'st', text: att.error ? '失败' : '上传中' }) : null,
      h('button', { type: 'button', title: '移除图片', 'aria-label': '移除图片', onclick: () => removeChatAttachment(att.localId) },
        svgIcon('ic', 'M18 6 6 18 M6 6l12 12'))));
  }
  updateChatComposerState();
}

function removeChatAttachment(localId) {
  const chat = state.chat;
  if (!chat) return;
  chat.attachments = (chat.attachments || []).filter((att) => att.localId !== localId);
  renderChatAttachments();
}

async function addChatFiles(files) {
  const chat = state.chat;
  if (!chat || !files || !files.length) return;
  chat.attachments = chat.attachments || [];
  chat.objectUrls = chat.objectUrls || [];
  for (const file of Array.from(files)) {
    if (!file || !String(file.type || '').startsWith('image/')) {
      toast('只能上传图片。', 'err');
      continue;
    }
    const previewUrl = URL.createObjectURL(file);
    chat.objectUrls.push(previewUrl);
    const att = { localId: 'local-' + Date.now() + '-' + Math.random().toString(16).slice(2), name: file.name, size: file.size, mime: file.type, previewUrl, uploading: true };
    chat.attachments.push(att);
    renderChatAttachments();
    uploadChatFile(chat, att, file);
  }
}

async function uploadChatFile(chat, att, file) {
  try {
    const fd = new FormData();
    fd.append('assistant', 'codex');
    fd.append('sessionId', chat.sessionId);
    fd.append('file', file, file.name || 'image');
    const r = await fetch(`${apiBase(chat.macId)}/api/chat/upload`, { method: 'POST', body: fd });
    if (!r.ok) {
      let msg = `上传失败：${r.status}`;
      try { const j = await r.json(); if (j && j.message) msg = j.message; } catch (_) {}
      throw new Error(msg);
    }
    const saved = await r.json();
    if (state.chatCache.get(chat.cacheKey) !== chat) return;
    Object.assign(att, saved, { uploading: false, error: '' });
  } catch (e) {
    att.uploading = false;
    att.error = e.message || '上传失败';
    toast('图片上传失败：' + att.error, 'err');
  } finally {
    if (state.chat === chat) renderChatAttachments();
  }
}

function renderChatError(msg) {
  return chatRow(h('div', { class: 'chat-error' },
    h('div', { text: msg }),
    h('div', { class: 'chat-error-actions' }, h('button', { class: 'btn sm accent', onclick: openChatFallback }, '用终端打开'))), 'error');
}

async function openChatSession(s) {
  if (!canSelfDrawChat()) return;
  state.selectedSid = s.sessionId;
  $$('.ses').forEach((el) => el.classList.toggle('sel', el.dataset.sid === s.sessionId));
  closeChatPane();
  stopWatch(); hideBanner(); closeMenus();
  const key = chatCacheKey(state.macId, s.sessionId);
  let chat = state.chatCache.get(key);
  if (chat) {
    chat.title = s.title || chat.title || 'Codex 会话';
    chat.cwd = s.cwd || chat.cwd || '';
  } else {
    chat = {
      cacheKey: key, macId: state.macId,
      sessionId: s.sessionId, title: s.title || 'Codex 会话', cwd: s.cwd || '',
      model: FleetChatModel.createChatState(), loading: true, events: null, resumePromise: null,
      attachments: [], objectUrls: [], draft: '', updatedAt: Number(s.mtime) || Date.now(),
      followups: [], sendingFollowup: false, interrupting: false,
      historyReady: false, historyLoading: false, historyCursor: '',
      models: [], efforts: [], serviceTiers: [], selectedModel: '', selectedEffort: '', selectedServiceTier: '',
      modelDirty: false, serviceTierDirty: false,
      approvalMode: 'on-request', approvalDirty: false,
    };
    state.chatCache.set(key, chat);
  }
  state.chat = chat;
  updateChatUpdatedAt(chat, s.mtime);
  evictChatCache();
  showChatPane(chat.title, chat.cwd);
  $('#chat-input').value = chat.draft || '';
  resizeChatInput();
  renderChatAttachments();
  renderChatFollowups();
  if (chat.historyReady) {
    $('#chat-approval').value = chat.approvalMode || 'on-request';
    $('#chat-approval').disabled = false;
    renderChatOptions(chat);
  } else {
    $('#chat-approval').disabled = true;
    $('#chat-options').hidden = true;
    closeChatOptions();
  }
  renderChat({ forceBottom: true });
  updateChatComposerState();
  if (chat.historyReady) {
    startChatEvents(chat);
    return;
  }
  if (chat.resumePromise) return;
  try {
    chat.resumePromise = api(chat.macId, 'chat/resume', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, mode: 'default' }),
    });
    const resumed = await chat.resumePromise;
    if (state.chatCache.get(chat.cacheKey) === chat) {
      chat.model = FleetChatModel.prependHistory(chat.model, resumed.history?.events || []);
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'thread_status', data: { status: resumed.status } });
      chat.historyCursor = resumed.history?.nextCursor || '';
      chat.historyReady = true;
      chat.loading = false;
      configureChatOptions(chat, resumed);
      applyChatMetadataDefaults(chat);
      startChatEvents(chat);
    }
    if (state.chat === chat) {
      renderChat({ forceBottom: true });
      updateChatComposerState();
      renderChatFollowups();
    }
  } catch (e) {
    chat.loading = false;
    chat.resumePromise = null;
    if (state.chat === chat) {
      $('#chat-approval').disabled = false;
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'error', data: { message: e.message } });
      renderChat();
    }
  }
}

const CHAT_EFFORT_LABELS = {
  none: '无', minimal: '最少', low: '低', medium: '中', high: '高', xhigh: '极高', max: '最大', ultra: '超高',
};
const CHAT_SERVICE_TIER_LABELS = { default: '标准', standard: '标准', fast: '快速', priority: '快速' };

function selectedChatModel(chat) {
  return chat.models.find((item) => item.value === chat.selectedModel);
}

function chatModelLabel(model, compact = false) {
  const label = model?.displayName || model?.value || '模型';
  return compact ? label.replace(/^GPT-/i, '').replaceAll('-', ' ') : label;
}

function chatEffortLabel(value) {
  return CHAT_EFFORT_LABELS[value] || value || '';
}

function chatServiceTierLabel(tier) {
  if (!tier?.value) return '标准';
  return CHAT_SERVICE_TIER_LABELS[tier.value] || CHAT_SERVICE_TIER_LABELS[String(tier.name || '').toLowerCase()] || tier.name || tier.value;
}

function chatServiceTierDescription(tier) {
  if (!tier?.value) return '标准速度';
  if (tier.value === 'priority' && tier.description === '1.5x speed, increased usage') return '1.5 倍速度，消耗更多额度';
  return tier.description || '';
}

function configureChatEfforts(chat, preferred = '') {
  const model = selectedChatModel(chat);
  const advertised = Array.isArray(model?.supportedEfforts) ? model.supportedEfforts.filter((item) => item?.value) : [];
  const efforts = advertised.slice();
  if (model?.defaultEffort && !efforts.some((item) => item.value === model.defaultEffort)) {
    efforts.push({ value: model.defaultEffort, description: '' });
  }
  chat.efforts = efforts;
  const values = new Set(efforts.map((item) => item.value));
  chat.selectedEffort = values.has(preferred) ? preferred : (model?.defaultEffort || efforts[0]?.value || '');
}

function configureChatServiceTiers(chat, preferred = '') {
  const model = selectedChatModel(chat);
  const advertised = Array.isArray(model?.serviceTiers) ? model.serviceTiers.filter((item) => item?.value) : [];
  chat.serviceTiers = [{ value: '', name: '标准', description: '标准速度' }, ...advertised];
  const values = new Set(chat.serviceTiers.map((item) => item.value));
  chat.selectedServiceTier = values.has(preferred) ? preferred : '';
}

function closeChatOptions() {
  const popover = $('#chat-options-popover');
  if (!popover) return;
  popover.hidden = true;
  popover.classList.remove('has-submenu');
  $('#chat-options-submenu').hidden = true;
  $('#chat-options-trigger').setAttribute('aria-expanded', 'false');
  $$('[data-chat-options-panel]').forEach((row) => row.removeAttribute('aria-current'));
}

function showChatOptionsMain({ focus = false } = {}) {
  const popover = $('#chat-options-popover');
  if (!popover || !state.chat || $('#chat-options').hidden) return;
  popover.hidden = false;
  popover.classList.remove('has-submenu');
  $('#chat-options-submenu').hidden = true;
  $('#chat-options-trigger').setAttribute('aria-expanded', 'true');
  $$('[data-chat-options-panel]').forEach((row) => row.removeAttribute('aria-current'));
  if (focus) $('[data-chat-options-panel]:not(:disabled)')?.focus();
}

function toggleChatOptions() {
  if ($('#chat-options-popover').hidden) showChatOptionsMain();
  else closeChatOptions();
}

function renderChatOptionChoice(kind, option, selected) {
  const value = option.value || '';
  const label = kind === 'model' ? chatModelLabel(option) : (kind === 'effort' ? chatEffortLabel(value) : chatServiceTierLabel(option));
  const description = kind === 'speed' ? chatServiceTierDescription(option) : option.description;
  const button = h('button', {
    type: 'button', class: 'chat-option-choice' + (selected ? ' selected' : ''), role: 'menuitemradio',
    'aria-checked': String(selected), dataset: { chatOptionKind: kind, chatOptionValue: value },
  }, h('span', { class: 'chat-option-choice-copy' },
    h('span', { class: 'chat-option-choice-label', text: label }),
    description ? h('span', { class: 'chat-option-choice-desc', text: description }) : null),
  selected ? svgIcon('chat-option-check', 'M20 6 9 17l-5-5') : null);
  button.onclick = () => selectChatOption(kind, value);
  return button;
}

function openChatOptionsPanel(kind) {
  const chat = state.chat;
  if (!chat) return;
  const popover = $('#chat-options-popover');
  const submenu = $('#chat-options-submenu');
  const choices = $('#chat-options-choices');
  const titles = { model: '模型', effort: '推理强度', speed: '速度' };
  $('#chat-options-submenu-title').textContent = titles[kind] || '';
  clear(choices);
  if (kind === 'model') {
    for (const model of chat.models) choices.append(renderChatOptionChoice(kind, model, model.value === chat.selectedModel));
  } else if (kind === 'effort') {
    for (const effort of chat.efforts) choices.append(renderChatOptionChoice(kind, effort, effort.value === chat.selectedEffort));
  } else if (kind === 'speed') {
    for (const tier of chat.serviceTiers) choices.append(renderChatOptionChoice(kind, tier, tier.value === chat.selectedServiceTier));
  }
  $$('[data-chat-options-panel]').forEach((row) => row.setAttribute('aria-current', String(row.dataset.chatOptionsPanel === kind)));
  popover.hidden = false;
  popover.classList.add('has-submenu');
  submenu.hidden = false;
  $('#chat-options-trigger').setAttribute('aria-expanded', 'true');
}

function selectChatOption(kind, value) {
  const chat = state.chat;
  if (!chat) return;
  if (kind === 'model' && chat.models.some((model) => model.value === value)) {
    chat.selectedModel = value;
    const model = selectedChatModel(chat);
    configureChatEfforts(chat, model?.defaultEffort || '');
    configureChatServiceTiers(chat, '');
    chat.modelDirty = true;
    chat.serviceTierDirty = true;
  } else if (kind === 'effort' && chat.efforts.some((effort) => effort.value === value)) {
    chat.selectedEffort = value;
    chat.modelDirty = true;
  } else if (kind === 'speed' && chat.serviceTiers.some((tier) => tier.value === value)) {
    chat.selectedServiceTier = value;
    chat.serviceTierDirty = true;
  }
  renderChatOptions(chat);
  closeChatOptions();
}

function renderChatOptions(chat) {
  const root = $('#chat-options');
  const model = selectedChatModel(chat);
  root.hidden = !model;
  if (!model) { closeChatOptions(); return; }
  const effort = chatEffortLabel(chat.selectedEffort);
  const tier = chat.serviceTiers.find((item) => item.value === chat.selectedServiceTier);
  $('#chat-options-model').textContent = chatModelLabel(model, true);
  $('#chat-options-effort').textContent = effort;
  $('#chat-options-model-value').textContent = chatModelLabel(model, true);
  $('#chat-options-effort-value').textContent = effort;
  $('#chat-options-speed-value').textContent = chatServiceTierLabel(tier);
  $('[data-chat-options-panel="model"]').disabled = chat.models.length === 0;
  $('[data-chat-options-panel="effort"]').disabled = chat.efforts.length === 0;
  $('[data-chat-options-panel="speed"]').disabled = chat.serviceTiers.length === 0;
}

function configureChatOptions(chat, resumed) {
  const approvalModes = new Set(['untrusted', 'on-request', 'never', 'full-access']);
  chat.approvalMode = approvalModes.has(resumed.approvalMode) ? resumed.approvalMode : 'on-request';
  chat.approvalDirty = false;

  const models = Array.isArray(resumed.models) ? resumed.models.slice() : [];
  if (resumed.model && !models.some((m) => m.value === resumed.model)) {
    models.unshift({ value: resumed.model, displayName: resumed.model, defaultEffort: '', supportedEfforts: [], serviceTiers: [] });
  }
  chat.models = models.filter((model) => model?.value);
  chat.selectedModel = resumed.model || (models.find((m) => m.isDefault)?.value || models[0]?.value || '');
  const selected = selectedChatModel(chat);
  configureChatEfforts(chat, resumed.effort || selected?.defaultEffort || '');
  configureChatServiceTiers(chat, resumed.serviceTier || '');
  chat.modelDirty = false;
  chat.serviceTierDirty = false;
  if (state.chat === chat) {
    const approval = $('#chat-approval');
    approval.value = chat.approvalMode;
    approval.disabled = false;
    renderChatOptions(chat);
  }
}

async function loadOlderChatHistory() {
  const chat = state.chat;
  if (!chat || !chat.historyReady || chat.historyLoading || !chat.historyCursor) return;
  chat.historyLoading = true;
  renderChat({ preserveScroll: true });
  try {
    const page = await api(chat.macId, `chat/history?assistant=codex&sessionId=${encodeURIComponent(chat.sessionId)}&cursor=${encodeURIComponent(chat.historyCursor)}`);
    if (state.chat !== chat) return;
    chat.model = FleetChatModel.prependHistory(chat.model, page.events || []);
    applyChatMetadataDefaults(chat);
    chat.historyCursor = page.nextCursor || '';
  } catch (e) {
    if (state.chat === chat) toast('加载历史失败：' + e.message, 'err');
  } finally {
    if (state.chat === chat) {
      chat.historyLoading = false;
      renderChat({ preserveScroll: true });
    }
  }
}

function startChatEvents(chat = state.chat) {
  if (!chat) return;
  if (chat.events && chat.events.readyState !== EventSource.CLOSED) return;
  const url = `${apiBase(chat.macId)}/api/chat/events?assistant=codex&sessionId=${encodeURIComponent(chat.sessionId)}`;
  const es = new EventSource(url);
  chat.events = es;
  syncChatConnectionIndicators();
  es.onmessage = (e) => {
    if (state.chatCache.get(chat.cacheKey) !== chat) return;
    try {
      const ev = JSON.parse(e.data);
      updateChatUpdatedAt(chat, Date.now());
      const wasRunning = isChatRunning(chat);
      chat.model = FleetChatModel.reduceChatEvent(chat.model, ev);
      applyChatMetadataDefaults(chat);
      if (state.chat === chat) {
        renderChat();
        updateChatComposerState();
        renderChatFollowups();
      }
      if (wasRunning && !isChatRunning(chat)) flushChatFollowups(chat);
    } catch (_) {}
  };
  es.onerror = () => {
    if (state.chatCache.get(chat.cacheKey) !== chat || chat.loading) return;
    // EventSource 会自动重连；瞬时断流不要写进消息流，否则会像真实回复一样污染历史。
    chat.streamState = 'reconnecting';
  };
}

async function submitChatInput() {
  const chat = state.chat;
  const input = $('#chat-input');
  const raw = typeof input.value === 'string' ? input.value : '';
  const text = raw.trim();
  if (!chat) return;
  const pending = chat.attachments || [];
  if (pending.some((att) => att.uploading)) { toast('图片还在上传，稍等一下。'); return; }
  if (pending.some((att) => att.error || !att.id)) { toast('有图片上传失败，先移除或重新选择。', 'err'); return; }
  if (!text && pending.length === 0) return;
  const images = pending.map((att) => ({ id: att.id, name: att.name, mime: att.mime, size: att.size, url: att.url, previewUrl: att.previewUrl }));
  input.value = '';
  chat.draft = '';
  resizeChatInput();
  chat.attachments = [];
  renderChatAttachments();
  const item = { id: `follow-${Date.now()}-${Math.random().toString(16).slice(2)}`, text: raw, images };
  if (isChatRunning(chat)) {
    enqueueChatFollowup(chat, item.text, item.images, item.id);
    renderChatFollowups();
    return;
  }
  await sendChatTurn(chat, item);
}

function chatTurnOptions(chat) {
  const turnOptions = {};
  if (chat.modelDirty && chat.selectedModel) {
    turnOptions.model = chat.selectedModel;
    if (chat.selectedEffort) turnOptions.effort = chat.selectedEffort;
  }
  if (chat.serviceTierDirty) turnOptions.serviceTier = chat.selectedServiceTier;
  if (chat.approvalDirty && chat.approvalMode) turnOptions.approvalMode = chat.approvalMode;
  return turnOptions;
}

async function sendChatTurn(chat, item) {
  chat.model = FleetChatModel.appendUserMessage(chat.model, item.text.trim(), 'user-' + Date.now(), item.images);
  chat.loading = false;
  chat.submitting = true;
  if (state.chat === chat) {
    renderChat();
    updateChatComposerState();
  }
  try {
    const started = await api(chat.macId, 'chat/input', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, text: item.text, images: item.images.map((img) => ({ id: img.id })), ...chatTurnOptions(chat) }),
    });
    chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'turn_started', turnId: started.turnId, data: { turnId: started.turnId } });
    return true;
  } catch (e) {
    if (state.chat === chat) {
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'error', data: { message: e.message } });
      renderChat();
    }
    return false;
  } finally {
    chat.submitting = false;
    if (state.chat === chat) updateChatComposerState();
  }
}

async function flushChatFollowups(chat) {
  if (!chat || chat.sendingFollowup || isChatRunning(chat) || !chat.followups?.length) return;
  chat.sendingFollowup = true;
  const item = chat.followups[0];
  const sent = await sendChatTurn(chat, item);
  if (sent) removeChatFollowup(chat, item.id);
  chat.sendingFollowup = false;
  if (state.chat === chat) renderChatFollowups();
}

async function guideChatFollowup(id) {
  const chat = state.chat;
  const item = chat?.followups?.find((entry) => entry.id === id);
  if (!chat || !item || !isChatRunning(chat)) return;
  try {
    await api(chat.macId, 'chat/steer', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, text: item.text, images: item.images.map((img) => ({ id: img.id })) }),
    });
    removeChatFollowup(chat, id);
    chat.model = FleetChatModel.appendUserMessage(chat.model, item.text.trim(), 'user-' + Date.now(), item.images);
    renderChatFollowups();
    renderChat();
  } catch (e) {
    toast('引导失败：' + e.message, 'err');
  }
}

async function interruptChat() {
  const chat = state.chat;
  if (!chat || !isChatRunning(chat) || chat.interrupting) return;
  chat.interrupting = true;
  updateChatComposerState();
  try {
    await api(chat.macId, 'chat/interrupt', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId }),
    });
  } catch (e) {
    toast('停止失败：' + e.message, 'err');
  } finally {
    chat.interrupting = false;
    updateChatComposerState();
  }
}

async function resolveApproval(requestId, decision) {
  const chat = state.chat;
  if (!chat) return;
  try {
    await api(chat.macId, 'chat/approve', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, requestId, decision }),
    });
    chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'approval_resolved', data: { requestId, decision } });
    renderChat();
  } catch (e) {
    toast('审批失败：' + e.message, 'err');
  }
}

function openChatFallback() {
  const chat = state.chat;
  if (!chat) return;
  const { sessionId, title, cwd } = chat;
  closeChatPane();
  connect(sessionId, title, cwd, 'default');
}

// 刷新/崩溃后从 sessionStorage 快照恢复终端（init 唯一入口；无快照则退回空态）。
// connect 取 state.macId / state.assistant，故恢复每条前先把这两者切到该条；逐条重连
// （受 poolMax 上限约束，poolAdd 内已 poolEvict）；全部恢复后复位到快照当前主机/助手、
// 刷新侧栏、显示其当前会话，不抢焦点切走。
async function restorePoolSnapshot() {
  let snap;
  try { snap = JSON.parse(sessionStorage.getItem(POOL_SNAP_KEY) || 'null'); } catch (_) { snap = null; }
  if (!snap || !Array.isArray(snap.items) || !snap.items.length) { restoreTermOrEmpty(); return; }
  // 同步先占位 state.macId：init 同步执行到此早于任何 refreshNodes 的 fetch 回调，故能抑制
  // refreshNodes 的 `!state.macId → selectMac(MACS[0])` 自动选台。再 await 一次 refreshNodes
  // 确保 MACS 已就绪，据此校验快照里的 Mac 仍在册（改名/下架的不强行恢复）。
  state.macId = snap.macId;
  await refreshNodes();
  const known = new Set(MACS.map((m) => m.id));
  if (!known.has(snap.macId)) { if (MACS.length) selectMac(MACS[0].id); else showEmpty(); return; }
  for (const it of snap.items) {
    if (!known.has(it.macId)) continue; // 已不在册的 Mac：其会话无从 attach，跳过
    state.macId = it.macId;
    state.assistant = it.assistant === 'codex' ? 'codex' : 'claude'; // connect 用 state.assistant 起对的助手
    try { await connect(it.sessionId, it.title, it.cwd, it.permMode || 'default'); } catch (_) {}
  }
  state.macId = snap.macId;
  state.assistant = snap.cur ? (snap.cur.assistant === 'codex' ? 'codex' : 'claude') : 'codex';
  state.selectedSid = snap.cur ? snap.cur.sessionId : null; // 侧栏高亮对齐快照当前会话
  $$('[data-assistant]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.assistant === state.assistant)));
  renderHosts();
  loadSessions();
  const cur = snap.cur && poolFind(state.macId, snap.cur.sessionId, snap.cur.assistant);
  if (cur) poolShow(cur); else restoreTermOrEmpty();
}

// 移动端从终端「返回」：仅收起 push，不结束进程（tmux 持久）
function backToList() { $('#app').classList.remove('term-open'); }

// ============================================================
//  文件浏览器
// ============================================================
function loadFiles() {
  if (!state.macId) return;
  closeChatPane();
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
    const data = await api(state.macId, `projects?assistant=${state.assistant}`);
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
  state.killAssistant = state.assistant;
  $('#ck-name').textContent = title || '该会话';
  openOverlay('confirm-kill');
}
async function closeSession() {
  const sid = state.killTarget;
  const assistant = state.killAssistant || state.assistant;
  closeOverlay('confirm-kill');
  if (!sid) return;
  try {
    const r = await api(state.macId, 'close', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant, sessionId: sid }),
    });
    toast(r.killed ? '已终止该会话进程（会话保留）' : '该会话没有正在运行的控制台进程', r.killed ? 'ok' : 'info');
    // 终止后把该会话从池里移除（进程已结束，留着 iframe 只会停在 [exited]/Press ⏎ to Reconnect）。
    const ent = poolFind(state.macId, sid, assistant);
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
// 代理默认值：未配置时直接填进输入框作为真实值（不靠 placeholder，避免"看着像填了其实是空"的陷阱）
const DEFAULT_PROXY = 'http://127.0.0.1:7897';
function setPingChip(text, tone = '') {
  const el = $('#hm-ping');
  if (!el) return;
  el.textContent = text;
  el.className = 'ping-chip' + (tone ? ' ' + tone : '');
}
async function pingHost(id) {
  if (!id) return;
  setPingChip('...', 'pending');
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), 5000);
  const t0 = performance.now();
  try {
    const r = await fetch(`${apiBase(id)}/api/health?probe=${Date.now()}`, {
      cache: 'no-store',
      signal: ctrl.signal,
    });
    const ms = Math.max(1, Math.round(performance.now() - t0));
    clearTimeout(timer);
    if (!r.ok) { setPingChip('失败', 'bad'); return; }
    setPingChip(ms + 'ms', ms <= 80 ? 'good' : (ms <= 200 ? 'warn' : 'bad'));
  } catch (_) {
    clearTimeout(timer);
    setPingChip('超时', 'bad');
  }
}
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
  setPingChip('...', 'pending');
  $('#hm-http').value = ''; $('#hm-https').value = ''; $('#hm-proxy-on').checked = false;
  closeMenus();
  openOverlay('host-modal');
  try {
    const info = await api(id, 'info');
    $('#hm-ip').textContent = info.meshIP || '—';
    pingHost(id);
    const p = info.proxy || {};
    $('#hm-http').value = p.http || DEFAULT_PROXY;
    $('#hm-https').value = p.https || DEFAULT_PROXY;
    $('#hm-proxy-on').checked = !!p.enabled;
  } catch (e) { $('#hm-ip').textContent = '连不上（' + e.message + '）'; setPingChip('失败', 'bad'); }
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
    if (e.key === 'Enter' && !e.shiftKey && !isIMEComposing(e, mobileIMEComposing)) { e.preventDefault(); $('#send-btn').click(); }
  });
  $('#cmd-input').addEventListener('compositionstart', () => { mobileIMEComposing = true; });
  $('#cmd-input').addEventListener('compositionend', () => { mobileIMEComposing = false; });
}

// ============================================================
//  初始化
// ============================================================
function init() {
  initTheme();
  initExperimentFlags();
  renderHosts();
  refreshNames();
  refreshSettings();
  refreshNodes(); setInterval(refreshNodes, 30000);
  setInterval(refreshSessionsSoft, 5000); // 会话列表轻量轮询：棕点(waiting)在用户答复 / 授权后自动退出（函数自带 mode/macId guard）
  wireMobileInput();

  // 模式 / 范围 / 刷新 / 新建
  // 注意 button[data-mode]：#app 本身带 data-mode（CSS 切栅格用），裸 [data-mode] 会把 #app 也选中，
  // 给根容器挂上 onclick → 点页面任意处都冒泡触发 setMode→loadSessions（每次点击闪一下）。
  $$('button[data-mode]').forEach((b) => b.onclick = () => setMode(b.dataset.mode));
  $$('[data-assistant]').forEach((b) => b.onclick = () => setAssistant(b.dataset.assistant));
  $$('[data-scope]').forEach((b) => b.onclick = () => {
    state.scope = b.dataset.scope;
    $$('[data-scope]').forEach((x) => x.setAttribute('aria-selected', String(x === b)));
    loadSessions({ clear: true });
  });
  $('#refresh-btn').onclick = () => { loadSessions(); refreshHostCounts(); };
  $('#new-session').onclick = showProjects;

  // 终端窗口
  $('#win-back').onclick = backToList;
  $('#reload-btn').onclick = doReload;
  $('#reload-dismiss').onclick = hideBanner;
  $('#reconnect-btn').onclick = () => { const f = curFrame(); try { f.contentWindow.location.reload(); } catch (_) { f.src = f.src; } };
  $('#fullscreen-btn').onclick = () => $('.win-body').requestFullscreen?.();
  $('#chat-composer').onsubmit = (e) => {
    e.preventDefault();
    if ($('#chat-send').dataset.action === 'interrupt') interruptChat();
    else submitChatInput();
  };
  $('#chat-attach').onclick = () => $('#chat-file').click();
  $('#chat-file').addEventListener('change', (e) => { addChatFiles(e.target.files); e.target.value = ''; });
  $('#chat-input').addEventListener('input', () => { saveChatDraft(); resizeChatInput(); updateChatComposerState(); });
  $('#chat-input').addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !isIMEComposing(e, chatIMEComposing)) { e.preventDefault(); submitChatInput(); }
  });
  $('#chat-input').addEventListener('paste', (e) => {
    const files = [...(e.clipboardData?.files || [])].filter((f) => String(f.type || '').startsWith('image/'));
    if (files.length) { e.preventDefault(); addChatFiles(files); }
  });
  $('#chat-input').addEventListener('compositionstart', () => { chatIMEComposing = true; });
  $('#chat-input').addEventListener('compositionend', () => { chatIMEComposing = false; });
  $('#chat-scroll').addEventListener('scroll', () => {
    $('#chat-jump').hidden = chatAtBottom();
    syncChatTurnPin();
    if ($('#chat-scroll').scrollTop <= 80) loadOlderChatHistory();
  });
  $('#chat-jump').onclick = () => { const sc = $('#chat-scroll'); sc.scrollTop = sc.scrollHeight; $('#chat-jump').hidden = true; };
  $('#chat-approval').addEventListener('change', (e) => {
    if (!state.chat) return;
    state.chat.approvalMode = e.target.value;
    state.chat.approvalDirty = true;
  });
  $('#chat-options-trigger').addEventListener('click', (e) => {
    e.stopPropagation();
    toggleChatOptions();
  });
  $$('[data-chat-options-panel]').forEach((row) => {
    row.addEventListener('click', (e) => {
      e.stopPropagation();
      openChatOptionsPanel(row.dataset.chatOptionsPanel);
    });
    row.addEventListener('mouseenter', () => {
      if (matchMedia('(hover: hover)').matches && !row.disabled) openChatOptionsPanel(row.dataset.chatOptionsPanel);
    });
  });
  $('#chat-options-back').addEventListener('click', (e) => { e.stopPropagation(); showChatOptionsMain({ focus: true }); });

  // 用户菜单（主题切换已收进菜单内 data-act="theme"，不再单独占一行）
  $('#user-btn').onclick = (e) => toggleMenu('usermenu', e);
  $('#m-menu-btn').onclick = (e) => toggleMenu('m-menu', e);
  $$('#usermenu button, #m-menu button').forEach((b) => {
    if (!b.dataset.act) return;
    b.onclick = () => {
      closeMenus();
      if (b.dataset.act === 'theme') toggleTheme();
      else if (b.dataset.act === 'settings') openSettings();
      else if (b.dataset.act === 'selfdraw') toggleSelfDraw();
      else if (b.dataset.act === 'logout') doLogout();
    };
  });
  $('#st-save').onclick = saveSettings;
  $$('[data-settings-tab]').forEach((b) => { b.onclick = () => showSettingsTab(b.dataset.settingsTab); });
  $('#m-info-btn').onclick = () => { if (state.macId) openHostModal(state.macId); };

  // 弹窗 / 抽屉
  $$('[data-close]').forEach((b) => b.onclick = () => closeOverlay(b.dataset.close));
  $$('.overlay').forEach((o) => o.addEventListener('click', (e) => { if (e.target === o) closeOverlay(o.id); }));
  $('#hm-save').onclick = saveHost;
  $('#hm-ping').onclick = () => pingHost(state.hostModalMac);
  $('#ck-confirm').onclick = closeSession;

  // 点空白处关菜单
  document.addEventListener('click', (e) => {
    if (!e.target.closest('.menu') && !e.target.closest('#user-btn') && !e.target.closest('#m-menu-btn')) closeMenus();
    if (!e.target.closest('#chat-options')) closeChatOptions();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || $('#chat-options-popover').hidden) return;
    closeChatOptions();
    $('#chat-options-trigger').focus();
  });
  // 跨断点时同步移动输入坞可见性
  addEventListener('resize', () => {
    if (state.mode === 'sessions' && state.termSid) $('#mobile-input').hidden = !isMobile();
    syncChatTurnPin();
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

  setMode('sessions');
  restorePoolSnapshot();

  if ('serviceWorker' in navigator) navigator.serviceWorker.register(`${BASE}/sw.js`).catch(() => {});
}
document.addEventListener('DOMContentLoaded', init);
