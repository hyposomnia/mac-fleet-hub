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
let fileLoadSeq = 0;    // 文件目录请求序号：切设备/目录时丢弃旧响应
let fileColumnLoadSeq = 0; // 分栏子目录请求序号：切列/视图时丢弃旧响应
let sessionSearchTimer = null;

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
  sessionMacId: 'all', // 会话列表设备范围：all | mN；终端 / 自绘会话仍使用具体 macId
  fileMacId: null,     // 文件始终绑定一台具体设备
  mode: 'sessions',      // sessions | files
  assistant: 'codex',    // claude | codex
  scope: 'active',       // active | all
  sessionView: 'project', // project | recent
  termSid: null,         // 当前终端 tmux 会话名（watch / reload 用）
  termUrl: null,         // 当前终端 iframe URL（files↔sessions 切换后恢复用）
  termSessionId: null,   // 当前终端对应的 sessionId（判断「进入连接」是否就是当前终端）
  selectedSid: null,     // 当前选中的 sessionId（高亮 + 展开按钮）
  curTitle: '',          // 当前终端标题
  curCwd: '',            // 当前终端会话目录（用于头部 meta）
  curMode: 'default',    // 当前终端的权限模式：default | bypass | auto
  killTarget: null,      // 待终止的 sessionId（二次确认用）
  killAssistant: null,   // 待终止会话所属助手
  killMacId: null,       // 待终止会话所在设备
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
  sessionSearch: '',
  sessionResults: [],
  sessionCursors: {},    // macId -> Codex nextCursor
  sessionErrors: {},
  sessionsLoadingMore: false,
  sessionsSoftLoading: false,
  selectedSessionMacId: null,
  selectedSessionAssistant: null,
  filePath: '',
  fileRoot: '',
  fileParent: '',
  fileEntries: [],
  fileLocations: [],
  filePaths: {},
  fileSearch: '',
  fileShowHidden: false,
  fileView: 'list',     // icons | list | columns
  fileColumns: [],
  fileSelectedPath: '',
  filePreviewPath: '',
  filePreviewDismissing: false,
  fileLoading: false,
  fileNameMode: '',
  fileTarget: null,
  fileDeleteTarget: null,
  devicePickerContext: 'sessions',
  deferredInstallPrompt: null,
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
    let code = '';
    try {
      const j = await r.json();
      if (j && j.message) msg = j.message;
      if (j && typeof j.error === 'string') code = j.error;
    } catch (_) {}
    const err = new Error(msg);
    err.code = code;
    err.status = r.status;
    throw err;
  }
  return r.json();
}

const SELF_DRAW_KEY = 'fleet-experiment-selfdraw';
const SESSION_ARCHIVE_KEY = 'fleet-show-archived-sessions';
const UI_STATE_KEY = 'fleet-ui-state-v1';
let chatIMEComposing = false;
let mobileIMEComposing = false;

function normalizeFileView(view) {
  return ['icons', 'list', 'columns'].includes(view) ? view : 'list';
}

function initUIState() {
  let saved = {};
  try { saved = JSON.parse(localStorage.getItem(UI_STATE_KEY) || '{}') || {}; } catch (_) {}
  const routeMode = new URLSearchParams(location.search).get('mode');
  state.mode = routeMode === 'files' || routeMode === 'sessions'
    ? routeMode
    : (saved.mode === 'files' ? 'files' : 'sessions');
  state.assistant = saved.assistant === 'claude' ? 'claude' : 'codex';
  state.sessionMacId = saved.sessionMacId === 'all' || /^m\d+$/.test(saved.sessionMacId || '')
    ? saved.sessionMacId
    : 'all';
  state.fileMacId = /^m\d+$/.test(saved.fileMacId || '') ? saved.fileMacId : null;
  state.sessionView = saved.sessionView === 'recent' ? 'recent' : 'project';
  state.filePaths = saved.filePaths && typeof saved.filePaths === 'object' ? saved.filePaths : {};
  state.fileShowHidden = saved.fileShowHidden === true;
  state.fileView = normalizeFileView(saved.fileView);
}

function persistUIState() {
  try {
    localStorage.setItem(UI_STATE_KEY, JSON.stringify({
      mode: state.mode,
      assistant: state.assistant,
      sessionMacId: state.sessionMacId,
      fileMacId: state.fileMacId,
      sessionView: state.sessionView,
      filePaths: state.filePaths,
      fileShowHidden: state.fileShowHidden,
      fileView: state.fileView,
    }));
  } catch (_) {}
}
function initSessionListPreferences() {
  try { state.scope = localStorage.getItem(SESSION_ARCHIVE_KEY) === '1' ? 'all' : 'active'; }
  catch (_) { state.scope = 'active'; }
}
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
//  设备范围（桌面侧栏 / 移动端选择器）
// ============================================================
function renderHosts() {
  const nav = $('#host-list'); clear(nav);
  nav.append(h('div', { class: 'hd eyebrow', text: state.mode === 'files' ? '文件所在设备' : '会话设备' }));
  const chips = $('#host-chips'); clear(chips);
  if (!MACS.length) { nav.append(h('div', { class: 'empty', text: '暂无已入网的 Mac' })); return; }
  const selected = state.mode === 'files' ? state.fileMacId : state.sessionMacId;
  if (state.mode === 'sessions') {
    const onlineCount = MACS.filter((m) => state.nodes[m.id]).length;
    const all = h('button', { class: 'host host-all', dataset: { mac: 'all' }, 'aria-current': String(selected === 'all') },
      h('span', { class: 'host-stack' }, svgIcon('ic', 'm12 3-8 4 8 4 8-4-8-4ZM4 12l8 4 8-4M4 17l8 4 8-4')),
      h('span', { class: 'nm', text: '全部设备' }),
      h('span', { class: 'ct', text: `${onlineCount}/${MACS.length} 在线` }),
    );
    all.onclick = () => setSessionDevice('all');
    nav.append(all);
  }
  for (const m of MACS) {
    const online = state.nodes[m.id];
    // 桌面行
    const info = h('span', { class: 'i', title: '设置 / 代理', text: 'ⓘ' });
    info.onclick = (e) => { e.stopPropagation(); openHostModal(m.id); };
    const row = h('button', { class: 'host', dataset: { mac: m.id }, 'aria-current': String(m.id === selected) },
      h('span', { class: 'dot ' + (online ? 'on' : 'off') }),
      h('span', { class: 'nm', text: macName(m.id) }),
      // 会话数不再显示；仅离线时标「离线」（在线/离线 dot 已在前面）
      h('span', { class: 'ct', text: online ? '' : '离线' }),
      info,
    );
    row.onclick = () => selectMac(m.id);
    nav.append(row);
  }
  updateDeviceScopeUI();
}

function selectMac(id) {
  if (state.mode === 'files') setFileDevice(id);
  else setSessionDevice(id);
}

function activateConcreteMac(id) {
  if (!/^m\d+$/.test(id || '')) return false;
  state.macId = id;
  renderHosts();
  return true;
}

function setSessionDevice(id) {
  if (id !== 'all' && !MACS.some((m) => m.id === id)) return;
  if (state.sessionMacId === id) { closeOverlay('device-modal'); return; }
  state.sessionMacId = id;
  if (id !== 'all') state.macId = id;
  $('#app').classList.remove('term-open');
  state.selectedSid = null;
  state.selectedSessionMacId = null;
  state.sessionResults = [];
  state.sessionCursors = {};
  renderHosts();
  updateDeviceScopeUI();
  persistUIState();
  closeMenus();
  closeOverlay('device-modal');
  loadSessions({ clear: true });
  showEmpty();
}

function setFileDevice(id) {
  if (!MACS.some((m) => m.id === id)) return;
  const changed = state.fileMacId !== id;
  const hadPreviewHistory = changed && !!history.state?.fleet && !!history.state.filePreviewPath;
  state.fileMacId = id;
  state.macId = id;
  state.filePath = state.filePaths[id] || '';
  state.fileSelectedPath = '';
  if (changed) {
    fileColumnLoadSeq++;
    state.fileColumns = [];
    closeFilePreview();
  }
  renderHosts();
  updateDeviceScopeUI();
  persistUIState();
  if (hadPreviewHistory) replaceFleetHistory({ fileMacId: id, filePath: state.filePath, filePreviewPath: '' });
  closeMenus();
  closeOverlay('device-modal');
  if (state.mode === 'files') loadFiles({ clear: changed });
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
    const previousIDs = MACS.map((m) => m.id).join(',');
    const previousOnline = JSON.stringify(state.nodes);
    MACS = ids.map((id) => ({ id }));
    state.nodes = online;
    const rosterChanged = previousIDs !== ids.join(',') || previousOnline !== JSON.stringify(online);
    const known = new Set(ids);
    if (state.sessionMacId !== 'all' && !known.has(state.sessionMacId)) state.sessionMacId = 'all';
    if (!known.has(state.fileMacId)) state.fileMacId = MACS.find((m) => online[m.id])?.id || MACS[0]?.id || null;
    if (!known.has(state.macId)) state.macId = state.fileMacId;
    renderHosts();
    updateDeviceScopeUI();
    if (state.mode === 'sessions' && (rosterChanged || !state.sessionResults.length)) loadSessions();
    else if (state.mode === 'files' && state.fileMacId && !state.fileEntries.length) loadFiles();
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
  $('#st-show-archived').checked = state.scope === 'all';
  renderChatCacheStats();
  showSettingsTab('sessions');
  openOverlay('settings-modal');
}
async function saveSettings() {
  const nextScope = $('#st-show-archived').checked ? 'all' : 'active';
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
    const scopeChanged = state.scope !== nextScope;
    state.scope = nextScope;
    try { localStorage.setItem(SESSION_ARCHIVE_KEY, nextScope === 'all' ? '1' : '0'); } catch (_) {}
    closeOverlay('settings-modal');
    toast('设置已保存', 'ok');
    poolEvict();              // 上限调小 → 立即按新上限释放多余窗口
    evictChatCache();         // 自绘缓存上限调小 → 立即释放最久未看的连接
    applyScrollbackToPool();  // 回滚行数即时作用到已开终端
    if (scopeChanged) {
      state.sessionResults = [];
      state.sessionCursors = {};
      updateSessionFilterUI();
      loadSessions({ clear: true });
    }
  } catch (e) { toast('保存失败：' + e.message, 'err'); }
}

function showSettingsTab(tab) {
  const key = tab === 'chat' || tab === 'sessions' ? tab : 'terminal';
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
  state.mode = mode === 'files' ? 'files' : 'sessions';
  mode = state.mode;
  $('#app').dataset.mode = mode;
  if (mode !== 'sessions') $('#app').classList.remove('term-open'); // 离开会话模式收起终端 push
  $$('button[data-mode]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.mode === mode)));
  renderHosts();
  updateDeviceScopeUI();
  persistUIState();
  if (mode === 'files') {
    $('#file-browser').hidden = false;
    loadFiles();
  } else {
    $('#file-browser').hidden = true;
    loadSessions();
    restoreTermOrEmpty();
  }
}

function setAssistant(assistant) {
  state.assistant = assistant === 'codex' ? 'codex' : 'claude';
  state.selectedSid = null;
  state.selectedSessionMacId = null;
  state.sessionSearch = '';
  state.sessionResults = [];
  state.sessionCursors = {};
  closeChatPane();
  $$('[data-assistant]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.assistant === state.assistant)));
  updateSessionFilterUI();
  persistUIState();
  loadSessions({ clear: true });
  refreshHostCounts();
}

// ============================================================
//  会话列表
// ============================================================
function setSessionsLoading(on) {
  const wrap = $('#session-groups');
  if (wrap) wrap.setAttribute('aria-busy', String(on));
  const head = $('.sc-head');
  if (head) head.classList.toggle('is-loading', on);
}

function renderSessionSkeleton(wrap) {
  clear(wrap);
  for (let i = 0; i < 3; i++) wrap.append(h('div', { class: 'skel-ses' }, h('div', { class: 'skel l1' }), h('div', { class: 'skel l2' })));
}

function updateSessionFilterUI() {
  const input = $('#session-search');
  if (input && input.value !== state.sessionSearch) input.value = state.sessionSearch;
  if (input) input.placeholder = state.scope === 'all' ? '搜索已归档会话' : '搜索全部未归档会话';
  const toggle = $('#session-view-toggle');
  const recent = state.sessionView === 'recent';
  if (toggle) {
    toggle.setAttribute('aria-pressed', String(recent));
    toggle.title = recent ? '按项目分组' : '按最近更新排序';
    toggle.setAttribute('aria-label', toggle.title);
  }
  const label = $('#session-view-label');
  if (label) label.textContent = recent ? '按最近更新' : '按项目分组';
}

function sessionKey(session) {
  return `${session?.macId || ''}\n${session?.assistant || state.assistant}\n${session?.sessionId || ''}`;
}

function sessionTargetMacs({ append = false } = {}) {
  let ids;
  if (state.sessionMacId === 'all') {
    ids = MACS.filter((m) => state.nodes[m.id]).map((m) => m.id);
  } else {
    ids = MACS.some((m) => m.id === state.sessionMacId) ? [state.sessionMacId] : [];
  }
  if (append) ids = ids.filter((id) => !!state.sessionCursors[id]);
  return ids;
}

function sessionHasMore() {
  return state.assistant === 'codex' && Object.values(state.sessionCursors).some(Boolean);
}

function sessionQuery(macId, { cursor = '', soft = false } = {}) {
  const query = new URLSearchParams({ assistant: state.assistant });
  if (state.assistant === 'codex') {
    query.set('archived', String(state.scope === 'all'));
    query.set('limit', soft ? '100' : '50');
    if (state.sessionSearch) query.set('search', state.sessionSearch);
    if (cursor) query.set('cursor', cursor);
  } else {
    query.set('scope', state.scope === 'all' ? 'all' : 'active');
  }
  return api(macId, `sessions?${query.toString()}`);
}

function normalizeDeviceSessions(macId, data) {
  let sessions = (data?.sessions || []).map((session) => ({
    ...session,
    macId,
    assistant: session.assistant || state.assistant,
  }));
  if (state.assistant === 'claude') {
    if (state.scope === 'all') sessions = sessions.filter((session) => !session.live);
    if (state.sessionSearch) {
      const needle = state.sessionSearch.toLocaleLowerCase();
      sessions = sessions.filter((session) =>
        `${session.title || ''}\n${session.cwd || ''}\n${macName(macId)}`.toLocaleLowerCase().includes(needle));
    }
  }
  return sessions;
}

function renderSessionResults(opts = {}) {
  const wrap = $('#session-groups');
  const previousScrollTop = opts.preserveScroll ? wrap.scrollTop : 0;
  const sessions = [...(state.sessionResults || [])].sort((a, b) =>
    (Number(b.pinned) - Number(a.pinned)) || (Number(b.mtime) - Number(a.mtime)));
  const count = $('#session-count');
  if (count) {
    const noun = state.scope === 'all' ? '个已归档' : '个未归档';
    count.textContent = `${sessions.length} ${noun}`;
  }

  clear(wrap);
  if (!sessions.length) {
    let message = state.sessionSearch ? '没有匹配的会话' : (state.scope === 'all' ? '没有已归档会话' : '没有未归档会话');
    if (state.sessionMacId === 'all' && MACS.length && !MACS.some((m) => state.nodes[m.id])) message = '设备均处于离线状态';
    wrap.append(h('div', { class: 'empty', text: message }));
  } else if (state.sessionView === 'recent') {
    wrap.append(h('div', { class: 'recent-session-list' }, ...sessions.map(sessionRow)));
  } else {
  const groups = {};
  for (const s of sessions) (groups[s.cwd] ||= []).push(s);
  const ordered = Object.entries(groups).map(([cwd, arr]) => {
    arr.sort((a, b) => (Number(b.pinned) - Number(a.pinned)) || (b.live - a.live) || (b.mtime - a.mtime));
    return { cwd, arr, pinned: arr.some((session) => session.pinned), last: Math.max(...arr.map((s) => s.mtime)) };
  }).sort((a, b) => (Number(b.pinned) - Number(a.pinned)) || (b.last - a.last));

    for (const g of ordered) {
      const collapsed = state.collapsed.has(g.cwd);
      const head = h('button', { class: 'grp-h' },
        svgIcon('chev', 'M6 9l6 6 6-6'),
        h('span', { class: 'gn', text: projName(g.cwd) }),
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
  const failed = Object.keys(state.sessionErrors || {});
  if (failed.length) {
    wrap.append(h('div', { class: 'session-partial-note', text: `${failed.map(macName).join('、')} 暂时无法连接` }));
  }
  if (opts.preserveScroll) wrap.scrollTop = previousScrollTop;
  requestAnimationFrame(maybeLoadMoreSessions);
}

function maybeLoadMoreSessions() {
  const wrap = $('#session-groups');
  if (!wrap || state.mode !== 'sessions' || !sessionHasMore() ||
      state.sessionsLoadingMore || wrap.getAttribute('aria-busy') === 'true') return;
  if (wrap.scrollHeight - wrap.scrollTop - wrap.clientHeight <= 240) loadSessions({ append: true });
}

async function loadSessions(opts = {}) {
  if (state.mode !== 'sessions' || !MACS.length) return;
  const wrap = $('#session-groups');
  const append = opts.append === true;
  if (append && (!sessionHasMore() || state.sessionsLoadingMore)) return;
  const req = ++sessionLoadSeq;
  const sessionMacId = state.sessionMacId;
  const assistant = state.assistant;
  const scope = state.scope;
  const search = state.sessionSearch;
  const targets = sessionTargetMacs({ append });
  const previousCursors = { ...state.sessionCursors };
  const stale = () => req !== sessionLoadSeq || state.mode !== 'sessions' || state.sessionMacId !== sessionMacId ||
    state.assistant !== assistant || state.scope !== scope || state.sessionSearch !== search;
  setSessionsLoading(true);
  state.sessionsLoadingMore = append;
  wrap.classList.toggle('loading-more', append);
  // 切主机/切助手/切范围时立即清空旧列表；普通刷新保留旧内容直到新数据就绪，避免闪。
  if (!append && (opts.clear || !wrap.querySelector('.grp, .empty, .skel-ses'))) renderSessionSkeleton(wrap);

  const results = await Promise.all(targets.map(async (macId) => {
    try {
      const data = await sessionQuery(macId, { cursor: append ? previousCursors[macId] : '' });
      return { macId, data };
    } catch (error) {
      return { macId, error };
    }
  }));
  if (stale()) {
    state.sessionsLoadingMore = false;
    wrap.classList.remove('loading-more');
    setSessionsLoading(false);
    return;
  }
  state.sessionsLoadingMore = false;
  wrap.classList.remove('loading-more');
  setSessionsLoading(false);

  const errors = {};
  const nextCursors = append ? { ...previousCursors } : {};
  const incoming = [];
  for (const result of results) {
    if (result.error) {
      errors[result.macId] = result.error.message;
      continue;
    }
    incoming.push(...normalizeDeviceSessions(result.macId, result.data));
    if (assistant === 'codex') nextCursors[result.macId] = result.data.nextCursor || '';
    else nextCursors[result.macId] = '';
  }
  if (!targets.length && sessionMacId !== 'all') errors[sessionMacId] = '设备不可用';
  state.sessionErrors = errors;
  state.sessionCursors = nextCursors;
  const seen = new Set((append ? state.sessionResults : []).map(sessionKey));
  const sessions = append
    ? [...state.sessionResults, ...incoming.filter((session) => {
      const key = sessionKey(session);
      if (seen.has(key)) return false;
      seen.add(key);
      return true;
    })]
    : incoming;
  state.sessionResults = sessions;
  for (const s of sessions) updateCachedChatFromSession(s.macId, s);
  for (const macId of targets) {
    const own = sessions.filter((s) => s.macId === macId);
    state.counts[macId] = scope === 'active' ? own.length : own.filter((s) => s.live).length;
  }
  renderSessionResults({ preserveScroll: append });
}

// 软刷新会话列表：定时静默拉取，只就地更新「易变字段」——waiting、Codex 运行状态、
// 相对时间与计数，不 clear 重建整列表（避免每周期把 hover tooltip、展开按钮闪断）。
// 会话集合或 pty 按钮结构发生变化时才回退全量 loadSessions。
async function refreshSessionsSoft() {
  if (state.mode !== 'sessions' || !MACS.length) return;
  if (state.sessionsLoadingMore || state.sessionsSoftLoading) return;
  const sessionMacId = state.sessionMacId;
  const assistant = state.assistant;
  const scope = state.scope;
  const search = state.sessionSearch;
  const rows = $$('#session-groups .ses');
  if (!rows.length) {
    if ($('#session-groups .empty')) loadSessions();
    return;
  }
  const targets = sessionTargetMacs();
  state.sessionsSoftLoading = true;
  const results = await Promise.all(targets.map(async (macId) => {
    try { return { macId, data: await sessionQuery(macId, { soft: true }) }; }
    catch (error) { return { macId, error }; }
  }));
  state.sessionsSoftLoading = false;
  if (state.mode !== 'sessions' || state.sessionMacId !== sessionMacId || state.assistant !== assistant ||
      state.scope !== scope || state.sessionSearch !== search) return;

  const fresh = [];
  const freshByMac = {};
  for (const result of results) {
    if (result.error) continue;
    const sessions = normalizeDeviceSessions(result.macId, result.data);
    fresh.push(...sessions);
    freshByMac[result.macId] = { sessions, nextCursor: result.data.nextCursor || '' };
  }
  const currentByKey = new Map(state.sessionResults.map((session) => [sessionKey(session), session]));
  const freshByKey = new Map(fresh.map((session) => [sessionKey(session), session]));
  const structuralChange = fresh.some((session) => {
    const current = currentByKey.get(sessionKey(session));
    return !current || !!current.pty !== !!session.pty;
  }) || targets.some((macId) => {
    const page = freshByMac[macId];
    if (!page) return false;
    const complete = assistant === 'claude' || (!state.sessionCursors[macId] && !page.nextCursor);
    return complete && state.sessionResults.some((session) =>
      session.macId === macId && !freshByKey.has(sessionKey(session)));
  });
  if (structuralChange) {
    loadSessions();
    return;
  }

  for (const session of fresh) {
    const current = currentByKey.get(sessionKey(session));
    if (current) {
      Object.assign(current, {
        live: session.live, waiting: session.waiting, status: session.status, mtime: session.mtime,
      });
    }
    updateCachedChatFromSession(session.macId, session);
  }
  for (const el of rows) {
    const session = freshByKey.get(`${el.dataset.mac}\n${el.dataset.assistant}\n${el.dataset.sid}`);
    if (!session) continue;
    const dot = el.querySelector('.dot');
    if (dot) {
      dot.classList.toggle('wait', !!session.waiting);
      dot.title = session.waiting ? '等待你的回复 / 选择' : '';
    }
    const tEl = el.querySelector('.ses-time');
    if (tEl) tEl.textContent = relTime(session.mtime);
    const status = el.querySelector('.ses-status');
    if (status) {
      const value = sessionStatus(session);
      status.textContent = value.text;
      status.className = `ses-status${value.className ? ' ' + value.className : ''}`;
    }
  }
  syncSessionRuntimeIndicators();
}

// 会话行：
// 已在池中 / 有运行中进程（行尾绿点）的会话：点行即直接进入——池内 poolShow 瞬时切换，
//   仅有进程未在池时 api open 重新 attach（tmux 复用，权限模式启动时已固定，不再让选）。
// 仅「冷会话」（无进程且未在池）点行才展开「连接 / Bypass / Auto」——那才是真正新起 Claude。
// 开了 pty 的会话另显「终止 ⏹」（与是否在池无关）。
function sessionStatus(session) {
  if (session.waiting) return { text: '等待回复', className: 'waiting' };
  if (session.pty || FleetChatModel.chatPhase(session.status) === 'running') return { text: '正在进行', className: 'running' };
  return { text: state.scope === 'all' ? '已归档' : '', className: '' };
}

function activateSession(session) {
  state.macId = session.macId;
  state.selectedSid = session.sessionId;
  state.selectedSessionMacId = session.macId;
  state.selectedSessionAssistant = session.assistant || state.assistant;
  renderHosts();
}

function sessionRow(s) {
  const sid = s.sessionId;
  const macId = s.macId;
  const assistant = s.assistant || state.assistant;
  const selfDraw = canSelfDrawChat() && assistant === 'codex';
  const inPool = !!poolFind(macId, sid, assistant);
  const chatConnected = assistant === 'codex' && isChatConnectionKept(macId, sid);
  const sessionRunning = assistant === 'codex' && isSessionRunning(s, macId);
  const live = !!s.pty; // 有运行中进程（行尾绿点）：再连只是重新 attach，不需选权限模式
  const stop = s.pty && h('span', { class: 'stopbtn', title: '终止进程（会话保留）',
    onclick: (e) => { e.stopPropagation(); termSes(sid, s.title, macId, assistant); } }, svgStop());
  const pin = assistant === 'codex' && s.pinned
    ? h('span', { class: 'ses-pin', title: '已置顶' }, svgIcon('ic', 'M12 17v5M5 3h14l-3 6v4l2 2H6l2-2V9Z'))
    : null;
  const menu = assistant === 'codex' ? renderCodexSessionMenu(s) : null;
  const top = h('div', { class: 'ses-top' },
    // 行首点位恒定留出（标题统一对齐）：默认透明占位，仅「等待你回复/选择」(s.waiting) 显棕色点
    h('span', { class: 'dot' + (s.waiting ? ' wait' : ''), title: s.waiting ? '等待你的回复 / 选择' : null }),
    h('span', { class: 't', text: s.title || '(无标题)' }),
    // 紧凑化：不再单起一行显示分支/路径，仅在同行标题后跟相对时间
    h('span', { class: 'ses-time', text: relTime(s.mtime) }),
    h('span', { class: 'session-running-status', title: '正在进行', 'aria-label': '正在进行' }),
    h('span', { class: 'chat-cache-status', title: '自绘会话保持连接', 'aria-label': '自绘会话保持连接' }),
    pin,
    stop,
    menu,
  );
  const status = sessionStatus(s);
  const meta = h('div', { class: 'ses-meta' },
    state.sessionMacId === 'all' ? h('span', { class: 'session-device-name', text: macName(macId) }) : null,
    state.sessionView === 'recent'
      ? h('span', { class: 'session-project-name', text: projName(s.cwd) })
      : null,
    h('span', { class: `ses-status${status.className ? ' ' + status.className : ''}`, text: status.text }),
  );
  // 池内 / 有进程的会话点行即直接进入，不需按钮；仅冷会话才展开三种权限模式。
  const acts = (selfDraw || inPool || live) ? null : h('div', { class: 'ses-acts' },
    h('button', { class: 'btn sm accent', title: '普通连接（逐项确认工具权限）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'default', macId, assistant); } },
      h('span', { class: 'gi', text: '→' }), '连接'),
    h('button', { class: 'btn sm danger', title: assistant === 'codex' ? 'codex --dangerously-bypass-approvals-and-sandbox' : 'claude --dangerously-skip-permissions（跳过全部工具权限确认）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'bypass', macId, assistant); } }, 'Bypass'),
    h('button', { class: 'btn sm warn', title: assistant === 'codex' ? 'codex --ask-for-approval never --sandbox workspace-write（自动批准 + 工作区可写沙箱）' : 'claude --permission-mode auto（自动批准 + 后台安全分类器）',
      onclick: (e) => { e.stopPropagation(); connect(sid, s.title, s.cwd, 'auto', macId, assistant); } }, 'Auto'));
  const selected = sid === state.selectedSid && macId === state.selectedSessionMacId &&
    assistant === (state.selectedSessionAssistant || state.assistant);
  const row = h('div', {
    class: 'ses' + (s.pty ? ' conn' : '') + (sessionRunning ? ' session-running' : '') +
      (chatConnected ? ' chat-connected' : '') + (selected ? ' sel' : ''),
    dataset: { sid, mac: macId, assistant },
  }, top, meta, acts);
  // 池内 → poolShow 瞬时切换；有进程未在池 → 直接重新 attach；冷会话 → 仅高亮 + 展开三按钮。
  row.onclick = () => {
    activateSession(s);
    if (selfDraw) { openChatSession(s); return; }
    if (!inPool && live) { connect(sid, s.title, s.cwd, 'default', macId, assistant); return; }
    selectSes(sid, macId, assistant);
  };
  return row;
}

function renderCodexSessionMenu(session) {
  const archived = state.scope === 'all';
  const menu = h('div', { class: 'ses-menu-wrap' },
    h('button', { type: 'button', class: 'iconbtn bare ses-menu-trigger', title: '会话操作', 'aria-label': '会话操作',
      onclick: (event) => {
        event.stopPropagation();
        const panel = $('.ses-menu', event.currentTarget.parentElement);
        const opening = panel.hidden;
        $$('.ses-menu').forEach((other) => { other.hidden = true; });
        panel.hidden = !opening;
      } },
      svgIconParts('ic', [
        { tag: 'circle', attrs: { cx: '5', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
        { tag: 'circle', attrs: { cx: '12', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
        { tag: 'circle', attrs: { cx: '19', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
      ])),
    h('div', { class: 'ses-menu', hidden: '' },
      h('button', { type: 'button', onclick: (event) => { event.stopPropagation(); mutateCodexSession(session, session.pinned ? 'unpin' : 'pin'); } },
        session.pinned ? '取消置顶' : '置顶'),
      h('button', { type: 'button', onclick: (event) => { event.stopPropagation(); renameCodexSession(session); } }, '重命名'),
      h('button', { type: 'button', onclick: (event) => { event.stopPropagation(); mutateCodexSession(session, archived ? 'unarchive' : 'archive'); } },
        archived ? '移回当前' : '归档'),
      h('button', { type: 'button', class: 'danger', onclick: (event) => { event.stopPropagation(); deleteCodexSession(session); } }, '删除')));
  return menu;
}

async function mutateCodexSession(session, action, value = '') {
  try {
    await api(session.macId, 'sessions/action', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: session.sessionId, action, value }),
    });
    if (action === 'pin' || action === 'unpin') {
      session.pinned = action === 'pin';
      renderSessionResults();
    } else {
      await loadSessions();
    }
  } catch (error) {
    toast('会话操作失败：' + error.message, 'err');
  }
}

function renameCodexSession(session) {
  const name = window.prompt('重命名会话', session.title || '');
  if (name == null || !name.trim() || name.trim() === session.title) return;
  mutateCodexSession(session, 'rename', name.trim());
}

function deleteCodexSession(session) {
  if (!window.confirm(`永久删除“${session.title || '这个会话'}”？此操作无法撤销。`)) return;
  if (state.chat?.sessionId === session.sessionId && state.chat?.macId === session.macId) closeChatPane();
  mutateCodexSession(session, 'delete');
}

function selectSes(sid, macId = state.macId, assistant = state.assistant) {
  state.macId = macId;
  state.selectedSid = sid;
  state.selectedSessionMacId = macId;
  state.selectedSessionAssistant = assistant;
  $$('.ses').forEach((el) => el.classList.toggle('sel',
    el.dataset.sid === sid && el.dataset.mac === macId && el.dataset.assistant === assistant));
  // 已在池中的会话：选中即瞬时切换，不必再点「进入连接」
  const e = poolFind(macId, sid, assistant);
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
  if (isMobile()) {
    if (!$('#app').classList.contains('term-open')) pushFleetHistory({ mode: 'sessions', term: true });
    $('#app').classList.add('term-open');
  }
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
async function connect(sessionId, title, cwd, mode, macId = state.macId, assistant = state.assistant) {
  mode = mode || 'default';
  if (!macId) return;
  state.macId = macId;
  selectSes(sessionId, macId, assistant); // 已在池则瞬时切过去；这里再确保权限模式一致
  const exist = poolFind(macId, sessionId, assistant);
  if (exist && exist.permMode === mode) { poolShow(exist); return; } // 池内同模式：瞬时切回，不重连
  if (exist) poolDrop(exist); // 权限模式变了 → 丢弃旧窗口按新模式重开
  try {
    const r = await api(macId, 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant, sessionId, mode }),
    });
    state.selectedSid = sessionId;
    state.selectedSessionMacId = macId;
    state.selectedSessionAssistant = assistant;
    poolAdd(macId, assistant, sessionId, r.sid, r.url, title || '会话', cwd, r.mode || mode);
    loadSessions(); // 刷新 pty 标记：该会话现在有进程 → 行变「进入连接」+ 显示 ⏹（无骨架闪）
  } catch (e) { toast('连接失败：' + e.message, 'err'); }
}

function newSessionIn(cwd) {
  closeOverlay('projects-modal');
  if (canSelfDrawChat()) {
    api(state.macId, 'chat/start', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', cwd, mode: 'default' }),
    }).then((r) => {
      openChatSession({
        assistant: 'codex',
        macId: state.macId,
        sessionId: r.sessionId,
        cwd: r.cwd || cwd,
        title: '新Codex会话 · ' + projName(r.cwd || cwd),
        mtime: Date.now(),
        fresh: true,
        startOptions: r,
      });
    }).catch((e) => toast('新建失败：' + e.message, 'err'));
    return;
  }
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

function normalizeChatDraft(value) {
  if (typeof value !== 'string' || value === 'null' || value === 'undefined') return '';
  return value;
}

function chatSkillTriggerAt(value, caret) {
  if (typeof value !== 'string' || !Number.isInteger(caret) || caret < 0 || caret > value.length) return null;
  const before = value.slice(0, caret);
  const match = before.match(/(^|\s)([$/])([A-Za-z0-9_.:-]*)$/);
  if (!match) return null;
  const tokenLength = match[2].length + match[3].length;
  return { start: caret - tokenLength, end: caret, marker: match[2], query: match[3] };
}

function chatSkillTokenNames(value) {
  return Array.from(String(value || '').matchAll(/(?<!\S)[$/]([A-Za-z0-9_.:-]+)(?=$|\s)/g), (match) => match[1]);
}

function parseChatSkillInput(value, available, preferredIDs = {}) {
  const byName = new Map();
  for (const skill of available || []) {
    if (!skill?.name) continue;
    const current = byName.get(skill.name);
    if (!current || (preferredIDs?.[skill.name] && preferredIDs[skill.name] === skill.id)) {
      byName.set(skill.name, skill);
    }
  }
  const skills = [];
  const seen = new Set();
  const text = String(value || '').replace(/(?<!\S)([$/])([A-Za-z0-9_.:-]+)(?=$|\s)[ \t]?/g, (token, marker, name) => {
    const skill = byName.get(name);
    if (!skill) return token;
    const key = skill.id || skill.name;
    if (!seen.has(key)) {
      skills.push(skill);
      seen.add(key);
    }
    return '';
  }).trim();
  return { text, skills };
}

async function loadChatSkills(chat) {
  const cwd = chat?.cwd || '';
  if (chat && chat.skillsCwd !== cwd) {
    chat.skills = [];
    chat.skillsLoaded = false;
    chat.skillsError = '';
    chat.skillsCwd = cwd;
  }
  if (!chat || chat.skillsLoaded) return chat?.skills || [];
  if (chat.skillsPromise) return chat.skillsPromise;
  const request = api(chat.macId, 'chat/skills', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ assistant: 'codex', cwd }),
  });
  const task = request.then((response) => {
    if (state.chatCache.get(chat.cacheKey) !== chat) return [];
    chat.skills = Array.isArray(response?.skills) ? response.skills.filter((skill) => skill?.id && skill?.name) : [];
    chat.skillsLoaded = true;
    chat.skillsError = '';
    if (state.chat === chat) updateChatSkillMenu();
    return chat.skills;
  }).catch((error) => {
    if (state.chatCache.get(chat.cacheKey) === chat) {
      chat.skillsLoaded = false;
      chat.skillsError = error?.message || 'Skill 列表加载失败';
      if (state.chat === chat) closeChatSkillMenu();
    }
    throw error;
  }).finally(() => {
    if (chat.skillsPromise === task) chat.skillsPromise = null;
  });
  chat.skillsPromise = task;
  return task;
}

function closeChatSkillMenu() {
  const menu = $('#chat-skill-menu');
  if (menu) {
    menu.hidden = true;
    clear(menu);
  }
  if (state.chat) state.chat.skillMenu = null;
}

function renderChatSkillMenu(chat) {
  const menu = $('#chat-skill-menu');
  if (!menu || !chat?.skillMenu) return;
  clear(menu);
  const { items, index, marker } = chat.skillMenu;
  items.forEach((skill, itemIndex) => {
    menu.append(h('button', {
      type: 'button', class: 'chat-skill-option' + (itemIndex === index ? ' active' : ''),
      role: 'option', 'aria-selected': itemIndex === index ? 'true' : 'false',
      onmousedown: (event) => event.preventDefault(),
      onclick: () => selectChatSkill(itemIndex),
    },
    h('span', { class: 'chat-skill-name', text: `${marker}${skill.name}` }),
    h('span', { class: 'chat-skill-description', text: skill.description || '' })));
  });
  menu.hidden = items.length === 0;
  menu.querySelector?.('.chat-skill-option.active')?.scrollIntoView?.({ block: 'nearest' });
}

function updateChatSkillMenu() {
  const chat = state.chat;
  const input = $('#chat-input');
  if (!chat || !input) return;
  const trigger = chatSkillTriggerAt(input.value, input.selectionStart);
  if (!trigger) { closeChatSkillMenu(); return; }
  if (!chat.skillsLoaded) {
    loadChatSkills(chat).catch(() => {});
    closeChatSkillMenu();
    return;
  }
  const query = trigger.query.toLowerCase();
  const items = chat.skills.filter((skill) => skill.name.toLowerCase().includes(query)).slice(0, 12);
  if (!items.length) { closeChatSkillMenu(); return; }
  const previousID = chat.skillMenu?.items?.[chat.skillMenu.index]?.id;
  const previousIndex = items.findIndex((skill) => skill.id === previousID);
  chat.skillMenu = { ...trigger, items, index: previousIndex >= 0 ? previousIndex : 0 };
  renderChatSkillMenu(chat);
}

function selectChatSkill(index = state.chat?.skillMenu?.index || 0) {
  const chat = state.chat;
  const input = $('#chat-input');
  const menu = chat?.skillMenu;
  const skill = menu?.items?.[index];
  if (!input || !skill) return;
  const token = `${menu.marker}${skill.name} `;
  input.value = input.value.slice(0, menu.start) + token + input.value.slice(menu.end);
  const caret = menu.start + token.length;
  input.setSelectionRange(caret, caret);
  chat.draft = input.value;
  chat.skillPreferences = chat.skillPreferences || {};
  chat.skillPreferences[skill.name] = skill.id;
  closeChatSkillMenu();
  resizeChatInput();
  updateChatComposerState();
  input.focus();
}

function saveChatDraft(chat = state.chat) {
  const input = $('#chat-input');
  if (chat && input) chat.draft = normalizeChatDraft(input.value);
}

function isChatRunning(chat) {
  return !!chat && (chat.model?.phase === 'running' || chat.submitting === true);
}

function enqueueChatFollowup(chat, text, images, id, skills, displayText) {
  if (!chat) return null;
  chat.followups = chat.followups || [];
  const item = {
    id: id || `follow-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    text: typeof text === 'string' ? text : '',
    displayText: typeof displayText === 'string' ? displayText : '',
    images: Array.isArray(images) ? images.map((image) => ({ ...image })) : [],
    skills: Array.isArray(skills) ? skills.map((skill) => ({ id: skill.id, name: skill.name })) : [],
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

function acknowledgeChatFollowups(chat, events) {
  let changed = false;
  for (const ev of events || []) {
    const id = FleetChatModel.followupAckId(ev);
    if (id && removeChatFollowup(chat, id)) changed = true;
  }
  return changed;
}

function disposeChat(chat) {
  if (!chat) return;
  if (chat.events) { try { chat.events.close(); } catch (_) {} }
  chat.events = null;
  if (chat.objectUrls) for (const u of chat.objectUrls) { try { URL.revokeObjectURL(u); } catch (_) {} }
  chat.objectUrls = [];
  syncSessionRuntimeIndicators();
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
  syncSessionRuntimeIndicators();
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

function isSessionRunning(session, macId = state.macId) {
  const chat = session?.sessionId ? state.chatCache.get(chatCacheKey(macId, session.sessionId)) : null;
  // 已恢复过的自绘会话有实时 turn 状态，优先于列表轮询的旧快照。
  if (chat && (chat.historyReady || chat.submitting)) return isChatRunning(chat);
  return FleetChatModel.chatPhase(session?.status) === 'running';
}

function syncSessionRuntimeIndicators() {
  if (state.assistant !== 'codex') return;
  const sessions = new Map(state.sessionResults.map((session) => [sessionKey(session), session]));
  $$('#session-groups .ses').forEach((row) => {
    const key = `${row.dataset.mac}\n${row.dataset.assistant}\n${row.dataset.sid}`;
    row.classList.toggle('chat-connected', isChatConnectionKept(row.dataset.mac, row.dataset.sid));
    row.classList.toggle('session-running', isSessionRunning(sessions.get(key), row.dataset.mac));
  });
}

function closeChatPane({ dispose = false } = {}) {
  const chat = state.chat;
  saveChatDraft(chat);
  closeChatSkillMenu();
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
  tt.append(h('span', { class: 'dot live' }), h('span', { class: 'ttl', text: title || 'Codex 会话' }));
  $('#win-meta').textContent = '';
  if (isMobile()) {
    if (!$('#app').classList.contains('term-open')) pushFleetHistory({ mode: 'sessions', term: true });
    $('#app').classList.add('term-open');
  }
}

function chatAtBottom() {
  const sc = $('#chat-scroll');
  return sc.scrollHeight - sc.scrollTop - sc.clientHeight < 64;
}

function firstChatLine(text) {
  return String(text || '').split(/\r?\n/, 1)[0].trim();
}

function chatTurnPinText(rows, viewportTop) {
  let text = '';
  for (const row of rows || []) {
    if (row.getBoundingClientRect().bottom > viewportTop) break;
    text = row.dataset.chatTurnPin || '';
  }
  return text;
}

function syncChatTurnPin() {
  const sc = $('#chat-scroll');
  const pin = $('#chat-turn-pin');
  const textEl = $('#chat-turn-pin-text');
  if (!sc || !pin || !textEl) {
    if (pin) pin.hidden = true;
    return;
  }
  const text = chatTurnPinText(sc.querySelectorAll('.chat-row.user[data-chat-turn-pin]'), sc.getBoundingClientRect().top);
  textEl.textContent = text;
  pin.hidden = !text;
}

function chatRenderUnits(entries) {
  const units = [];
  let trace = [];
  const flushTrace = () => {
    if (!trace.length) return;
    units.push({ kind: 'trace', entries: trace });
    trace = [];
  };
  for (const entry of entries || []) {
    // Codex Desktop keeps internal reasoning items out of the transcript. The
    // user-facing progress text arrives separately as assistant commentary.
    if (entry?.item?.type === 'reasoning') continue;
    if (isChatTraceItem(entry?.item)) {
      trace.push(entry);
      continue;
    }
    flushTrace();
    units.push({ kind: 'item', entries: [entry] });
  }
  flushTrace();
  return units;
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
  const metaVisible = chatMessageMetaVisibility(model);
  const entries = model.messages
    .map((id) => ({ id, item: model.items[id] }))
    .filter((entry) => entry.item && !isInternalChatTool(entry.item));
  for (const unit of chatRenderUnits(entries)) {
    const rows = unit.kind === 'trace'
      ? renderChatActivityRun(unit.entries.map((entry) => entry.item))
      : [renderChatItem(unit.entries[0].item,
        unit.entries[0].item.type === 'user' && metaVisible.has(unit.entries[0].id))];
    for (const row of rows) {
      if (row) stack.append(row);
    }
    const lastId = unit.entries[unit.entries.length - 1].id;
    const turnMeta = metaVisible.get(lastId);
    if (turnMeta?.type === 'assistant') stack.append(renderChatTurnMeta(turnMeta));
  }
  if (model.error) stack.append(renderChatError(model.error));
  clear(sc); sc.append(stack);
  if (preserveScroll) sc.scrollTop = oldTop + (sc.scrollHeight - oldHeight);
  else if (forceBottom || stick) sc.scrollTop = sc.scrollHeight;
  syncChatTurnPin();
  $('#chat-jump').hidden = chatAtBottom();
}

function chatRow(body, cls = '') {
  return h('div', { class: 'chat-row ' + cls }, body);
}

function chatToolStatus(status) {
  const value = String(status || '').toLowerCase();
  if (['inprogress', 'running', 'pending', 'started', 'interacted'].includes(value)) return { key: 'running', label: '运行中' };
  if (['interrupted', 'cancelled', 'canceled', 'stopped'].includes(value)) return { key: 'stopped', label: '已停止' };
  if (['failed', 'errored', 'error'].includes(value)) return { key: 'failed', label: '失败' };
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

function chatToolStatusVerb(status, verbs) {
  if (status.key === 'running') return verbs.running;
  if (status.key === 'stopped') return verbs.stopped || verbs.failed || verbs.completed;
  if (status.key === 'failed') return verbs.failed || verbs.stopped || verbs.completed;
  return verbs.completed;
}

function chatToolTimerLabel(status, duration) {
  if (!duration) return '';
  return status.key === 'running' ? `，已持续 ${duration}` : `，耗时 ${duration}`;
}

function chatToolInlineValue(text, className) {
  const value = String(text || '').trim();
  return value ? h('span', { class: className, text: value }) : null;
}

function shellCommandBase(command) {
  const value = String(command || '').trim();
  const shell = value.match(/^(?:\/usr\/bin\/env\s+)?(?:bash|zsh|sh)\s+-lc\s+(.+)$/);
  return shell ? shell[1].replace(/^['"]|['"]$/g, '').trim() : value;
}

function chatCommandSemantic(item) {
  if (item?.kind !== 'commandExecution') return '';
  const actions = Array.isArray(item.commandActions) ? item.commandActions : [];
  if (actions.some((action) => action?.type === 'search')) return 'search';
  if (actions.some((action) => action?.type === 'listFiles')) return 'list';
  if (actions.length && actions.every((action) => action?.type === 'read')) return 'read';
  const cmd = shellCommandBase(item.summary || item.title || '').toLowerCase();
  if (!cmd) return '';
  if (/^(?:cat|sed|nl|head|tail|less|more|awk|wc)\b/.test(cmd)) return 'read';
  if (/^(?:rg|grep)\b/.test(cmd)) return 'search';
  if (/^(?:ls|find)\b/.test(cmd)) return 'list';
  return '';
}

function chatCommandLoadsSkill(item) {
  if (item?.kind !== 'commandExecution') return false;
  const actions = Array.isArray(item.commandActions) ? item.commandActions : [];
  return actions.some((action) => (
    action?.type === 'read' && /(?:^|\/)SKILL\.md$/i.test(String(action.path || action.name || '').trim())
  ));
}

function isNodeReplTool(item, title, summary) {
  const raw = `${title || ''}\n${summary || ''}`.toLowerCase();
  return item.kind === 'mcpToolCall' && (
    raw.includes('node_repl') ||
    raw.includes('运行 javascript') ||
    raw.includes('run javascript') ||
    raw.includes('javascript')
  );
}

function chatMcpToolActivityLabel(item, status) {
  const summary = String(item.summary || '').trim();
  const title = String(item.title || '').trim();
  const raw = `${title}\n${summary}`.toLowerCase();
  const verb = (text) => h('span', { class: 'chat-tool-verb', text });
  const muted = (text) => text ? h('span', { class: 'chat-tool-muted', text }) : null;
  const source = summary && summary !== title ? summary : '';

  if (raw.includes('chrome')) return [verb(status.key === 'running' ? '正在使用 Chrome' : '已使用 Chrome')];
  if (raw.includes('调用内部浏览器') || raw.includes('internal browser') || raw.includes('browser')) {
    return [verb(status.key === 'running' ? '正在使用浏览器' : '已使用浏览器')];
  }
  if (isNodeReplTool(item, title, summary)) {
    return [verb(status.key === 'running' ? '正在运行 1 条命令' : '已运行 1 条命令')];
  }
  return [verb(status.key === 'running' ? '正在调用 1 个工具' : '已调用 1 个工具'), muted(source || title || 'MCP')];
}

function chatToolActivityLabel(item, status, duration) {
  const summary = String(item.summary || '').trim();
  const title = String(item.title || '').trim();
  const timer = chatToolTimerLabel(status, duration);
  const verb = (text) => h('span', { class: 'chat-tool-verb', text });
  const muted = (text) => text ? h('span', { class: 'chat-tool-muted', text }) : null;
  const command = chatToolInlineValue(summary || title, 'chat-tool-command mono');
  const path = chatToolInlineValue(summary, 'chat-tool-path mono');

  if (item.kind === 'commandExecution') {
    const semantic = chatCommandSemantic(item);
    if (semantic) {
      const labels = {
        read: status.key === 'running' ? '正在读取文件' : '已读取文件',
        search: status.key === 'running' ? '正在搜索文件' : '已搜索文件',
        list: status.key === 'running' ? '正在列出文件' : '已列出文件',
      };
      return [verb(labels[semantic])];
    }
    if (status.key === 'running') return [verb('正在运行命令'), muted(timer)];
    const label = chatToolStatusVerb(status, { completed: '已运行', stopped: '已停止', failed: '运行失败' });
    return command
      ? [verb(label), command, muted(timer)]
      : [verb(label === '已运行' ? '已运行命令' : `${label}命令`), muted(timer)];
  }
  if (item.kind === 'mcpToolCall') return chatMcpToolActivityLabel(item, status);
  if (item.kind === 'webSearch') {
    const label = status.key === 'running' ? '正在搜索' : '已对';
    return summary
      ? [verb(label), h('span', { class: 'chat-tool-query', text: `“${summary}”` }), muted(status.key === 'running' ? '' : '进行搜索')]
      : [verb(status.key === 'running' ? '正在搜索' : '已搜索'), muted('网页')];
  }
  if (item.kind === 'fileRead' || item.kind === 'imageView') {
    const label = status.key === 'running'
      ? (item.kind === 'imageView' ? '正在查看图片' : '正在读取')
      : (item.kind === 'imageView' ? '已查看图片' : '已读取');
    return path ? [verb(label), path] : [verb(label)];
  }
  if (item.kind === 'imageGeneration') {
    const label = chatToolStatusVerb(status, { running: '正在生成图片', completed: '已生成图片', failed: '图片生成失败' });
    return path ? [verb(label), path] : [verb(label)];
  }
  if (item.kind === 'sleep') {
    return [verb(status.key === 'running' ? '正在等待' : '已等待'), muted(summary || duration)];
  }
  if (item.kind === 'collabAgentToolCall' || item.kind === 'subAgentActivity') {
    return [verb(status.key === 'running' ? '正在调用' : '已调用'), muted(title || '子任务'), summary ? h('span', { class: 'chat-tool-command mono', text: summary }) : null];
  }
  const label = status.key === 'running' ? '正在使用' : '已使用';
  return [verb(label), muted(title || '工具'), summary && summary !== title ? h('span', { class: 'chat-tool-command mono', text: summary }) : null, muted(timer)];
}

function chatToolHasExpandableBody(item) {
  const hasDetail = Boolean(item.detail || item.output || item.progress || item.meta || item.mediaPath || item.exitCode !== undefined);
  if (chatCommandSemantic(item)) return false;
  if (item.kind === 'commandExecution') return Boolean(item.summary || hasDetail);
  if (item.kind === 'imageView') return Boolean(item.mediaPath);
  if (['fileRead', 'webSearch', 'sleep'].includes(item.kind)) return false;
  return hasDetail;
}

function formatChatDate(ms, nowMs = Date.now()) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return '';
  const d = new Date(value);
  if (!Number.isFinite(d.getTime())) return '';
  const pad = (n) => String(n).padStart(2, '0');
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
  const now = new Date(nowMs);
  const dayOrdinal = (date) => Math.floor(Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) / 86400000);
  const dayDiff = dayOrdinal(now) - dayOrdinal(d);
  if (dayDiff === 0) return time;
  if (dayDiff === 1) return `昨天 ${time}`;
  if (dayDiff === 2) return `前天 ${time}`;
  const monthDay = `${d.getMonth() + 1}-${d.getDate()}`;
  if (d.getFullYear() === now.getFullYear()) return `${monthDay} ${time}`;
  return `${d.getFullYear()}-${monthDay} ${time}`;
}

function formatChatInteger(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return '';
  return Math.round(n).toLocaleString('en-US');
}

function chatUserMetaText(item) {
  const sent = formatChatDate(item.sentAtMs || item.createdAtMs || item.created_at_ms);
  return sent;
}

function chatAssistantMetaText(item) {
  const chunks = [];
  const identity = [item.model, item.effort].filter(Boolean).join(', ');
  if (identity) chunks.push(identity);
  const usage = item.usage || {};
  const inputTokens = formatChatInteger(usage.inputTokens);
  const outputTokens = formatChatInteger(item.usage?.outputTokens);
  const inputCount = Number(usage.inputTokens);
  const cachedInputCount = Number(usage.cachedInputTokens);
  let inputText = inputTokens || '-';
  if (inputTokens && Number.isFinite(inputCount) && inputCount > 0 && Number.isFinite(cachedInputCount) && cachedInputCount > 0) {
    const pct = Math.round(Math.max(0, Math.min(1, cachedInputCount / inputCount)) * 100);
    inputText += ` (${pct}% cached)`;
  }
  if (inputTokens || outputTokens) chunks.push(`in ${inputText} / out ${outputTokens || '-'}`);
  const completed = formatChatDate(item.completedAtMs || item.finishedAtMs || item.finished_at_ms);
  if (completed) chunks.push(completed);
  return chunks.join('  |  ');
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

function chatAssistantTurnKey(item, fallbackId) {
  return item?.turnId ? `turn:${item.turnId}` : `item:${fallbackId}`;
}

function isInternalChatTool(item) {
  return Boolean(item?.internal);
}

function isChatTraceItem(item) {
  return item?.type === 'tool' || item?.type === 'diff';
}

function isChatActivityItem(item) {
  if (item?.type === 'diff') return true;
  if (item?.type !== 'tool') return false;
  if (item.kind === 'mcpToolCall') {
    const raw = `${item.server || ''}\n${item.title || ''}\n${item.summary || ''}`.toLowerCase();
    if (/(?:^|[\s._-])computer[\s._-]?use(?:$|[\s._-])/.test(raw)) return false;
  }
  return ['commandExecution', 'fileRead', 'webSearch', 'mcpToolCall', 'dynamicToolCall'].includes(item.kind);
}

function chatMessageMetaVisibility(model) {
  const visible = new Map();
  const messages = model?.messages || [];
  const items = model?.items || {};
  const turns = new Map();
  for (const id of messages) {
    const item = items[id];
    if (!item || item.type === 'reasoning' || isInternalChatTool(item)) continue;
    if (item.type === 'user') {
      if (chatUserMetaText(item)) visible.set(id, item);
    }
    const key = item.turnId ? chatAssistantTurnKey(item, id) : (item.type === 'assistant' ? `item:${id}` : '');
    if (!key) continue;
    const turn = turns.get(key) || { lastItemId: '', assistant: null };
    turn.lastItemId = id;
    if (item.type === 'assistant' && item.turnComplete && chatAssistantMetaText(item)) turn.assistant = item;
    turns.set(key, turn);
  }
  for (const turn of turns.values()) {
    if (turn.lastItemId && turn.assistant) visible.set(turn.lastItemId, turn.assistant);
  }
  return visible;
}

function renderChatMessageMeta(text) {
  return text ? h('div', { class: 'chat-msg-meta tnum', text }) : null;
}

function renderChatTurnMeta(item) {
  const meta = renderChatMessageMeta(chatAssistantMetaText(item));
  return meta ? chatRow(meta, 'assistant turn-meta') : null;
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

function renderChatToolSurface(item, extraClass = '') {
  const status = chatToolStatus(item.status);
  const duration = chatToolDuration(item.durationMs);
  const hasBody = chatToolHasExpandableBody(item);
  const isCommand = item.kind === 'commandExecution';
  const label = chatToolActivityLabel(item, status, duration);
  const header = h('span', { class: 'chat-tool-summary', dataset: { status: status.key } },
    h('span', { class: 'chat-tool-icon' }, chatToolIcon(item.kind)),
    h('span', { class: 'chat-tool-label' }, label),
    h('span', { class: 'chat-tool-aside' },
      hasBody ? svgIcon('chat-tool-chevron', 'M6 9l6 6 6-6') : null));
  const cls = ['chat-tool compact', extraClass].filter(Boolean).join(' ');
  if (!hasBody) return h('div', { class: cls }, header);
  const body = h('div', { class: 'chat-tool-body' },
    item.mediaPath ? h('img', { class: 'chat-tool-media', src: chatMediaSrc(item.mediaPath), alt: item.summary || 'Codex 图片' }) : null,
    item.progress ? h('div', { class: 'chat-tool-progress', text: item.progress }) : null,
    item.meta ? h('div', { class: 'chat-tool-meta mono', text: item.meta }) : null,
    (item.summary || item.output || item.detail) ? h('div', { class: 'chat-tool-section' },
      h('div', { class: 'chat-tool-section-title', text: isCommand ? 'Shell' : '详情' }),
      h('pre', { text: [
        item.summary ? `$ ${item.summary}` : '',
        item.detail || '',
        item.output || '',
      ].filter(Boolean).join('\n') })) : null,
    item.exitCode !== undefined ? h('div', { class: 'chat-tool-exit tnum', text: `退出码 ${item.exitCode}` }) : null);
  return h('details', { class: cls }, h('summary', {}, header), body);
}

function renderChatTool(item) {
  return chatRow(renderChatToolSurface(item), 'tool');
}

function renderChatDiffSurface(item, extraClass = '') {
  const files = item.files || [];
  const header = h('span', { class: 'chat-tool-summary' },
    h('span', { class: 'chat-tool-icon' }, chatToolIcon('fileChange')),
    h('span', { class: 'chat-tool-label' }, h('span', { class: 'chat-tool-verb', text: '已编辑' }), h('span', { class: 'chat-tool-muted', text: '文件' })),
    h('span', { class: 'chat-tool-aside' }, files.length ? svgIcon('chat-tool-chevron', 'M6 9l6 6 6-6') : null));
  const cls = ['chat-tool chat-diff compact', extraClass].filter(Boolean).join(' ');
  if (!files.length) return h('div', { class: cls }, header);
  return h('details', { class: cls },
    h('summary', {}, header),
    h('div', { class: 'chat-diff-files' }, files.map((file) => h('div', { class: 'chat-diff-file' },
      h('span', { class: 'chat-diff-path mono', text: file.path }),
      h('span', { class: 'chat-diff-stats tnum' },
        h('span', { class: 'chat-diff-add', text: `+${file.additions || 0}` }),
        h('span', { class: 'chat-diff-del', text: `-${file.deletions || 0}` }))))));
}

function renderChatDiff(item) {
  return chatRow(renderChatDiffSurface(item), 'diff');
}

function chatActivitySourceLabel(item) {
  const raw = `${item.title || ''}\n${item.summary || ''}`.toLowerCase();
  if (raw.includes('chrome')) return 'Chrome';
  if (raw.includes('browser') || raw.includes('浏览器')) return '浏览器';
  const title = String(item.title || '').trim();
  const first = title.split(/[·:]/, 1)[0]?.trim();
  return first && !/^(mcp|工具|调用工具)$/.test(first.toLowerCase()) ? first : '';
}

function chatActivityGroupStats(items) {
  const stats = {
    commands: 0, runningCommands: 0,
    loadedTools: 0, reads: 0, webSearches: 0,
    files: 0, tools: 0, runningTools: 0,
    sources: [],
  };
  for (const item of items) {
    const status = chatToolStatus(item.status);
    const isCommand = item.type === 'tool' && (item.kind === 'commandExecution' || isNodeReplTool(item, item.title, item.summary));
    if (isCommand) {
      const semantic = chatCommandSemantic(item);
      if (semantic === 'read' && chatCommandLoadsSkill(item)) stats.loadedTools += 1;
      else if (semantic) stats.reads += 1;
      else {
        stats.commands += 1;
        if (status.key === 'running') stats.runningCommands += 1;
      }
    } else if (item.type === 'diff') {
      stats.files += Math.max(1, (item.files || []).length);
    } else if (item.kind === 'webSearch') {
      stats.webSearches += 1;
    } else if (item.kind === 'fileRead' || item.kind === 'imageView') {
      stats.reads += 1;
    } else {
      if (item.kind === 'mcpToolCall') {
        const source = chatActivitySourceLabel(item);
        if (source) {
          if (!stats.sources.includes(source)) stats.sources.push(source);
          continue;
        }
      }
      stats.tools += 1;
      if (status.key === 'running') stats.runningTools += 1;
    }
  }
  return stats;
}

function chatActivityPlural(count, one, many) {
  return count === 1 ? one : many;
}

function chatActivityGroupSummarySegments(items) {
  const stats = chatActivityGroupStats(items);
  const parts = [];
  const leading = () => parts.length === 0;
  if (stats.sources.length) parts.push(`已使用 ${stats.sources.join('、')} 集成`);
  if (stats.loadedTools > 0) {
    parts.push(stats.loadedTools === 1
      ? (leading() ? '已加载一个工具' : '加载了一个工具')
      : (leading() ? '已加载工具' : '加载了工具'));
  }
  const completedTools = stats.tools - stats.runningTools;
  if (completedTools > 0) parts.push(chatActivityPlural(completedTools, '调用了一个工具', '调用了工具'));
  if (stats.runningTools > 0) parts.push(chatActivityPlural(stats.runningTools, '正在调用一个工具', '正在调用工具'));
  if (stats.files > 0) {
    parts.push(stats.files === 1 ? '编辑了一个文件' : (leading() ? '编辑了文件' : '编辑了多个文件'));
  }
  if (stats.reads > 0) parts.push(leading() ? '已读取文件' : '读取文件');
  const completedCommands = stats.commands - stats.runningCommands;
  if (completedCommands > 0) parts.push(chatActivityPlural(completedCommands, '运行了一个命令', '运行了多个命令'));
  if (stats.runningCommands > 0) parts.push(chatActivityPlural(stats.runningCommands, '正在运行一个命令', '正在运行多个命令'));
  if (stats.webSearches > 0) parts.push('已搜索网页');
  return parts.length ? parts : ['已完成'];
}

function chatActivityGroupSummaryText(items) {
  return chatActivityGroupSummarySegments(items).join('');
}

function chatActivityActiveItem(items) {
  for (let i = items.length - 1; i >= 0; i -= 1) {
    const item = items[i];
    if ((item.type === 'tool' || item.type === 'diff') && chatToolStatus(item.status).key === 'running') return item;
  }
  return null;
}

function chatActivityActiveSummarySegments(item) {
  if (!item) return null;
  if (item.type === 'diff') return ['正在编辑文件'];
  if (item.kind === 'commandExecution') {
    const semantic = chatCommandSemantic(item);
    if (semantic === 'read') return ['正在读取文件'];
    if (semantic === 'search') return ['正在搜索文件'];
    if (semantic === 'list') return ['正在列出文件'];
    return ['正在运行命令'];
  }
  if (item.kind === 'webSearch') return ['正在搜索网页'];
  if (item.kind === 'mcpToolCall') {
    const source = chatActivitySourceLabel(item);
    return source ? [`正在使用 ${source} 集成`] : ['正在调用工具'];
  }
  return null;
}

function chatActivityGroupIconKind(items) {
  const stats = chatActivityGroupStats(items);
  if (stats.sources.length) return 'mcpToolCall';
  if (stats.loadedTools > 0) return 'commandExecution';
  if (stats.tools > 0) return items.find((item) => item.kind === 'mcpToolCall' || item.kind === 'dynamicToolCall')?.kind || 'tool';
  if (stats.files > 0) return 'fileChange';
  if (stats.reads > 0) return 'fileRead';
  if (stats.commands > 0) return 'commandExecution';
  if (stats.webSearches > 0) return 'webSearch';
  return items[0]?.kind || 'tool';
}

function renderChatActivityGroup(items) {
  const segments = chatActivityActiveSummarySegments(chatActivityActiveItem(items)) || chatActivityGroupSummarySegments(items);
  const header = h('span', { class: 'chat-tool-summary chat-activity-group-summary' },
    h('span', { class: 'chat-tool-icon' }, chatToolIcon(chatActivityGroupIconKind(items))),
    h('span', { class: 'chat-tool-label' }, segments.map((segment) => h('span', { class: 'chat-tool-verb', text: segment }))),
    h('span', { class: 'chat-tool-aside' }, svgIcon('chat-tool-chevron', 'M6 9l6 6 6-6')));
  return chatRow(h('details', { class: 'chat-activity-group chat-tool compact' },
    h('summary', {}, header),
    h('div', { class: 'chat-activity-group-body' }, items.map((item) => (
      item.type === 'diff' ? renderChatDiffSurface(item, 'grouped') : renderChatToolSurface(item, 'grouped')
    )))),
  'tool activity-group');
}

function renderChatActivityRun(items) {
  const visibleItems = items.filter((item) => item.type !== 'reasoning');
  const rows = [];
  for (let i = 0; i < visibleItems.length; i += 1) {
    const item = visibleItems[i];
    if (!isChatActivityItem(item)) {
      rows.push(renderChatItem(item, false));
      continue;
    }
    const group = [item];
    while (i + 1 < visibleItems.length && isChatActivityItem(visibleItems[i + 1])) {
      group.push(visibleItems[i + 1]);
      i += 1;
    }
    rows.push(group.length > 1 ? renderChatActivityGroup(group) : renderChatItem(item, false));
  }
  return rows.filter(Boolean);
}

function requestActionButton(label, response, primary = false) {
  return h('button', {
    type: 'button',
    class: `btn sm${primary ? ' primary' : ''}`,
    onclick: (event) => respondChatRequest(event.currentTarget.closest('[data-request-id]')?.dataset.requestId, response),
  }, label);
}

function renderChatApprovalRequest(item) {
  const pending = item.status !== 'resolved';
  const raw = item.raw || {};
  let title = '需要批准文件改动';
  if (item.kind === 'command') title = '需要批准命令执行';
  if (item.kind === 'permission') title = '需要授予额外权限';
  const details = [];
  if (item.reason) details.push(h('div', { class: 'chat-request-message', text: item.reason }));
  if (item.command) details.push(h('code', { text: item.command }));
  if (item.cwd) details.push(h('div', { class: 'muted mono', text: item.cwd }));
  if (raw.grantRoot) details.push(h('div', { class: 'chat-request-detail', text: `写入范围：${raw.grantRoot}` }));
  if (item.kind === 'permission' && raw.permissions) {
    details.push(h('pre', { class: 'chat-request-json', text: JSON.stringify(raw.permissions, null, 2) }));
  }
  let actions = null;
  if (pending && item.kind === 'permission') {
    actions = h('div', { class: 'chat-approval-actions' },
      requestActionButton('允许本轮', { permissions: raw.permissions || {}, scope: 'turn' }, true),
      requestActionButton('允许本会话', { permissions: raw.permissions || {}, scope: 'session' }),
      requestActionButton('拒绝', { permissions: {}, scope: 'turn' }));
  } else if (pending) {
    actions = h('div', { class: 'chat-approval-actions' },
      requestActionButton('允许一次', { decision: 'accept' }, true),
      requestActionButton('本会话允许', { decision: 'acceptForSession' }),
      requestActionButton('拒绝', { decision: 'decline' }));
  }
  return chatRow(h('section', { class: 'chat-approval chat-request', dataset: { requestId: item.requestId } },
    h('div', { class: 'chat-approval-h', text: pending ? title : '请求已处理' }),
    h('div', { class: 'chat-approval-body' }, details,
      pending ? actions : h('div', { class: 'muted', text: '已发送响应。' }))), 'approval');
}

function renderUserInputQuestion(item, question, index) {
  const name = `request-${item.requestId}-${index}`;
  const options = Array.isArray(question.options) ? question.options : [];
  const fields = [];
  if (options.length) {
    for (const option of options) {
      fields.push(h('label', { class: 'chat-request-option' },
        h('input', { type: 'radio', name, value: option.label, required: '' }),
        h('span', { class: 'chat-request-option-copy' },
          h('span', { class: 'chat-request-option-label', text: option.label }),
          option.description ? h('span', { class: 'chat-request-option-desc', text: option.description }) : null)));
    }
    if (question.isOther) {
      fields.push(h('label', { class: 'chat-request-option other' },
        h('input', { type: 'radio', name, value: '__other__', required: '' }),
        h('span', { class: 'chat-request-option-copy' },
          h('span', { class: 'chat-request-option-label', text: '其他' }),
          h('input', { class: 'chat-request-input', type: question.isSecret ? 'password' : 'text',
            dataset: { otherFor: name }, autocomplete: 'off', placeholder: '输入其他答案' }))));
    }
  } else {
    fields.push(h(question.isSecret ? 'input' : 'textarea', {
      class: 'chat-request-input',
      type: question.isSecret ? 'password' : null,
      name,
      rows: question.isSecret ? null : '3',
      required: '',
      autocomplete: question.isSecret ? 'new-password' : 'off',
    }));
  }
  return h('fieldset', { class: 'chat-request-fieldset', dataset: { questionId: question.id, fieldName: name } },
    question.header ? h('legend', { text: question.header }) : null,
    h('div', { class: 'chat-request-question', text: question.question || '' }),
    ...fields);
}

function userInputResponse(form, item) {
  const answers = {};
  for (const fieldset of $$('[data-question-id]', form)) {
    const id = fieldset.dataset.questionId;
    const name = fieldset.dataset.fieldName;
    const options = $$(`input[type="radio"][name="${CSS.escape(name)}"]`, fieldset);
    let value = '';
    if (options.length) {
      const selected = options.find((option) => option.checked);
      if (!selected) throw new Error('请回答所有问题。');
      value = selected.value;
      if (value === '__other__') {
        value = $(`[data-other-for="${CSS.escape(name)}"]`, fieldset)?.value.trim() || '';
        if (!value) throw new Error('请输入“其他”答案。');
      }
    } else {
      value = $(`[name="${CSS.escape(name)}"]`, fieldset)?.value || '';
      if (!value.trim()) throw new Error('请回答所有问题。');
    }
    answers[id] = { answers: [value] };
  }
  return { answers };
}

function renderChatUserInputRequest(item) {
  if (item.status === 'resolved') {
    return chatRow(h('section', { class: 'chat-approval chat-request' },
      h('div', { class: 'chat-approval-h', text: '问题已回答' })), 'approval');
  }
  const form = h('form', {
    class: 'chat-approval chat-request',
    dataset: { requestId: item.requestId },
    onsubmit: async (event) => {
      event.preventDefault();
      try {
        await respondChatRequest(item.requestId, userInputResponse(event.currentTarget, item));
      } catch (error) {
        toast(error.message, 'err');
      }
    },
  },
  h('div', { class: 'chat-approval-h', text: 'Codex 需要你的回答' }),
  h('div', { class: 'chat-approval-body' },
    ...(item.questions || []).map((question, index) => renderUserInputQuestion(item, question, index)),
    h('div', { class: 'chat-approval-actions' },
      h('button', { type: 'submit', class: 'btn sm primary' }, '提交回答'))));
  return chatRow(form, 'approval');
}

function mcpEnumOptions(schema) {
  if (Array.isArray(schema.enum)) return schema.enum.map((value, index) => ({
    value, label: Array.isArray(schema.enumNames) ? (schema.enumNames[index] || value) : value,
  }));
  const values = schema.oneOf || schema.items?.anyOf;
  if (Array.isArray(values)) return values.map((entry) => ({ value: entry.const, label: entry.title || entry.const }));
  if (Array.isArray(schema.items?.enum)) return schema.items.enum.map((value) => ({ value, label: value }));
  return [];
}

function renderMcpFormField(key, schema, required) {
  const label = schema.title || key;
  const options = mcpEnumOptions(schema);
  let control;
  if (options.length && schema.type === 'array') {
    control = h('div', { class: 'chat-request-options' }, options.map((option) => h('label', { class: 'chat-request-option' },
      h('input', { type: 'checkbox', name: key, value: option.value }),
      h('span', { text: option.label }))));
  } else if (options.length) {
    control = h('select', { class: 'chat-request-input', name: key, required: required ? '' : null },
      required ? h('option', { value: '', text: '请选择' }) : null,
      ...options.map((option) => h('option', { value: option.value, text: option.label,
        selected: schema.default === option.value ? '' : null })));
  } else if (schema.type === 'boolean') {
    control = h('label', { class: 'chat-request-option inline' },
      h('input', { type: 'checkbox', name: key, checked: schema.default === true ? '' : null }),
      h('span', { text: schema.description || label }));
  } else {
    control = h('input', {
      class: 'chat-request-input',
      name: key,
      type: schema.type === 'number' || schema.type === 'integer' ? 'number'
        : (schema.format === 'email' ? 'email' : (schema.format === 'uri' ? 'url' : (schema.format === 'date' ? 'date' : 'text'))),
      step: schema.type === 'integer' ? '1' : (schema.type === 'number' ? 'any' : null),
      min: schema.minimum,
      max: schema.maximum,
      minlength: schema.minLength,
      maxlength: schema.maxLength,
      value: schema.default ?? '',
      required: required ? '' : null,
    });
  }
  return h('label', { class: 'chat-request-field', dataset: { mcpField: key, fieldType: schema.type || 'string' } },
    schema.type !== 'boolean' ? h('span', { class: 'chat-request-field-label', text: label }) : null,
    schema.description && schema.type !== 'boolean' ? h('span', { class: 'chat-request-option-desc', text: schema.description }) : null,
    control);
}

function mcpFormResponse(form, item) {
  const content = {};
  for (const field of $$('[data-mcp-field]', form)) {
    const key = field.dataset.mcpField;
    const type = field.dataset.fieldType;
    const controls = $$(`[name="${CSS.escape(key)}"]`, field);
    if (type === 'array') {
      content[key] = controls.filter((control) => control.checked).map((control) => control.value);
    } else if (type === 'boolean') {
      content[key] = !!controls[0]?.checked;
    } else {
      const value = controls[0]?.value ?? '';
      if (value === '' && !controls[0]?.required) continue;
      content[key] = type === 'number' || type === 'integer' ? Number(value) : value;
    }
  }
  return { action: 'accept', content };
}

function renderChatElicitationRequest(item) {
  const pending = item.status !== 'resolved';
  const schema = item.requestedSchema || {};
  const required = new Set(Array.isArray(schema.required) ? schema.required : []);
  const fields = Object.entries(schema.properties || {});
  const body = [];
  if (item.message) body.push(h('div', { class: 'chat-request-message', text: item.message }));
  if (!pending) body.push(h('div', { class: 'muted', text: '已发送响应。' }));
  else if (item.mode === 'url') {
    body.push(h('a', { class: 'btn sm primary', href: item.url, target: '_blank', rel: 'noopener noreferrer' }, '打开授权页面'));
    body.push(h('div', { class: 'chat-approval-actions' },
      requestActionButton('已完成', { action: 'accept' }, true),
      requestActionButton('拒绝', { action: 'decline' })));
  } else {
    body.push(...fields.map(([key, field]) => renderMcpFormField(key, field || {}, required.has(key))));
    body.push(h('div', { class: 'chat-approval-actions' },
      h('button', { type: 'submit', class: 'btn sm primary' }, '提交'),
      requestActionButton('拒绝', { action: 'decline' })));
  }
  const form = h('form', {
    class: 'chat-approval chat-request',
    dataset: { requestId: item.requestId },
    onsubmit: async (event) => {
      event.preventDefault();
      if (item.mode === 'url') return;
      await respondChatRequest(item.requestId, mcpFormResponse(event.currentTarget, item));
    },
  }, h('div', { class: 'chat-approval-h', text: item.serverName ? `${item.serverName} 需要更多信息` : '工具需要更多信息' }),
  h('div', { class: 'chat-approval-body' }, body));
  return chatRow(form, 'approval');
}

function renderChatItem(item, showMeta = true) {
  if (item.type === 'user') {
    const parts = [];
    if (item.text) parts.push(h('div', { text: item.text }));
    if (item.images && item.images.length) {
      parts.push(h('div', { class: 'chat-images' }, item.images.map((img) => {
        const src = chatImageSrc(img);
        return src ? h('img', { class: 'chat-img', src, alt: img.name || 'image' }) : h('div', { class: 'chat-img muted', text: img.name || '图片' });
      })));
    }
    const steeringLabel = item.steering && item.steeringStatus !== 'persisted'
      ? h('span', { class: `chat-steer-state ${item.steeringStatus || 'pending'}`,
        text: item.steeringStatus === 'accepted' ? '已插入当前任务' : '正在插入当前任务' })
      : null;
    const metaText = showMeta ? chatUserMetaText(item) : '';
    const meta = (steeringLabel || metaText) ? h('div', { class: 'chat-user-meta' },
      steeringLabel, renderChatMessageMeta(metaText)) : null;
    const row = chatRow(h('div', { class: 'chat-user-wrap' },
      h('div', { class: 'chat-card' }, parts.length ? parts : h('div', { text: '' })),
      meta),
    'user');
    row.dataset.chatTurnPin = firstChatLine(item.text);
    return row;
  }
  if (item.type === 'assistant') {
    return chatRow(h('div', { class: 'chat-card' },
      FleetMarkdown.renderMarkdown(item.text, chatMediaSrc, chatLinkHref)),
    'assistant');
  }
  if (item.type === 'reasoning') {
    return null;
  }
  if (item.type === 'plan') {
    if (!item.text) return null;
    return chatRow(h('section', { class: 'chat-plan' },
      h('div', { class: 'chat-plan-title', text: 'Proposed plan' }),
      h('div', { class: 'chat-plan-body' }, FleetMarkdown.renderMarkdown(item.text, chatMediaSrc, chatLinkHref))), 'plan');
  }
  if (item.type === 'todo') {
    if (!item.steps?.length) return null;
    return chatRow(h('section', { class: 'chat-todo' },
      item.explanation ? h('div', { class: 'chat-todo-explanation', text: item.explanation }) : null,
      ...item.steps.map((step) => h('div', { class: `chat-todo-step ${step.status || 'pending'}` },
        h('span', { class: 'chat-todo-mark', text: step.status === 'completed' ? '✓' : (step.status === 'inProgress' ? '•' : '○') }),
        h('span', { text: step.step || '' })))), 'todo');
  }
  if (item.type === 'context') {
    return chatRow(h('div', { class: 'chat-context-note', text: '上下文已自动压缩' }), 'context');
  }
  if (item.type === 'review') return null;
  if (item.type === 'tool') return renderChatTool(item);
  if (item.type === 'approval') return renderChatApprovalRequest(item);
  if (item.type === 'request_user_input') return renderChatUserInputRequest(item);
  if (item.type === 'elicitation') return renderChatElicitationRequest(item);
  if (item.type === 'diff') return renderChatDiff(item);
  return chatRow(h('div', { class: 'chat-card muted', text: JSON.stringify(item) }));
}

function chatImageSrc(img) {
  if (!img) return '';
  if (img.previewUrl) return img.previewUrl;
  if (img.url && img.url.startsWith('/api/')) return `${apiBase(state.macId)}${img.url}`;
  if (img.path) return chatMediaSrc(img.path);
  return img.url || '';
}

function chatMediaSrc(source) {
  const value = String(source || '').trim();
  if (!value) return '';
  if (/^(?:https?:|data:image\/|blob:)/i.test(value)) return value;
  if (value.startsWith('/api/')) return `${apiBase(state.chat?.macId || state.macId)}${value}`;
  if (!value.startsWith('/')) return '';
  return `${apiBase(state.chat?.macId || state.macId)}/api/chat/media?path=${encodeURIComponent(value)}`;
}

function chatLinkHref(source) {
  const chat = state.chat;
  return FleetPreview.resolveLocalLink(source, {
    macId: chat?.macId || state.macId,
    cwd: chat?.cwd || '',
  }) || source;
}

function resizeChatInput() {
  const input = $('#chat-input');
  if (!input) return;
  input.style.height = 'auto';
  input.style.height = Math.min(input.scrollHeight, 180) + 'px';
  input.style.overflowY = input.scrollHeight > 180 ? 'auto' : 'hidden';
}

function chatComposerAction(chat, hasContent) {
  if (!isChatRunning(chat)) return 'send';
  return hasContent ? 'queue' : 'interrupt';
}

function updateChatComposerState() {
  const input = $('#chat-input');
  const send = $('#chat-send');
  if (!send) return;
  const attachments = state.chat?.attachments || [];
  const blocked = attachments.some((att) => att.uploading || att.error || !att.id);
  const hasContent = Boolean(input?.value.trim()) || attachments.length > 0;
  const action = chatComposerAction(state.chat, hasContent);
  send.dataset.action = action;
  send.disabled = !state.chat || (action === 'interrupt'
    ? !!state.chat.interrupting
    : (!hasContent || blocked));
  send.title = action === 'interrupt'
    ? '停止生成'
    : (blocked ? '等待图片上传完成' : (action === 'queue' ? '加入队列' : '发送'));
  send.setAttribute('aria-label', send.title);
}

function mergeChatComposerText(failedText, currentText) {
  const failed = normalizeChatDraft(failedText).trim();
  const current = normalizeChatDraft(currentText).trim();
  return [failed, current].filter(Boolean).join(failed && current ? '\n' : '');
}

function mergeChatAttachments(failedImages, currentImages) {
  const merged = [];
  const seen = new Set();
  for (const image of [...(failedImages || []), ...(currentImages || [])]) {
    if (!image) continue;
    const key = image.localId || image.id || image.previewUrl || image.url || image.name;
    if (key && seen.has(key)) continue;
    if (key) seen.add(key);
    merged.push({ ...image });
  }
  return merged;
}

function restoreChatComposerItem(chat, item) {
  if (!chat || !item) return;
  const input = state.chat === chat ? $('#chat-input') : null;
  const currentText = input ? input.value : chat.draft;
  chat.draft = mergeChatComposerText(item.displayText || item.text, currentText);
  chat.attachments = mergeChatAttachments(item.images, chat.attachments);
  if (!input) return;
  input.value = chat.draft;
  renderChatAttachments();
  resizeChatInput();
  updateChatComposerState();
}

function renderChatFollowups() {
  const box = $('#chat-followups');
  const chat = state.chat;
  if (!box) return;
  clear(box);
  const items = chat?.followups || [];
  box.hidden = items.length === 0;
  for (const item of items) {
    const label = item.displayText || item.text
      || (item.skills?.length ? item.skills.map((skill) => `$${skill.name}`).join(' ') : `${item.images.length} 张图片`);
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
        h('button', { type: 'button', class: 'chat-followup-guide', title: item.guiding ? '正在引导' : '引导当前任务',
          disabled: item.guiding ? '' : null, onclick: () => guideChatFollowup(item.id) },
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
  input.value = [current, item.displayText || item.text].filter(Boolean).join(current ? '\n' : '');
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
  const macId = s.macId || state.macId;
  if (!macId) return;
  state.macId = macId;
  state.selectedSid = s.sessionId;
  state.selectedSessionMacId = macId;
  state.selectedSessionAssistant = 'codex';
  $$('.ses').forEach((el) => el.classList.toggle('sel',
    el.dataset.sid === s.sessionId && el.dataset.mac === macId && el.dataset.assistant === 'codex'));
  closeChatPane();
  stopWatch(); hideBanner(); closeMenus();
  const key = chatCacheKey(macId, s.sessionId);
  let chat = state.chatCache.get(key);
  if (chat) {
    chat.title = s.title || chat.title || 'Codex 会话';
    chat.cwd = s.cwd || chat.cwd || '';
  } else {
    chat = {
      cacheKey: key, macId,
      sessionId: s.sessionId, title: s.title || 'Codex 会话', cwd: s.cwd || '',
      model: FleetChatModel.createChatState(), loading: true, events: null, resumePromise: null,
      attachments: [], objectUrls: [], draft: '', updatedAt: Number(s.mtime) || Date.now(),
      followups: [], sendingFollowup: false, interrupting: false,
      skills: [], skillsLoaded: false, skillsPromise: null, skillsError: '',
      skillsCwd: s.cwd || '', skillMenu: null, skillPreferences: {},
      historyReady: false, historyLoading: false, historyCursor: '',
      models: [], efforts: [], serviceTiers: [], selectedModel: '', selectedEffort: '', selectedServiceTier: '',
      modelDirty: false, serviceTierDirty: false,
      approvalMode: 'on-request', approvalConfirmedMode: 'on-request',
      approvalUpdateChain: Promise.resolve(),
    };
    state.chatCache.set(key, chat);
  }
  state.chat = chat;
  if (!Array.isArray(chat.skills)) chat.skills = [];
  if (typeof chat.skillsLoaded !== 'boolean') chat.skillsLoaded = false;
  if (!chat.skillPreferences || typeof chat.skillPreferences !== 'object') chat.skillPreferences = {};
  if (s.fresh) {
    chat.historyReady = true;
    chat.loading = false;
    configureChatOptions(chat, s.startOptions || {});
  }
  updateChatUpdatedAt(chat, s.mtime);
  evictChatCache();
  showChatPane(chat.title, chat.cwd);
  chat.draft = normalizeChatDraft(chat.draft);
  $('#chat-input').value = chat.draft;
  resizeChatInput();
  renderChatAttachments();
  renderChatFollowups();
  closeChatSkillMenu();
  loadChatSkills(chat).catch(() => {});
  if (chat.historyReady) {
    setChatApprovalEnabled(chat, true);
    renderChatOptions(chat);
  } else {
    setChatApprovalEnabled(chat, false);
    $('#chat-options').hidden = true;
    closeChatApproval();
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
      acknowledgeChatFollowups(chat, resumed.history?.events);
      chat.model = FleetChatModel.prependHistory(chat.model, resumed.history?.events || []);
      chat.model = FleetChatModel.reduceChatEvent(chat.model, {
        type: 'thread_status',
        data: { status: resumed.status, activeTurnId: resumed.activeTurnId },
      });
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
      setChatApprovalEnabled(chat, true);
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'error', data: { message: e.message } });
      renderChat();
    }
  }
}

const CHAT_EFFORT_LABELS = {
  none: '无', minimal: '最少', low: '低', medium: '中', high: '高', xhigh: '极高', max: '最大', ultra: '超高',
};
const CHAT_SERVICE_TIER_LABELS = { default: '标准', standard: '标准', fast: '快速', priority: '快速' };
const CHAT_APPROVAL_OPTIONS = [
  { value: 'untrusted', label: '请求批准', description: '编辑外部文件和使用互联网时始终询问' },
  { value: 'on-request', label: '替我审批', description: '仅对检测到的风险操作请求批准' },
  { value: 'full-access', label: '完全访问权限', description: '可不受限制地访问互联网和您电脑上的任何文件', danger: true },
];

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

function normalizeChatApprovalMode(value) {
  if (value === 'untrusted' || value === 'full-access') return value;
  return 'on-request';
}

function selectedChatApproval(mode) {
  const normalized = normalizeChatApprovalMode(mode);
  return CHAT_APPROVAL_OPTIONS.find((item) => item.value === normalized) || CHAT_APPROVAL_OPTIONS[1];
}

function chatApprovalIcon(value, cls = 'ic') {
  if (value === 'untrusted') return svgIconParts(cls, [
    { tag: 'path', attrs: { d: 'M18 11V7a2 2 0 0 0-4 0v4' } },
    { tag: 'path', attrs: { d: 'M14 10V5a2 2 0 0 0-4 0v6' } },
    { tag: 'path', attrs: { d: 'M10 10.5V6a2 2 0 0 0-4 0v8' } },
    { tag: 'path', attrs: { d: 'M6 14v-2a2 2 0 0 0-4 0v2a8 8 0 0 0 8 8h2a8 8 0 0 0 8-8v-3a2 2 0 0 0-4 0v1' } },
  ]);
  if (value === 'full-access') return svgIconParts(cls, [
    { tag: 'path', attrs: { d: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10' } },
    { tag: 'path', attrs: { d: 'M12 8v5' } },
    { tag: 'path', attrs: { d: 'M12 17h.01' } },
  ]);
  return svgIconParts(cls, [
    { tag: 'path', attrs: { d: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10' } },
    { tag: 'path', attrs: { d: 'm9 12 2 2 4-4' } },
  ]);
}

function closeChatApproval() {
  const popover = $('#chat-approval-popover');
  if (!popover) return;
  popover.hidden = true;
  $('#chat-approval')?.setAttribute('aria-expanded', 'false');
}

function renderChatApprovalMenu(chat = state.chat) {
  const trigger = $('#chat-approval');
  const label = $('#chat-approval-label');
  const popover = $('#chat-approval-popover');
  if (!trigger || !label || !popover) return;
  const selected = selectedChatApproval(chat?.approvalMode);
  label.textContent = selected.label;
  trigger.dataset.value = selected.value;
  clear(popover);
  popover.append(
    h('div', { class: 'chat-approval-head' },
      h('span', { text: '如何批准 ChatGPT 操作？' }),
      h('a', {
        class: 'chat-approval-learn',
        href: 'https://developers.openai.com/codex/concepts/sandboxing#how-you-control-it',
        target: '_blank',
        rel: 'noopener noreferrer',
        text: '了解更多',
      })),
    ...CHAT_APPROVAL_OPTIONS.map((option) => {
      const active = option.value === selected.value;
      return h('button', {
        type: 'button',
        class: 'chat-approval-choice' + (active ? ' selected' : '') + (option.danger ? ' full-access' : ''),
        role: 'menuitemradio',
        'aria-checked': String(active),
        dataset: { approvalMode: option.value },
        onclick: (e) => { e.stopPropagation(); selectChatApprovalMode(option.value); },
      },
      h('span', { class: 'chat-approval-icon' }, chatApprovalIcon(option.value)),
      h('span', { class: 'chat-approval-copy' },
        h('span', { class: 'chat-approval-title', text: option.label }),
        h('span', { class: 'chat-approval-desc', text: option.description })),
      active ? svgIcon('chat-approval-check', 'M20 6 9 17l-5-5') : null);
    }),
  );
}

function setChatApprovalEnabled(chat, enabled) {
  const trigger = $('#chat-approval');
  if (!trigger) return;
  if (chat) chat.approvalMode = normalizeChatApprovalMode(chat.approvalMode);
  trigger.disabled = !enabled;
  if (!enabled) closeChatApproval();
  renderChatApprovalMenu(chat);
}

function toggleChatApproval() {
  const popover = $('#chat-approval-popover');
  const trigger = $('#chat-approval');
  if (!popover || !trigger || trigger.disabled) return;
  if (popover.hidden) {
    renderChatApprovalMenu();
    popover.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    closeChatOptions();
  } else {
    closeChatApproval();
  }
}

async function selectChatApprovalMode(value) {
  if (!state.chat) return;
  const chat = state.chat;
  const approvalMode = normalizeChatApprovalMode(value);
  if (approvalMode === chat.approvalMode) {
    closeChatApproval();
    return;
  }
  const appliesNextTurn = isChatRunning(chat);
  chat.approvalMode = approvalMode;
  renderChatApprovalMenu(chat);
  closeChatApproval();
  $('#chat-approval')?.focus();
  const previousUpdate = chat.approvalUpdateChain || Promise.resolve();
  const update = previousUpdate.catch(() => {}).then(() => api(chat.macId, 'chat/settings', {
    method: 'POST', headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, approvalMode }),
  }));
  chat.approvalUpdateChain = update;
  try {
    const result = await update;
    chat.approvalConfirmedMode = normalizeChatApprovalMode(result?.approvalMode || approvalMode);
    if (chat.approvalUpdateChain === update) {
      chat.approvalMode = chat.approvalConfirmedMode;
      if (state.chat === chat) renderChatApprovalMenu(chat);
      if (appliesNextTurn && state.chat === chat) toast('权限已更新，将从下一轮任务生效。');
    }
  } catch (e) {
    if (chat.approvalUpdateChain === update) {
      chat.approvalMode = normalizeChatApprovalMode(chat.approvalConfirmedMode);
      if (state.chat === chat) {
        renderChatApprovalMenu(chat);
        toast('权限更新失败：' + e.message, 'err');
      }
    }
  }
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
  closeChatApproval();
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
  chat.approvalMode = normalizeChatApprovalMode(resumed.approvalMode);
  chat.approvalConfirmedMode = chat.approvalMode;
  chat.approvalUpdateChain = Promise.resolve();

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
    setChatApprovalEnabled(chat, true);
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
    acknowledgeChatFollowups(chat, page.events);
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
  syncSessionRuntimeIndicators();
  es.onmessage = (e) => {
    if (state.chatCache.get(chat.cacheKey) !== chat) return;
    try {
      const ev = JSON.parse(e.data);
      updateChatUpdatedAt(chat, Date.now());
      const wasRunning = isChatRunning(chat);
      acknowledgeChatFollowups(chat, [ev]);
      chat.model = FleetChatModel.reduceChatEvent(chat.model, ev);
      applyChatMetadataDefaults(chat);
      syncSessionRuntimeIndicators();
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

async function submitChatInput({ forceQueue = false } = {}) {
  const chat = state.chat;
  const input = $('#chat-input');
  let raw = typeof input.value === 'string' ? input.value : '';
  if (!chat) return;
  if (!chat.skillsLoaded && chatSkillTokenNames(raw).length) {
    try {
      await loadChatSkills(chat);
    } catch (_) {
      if (state.chat === chat) toast('Skill 列表加载失败，消息未发送。请检查连接后重试。', 'err');
      return;
    }
    if (state.chat !== chat) return;
    raw = typeof input.value === 'string' ? input.value : '';
  }
  const parsed = chat.skillsLoaded
    ? parseChatSkillInput(raw, chat.skills, chat.skillPreferences)
    : { text: raw.trim(), skills: [] };
  const text = parsed.text;
  const pending = chat.attachments || [];
  if (pending.some((att) => att.uploading)) { toast('图片还在上传，稍等一下。'); return; }
  if (pending.some((att) => att.error || !att.id)) { toast('有图片上传失败，先移除或重新选择。', 'err'); return; }
  if (!text && pending.length === 0 && parsed.skills.length === 0) return;
  const images = pending.map((att) => ({
    localId: att.localId, id: att.id, name: att.name, mime: att.mime, size: att.size,
    url: att.url, previewUrl: att.previewUrl,
  }));
  input.value = '';
  chat.draft = '';
  resizeChatInput();
  chat.attachments = [];
  renderChatAttachments();
  closeChatSkillMenu();
  const item = {
    id: `follow-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    text, displayText: raw.trim(), images,
    skills: parsed.skills.map((skill) => ({ id: skill.id, name: skill.name })),
  };
  if (isChatRunning(chat)) {
    if (forceQueue) {
      enqueueChatFollowup(chat, item.text, item.images, item.id, item.skills, item.displayText);
      renderChatFollowups();
    } else {
      const steered = await sendChatSteer(chat, item);
      if (!steered) {
        enqueueChatFollowup(chat, item.text, item.images, item.id, item.skills, item.displayText);
        if (state.chat === chat) {
          renderChatFollowups();
          toast('追问未能插入当前任务，已保留到下一轮。', 'err');
        }
        if (!isChatRunning(chat)) flushChatFollowups(chat);
      }
    }
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
  if (chat.approvalMode) turnOptions.approvalMode = chat.approvalMode;
  return turnOptions;
}

async function sendChatTurn(chat, item, { restoreOnFailure = true } = {}) {
  const optimisticId = item.id || ('user-' + Date.now());
  chat.model = FleetChatModel.appendUserMessage(chat.model, (item.displayText || item.text).trim(), optimisticId, item.images);
  chat.loading = false;
  chat.submitting = true;
  syncSessionRuntimeIndicators();
  if (state.chat === chat) {
    renderChat();
    updateChatComposerState();
  }
  try {
    const started = await api(chat.macId, 'chat/input', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        assistant: 'codex', sessionId: chat.sessionId, clientMessageId: optimisticId,
        cwd: chat.cwd || '', text: item.text, skills: item.skills || [],
        images: item.images.map((img) => ({ id: img.id })), ...chatTurnOptions(chat),
      }),
    });
    if (!started || typeof started.turnId !== 'string' || !started.turnId.trim()) {
      throw new Error('Codex 未返回有效的任务 ID，消息未发送。');
    }
    chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'turn_started', turnId: started.turnId, data: { turnId: started.turnId } });
    return true;
  } catch (e) {
    chat.model = FleetChatModel.removeMessage(chat.model, optimisticId);
    chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'error', data: { message: e.message } });
    if (restoreOnFailure) restoreChatComposerItem(chat, item);
    if (state.chat === chat) {
      renderChat();
    }
    return false;
  } finally {
    chat.submitting = false;
    syncSessionRuntimeIndicators();
    if (state.chat === chat) updateChatComposerState();
  }
}

async function sendChatSteer(chat, item) {
  const clientId = item.id || `steer-${Date.now()}-${Math.random().toString(16).slice(2)}`;
  chat.model = FleetChatModel.appendSteeringMessage(
    chat.model, (item.displayText || item.text).trim(), clientId, item.images, chat.model.activeTurnId,
  );
  if (state.chat === chat) renderChat();
  try {
    const steered = await api(chat.macId, 'chat/steer', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        assistant: 'codex', sessionId: chat.sessionId, clientMessageId: clientId,
        cwd: chat.cwd || '', text: item.text, skills: item.skills || [],
        images: item.images.map((img) => ({ id: img.id })),
      }),
    });
    chat.model = FleetChatModel.reduceChatEvent(chat.model, {
      type: 'steer_accepted', itemId: clientId, turnId: steered.turnId,
      data: { clientId, turnId: steered.turnId },
    });
    if (state.chat === chat) renderChat();
    return true;
  } catch (e) {
    chat.model = FleetChatModel.reduceChatEvent(chat.model, {
      type: 'steer_failed', itemId: clientId, data: { clientId, message: e.message },
    });
    item.steerError = e.message;
    if (state.chat === chat) {
      renderChat();
    }
    return false;
  }
}

async function flushChatFollowups(chat) {
  if (!chat || chat.sendingFollowup || isChatRunning(chat) || !chat.followups?.length) return;
  chat.sendingFollowup = true;
  const item = chat.followups[0];
  const sent = await sendChatTurn(chat, item, { restoreOnFailure: false });
  if (sent) removeChatFollowup(chat, item.id);
  chat.sendingFollowup = false;
  if (state.chat === chat) renderChatFollowups();
}

async function guideChatFollowup(id) {
  const chat = state.chat;
  const item = chat?.followups?.find((entry) => entry.id === id);
  if (!chat || !item || item.guiding || !isChatRunning(chat)) return;
  item.guiding = true;
  renderChatFollowups();
  try {
    const sent = await sendChatSteer(chat, item);
    if (!sent) {
      item.guiding = false;
      renderChatFollowups();
      toast('引导失败，追问仍保留在队列：' + (item.steerError || '当前任务已经结束'), 'err');
      return;
    }
    removeChatFollowup(chat, id);
    renderChatFollowups();
    renderChat();
  } catch (e) {
    const pending = chat.followups?.find((entry) => entry.id === id);
    if (pending) {
      pending.guiding = false;
      renderChatFollowups();
      toast('引导失败：' + e.message, 'err');
    }
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
    if (e.code === 'no_active_turn') {
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'thread_status', data: { status: 'idle' } });
      syncSessionRuntimeIndicators();
      if (state.chat === chat) renderChat();
      toast('任务已结束，状态已同步。');
    } else {
      toast('停止失败：' + e.message, 'err');
    }
  } finally {
    chat.interrupting = false;
    updateChatComposerState();
  }
}

async function respondChatRequest(requestId, response) {
  const chat = state.chat;
  const request = chat?.model?.requests?.[String(requestId)];
  if (!chat || !requestId || request?.submitting) return;
  if (request) request.submitting = true;
  if (state.chat === chat) renderChat();
  try {
    await api(chat.macId, 'chat/respond', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant: 'codex', sessionId: chat.sessionId, requestId, response }),
    });
    chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'interaction_resolved', data: { requestId, response } });
    renderChat();
  } catch (e) {
    if (request) request.submitting = false;
    if (e.code === 'chat_request_not_found') {
      chat.model = FleetChatModel.reduceChatEvent(chat.model, { type: 'interaction_resolved', data: { requestId } });
      renderChat();
    }
    toast('提交失败：' + e.message, 'err');
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
  state.selectedSessionMacId = snap.cur ? snap.macId : null;
  state.selectedSessionAssistant = snap.cur ? state.assistant : null;
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
function updateDeviceScopeUI() {
  const onlineCount = MACS.filter((m) => state.nodes[m.id]).length;
  const sessionLabel = $('#session-device-label');
  const sessionMeta = $('#session-device-meta');
  if (sessionLabel) sessionLabel.textContent = state.sessionMacId === 'all' ? '全部设备' : macName(state.sessionMacId);
  if (sessionMeta) {
    sessionMeta.textContent = state.sessionMacId === 'all'
      ? `${onlineCount}/${MACS.length} 台在线`
      : (state.nodes[state.sessionMacId] ? '在线' : '离线');
  }
  const fileLabel = $('#file-device-label');
  const fileMeta = $('#file-device-meta');
  const fileAvatar = $('#file-device-avatar');
  if (fileLabel) fileLabel.textContent = state.fileMacId ? macName(state.fileMacId) : '选择设备';
  if (fileMeta) fileMeta.textContent = state.fileMacId
    ? (state.nodes[state.fileMacId] ? '在线 · 文件仅限当前设备' : '离线')
    : '文件仅浏览单台设备';
  if (fileAvatar) fileAvatar.textContent = state.fileMacId ? state.fileMacId.toUpperCase() : 'M';
  const statusDevice = $('#file-status-device');
  if (statusDevice) statusDevice.textContent = state.fileMacId ? macName(state.fileMacId) : '';
}

function openDevicePicker(context = 'sessions') {
  state.devicePickerContext = context;
  const title = context === 'new-session' ? '选择运行设备' : (context === 'files' ? '选择文件设备' : '选择会话设备');
  const subtitle = context === 'sessions'
    ? '可汇总全部在线设备的未归档会话'
    : (context === 'files' ? '文件操作一次只作用于一台 Mac' : '新会话需要在一台具体的 Mac 上运行');
  $('#device-modal-title').textContent = title;
  $('#device-modal-subtitle').textContent = subtitle;
  $('#device-search').value = '';
  renderDeviceOptions();
  openOverlay('device-modal');
  requestAnimationFrame(() => $('#device-search').focus());
}

function renderDeviceOptions() {
  const wrap = $('#device-options');
  if (!wrap) return;
  clear(wrap);
  const query = ($('#device-search')?.value || '').trim().toLocaleLowerCase();
  const context = state.devicePickerContext;
  const selected = context === 'sessions' ? state.sessionMacId : state.fileMacId;
  const addOption = ({ id, name, detail, online, all = false }) => {
    if (query && !`${name}\n${detail}`.toLocaleLowerCase().includes(query)) return;
    const choose = () => {
      if (context === 'sessions') setSessionDevice(id);
      else if (context === 'files') setFileDevice(id);
      else {
        activateConcreteMac(id);
        closeOverlay('device-modal');
        showProjects();
      }
    };
    const main = h('button', { type: 'button', class: 'device-option-main', onclick: choose },
      h('span', { class: 'device-option-avatar', text: all ? 'ALL' : id.toUpperCase() }),
      h('span', { class: 'device-option-copy' },
        h('strong', { text: name }),
        h('small', { text: detail }),
      ),
      h('span', { class: `device-option-state${online ? ' online' : ''}`, text: all ? '' : (online ? '在线' : '离线') }),
      id === selected ? svgIcon('device-option-check', 'M5 12l4 4L19 6') : h('span'),
    );
    const info = all ? h('span') : h('button', {
      type: 'button', class: 'iconbtn bare device-option-info', title: '设备设置', 'aria-label': `${name} 设置`,
      onclick: (event) => { event.stopPropagation(); closeOverlay('device-modal'); openHostModal(id); },
    }, svgIconParts('ic', [
      { tag: 'circle', attrs: { cx: '12', cy: '12', r: '9' } },
      { tag: 'path', attrs: { d: 'M12 11v5M12 8h.01' } },
    ]));
    wrap.append(h('div', { class: 'device-option', 'aria-current': String(id === selected) }, main, info));
  };
  if (context === 'sessions') {
    const online = MACS.filter((m) => state.nodes[m.id]).length;
    addOption({ id: 'all', name: '全部设备', detail: `${online}/${MACS.length} 台设备在线`, online: online > 0, all: true });
  }
  for (const mac of MACS) {
    addOption({
      id: mac.id,
      name: macName(mac.id),
      detail: context === 'sessions' ? `${state.counts[mac.id] || 0} 个近期会话` : '主目录与常用位置',
      online: !!state.nodes[mac.id],
    });
  }
  if (!wrap.children.length) wrap.append(h('div', { class: 'empty', text: '没有匹配的设备' }));
}

function requestNewSession() {
  if (state.sessionMacId === 'all') {
    openDevicePicker('new-session');
    return;
  }
  activateConcreteMac(state.sessionMacId);
  showProjects();
}

function filePreviewRoute(macId, path, embed = false) {
  const query = new URLSearchParams({ mac: macId, path });
  if (embed) query.set('embed', '1');
  return `${BASE}/view?${query.toString()}`;
}

function fileDownloadURL(macId, path) {
  return window.FleetPreview?.fileEndpoint('content', macId, path, { download: true })
    || `${apiBase(macId)}/api/file/content?path=${encodeURIComponent(path)}&download=1`;
}

function fileBaseName(path) {
  const parts = String(path || '').split('/').filter(Boolean);
  return parts.pop() || '文件';
}

function formatFileTime(ms) {
  const value = Number(ms);
  if (!value) return '—';
  const date = new Date(value);
  const sameYear = date.getFullYear() === new Date().getFullYear();
  return new Intl.DateTimeFormat('zh-CN', {
    year: sameYear ? undefined : 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(date);
}

function formatFileInfoTime(ms) {
  const value = Number(ms);
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: 'long', day: 'numeric',
    hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(new Date(value));
}

function filePreviewTypeLabel(entry) {
  const name = String(entry?.name || '');
  const nameExtension = name.includes('.') ? name.slice(name.lastIndexOf('.')) : '';
  const extension = String(entry?.extension || nameExtension).replace(/^\./, '').toUpperCase();
  const mime = String(entry?.mime || '').toLowerCase();
  let kind = '文件';
  if (mime.startsWith('image/')) kind = '图像';
  else if (mime.startsWith('audio/')) kind = '音频';
  else if (mime.startsWith('video/')) kind = '视频';
  else if (mime === 'application/pdf') kind = '文档';
  else if (mime.startsWith('text/') || ['application/json', 'application/xml'].includes(mime)) kind = '文本文档';
  return extension ? `${extension} ${kind}` : kind;
}

function filePreviewLocation(path, root) {
  const normalizedPath = String(path || '').replace(/\/+$/, '');
  const slash = normalizedPath.lastIndexOf('/');
  const parent = slash > 0 ? normalizedPath.slice(0, slash) : (slash === 0 ? '/' : '');
  const normalizedRoot = String(root || '').replace(/\/+$/, '') || '/';
  if (!parent) return '—';
  if (parent === normalizedRoot) return '~';
  if (normalizedRoot !== '/' && parent.startsWith(`${normalizedRoot}/`)) {
    return `~${parent.slice(normalizedRoot.length)}`;
  }
  return parent;
}

function fileColumnFromData(data) {
  return {
    path: data.path || data.root || '',
    parent: data.parent || '',
    entries: data.entries || [],
    selectedPath: '',
    loading: false,
    error: '',
  };
}

function truncateFileColumns(columns, columnIndex, selectedPath) {
  return (columns || []).slice(0, columnIndex + 1).map((column, index) =>
    index === columnIndex ? { ...column, selectedPath } : column);
}

function fileColumnRequestCurrent(request) {
  const column = state.fileColumns[request.columnIndex];
  return request.seq === fileColumnLoadSeq &&
    state.mode === 'files' &&
    state.fileView === 'columns' &&
    state.fileMacId === request.macId &&
    column?.selectedPath === request.path;
}

async function fetchFileDirectory(macId, path = '') {
  const query = new URLSearchParams();
  if (path) query.set('path', path);
  const queryString = query.toString();
  return api(macId, `file/list${queryString ? '?' + queryString : ''}`);
}

function applyFileDirectoryData(data, macId, { resetColumns = true, selectedPath = '' } = {}) {
  state.fileRoot = data.root || state.fileRoot || '';
  state.filePath = data.path || data.root || '';
  state.fileParent = data.parent || '';
  state.fileEntries = data.entries || [];
  state.fileLocations = data.locations || [];
  state.filePaths[macId] = state.filePath;
  state.fileSelectedPath = selectedPath;
  if (resetColumns) state.fileColumns = [fileColumnFromData(data)];
}

function syncFileStateFromColumn(column, selectedPath = '') {
  if (!column) return;
  state.filePath = column.path;
  state.fileParent = column.parent || '';
  state.fileEntries = column.entries || [];
  state.filePaths[state.fileMacId] = state.filePath;
  state.fileSelectedPath = selectedPath;
  persistUIState();
}

function loadFiles(opts = {}) {
  if (!state.fileMacId) {
    state.fileMacId = MACS.find((m) => state.nodes[m.id])?.id || MACS[0]?.id || null;
  }
  if (!state.fileMacId) {
    const list = $('#file-list');
    if (list) { clear(list); list.append(h('div', { class: 'file-empty', text: '暂无可用设备' })); }
    return;
  }
  state.macId = state.fileMacId;
  closeChatPane();
  stopWatch(); hideBanner();
  $('#app').classList.remove('term-open');
  state.current = null;
  for (const e of state.pool) e.iframe.classList.remove('show');
  $('#frame').classList.remove('show');
  $('#file-browser').hidden = false;
  $('#empty-state').hidden = true;
  $('#reconnect-btn').hidden = true;
  $('#fullscreen-btn').hidden = true;
  $('#mobile-input').hidden = true;
  updateDeviceScopeUI();
  const path = opts.clear ? '' : (state.filePath || state.filePaths[state.fileMacId] || '');
  loadFileDirectory(path, { fallback: true });
  closeMenus();
}

async function loadFileDirectory(path = '', opts = {}) {
  if (!state.fileMacId) return;
  const req = ++fileLoadSeq;
  fileColumnLoadSeq++;
  const macId = state.fileMacId;
  const replacePreviewHistory = opts.pushHistory && !!history.state?.fleet && !!history.state.filePreviewPath;
  if (replacePreviewHistory) closeFilePreview();
  state.fileLoading = true;
  const list = $('#file-list');
  syncFileViewUI();
  clear(list);
  list.append(h('div', { class: 'file-loading', text: '正在载入文件…' }));
  try {
    const data = await fetchFileDirectory(macId, path);
    if (req !== fileLoadSeq || state.fileMacId !== macId || state.mode !== 'files') return;
    applyFileDirectoryData(data, macId);
    state.fileLoading = false;
    persistUIState();
    renderFileBrowser();
    if (opts.pushHistory) {
      const historyState = { mode: 'files', fileMacId: macId, filePath: state.filePath, filePreviewPath: '' };
      if (replacePreviewHistory) replaceFleetHistory(historyState);
      else pushFleetHistory(historyState);
    } else if (history.state?.fleet && !history.state.filePreviewPath) {
      replaceFleetHistory({ mode: 'files', fileMacId: macId, filePath: state.filePath, filePreviewPath: '' });
    }
  } catch (error) {
    if (req !== fileLoadSeq || state.fileMacId !== macId) return;
    if (path && opts.fallback !== false) {
      state.filePath = '';
      loadFileDirectory('', { fallback: false });
      return;
    }
    state.fileLoading = false;
    clear(list);
    list.append(h('div', { class: 'file-empty' }, '无法读取文件', h('small', { text: error.message })));
    $('#file-count').textContent = '0 项';
  }
}

function renderFileBrowser() {
  renderFileLocations();
  renderFileBreadcrumbs();
  renderFileEntries();
  $('#file-folder-title').textContent = state.filePath === state.fileRoot ? '主目录' : fileBaseName(state.filePath);
  updateDeviceScopeUI();
}

function fileLocationIcon(id) {
  if (id === 'downloads') return svgIcon('ic', 'M12 3v12m-5-5 5 5 5-5M5 21h14');
  if (id === 'desktop') return svgIcon('ic', 'M4 4h16v12H4zM9 20h6M12 16v4');
  if (id === 'documents') return svgIcon('ic', 'M6 2h8l4 4v16H6zM14 2v5h5M9 12h6M9 16h6');
  if (id === 'projects') return svgIcon('ic', 'M3 7h7l2 2h9v10H3z');
  if (id.startsWith('favorite-')) return svgIcon('ic', 'M3 6.5A2.5 2.5 0 0 1 5.5 4H9l2 2h7.5A2.5 2.5 0 0 1 21 8.5v8A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5z');
  return svgIcon('ic', 'M3 11l9-8 9 8v10h-6v-6H9v6H3z');
}

function renderFileLocations() {
  const wrap = $('#file-locations');
  clear(wrap);
  for (const location of state.fileLocations) {
    const current = state.filePath === location.path || state.filePath.startsWith(location.path + '/');
    const button = h('button', { class: 'file-location', 'aria-current': String(current) },
      fileLocationIcon(location.id),
      h('span', { text: location.name }),
    );
    button.onclick = () => loadFileDirectory(location.path, { pushHistory: true, fallback: false });
    wrap.append(button);
  }
}

function renderFileBreadcrumbs() {
  const wrap = $('#file-breadcrumbs');
  clear(wrap);
  const root = state.fileRoot;
  const relative = state.filePath.startsWith(root) ? state.filePath.slice(root.length) : '';
  const parts = relative.split('/').filter(Boolean);
  let current = root;
  const appendCrumb = (name, path, last) => {
    const button = h('button', { class: 'file-crumb', text: name, 'aria-current': last ? 'page' : null });
    button.onclick = () => loadFileDirectory(path, { pushHistory: true, fallback: false });
    wrap.append(button);
  };
  appendCrumb('主目录', root, !parts.length);
  parts.forEach((part, index) => {
    current += '/' + part;
    wrap.append(h('span', { class: 'file-crumb-separator', text: '/' }));
    appendCrumb(part, current, index === parts.length - 1);
  });
  requestAnimationFrame(() => { wrap.scrollLeft = wrap.scrollWidth; });
}

function fileGlyph(entry) {
  const folder = entry.kind === 'folder';
  return h('span', { class: `file-glyph${folder ? ' folder' : (entry.previewable ? ' previewable' : '')}` },
    folder
      ? svgIcon('ic', 'M3 6.5A2.5 2.5 0 0 1 5.5 4H9l2 2h7.5A2.5 2.5 0 0 1 21 8.5v8a2.5 2.5 0 0 1-2.5 2.5h-13A2.5 2.5 0 0 1 3 16.5z')
      : svgIcon('ic', 'M6 2h8l4 4v16H6zM14 2v5h5'));
}

function closeFileMenus() {
  $$('.file-row-menu').forEach((menu) => {
    menu.hidden = true;
    $('.file-row-menu-trigger', menu.parentElement)?.setAttribute('aria-expanded', 'false');
  });
}

function filterFileEntries(entries, showHidden, query) {
  const needle = String(query || '').trim().toLocaleLowerCase();
  return (entries || []).filter((entry) =>
    (showHidden || !entry.hidden) &&
    (!needle || entry.name.toLocaleLowerCase().includes(needle)));
}

function syncFileViewUI() {
  const view = normalizeFileView(state.fileView);
  state.fileView = view;
  const main = $('.file-main');
  if (main) main.dataset.fileView = view;
  const head = $('.file-table-head');
  if (head) head.hidden = view !== 'list';
  const wrap = $('#file-list');
  if (wrap) {
    wrap.className = `file-list file-list-${view}`;
    wrap.setAttribute('role', 'listbox');
    wrap.setAttribute('aria-label', view === 'icons' ? '图标视图' : (view === 'columns' ? '分栏视图' : '列表视图'));
  }
  $$('[data-file-view]').forEach((button) => {
    button.setAttribute('aria-pressed', String(button.dataset.fileView === view));
  });
}

function ensureFileColumns() {
  if (state.fileColumns.length || !state.filePath) return;
  state.fileColumns = [{
    path: state.filePath,
    parent: state.fileParent,
    entries: state.fileEntries,
    selectedPath: '',
    loading: false,
    error: '',
  }];
}

function scrollFileColumnsToEnd() {
  const wrap = $('#file-list');
  if (!wrap || state.fileView !== 'columns') return;
  requestAnimationFrame(() => {
    wrap.scrollTo({ left: wrap.scrollWidth, behavior: isMobile() ? 'smooth' : 'auto' });
  });
}

function setFileView(view) {
  const next = normalizeFileView(view);
  if (next === state.fileView) {
    syncFileViewUI();
    return;
  }
  fileColumnLoadSeq++;
  while (state.fileColumns[state.fileColumns.length - 1]?.loading) state.fileColumns.pop();
  state.fileView = next;
  if (next === 'columns') {
    const last = state.fileColumns[state.fileColumns.length - 1];
    if (!last || last.path !== state.filePath) state.fileColumns = [];
    ensureFileColumns();
  }
  closeFileMenus();
  persistUIState();
  renderFileEntries();
  if (next === 'columns') scrollFileColumnsToEnd();
}

function fileEntryMenu(entry, columnIndex = null) {
  const selectColumn = () => {
    if (!Number.isInteger(columnIndex)) return;
    state.fileColumns = truncateFileColumns(state.fileColumns, columnIndex, entry.path);
    syncFileStateFromColumn(state.fileColumns[columnIndex], entry.path);
    renderFileBrowser();
  };
  const menuWrap = h('div', { class: 'file-row-menu-wrap' });
  const trigger = h('button', {
    type: 'button',
    class: 'iconbtn bare file-row-menu-trigger',
    title: '文件操作',
    'aria-label': `${entry.name} 操作`,
    'aria-haspopup': 'menu',
    'aria-expanded': 'false',
    onclick: (event) => {
      event.stopPropagation();
      const panel = $('.file-row-menu', menuWrap);
      const opening = panel.hidden;
      closeFileMenus();
      panel.hidden = !opening;
      trigger.setAttribute('aria-expanded', String(opening));
    },
  }, svgIconParts('ic', [
    { tag: 'circle', attrs: { cx: '5', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
    { tag: 'circle', attrs: { cx: '12', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
    { tag: 'circle', attrs: { cx: '19', cy: '12', r: '1', fill: 'currentColor', stroke: 'none' } },
  ]));
  const menu = h('div', { class: 'file-row-menu', role: 'menu', hidden: '' });
  if (entry.previewable) {
    menu.append(h('button', {
      type: 'button',
      role: 'menuitem',
      onclick: (event) => {
        event.stopPropagation();
        closeFileMenus();
        if (Number.isInteger(columnIndex)) activateFileEntry(entry, columnIndex);
        else openFilePreview(entry);
      },
    }, '预览'));
  }
  if (entry.kind === 'file') {
    menu.append(h('a', {
      href: fileDownloadURL(state.fileMacId, entry.path),
      download: entry.name,
      role: 'menuitem',
      onclick: (event) => event.stopPropagation(),
    }, '下载'));
  }
  menu.append(
    h('button', {
      type: 'button',
      role: 'menuitem',
      onclick: (event) => {
        event.stopPropagation();
        closeFileMenus();
        selectColumn();
        openFileNameModal('rename', entry);
      },
    }, '重命名'),
    h('button', {
      type: 'button',
      role: 'menuitem',
      class: 'danger',
      onclick: (event) => {
        event.stopPropagation();
        closeFileMenus();
        selectColumn();
        confirmFileDelete(entry);
      },
    }, '删除'),
  );
  menuWrap.append(trigger, menu);
  return menuWrap;
}

function fileEntryMeta(entry) {
  return entry.kind === 'folder' ? '文件夹' : `${formatBytes(entry.size)} · ${formatFileTime(entry.modifiedAt)}`;
}

function fileEntryKeydown(event) {
  if (event.key !== 'Enter' && event.key !== ' ') return;
  event.preventDefault();
  event.currentTarget.click();
}

function fileEmptyCopy(sourceEntries, query) {
  if (query) return '没有匹配的文件';
  return sourceEntries.length && !state.fileShowHidden ? '这个文件夹没有可见项目' : '这个文件夹是空的';
}

function updateFileCount(entries, visibleEntries) {
  $('#file-count').textContent = state.fileSearch && entries.length !== visibleEntries.length
    ? `${entries.length}/${visibleEntries.length} 项`
    : `${entries.length} 项`;
}

function activateFileEntry(entry, columnIndex = null) {
  if (Number.isInteger(columnIndex)) {
    if (entry.kind === 'folder') {
      openFileColumn(entry, columnIndex);
      return;
    }
    state.fileColumns = truncateFileColumns(state.fileColumns, columnIndex, entry.path);
    syncFileStateFromColumn(state.fileColumns[columnIndex], entry.path);
    renderFileBrowser();
  } else {
    state.fileSelectedPath = entry.path;
  }
  if (entry.kind === 'folder') loadFileDirectory(entry.path, { pushHistory: true, fallback: false });
  else if (entry.previewable) openFilePreview(entry);
  else renderFileEntries();
}

function renderFileList(wrap, entries) {
  for (const entry of entries) {
    const name = h('div', { class: 'file-name-cell', dataset: { mobileMeta: fileEntryMeta(entry) } },
      fileGlyph(entry),
      h('strong', { text: entry.name }),
    );
    const row = h('div', {
      class: `file-row${entry.hidden ? ' is-hidden' : ''}`,
      role: 'option',
      tabindex: '0',
      'aria-selected': String(entry.path === state.fileSelectedPath),
      onclick: () => activateFileEntry(entry),
      onkeydown: fileEntryKeydown,
    }, name,
    h('span', { class: 'file-row-time', text: formatFileTime(entry.modifiedAt) }),
    h('span', { class: 'file-row-size', text: entry.kind === 'folder' ? '—' : formatBytes(entry.size) }),
    fileEntryMenu(entry));
    wrap.append(row);
  }
}

function renderFileIcons(wrap, entries) {
  for (const entry of entries) {
    const item = h('div', {
      class: `file-icon-item${entry.hidden ? ' is-hidden' : ''}`,
      role: 'option',
      tabindex: '0',
      'aria-selected': String(entry.path === state.fileSelectedPath),
      onclick: () => activateFileEntry(entry),
      onkeydown: fileEntryKeydown,
    },
    fileGlyph(entry),
    h('strong', { text: entry.name }),
    h('span', { class: 'file-icon-meta', text: entry.kind === 'folder' ? '文件夹' : formatBytes(entry.size) }),
    fileEntryMenu(entry));
    wrap.append(item);
  }
}

function renderFileColumns(wrap) {
  ensureFileColumns();
  const columns = state.fileColumns;
  if (!columns.length) {
    wrap.append(h('div', { class: 'file-empty', text: '这个文件夹是空的' }));
    updateFileCount([], []);
    return;
  }
  columns.forEach((column, columnIndex) => {
    const last = columnIndex === columns.length - 1;
    const query = last ? state.fileSearch : '';
    const visibleEntries = filterFileEntries(column.entries, state.fileShowHidden, '');
    const entries = filterFileEntries(column.entries, state.fileShowHidden, query);
    const body = h('div', { class: 'file-column-body' });
    if (column.loading) {
      body.append(h('div', { class: 'file-column-message', text: '正在载入…' }));
    } else if (column.error) {
      body.append(h('div', { class: 'file-column-message' },
        '无法读取文件',
        h('small', { text: column.error }),
      ));
    } else if (!entries.length) {
      body.append(h('div', { class: 'file-column-message', text: fileEmptyCopy(column.entries, query) }));
    }
    for (const entry of entries) {
      const menu = fileEntryMenu(entry, columnIndex);
      const tail = h('div', { class: 'file-column-tail' },
        entry.kind === 'folder' ? svgIcon('file-column-chevron', 'm9 18 6-6-6-6') : null,
        menu,
      );
      const row = h('div', {
        class: `file-column-row${entry.hidden ? ' is-hidden' : ''}`,
        role: 'option',
        tabindex: '0',
        'aria-selected': String(entry.path === column.selectedPath),
        onclick: () => activateFileEntry(entry, columnIndex),
        onkeydown: fileEntryKeydown,
      },
      fileGlyph(entry),
      h('strong', { text: entry.name }),
      tail);
      body.append(row);
    }
    const label = column.path === state.fileRoot ? '主目录' : fileBaseName(column.path);
    const section = h('section', {
      class: 'file-column',
      'aria-label': label,
    },
    h('header', { class: 'file-column-head' },
      h('strong', { text: label }),
      h('span', { text: column.loading ? '' : `${entries.length} 项` }),
    ),
    body);
    wrap.append(section);
    if (last) updateFileCount(entries, visibleEntries);
  });
}

async function openFileColumn(entry, columnIndex) {
  if (!state.fileMacId || entry.kind !== 'folder') return;
  const current = state.fileColumns[columnIndex];
  const existing = state.fileColumns[columnIndex + 1];
  if (current?.selectedPath === entry.path && existing?.path === entry.path && !existing.loading && !existing.error) return;

  const macId = state.fileMacId;
  const seq = ++fileColumnLoadSeq;
  const request = { seq, macId, columnIndex, path: entry.path };
  const replacePreviewHistory = !!history.state?.fleet && !!history.state.filePreviewPath;
  if (replacePreviewHistory) closeFilePreview();
  state.fileColumns = truncateFileColumns(state.fileColumns, columnIndex, entry.path);
  syncFileStateFromColumn(state.fileColumns[columnIndex], entry.path);
  state.fileColumns.push({
    path: entry.path,
    parent: state.fileColumns[columnIndex]?.path || '',
    entries: [],
    selectedPath: '',
    loading: true,
    error: '',
  });
  renderFileBrowser();
  scrollFileColumnsToEnd();

  try {
    const data = await fetchFileDirectory(macId, entry.path);
    if (!fileColumnRequestCurrent(request)) return;
    state.fileColumns = [
      ...state.fileColumns.slice(0, columnIndex + 1),
      fileColumnFromData(data),
    ];
    applyFileDirectoryData(data, macId, { resetColumns: false, selectedPath: entry.path });
    persistUIState();
    renderFileBrowser();
    const historyState = { mode: 'files', fileMacId: macId, filePath: state.filePath, filePreviewPath: '' };
    if (replacePreviewHistory) replaceFleetHistory(historyState);
    else pushFleetHistory(historyState);
    scrollFileColumnsToEnd();
  } catch (error) {
    if (!fileColumnRequestCurrent(request)) return;
    state.fileColumns[columnIndex + 1] = {
      ...state.fileColumns[columnIndex + 1],
      loading: false,
      error: error.message,
    };
    renderFileEntries();
  }
}

function renderFileEntries() {
  const wrap = $('#file-list');
  syncFileViewUI();
  clear(wrap);
  if (state.fileView === 'columns') {
    renderFileColumns(wrap);
    return;
  }
  const visibleEntries = filterFileEntries(state.fileEntries, state.fileShowHidden, '');
  const entries = filterFileEntries(state.fileEntries, state.fileShowHidden, state.fileSearch);
  if (!entries.length) wrap.append(h('div', { class: 'file-empty', text: fileEmptyCopy(state.fileEntries, state.fileSearch) }));
  if (state.fileView === 'icons') renderFileIcons(wrap, entries);
  else renderFileList(wrap, entries);
  updateFileCount(entries, visibleEntries);
}

let fileSettingsTrigger = null;

function syncFileSettingsUI() {
  const input = $('#file-show-hidden');
  if (input) input.checked = state.fileShowHidden;
  const expanded = !$('#file-settings-menu')?.hidden;
  $$('.file-settings-trigger').forEach((button) => {
    button.setAttribute('aria-expanded', String(expanded));
  });
}

function closeFileSettings({ restoreFocus = false } = {}) {
  const menu = $('#file-settings-menu');
  if (!menu || menu.hidden) return;
  menu.hidden = true;
  syncFileSettingsUI();
  if (restoreFocus && fileSettingsTrigger?.isConnected) fileSettingsTrigger.focus();
}

function positionFileSettings(trigger) {
  const menu = $('#file-settings-menu');
  if (!menu || menu.hidden || !trigger) return;
  const rect = trigger.getBoundingClientRect();
  const gap = 6;
  const edge = 8;
  const width = menu.offsetWidth;
  const height = menu.offsetHeight;
  const left = Math.min(
    Math.max(edge, rect.right - width),
    Math.max(edge, window.innerWidth - width - edge),
  );
  const below = rect.bottom + gap;
  const top = below + height <= window.innerHeight - edge
    ? below
    : Math.max(edge, rect.top - height - gap);
  menu.style.left = `${Math.round(left)}px`;
  menu.style.top = `${Math.round(top)}px`;
}

function toggleFileSettings(trigger, event) {
  event?.stopPropagation();
  const menu = $('#file-settings-menu');
  if (!menu) return;
  const willOpen = menu.hidden;
  closeMenus();
  if (!willOpen) return;
  fileSettingsTrigger = trigger;
  menu.hidden = false;
  syncFileSettingsUI();
  positionFileSettings(trigger);
}

function showFilePreview(entry) {
  if (!entry?.previewable || !state.fileMacId) return;
  const deviceName = macName(state.fileMacId);
  state.fileSelectedPath = entry.path;
  state.filePreviewPath = entry.path;
  $('#file-preview-title').textContent = entry.name;
  $('#file-preview-subtitle').textContent = deviceName;
  $('#file-preview-type').textContent = filePreviewTypeLabel(entry);
  $('#file-preview-size').textContent = formatBytes(entry.size);
  $('#file-preview-modified').textContent = formatFileInfoTime(entry.modifiedAt);
  $('#file-preview-location').textContent = filePreviewLocation(entry.path, state.fileRoot);
  $('#file-preview-device').textContent = deviceName;
  $('#file-preview-frame').src = filePreviewRoute(state.fileMacId, entry.path, true);
  $('#file-preview-open').href = filePreviewRoute(state.fileMacId, entry.path, false);
  $('#file-preview-panel').hidden = false;
  $('.file-layout').classList.add('preview-open');
  renderFileEntries();
}

function openFilePreview(entry) {
  if (!entry?.previewable || !state.fileMacId) return;
  const replace = !!state.filePreviewPath;
  showFilePreview(entry);
  const historyState = {
    mode: 'files',
    fileMacId: state.fileMacId,
    filePath: state.filePath,
    filePreviewPath: entry.path,
  };
  if (replace) replaceFleetHistory(historyState);
  else pushFleetHistory(historyState);
}

function closeFilePreview() {
  state.filePreviewPath = '';
  const panel = $('#file-preview-panel');
  if (panel) panel.hidden = true;
  $('.file-layout')?.classList.remove('preview-open');
  const frame = $('#file-preview-frame');
  if (frame) frame.src = 'about:blank';
}

function dismissFilePreview() {
  const hasPreviewEntry = !!history.state?.fleet && !!history.state.filePreviewPath;
  closeFilePreview();
  if (!hasPreviewEntry) return;
  state.filePreviewDismissing = true;
  history.back();
  setTimeout(() => { state.filePreviewDismissing = false; }, 2000);
}

function openFileNameModal(mode, entry = null) {
  state.fileNameMode = mode;
  state.fileTarget = entry;
  const rename = mode === 'rename';
  $('#file-name-title').textContent = rename ? '重命名' : '新建文件夹';
  $('#file-name-submit').textContent = rename ? '保存' : '创建';
  $('#file-name-input').value = rename ? entry.name : '';
  openOverlay('file-name-modal');
  requestAnimationFrame(() => { $('#file-name-input').focus(); $('#file-name-input').select(); });
}

async function submitFileName(event) {
  event.preventDefault();
  const name = $('#file-name-input').value.trim();
  if (!name || !state.fileMacId) return;
  const rename = state.fileNameMode === 'rename';
  const path = rename ? state.fileTarget?.path : state.filePath;
  if (!path) return;
  try {
    await api(state.fileMacId, rename ? 'file/rename' : 'file/mkdir', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path, name }),
    });
    closeOverlay('file-name-modal');
    if (rename && state.filePreviewPath === path) closeFilePreview();
    toast(rename ? '已重命名' : '文件夹已创建', 'ok');
    loadFileDirectory(state.filePath, { fallback: false });
  } catch (error) {
    toast((rename ? '重命名失败：' : '创建失败：') + error.message, 'err');
  }
}

function confirmFileDelete(entry) {
  state.fileDeleteTarget = entry;
  $('#file-delete-copy').textContent = entry.kind === 'folder'
    ? `删除文件夹“${entry.name}”及其中的全部内容？此操作无法撤销。`
    : `删除文件“${entry.name}”？此操作无法撤销。`;
  openOverlay('confirm-file-delete');
}

async function deleteFileTarget() {
  const entry = state.fileDeleteTarget;
  closeOverlay('confirm-file-delete');
  if (!entry || !state.fileMacId) return;
  try {
    await api(state.fileMacId, 'file/delete', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path: entry.path, recursive: entry.kind === 'folder' }),
    });
    if (state.filePreviewPath === entry.path) closeFilePreview();
    state.fileDeleteTarget = null;
    toast('已删除', 'ok');
    loadFileDirectory(state.filePath, { fallback: false });
  } catch (error) {
    toast('删除失败：' + error.message, 'err');
  }
}

async function uploadFiles(files) {
  const selected = [...(files || [])];
  if (!selected.length || !state.fileMacId) return;
  let uploaded = 0;
  const failures = [];
  $('.file-layout')?.classList.add('is-uploading');
  for (const file of selected) {
    const body = new FormData();
    body.append('file', file, file.name);
    try {
      await api(state.fileMacId, `file/upload?path=${encodeURIComponent(state.filePath)}`, { method: 'POST', body });
      uploaded++;
    } catch (error) {
      failures.push(`${file.name}：${error.message}`);
    }
  }
  $('.file-layout')?.classList.remove('is-uploading');
  if (uploaded) toast(`已上传 ${uploaded} 个文件`, 'ok');
  if (failures.length) toast(failures[0] + (failures.length > 1 ? `，另有 ${failures.length - 1} 个失败` : ''), 'err');
  loadFileDirectory(state.filePath, { fallback: false });
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
function termSes(sessionId, title, macId = state.macId, assistant = state.assistant) {
  state.killTarget = sessionId;
  state.killAssistant = assistant;
  state.killMacId = macId;
  $('#ck-name').textContent = title || '该会话';
  openOverlay('confirm-kill');
}
async function closeSession() {
  const sid = state.killTarget;
  const assistant = state.killAssistant || state.assistant;
  const macId = state.killMacId || state.macId;
  closeOverlay('confirm-kill');
  if (!sid || !macId) return;
  try {
    const r = await api(macId, 'close', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ assistant, sessionId: sid }),
    });
    toast(r.killed ? '已终止该会话进程（会话保留）' : '该会话没有正在运行的控制台进程', r.killed ? 'ok' : 'info');
    // 终止后把该会话从池里移除（进程已结束，留着 iframe 只会停在 [exited]/Press ⏎ to Reconnect）。
    const ent = poolFind(macId, sid, assistant);
    if (ent) {
      const wasCurrent = ent === state.current;
      poolDrop(ent);
      if (wasCurrent) { $('#app').classList.remove('term-open'); restoreTermOrEmpty(); }
    }
  } catch (e) { toast('终止失败：' + e.message, 'err'); }
  state.killTarget = state.killAssistant = state.killMacId = null;
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
function closeMenus() {
  $('#usermenu').hidden = true;
  $('#m-menu').hidden = true;
  $$('.ses-menu').forEach((menu) => { menu.hidden = true; });
  closeFileSettings();
}
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
//  PWA 导航、离线状态、安装与外壳更新
// ============================================================
function fleetHistoryState(extra = {}) {
  return {
    fleet: true,
    mode: state.mode,
    fileMacId: state.fileMacId,
    filePath: state.filePath,
    filePreviewPath: state.filePreviewPath,
    term: $('#app')?.classList.contains('term-open') || false,
    ...extra,
  };
}

function pushFleetHistory(extra = {}) {
  try { history.pushState(fleetHistoryState(extra), '', location.href); } catch (_) {}
}

function replaceFleetHistory(extra = {}) {
  try { history.replaceState(fleetHistoryState(extra), '', location.href); } catch (_) {}
}

function initFleetHistory() {
  // 完整刷新不会恢复已打开的预览，因此把当前历史项同步为屏幕上的真实状态，
  // 避免 WebView 留着旧预览记录导致关闭按钮退回另一个预览。
  replaceFleetHistory();
  addEventListener('popstate', async (event) => {
    let target = event.state;
    if (!target?.fleet) return;
    if (state.filePreviewDismissing) {
      state.filePreviewDismissing = false;
      if (target.filePreviewPath) {
        target = { ...target, filePreviewPath: '' };
        replaceFleetHistory(target);
      }
    }
    if ($('#app').classList.contains('term-open') && !target.term) {
      backToList();
      return;
    }
    if (target.mode === 'files' && state.mode === 'files') {
      if (target.fileMacId && target.fileMacId !== state.fileMacId) {
        state.fileMacId = target.fileMacId;
        state.macId = target.fileMacId;
        state.filePath = state.filePaths[target.fileMacId] || '';
        renderHosts();
        updateDeviceScopeUI();
      }
      if (target.filePath !== undefined && target.filePath !== state.filePath) {
        await loadFileDirectory(target.filePath || '', { fallback: true });
      }
      if (target.filePreviewPath) {
        const entry = state.fileEntries.find((item) =>
          item.kind === 'file' && item.previewable && item.path === target.filePreviewPath);
        if (entry) showFilePreview(entry);
        else closeFilePreview();
      } else if (state.filePreviewPath) {
        closeFilePreview();
      }
    }
  });
}

function updateNetworkStatus({ refresh = false } = {}) {
  const online = navigator.onLine;
  const status = $('#network-status');
  if (status) status.hidden = online;
  const app = $('#app');
  if (app) app.dataset.network = online ? 'online' : 'offline';
  if (online && refresh) {
    if (state.mode === 'files') loadFileDirectory(state.filePath, { fallback: true });
    else loadSessions();
  }
}

function syncInstallActions() {
  const standalone = matchMedia('(display-mode: standalone)').matches || navigator.standalone === true;
  const isiOS = /iPad|iPhone|iPod/.test(navigator.userAgent || '');
  const available = !standalone && (!!state.deferredInstallPrompt || isiOS);
  $$('.pwa-install-action').forEach((button) => { button.hidden = !available; });
}

async function promptPWAInstall() {
  closeMenus();
  if (!state.deferredInstallPrompt) {
    toast('请在 Safari 的分享菜单中选择“添加到主屏幕”');
    return;
  }
  const prompt = state.deferredInstallPrompt;
  state.deferredInstallPrompt = null;
  await prompt.prompt();
  await prompt.userChoice.catch(() => null);
  syncInstallActions();
}

function initPWAExperience() {
  updateNetworkStatus();
  addEventListener('online', () => { updateNetworkStatus({ refresh: true }); toast('网络已恢复', 'ok'); });
  addEventListener('offline', () => updateNetworkStatus());
  addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault();
    state.deferredInstallPrompt = event;
    syncInstallActions();
  });
  addEventListener('appinstalled', () => {
    state.deferredInstallPrompt = null;
    syncInstallActions();
    toast('已安装到设备', 'ok');
  });
  syncInstallActions();
}

async function registerServiceWorker() {
  if (!('serviceWorker' in navigator)) return;
  try {
    const hadController = !!navigator.serviceWorker.controller;
    let reloadForUpdate = hadController;
    const registration = await navigator.serviceWorker.register(`${BASE}/sw.js`);
    const offerUpdate = (worker) => {
      if (!worker || !navigator.serviceWorker.controller) return;
      const banner = $('#pwa-update');
      if (banner) banner.hidden = false;
      const button = $('#pwa-update-now');
      if (button) button.onclick = () => worker.postMessage({ type: 'SKIP_WAITING' });
    };
    if (registration.waiting) offerUpdate(registration.waiting);
    registration.addEventListener('updatefound', () => {
      const worker = registration.installing;
      if (!worker) return;
      worker.addEventListener('statechange', () => {
        if (worker.state === 'installed') offerUpdate(worker);
      });
    });
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (reloadForUpdate) location.reload();
      reloadForUpdate = true;
    });
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') registration.update().catch(() => {});
    });
  } catch (_) {}
}

// ============================================================
//  初始化
// ============================================================
function init() {
  initTheme();
  if (window.FleetPreview?.isPreviewRoute()) {
    FleetPreview.initRoute();
    initPWAExperience();
    registerServiceWorker();
    return;
  }
  initUIState();
  initSessionListPreferences();
  initExperimentFlags();
  renderHosts();
  refreshNames();
  refreshSettings();
  refreshNodes(); setInterval(refreshNodes, 30000);
  setInterval(refreshSessionsSoft, 5000); // 轻量轮询 waiting / Codex 进行中状态（函数自带 mode/macId guard）
  wireMobileInput();

  // 模式 / 助手 / 搜索 / 新建
  // 注意 button[data-mode]：#app 本身带 data-mode（CSS 切栅格用），裸 [data-mode] 会把 #app 也选中，
  // 给根容器挂上 onclick → 点页面任意处都冒泡触发 setMode→loadSessions（每次点击闪一下）。
  $$('button[data-mode]').forEach((b) => b.onclick = () => setMode(b.dataset.mode));
  $$('[data-assistant]').forEach((b) => b.onclick = () => setAssistant(b.dataset.assistant));
  $('#session-groups').onscroll = maybeLoadMoreSessions;
  $('#session-search').oninput = (event) => {
    clearTimeout(sessionSearchTimer);
    sessionSearchTimer = setTimeout(() => {
      state.sessionSearch = event.target.value.trim();
      state.sessionResults = [];
      state.sessionCursors = {};
      loadSessions({ clear: true });
    }, 180);
  };
  $('#session-view-toggle').onclick = () => {
    state.sessionView = state.sessionView === 'project' ? 'recent' : 'project';
    updateSessionFilterUI();
    persistUIState();
    renderSessionResults();
  };
  $('#new-session').onclick = requestNewSession;
  $('#session-device-button').onclick = () => openDevicePicker('sessions');
  $('#file-device-button').onclick = () => openDevicePicker('files');
  $('#device-search').oninput = renderDeviceOptions;

  // 自绘文件浏览器
  $('#file-search').oninput = (event) => { state.fileSearch = event.target.value.trim(); renderFileEntries(); };
  $$('[data-file-view]').forEach((button) => {
    button.onclick = () => setFileView(button.dataset.fileView);
  });
  $('#file-new-folder').onclick = () => openFileNameModal('mkdir');
  $('#file-name-form').onsubmit = submitFileName;
  $('#file-delete-confirm').onclick = deleteFileTarget;
  $('#file-preview-close').onclick = dismissFilePreview;
  $('#file-preview-back').onclick = dismissFilePreview;
  const pickUpload = () => $('#file-upload-input').click();
  $('#file-upload').onclick = pickUpload;
  $$('.file-settings-trigger').forEach((button) => {
    button.onclick = (event) => toggleFileSettings(button, event);
  });
  $('#file-show-hidden').onchange = (event) => {
    state.fileShowHidden = event.target.checked;
    persistUIState();
    syncFileSettingsUI();
    renderFileEntries();
  };
  syncFileSettingsUI();
  $('#file-upload-input').onchange = (event) => {
    uploadFiles(event.target.files);
    event.target.value = '';
  };

  // 终端窗口
  $('#win-back').onclick = backToList;
  $('#reload-btn').onclick = doReload;
  $('#reload-dismiss').onclick = hideBanner;
  $('#reconnect-btn').onclick = () => { const f = curFrame(); try { f.contentWindow.location.reload(); } catch (_) { f.src = f.src; } };
  $('#fullscreen-btn').onclick = () => $('.win-body').requestFullscreen?.();
  $('#chat-composer').onsubmit = (e) => {
    e.preventDefault();
    const action = $('#chat-send').dataset.action;
    if (action === 'interrupt') interruptChat();
    else submitChatInput({ forceQueue: action === 'queue' });
  };
  $('#chat-attach').onclick = () => $('#chat-file').click();
  $('#chat-file').addEventListener('change', (e) => { addChatFiles(e.target.files); e.target.value = ''; });
  $('#chat-input').addEventListener('input', () => {
    saveChatDraft(); resizeChatInput(); updateChatComposerState(); updateChatSkillMenu();
  });
  $('#chat-input').addEventListener('click', updateChatSkillMenu);
  $('#chat-input').addEventListener('keyup', (e) => {
    if (['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) updateChatSkillMenu();
  });
  $('#chat-input').addEventListener('keydown', (e) => {
    const menu = state.chat?.skillMenu;
    if (menu && !$('#chat-skill-menu').hidden && !isIMEComposing(e, chatIMEComposing)) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault();
        const delta = e.key === 'ArrowDown' ? 1 : -1;
        menu.index = (menu.index + delta + menu.items.length) % menu.items.length;
        renderChatSkillMenu(state.chat);
        return;
      }
      if (e.key === 'Enter' || e.key === 'Tab') {
        e.preventDefault();
        selectChatSkill();
        return;
      }
      if (e.key === 'Escape') {
        e.preventDefault();
        closeChatSkillMenu();
        return;
      }
    }
    if (e.key === 'Enter' && !isIMEComposing(e, chatIMEComposing)) {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey) {
        e.preventDefault();
        submitChatInput({ forceQueue: true });
      } else if (!e.shiftKey) {
        e.preventDefault();
        submitChatInput({ forceQueue: $('#chat-send').dataset.action === 'queue' });
      }
    }
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
  $('#chat-approval').addEventListener('click', (e) => {
    e.stopPropagation();
    toggleChatApproval();
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
  $$('.mobile-menu-trigger').forEach((button) => {
    button.onclick = (event) => {
      $('#m-menu').classList.add('mobile-global-menu');
      toggleMenu('m-menu', event);
    };
  });
  $$('#usermenu button, #m-menu button').forEach((b) => {
    if (!b.dataset.act) return;
    b.onclick = () => {
      closeMenus();
      if (b.dataset.act === 'theme') toggleTheme();
      else if (b.dataset.act === 'settings') openSettings();
      else if (b.dataset.act === 'selfdraw') toggleSelfDraw();
      else if (b.dataset.act === 'install') promptPWAInstall();
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
    if (!e.target.closest('.menu') && !e.target.closest('#user-btn') && !e.target.closest('#m-menu-btn') &&
        !e.target.closest('.mobile-menu-trigger') && !e.target.closest('.file-settings-menu') &&
        !e.target.closest('.file-settings-trigger')) closeMenus();
    if (!e.target.closest('.file-row-menu-wrap')) closeFileMenus();
    if (!e.target.closest('#chat-approval-menu')) closeChatApproval();
    if (!e.target.closest('#chat-options')) closeChatOptions();
    if (!e.target.closest('#chat-skill-menu') && !e.target.closest('#chat-input')) closeChatSkillMenu();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    if (!$('#file-settings-menu').hidden) {
      closeFileSettings({ restoreFocus: true });
      return;
    }
    if (state.filePreviewPath) {
      dismissFilePreview();
      return;
    }
    if (!$('#chat-approval-popover').hidden) {
      closeChatApproval();
      $('#chat-approval').focus();
      return;
    }
    if (!$('#chat-options-popover').hidden) {
      closeChatOptions();
      $('#chat-options-trigger').focus();
    }
  });
  // 跨断点时同步移动输入坞可见性
  addEventListener('resize', () => {
    if (state.mode === 'sessions' && state.termSid) $('#mobile-input').hidden = !isMobile();
    if (!$('#file-settings-menu').hidden) positionFileSettings(fileSettingsTrigger);
    if (state.mode === 'files' && state.fileView === 'columns') scrollFileColumnsToEnd();
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

  updateSessionFilterUI();
  $$('[data-assistant]').forEach((b) => b.setAttribute('aria-selected', String(b.dataset.assistant === state.assistant)));
  initFleetHistory();
  setMode(state.mode);
  if (state.mode === 'sessions') restorePoolSnapshot();
  initPWAExperience();
  registerServiceWorker();
}
document.addEventListener('DOMContentLoaded', init);
