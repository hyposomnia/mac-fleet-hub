import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const modelSrc = await readFile(new URL('./upload_model.js', import.meta.url), 'utf8');
const appSrc = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const styleCSS = await readFile(new URL('./style.css', import.meta.url), 'utf8');
const serviceWorker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');

// ---------- 模型层 ----------
const modelSandbox = { globalThis: {} };
vm.createContext(modelSandbox);
vm.runInContext(modelSrc, modelSandbox);
const M = modelSandbox.globalThis.FleetUploadModel;

const file = (name, size) => ({ name, size });
// vm 沙箱里造的数组/对象来自另一个 realm，deepEqual 会因原型不同而失败，统一过一遍 JSON。
const plain = (value) => JSON.parse(JSON.stringify(value));

test('入队保持顺序、带上目标设备与目录、忽略无名条目', () => {
  const queue = M.createQueue();
  const created = M.addFiles(queue, [file('a.dmg', 10), { name: '' }, file('b.zip', 20)],
    { macId: 'm2', path: '/Users/hjc/Downloads' });
  assert.equal(created.length, 2);
  assert.deepEqual(plain(queue.items.map((i) => i.name)), ['a.dmg', 'b.zip']);
  assert.deepEqual(plain(queue.items.map((i) => i.status)), ['pending', 'pending']);
  assert.equal(queue.items[0].macId, 'm2');
  assert.equal(queue.items[0].folder, 'Downloads');
  assert.equal(queue.items[0].size, 10);
  assert.notEqual(queue.items[0].id, queue.items[1].id);
});

test('同一时刻只放行一个上传（串行泵）', () => {
  const queue = M.createQueue();
  M.addFiles(queue, [file('a', 1), file('b', 1)], { macId: 'm1', path: '/tmp' });
  const first = M.nextPending(queue);
  assert.equal(first.name, 'a');
  assert.equal(M.beginItem(queue, first.id), true);
  // 上传中：不再放行任何待传项
  assert.equal(M.nextPending(queue), null);
  assert.equal(M.beginItem(queue, queue.items[1].id), false);
  M.finishItem(queue, first.id);
  assert.equal(M.nextPending(queue).name, 'b');
});

test('进度按已传/总量换算并夹在 0–100，总量未知时回落到文件大小', () => {
  const queue = M.createQueue();
  const [item] = M.addFiles(queue, [file('a.dmg', 1000)], { macId: 'm1', path: '/tmp' });
  M.beginItem(queue, item.id);
  assert.equal(M.setProgress(queue, item.id, 250, 1000), true);
  assert.equal(item.progress, 25);
  M.setProgress(queue, item.id, 4000, 1000);
  assert.equal(item.progress, 100);
  M.setProgress(queue, item.id, -5, 1000);
  assert.equal(item.progress, 0);
  // 浏览器拿不到 total 时用文件大小兜底，不会一直停在 0%
  M.setProgress(queue, item.id, 500, 0);
  assert.equal(item.progress, 50);
  // 未在传的条目不接受进度回写
  const [idle] = M.addFiles(queue, [file('b', 10)], { macId: 'm1', path: '/tmp' });
  assert.equal(M.setProgress(queue, idle.id, 5, 10), false);
  assert.equal(idle.progress, 0);
});

test('完成/失败都释放串行位，未知 id 静默忽略', () => {
  const queue = M.createQueue();
  const [a, b] = M.addFiles(queue, [file('a', 1), file('b', 1)], { macId: 'm1', path: '/tmp' });
  M.beginItem(queue, a.id);
  assert.equal(M.finishItem(queue, a.id), true);
  assert.equal(a.status, 'done');
  assert.equal(a.progress, 100);
  assert.equal(queue.activeId, '');
  M.beginItem(queue, b.id);
  assert.equal(M.failItem(queue, b.id, '同名文件已经存在。'), true);
  assert.equal(b.status, 'error');
  assert.equal(b.error, '同名文件已经存在。');
  assert.equal(queue.activeId, '');
  assert.equal(M.finishItem(queue, 'nope'), false);
  assert.equal(M.failItem(queue, 'nope', 'x'), false);
});

test('视图行按队列顺序给出名称/目录/大小/状态文案', () => {
  const queue = M.createQueue();
  const [a, b, c, d] = M.addFiles(queue,
    [file('a.dmg', 190468772), file('b.zip', 2048), file('c.txt', 5), file('d.mov', 99)],
    { macId: 'm2', path: '/Users/hjc/Downloads' });
  M.beginItem(queue, a.id);
  M.setProgress(queue, a.id, 190468772 / 2, 190468772);
  const rows = M.rows(queue);
  assert.deepEqual(plain(rows.map((r) => r.name)), ['a.dmg', 'b.zip', 'c.txt', 'd.mov']);
  assert.equal(rows[0].stateText, '50%');
  assert.equal(rows[0].tone, 'active');
  assert.equal(rows[0].folder, 'Downloads');
  assert.equal(rows[0].size, 190468772);
  assert.equal(rows[1].stateText, '等待');
  assert.equal(rows[1].tone, 'pending');
  M.beginItem(queue, b.id);
  M.failItem(queue, b.id, '同名文件已经存在。');
  M.beginItem(queue, c.id);
  M.finishItem(queue, c.id);
  const after = M.rows(queue);
  assert.equal(after[1].stateText, '同名文件已经存在。');
  assert.equal(after[1].tone, 'error');
  assert.equal(after[2].stateText, '完成');
  assert.equal(after[2].tone, 'done');
  assert.equal(after[2].percent, 100);
  assert.equal(d.status, 'pending');
});

test('汇总给面板头部用：计数、总进度、是否仍在忙、是否有失败', () => {
  const queue = M.createQueue();
  assert.deepEqual(plain(M.summary(queue)), {
    total: 0, counter: '0/0', activeName: '', percent: 0, busy: false, failed: 0, skipped: 0, hasRows: false,
  });
  const items = M.addFiles(queue, [file('a', 200), file('b', 200), file('c', 200)],
    { macId: 'm1', path: '/tmp' });
  assert.equal(M.summary(queue).counter, '0/3');
  assert.equal(M.summary(queue).busy, true);
  M.beginItem(queue, items[0].id);
  M.setProgress(queue, items[0].id, 100, 200);
  M.finishItem(queue, items[0].id);
  const mid = M.summary(queue);
  assert.equal(mid.counter, '1/3');
  assert.equal(mid.busy, true);
  assert.equal(mid.activeName, '');
  M.beginItem(queue, items[1].id);
  M.setProgress(queue, items[1].id, 100, 200);
  assert.equal(M.summary(queue).activeName, 'b');
  M.finishItem(queue, items[1].id);
  M.beginItem(queue, items[2].id);
  M.failItem(queue, items[2].id, '中断');
  const end = M.summary(queue);
  assert.equal(end.counter, '2/3');
  assert.equal(end.busy, false);
  assert.equal(end.failed, 1);
});

test('清除只摘掉已完成行，失败与待传行保留', () => {
  const queue = M.createQueue();
  const items = M.addFiles(queue, [file('a', 1), file('b', 1), file('c', 1)], { macId: 'm1', path: '/tmp' });
  M.beginItem(queue, items[0].id); M.finishItem(queue, items[0].id);
  M.beginItem(queue, items[1].id); M.failItem(queue, items[1].id, '断了');
  M.clearSettled(queue);
  assert.deepEqual(plain(queue.items.map((i) => i.name)), ['b', 'c']);
  assert.equal(M.removeItem(queue, items[2].id), true);
  assert.deepEqual(plain(queue.items.map((i) => i.name)), ['b']);
  assert.equal(M.basename('/Users/hjc/Downloads/'), 'Downloads');
  assert.equal(M.basename(''), '');
});

test('校验阶段先占住串行位，校验通过再转上传', () => {
  const queue = M.createQueue();
  const [a, b] = M.addFiles(queue, [file('a.dmg', 100), file('b.zip', 100)], { macId: 'm2', path: '/tmp' });
  assert.equal(M.beginItem(queue, a.id, 'checking'), true);
  assert.equal(a.status, 'checking');
  assert.equal(queue.activeId, a.id);
  assert.equal(M.nextPending(queue), null, '校验中也不许放行下一个');
  assert.equal(M.markUploading(queue, a.id), true);
  assert.equal(a.status, 'uploading');
  assert.equal(M.markUploading(queue, b.id), false, '没在传的条目不能直接转上传');
  M.finishItem(queue, a.id);
  assert.equal(M.nextPending(queue).name, 'b.zip');
});

test('同名跳过：标记 conflict、给出原因、释放串行位并计入汇总', () => {
  const queue = M.createQueue();
  const items = M.addFiles(queue, [file('a.dmg', 1), file('b.zip', 1)], { macId: 'm2', path: '/tmp' });
  M.beginItem(queue, items[0].id, 'checking');
  assert.equal(M.skipItem(queue, items[0].id, '已存在，已跳过'), true);
  assert.equal(items[0].status, 'conflict');
  assert.equal(items[0].reason, '已存在，已跳过');
  assert.equal(queue.activeId, '');
  const rows = M.rows(queue);
  assert.equal(rows[0].stateText, '已存在，已跳过');
  assert.equal(rows[0].tone, 'conflict');
  assert.equal(rows[0].percent, 0);
  const summary = M.summary(queue);
  assert.equal(summary.skipped, 1);
  assert.equal(summary.failed, 0);
  assert.equal(summary.busy, true, '还有待传项');
  M.beginItem(queue, items[1].id, 'checking');
  M.skipItem(queue, items[1].id, '已存在，已跳过');
  assert.equal(M.summary(queue).busy, false);
  assert.equal(M.summary(queue).skipped, 2);
});

test('同名比较在 macOS 默认的文件系统语义下不区分大小写', () => {
  assert.equal(M.sameName('A.dmg', 'a.DMG'), true);
  assert.equal(M.sameName('a.dmg', 'b.dmg'), false);
  assert.equal(M.sameName('', ''), false);
});

// ---------- app.js / 外壳契约 ----------
class FakeXHR {
  static instances = [];
  constructor() {
    this.upload = {};
    this.status = 0;
    this.responseText = '';
    FakeXHR.instances.push(this);
  }
  open(method, url) { this.method = method; this.url = url; }
  send(body) { this.body = body; this.sent = true; FakeXHR.last = this; }
  respond(status, body = '{}') { this.status = status; this.responseText = body; this.onload?.(); }
}

function testElement(tag) {
  return {
    nodeType: 1,
    tagName: tag,
    className: '',
    dataset: {},
    attributes: {},
    style: {},
    children: [],
    textContent: '',
    hidden: false,
    append(...nodes) { this.children.push(...nodes); },
    appendChild(node) { this.children.push(node); return node; },
    replaceChildren(...nodes) { this.children = [...nodes]; },
    setAttribute(name, value) { this.attributes[name] = String(value); },
    getAttribute(name) { return this.attributes[name] ?? null; },
    removeAttribute(name) { delete this.attributes[name]; },
    remove() { this.removed = true; },
    focus() { this.focused = true; },
    onclick: null,
  };
}

const timers = [];
const hosts = new Map();
function hostFor(selector) {
  if (!hosts.has(selector)) hosts.set(selector, testElement('div'));
  return hosts.get(selector);
}

const appSandbox = {
  document: {
    documentElement: { attributes: {}, style: {}, setAttribute() {}, getAttribute() { return null; } },
    addEventListener() {},
    createElement: testElement,
    createElementNS: (_ns, tag) => testElement(tag),
    createTextNode: (text) => ({ nodeType: 3, textContent: String(text), children: [] }),
    querySelector: (selector) => (
      ['#file-upload-queue', '#file-upload-queue-mobile', '#toast-wrap'].includes(selector)
        ? hostFor(selector) : null
    ),
    querySelectorAll: () => [],
  },
  XMLHttpRequest: FakeXHR,
  FormData: class {
    constructor() { this.entries = []; }
    append(key, value, name) { this.entries.push([key, name || value?.name || String(value)]); }
  },
  setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
  clearTimeout: (id) => { if (timers[id - 1]) timers[id - 1].cancelled = true; },
  EventSource: class { constructor(url) { this.url = url; } close() {} },
  FleetChatModel: {
    chatPhase: () => 'idle', createChatState: () => ({}), reduceChatEvent: (s) => s,
    removeMessage: (s) => s, appendUserMessage: (s) => s, prependHistory: (s) => s,
  },
  FleetUploadModel: M,
};
vm.createContext(appSandbox);
vm.runInContext(`${appSrc}
;globalThis.__uploadQueueTest = { enqueueFileUploads, renderFileUploadQueue, pumpFileUploads, state, scheduleFileUploadPanelHide: typeof scheduleFileUploadPanelHide === 'function' ? scheduleFileUploadPanelHide : null };`,
appSandbox);
const app = appSandbox.__uploadQueueTest;
// 目录刷新会去摸真实 DOM/网络，这里换成一个记录器，顺便验证「只在还停在该目录时刷新」。
const refreshes = [];
appSandbox.loadFileDirectory = (path) => { refreshes.push(path); };
// 上传前的同名校验会去问 agent 要目标目录清单，这里换成可控的假实现。
const remoteDirs = new Map();   // 'macId|path' -> [{ name, kind }]
const checks = [];
appSandbox.fetchFileDirectory = async (macId, path = '') => {
  checks.push(`${macId}|${path}`);
  return { entries: remoteDirs.get(`${macId}|${path}`) || [] };
};

function nodesWithClass(node, className, matches = []) {
  if (!node) return matches;
  const classes = node.className || node.attributes?.class || '';
  if (String(classes).split(/\s+/).includes(className)) matches.push(node);
  for (const child of node.children || []) nodesWithClass(child, className, matches);
  return matches;
}
function textOf(node) {
  if (!node) return '';
  return String(node.textContent || '') + (node.children || []).map(textOf).join('');
}
const rowTexts = (host) => nodesWithClass(host, 'file-upload-name').map((n) => n.textContent);
const stateTexts = (host) => nodesWithClass(host, 'file-upload-state').map((n) => n.textContent);

function resetQueue() {
  FakeXHR.instances.length = 0;
  timers.length = 0;
  refreshes.length = 0;
  checks.length = 0;
  remoteDirs.clear();
  app.state.fileUploads = M.createQueue();
  app.state.fileUploadPumping = false;
  app.state.fileUploadPanelOpen = false;
  app.state.mode = 'files';
  app.state.filePath = '/Users/hjc/Downloads';
  app.state.fileMacId = 'm2';
  for (const selector of ['#file-upload-queue', '#file-upload-queue-mobile']) clearHost(selector);
}

// 串行泵在 await 之后才推进，等一个宏任务再断言
const tick = () => new Promise((resolve) => setImmediate(resolve));

// 跑掉所有已安排的定时器（进度节流、目录刷新、面板自动收起）
function flushTimers(rounds = 8) {
  for (let round = 0; round < rounds; round++) {
    const pending = timers.filter((t) => !t.cancelled && !t.done);
    if (!pending.length) return;
    for (const timer of pending) { timer.done = true; timer.fn(); }
  }
}
function clearHost(selector) {
  const host = hostFor(selector);
  host.children = [];
  host.hidden = false;
}

test('选中多个文件后排成队列：只发一个请求，其余显示等待', async () => {
  resetQueue();
  app.enqueueFileUploads([file('a.dmg', 1000), file('b.zip', 2000), file('c.txt', 3000)]);
  await tick();   // 上传前先做同名校验，校验是异步的
  assert.equal(FakeXHR.instances.length, 1, '同一时刻只应有一个上传请求在跑');
  assert.match(FakeXHR.last.url, /\/m2\/api\/file\/upload\?path=%2FUsers%2Fhjc%2FDownloads$/);
  for (const selector of ['#file-upload-queue', '#file-upload-queue-mobile']) {
    const host = hostFor(selector);
    assert.equal(host.hidden, false, `${selector} 应显示`);
    assert.deepEqual(rowTexts(host), ['a.dmg', 'b.zip', 'c.txt']);
    assert.deepEqual(stateTexts(host), ['上传中', '等待', '等待']);
  }
});

test('进度事件实时反映到进度条与百分比，且请求完成后自动接下一个', async () => {
  resetQueue();
  app.enqueueFileUploads([file('a.dmg', 1000), file('b.zip', 2000)]);
  await tick();
  const first = FakeXHR.instances[0];
  first.upload.onprogress({ loaded: 500, total: 1000 });
  flushTimers();   // 进度回写是节流的，等一拍再断言
  const host = hostFor('#file-upload-queue');
  assert.deepEqual(stateTexts(host), ['50%', '等待']);
  const bar = nodesWithClass(host, 'file-upload-fill')[0];
  assert.equal(bar.attributes.style, 'width:50%');
  first.respond(201, '{"size":1000}');
  await tick();
  await tick();
  assert.equal(FakeXHR.instances.length, 2, '第一个完成后应自动开始第二个');
  assert.deepEqual(stateTexts(host), ['完成', '上传中']);
});

test('单个文件失败不阻塞队列，失败原因留在该行', async () => {
  resetQueue();
  app.enqueueFileUploads([file('a.dmg', 10), file('b.zip', 20)]);
  await tick();
  FakeXHR.instances[0].respond(409, '{"error":"file_exists","message":"同名文件已经存在。"}');
  await tick();
  await tick();
  assert.equal(FakeXHR.instances.length, 2);
  assert.deepEqual(stateTexts(hostFor('#file-upload-queue')), ['同名文件已经存在。', '上传中']);
  FakeXHR.instances[1].respond(500, '<html>nginx</html>');
  await tick();
  assert.deepEqual(stateTexts(hostFor('#file-upload-queue')), ['同名文件已经存在。', '上传失败（500）。']);
  // 全部结束但仍有一行失败：面板保留，供用户看清原因
  const host = hostFor('#file-upload-queue');
  assert.equal(host.hidden, false);
});

test('队列跑完且没有失败时自动收起面板', async () => {
  resetQueue();
  app.enqueueFileUploads([file('a.dmg', 10)]);
  await tick();
  FakeXHR.instances[0].respond(201, '{"size":10}');
  await tick();
  assert.equal(hostFor('#file-upload-queue').hidden, false, '跑完先留着让用户看到「完成」');
  flushTimers();
  assert.equal(hostFor('#file-upload-queue').hidden, true);
  assert.equal(hostFor('#file-upload-queue-mobile').hidden, true);
});

test('上传完成后只在还停在该目录时才刷新列表', async () => {
  resetQueue();
  app.enqueueFileUploads([file('a.dmg', 10), file('b.zip', 10)]);
  await tick();
  FakeXHR.instances[0].respond(201, '{"size":10}');
  await tick();
  await tick();
  flushTimers();
  assert.deepEqual(refreshes, ['/Users/hjc/Downloads']);
  // 用户在上传过程中翻到了别的目录：不再刷新（避免把当前视图拉回旧目录）
  refreshes.length = 0;
  app.state.filePath = '/Users/hjc/Documents';
  FakeXHR.instances[1].respond(201, '{"size":10}');
  await tick();
  await tick();
  flushTimers();
  assert.deepEqual(refreshes, []);
});

test('目标目录已有同名文件：先校验，不发一个字节，队列继续下一个', async () => {
  resetQueue();
  remoteDirs.set('m2|/Users/hjc/Downloads', [
    { name: 'dsh-desktop-mac-arm64.dmg', kind: 'file' },
    { name: 'Other', kind: 'folder' },
  ]);
  app.enqueueFileUploads([file('dsh-desktop-mac-arm64.dmg', 190468772), file('b.zip', 10)]);
  await tick();
  assert.deepEqual(checks, ['m2|/Users/hjc/Downloads', 'm2|/Users/hjc/Downloads'], '每个文件上传前各查一次目标目录');
  assert.equal(FakeXHR.instances.length, 1, '同名文件不该发起上传');
  assert.equal(FakeXHR.last.body.entries[0][1], 'b.zip', '第二个文件应当已开始上传');
  const host = hostFor('#file-upload-queue');
  assert.deepEqual(stateTexts(host), ['已存在，已跳过', '上传中']);
  assert.equal(nodesWithClass(host, 'file-upload-item')[0].dataset.status, 'conflict');
});

test('目录名相同也算同名（agent 用 O_EXCL 建文件，同名文件夹一样会 409）', async () => {
  resetQueue();
  remoteDirs.set('m2|/Users/hjc/Downloads', [{ name: 'lampp', kind: 'folder' }]);
  app.enqueueFileUploads([file('LAMPP', 10)]);
  await tick();
  assert.equal(FakeXHR.instances.length, 0);
  assert.deepEqual(stateTexts(hostFor('#file-upload-queue')), ['已存在，已跳过']);
});

test('有同名跳过时面板不自动收起，并提示跳过了几个', async () => {
  resetQueue();
  remoteDirs.set('m2|/Users/hjc/Downloads', [{ name: 'a.dmg', kind: 'file' }]);
  app.enqueueFileUploads([file('a.dmg', 10)]);
  await tick();
  flushTimers();
  assert.equal(hostFor('#file-upload-queue').hidden, false, '跳过也要让用户看见原因');
  const host = hostFor('#file-upload-queue');
  assert.match(textOf(host), /同名文件未上传：先在文件页删除它/, '跳过时要告诉用户怎么继续');
  assert.equal(nodesWithClass(host, 'file-upload-bar').length, 0, '跳过的行不画进度条');
  const toastText = textOf(hostFor('#toast-wrap'));
  assert.match(toastText, /同名已存在/);
});

test('校验失败（拿不到目录）不阻塞上传，仍交给服务端兜底', async () => {
  resetQueue();
  appSandbox.fetchFileDirectory = async () => { throw new Error('agent_unreachable'); };
  try {
    app.enqueueFileUploads([file('a.dmg', 10)]);
    await tick();
    assert.equal(FakeXHR.instances.length, 1, '拿不到清单时按老路子上传，由 409 兜底');
  } finally {
    appSandbox.fetchFileDirectory = async (macId, path = '') => {
      checks.push(`${macId}|${path}`);
      return { entries: remoteDirs.get(`${macId}|${path}`) || [] };
    };
  }
});

test('外壳契约：两个挂载点、资源版本一致、上传走 XHR 且不再用转圈光标', () => {
  assert.match(indexHTML, /<div id="file-upload-queue" class="file-upload-queue"[^>]*hidden/);
  assert.match(indexHTML, /<div id="file-upload-queue-mobile" class="file-upload-queue file-upload-queue-inline"[^>]*hidden/);
  const modelURL = indexHTML.match(/upload_model\.js\?v=([a-zA-Z0-9_-]+)/);
  assert.ok(modelURL, 'index.html 应引入 upload_model.js');
  assert.ok(indexHTML.indexOf('<script src="upload_model.js') < indexHTML.indexOf('<script src="app.js'));
  assert.match(serviceWorker, new RegExp(`/upload_model\\.js\\?v=${modelURL[1]}`));
  assert.match(serviceWorker, /const CACHE = 'fleet-shell-v124'/);
  assert.match(styleCSS, /\.file-upload-item\[data-status="conflict"\] \.file-upload-state \{ color: var\(--wait\); \}/);
  assert.match(appSrc, /new XMLHttpRequest\(\)/);
  assert.match(appSrc, /xhr\.upload\.onprogress/);
  assert.match(appSrc, /FleetUploadModel\.nextPending/);
  assert.doesNotMatch(appSrc, /is-uploading/);
  assert.doesNotMatch(styleCSS, /is-uploading/);
  assert.match(styleCSS, /\.file-upload-queue \{/);
  assert.match(styleCSS, /\.file-upload-queue\[hidden\] \{ display: none !important; \}/);
  assert.match(styleCSS, /\.file-upload-queue-inline \{ display: none; \}/);
  assert.match(styleCSS, /\.file-upload-fill \{/);
  assert.match(indexHTML, /<input id="file-upload-input" type="file" multiple hidden>/);
});
