// 「网关不可达」与「确实没有 Mac 入网」必须能被区分开。
//
// 起因是一次真实误判：网关整个宕机后，dashboard 左栏显示「暂无已入网的 Mac」，
// 看起来和「车队本来就是空的」一模一样，于是人跑去查终端/设备，而问题其实在网关那一层。
// 根因是 refreshNodes() 里 `if (!r.ok) return;` —— 失败路径静默返回，什么都没记。
//
// 这份测试直接执行 app.js 里的真实函数（沿用 assistant_gate.test.mjs 的抽函数手法），
// 锁住三件事：
//   1. HTTP 非 2xx 和 fetch 抛错都算「网关不可达」，不能静默吞；
//   2. 拉取成功要能恢复（否则一次抖动就永久卡在故障态）；
//   3. 两种空态渲染出的文案必须不同 —— 这是用户唯一能看到的差别，改坏了整个修复就白做。

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const appSrc = readFileSync(new URL('./app.js', import.meta.url), 'utf8');

function extractFunction(name) {
  const start = appSrc.indexOf(`function ${name}(`);
  assert.ok(start >= 0, `app.js 里找不到函数 ${name}`);
  let depth = 0;
  let started = false;
  for (let i = start; i < appSrc.length; i++) {
    const ch = appSrc[i];
    if (ch === '{') {
      depth++;
      started = true;
    } else if (ch === '}') {
      depth--;
      if (started && depth === 0) return appSrc.slice(start, i + 1);
    }
  }
  assert.fail(`函数 ${name} 的大括号没有闭合`);
}

// ---- 1. 可达性状态机 ----

// markGatewayUnreachable / markGatewayReachable 依赖 state + renderHosts + toast。
// 这里用最小桩，只关心状态怎么翻。
function buildGatewayState() {
  const calls = { renderHosts: 0 };
  const state = { gatewayDown: false, gatewayReason: '' };
  const renderHosts = () => { calls.renderHosts++; };
  const src = ['markGatewayUnreachable', 'markGatewayReachable']
    .map(extractFunction)
    .join('\n');
  const api = new Function('state', 'renderHosts', `${src}; return { markGatewayUnreachable, markGatewayReachable };`)(state, renderHosts);
  return { state, calls, ...api };
}

test('HTTP 非 2xx 记为网关不可达，并带上状态码', () => {
  const g = buildGatewayState();
  g.markGatewayUnreachable('HTTP 502');
  assert.equal(g.state.gatewayDown, true);
  assert.equal(g.state.gatewayReason, 'HTTP 502', '排障要能区分 5xx 和连不上');
});

test('网络错误（fetch 抛错）同样记为网关不可达', () => {
  const g = buildGatewayState();
  g.markGatewayUnreachable('Failed to fetch');
  assert.equal(g.state.gatewayDown, true);
});

test('恢复可达后故障态被清除', () => {
  const g = buildGatewayState();
  g.markGatewayUnreachable('HTTP 502');
  g.markGatewayReachable();
  assert.equal(g.state.gatewayDown, false);
  assert.equal(g.state.gatewayReason, '');
});

test('重复同一原因不重复重渲染（30s 轮询不该每轮刷屏）', () => {
  const g = buildGatewayState();
  g.markGatewayUnreachable('HTTP 502');
  const after1 = g.calls.renderHosts;
  g.markGatewayUnreachable('HTTP 502');
  assert.equal(g.calls.renderHosts, after1, '状态没变就不该再渲染');
  g.markGatewayUnreachable('Failed to fetch');
  assert.equal(g.calls.renderHosts, after1 + 1, '原因变了说明情况在变，要重渲染');
});

test('本来就是好的，再报可达不触发多余渲染', () => {
  const g = buildGatewayState();
  g.markGatewayReachable();
  assert.equal(g.calls.renderHosts, 0);
  assert.equal(g.state.gatewayDown, false);
});

// ---- 2. 失败路径真的被接住了（守住最初的 bug） ----

test('refreshNodes 的失败分支不再静默 return', () => {
  const fn = extractFunction('refreshNodes');
  assert.ok(/markGatewayUnreachable\(/.test(fn), 'r.ok 为假时必须记状态');
  assert.ok(/catch\s*\(e\)\s*{[\s\S]*markGatewayUnreachable\(/.test(fn), 'catch 里也必须记状态，不能 catch(_){} 吞掉');
  assert.ok(!/catch\s*\(\s*_\s*\)\s*{\s*}/.test(fn), '不许再出现空 catch');
  assert.ok(/markGatewayReachable\(/.test(fn), '成功路径要能清除故障态');
});

test('可达性判定发生在 JSON 解析之后（网关有响应就不算连不上）', () => {
  const fn = extractFunction('refreshNodes');
  const okIdx = fn.indexOf('if (!r.ok)');
  const parseIdx = fn.indexOf('await r.json()');
  const reachIdx = fn.indexOf('markGatewayReachable()');
  assert.ok(okIdx >= 0 && parseIdx > okIdx, '先判 r.ok 再解析');
  assert.ok(reachIdx > parseIdx, '解析成功才算网关活着，避免数据形状问题被误报成断连');
});

// ---- 3. 两种空态的文案必须不同（用户唯一能看到的差别） ----

function renderEmptyState({ gatewayDown, mode = 'sessions' }) {
  const created = [];
  const el = (tag) => {
    const node = {
      tag, children: [], attrs: {}, className: '', textContent: '',
      set className(v) { this.attrs.class = v; },
      get className() { return this.attrs.class || ''; },
      append(...kids) { for (const k of kids) this.children.push(k); },
      setAttribute(k, v) { this.attrs[k] = v; },
    };
    created.push(node);
    return node;
  };
  const h = (tag, props, ...kids) => {
    const node = el(tag);
    if (props) for (const k in props) {
      if (k === 'class') node.attrs.class = props[k];
      else if (k === 'text') node.textContent = props[k];
      else if (k === 'type') node.attrs.type = props[k];
      else if (k === 'dataset') node.attrs.dataset = props[k];
      else node.attrs[k] = props[k];
    }
    node.append(...kids.flat().filter((c) => c != null && c !== false));
    return node;
  };
  const clear = (n) => { n.children = []; };
  const $ = () => el('div');
  const MACS = [];
  const state = { gatewayDown, nodes: {}, mode };
  const toast = () => {};
  const refreshNodes = () => {};
  const src = ['renderHosts'].map(extractFunction).join('\n');
  const stubs = `
    const svgIcon = () => ({ nodeType: 1 });
    const svgIconParts = () => ({ nodeType: 1 });
    const syncDshNativeButtons = () => {};
    const updateDeviceScopeUI = () => {};
    const macName = (id) => id;
    const setSessionDevice = () => {};
    const openHostModal = () => {};
    const retryNodes = () => {};
    const h = arguments[0], clear = arguments[1], $ = arguments[2];
    const MACS = arguments[3], state = arguments[4], toast = arguments[5], refreshNodes = arguments[6];
  `;
  const fn = new Function(`${stubs}\n${src}\nreturn renderHosts;`)(h, clear, $, MACS, state, toast, refreshNodes);
  fn();
  return { created, state };
}

function flattenText(node, out = []) {
  if (!node) return out;
  if (node.textContent) out.push(node.textContent);
  for (const c of node.children || []) flattenText(c, out);
  return out;
}

test('网关不可达时空态提示「服务器不可用」，而不是「暂无已入网的 Mac」', () => {
  const { created } = renderEmptyState({ gatewayDown: true });
  const texts = created.flatMap((n) => flattenText(n)).join(' | ');
  assert.match(texts, /无法连接服务器/, '必须明确说是服务器/网关的问题');
  assert.match(texts, /不代表没有 Mac 入网/, '要点破这个最容易被误读的结论');
  assert.ok(!/暂无已入网的 Mac/.test(texts), '故障态绝不能复用「没有 Mac」的文案');
});

test('网关正常但没有 Mac 时，仍是原来的中性文案', () => {
  const { created } = renderEmptyState({ gatewayDown: false });
  const texts = created.flatMap((n) => flattenText(n)).join(' | ');
  assert.match(texts, /暂无已入网的 Mac/);
  assert.ok(!/无法连接服务器/.test(texts), '正常空态不该吓唬人');
});

test('故障态提供重试按钮，且点击会重新拉取', () => {
  const { created } = renderEmptyState({ gatewayDown: true });
  const all = [];
  const walk = (n) => { all.push(n); (n.children || []).forEach(walk); };
  created.forEach(walk);
  const retry = all.find((n) => n.textContent === '重试');
  assert.ok(retry, '故障态要给出可操作的出口，不能只报错');
  assert.equal(typeof retry.onclick, 'function', '重试按钮必须真的能点');
});

test('故障态用 danger 色系，和正常空态视觉上分开', () => {
  const { created } = renderEmptyState({ gatewayDown: true });
  const all = [];
  const walk = (n) => { all.push(n); (n.children || []).forEach(walk); };
  created.forEach(walk);
  assert.ok(all.some((n) => (n.attrs.class || '').includes('empty-down')), '故障态需要独立的样式钩子');
  const title = all.find((n) => n.textContent === '⚠ 无法连接服务器');
  assert.ok(title && (title.attrs.class || '').includes('ed-t'));
});
