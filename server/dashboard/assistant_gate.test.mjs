// DeepSeek 入口（第三个 assistant）的门控决策表。
//
// 这份测试直接**执行 app.js 里的真实函数**（按大括号配对把函数体抽出来在 Node 里跑），
// 而不是把逻辑重写一遍：重写只能证明我理解对了，证明不了上线的那份代码是对的。
// app.js 是浏览器脚本、不导出模块，所以这是在不改动它的前提下唯一能执行它的办法。
//
// 锁住的语义里有一条特别容易被改坏：Desktop 没跑（degraded）时，
//   - dshReady 必须为 false —— 会话列表还看得见，但发送/审批这些写操作不能放开；
//   - dshEnabled 必须仍为 true —— 降级时入口要留着并显示横幅，而不是整个入口消失。
// 这两者一个面向"能不能写"，一个面向"要不要显示"，混成同一个判断就会出问题。

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

const gateSource = ['assistantCapabilities', 'dshReady', 'dshEnabled']
  .map(extractFunction)
  .join('\n');

// state 是 app.js 的模块级对象；用最小桩替换掉浏览器环境。
globalThis.state = { assistant: 'codex', assistantInfo: {}, macId: 'm1' };
const gate = new Function(`${gateSource}; return { assistantCapabilities, dshReady, dshEnabled };`)();

// 真实 payload：旧 agent（没有 dsh 块）取自线上 /api/info 的实际返回。
const OLD_AGENT = {
  macIndex: '2', meshIP: '100.64.0.3', codexAppServerMode: 'shared', fileRoot: '/Users/hjc',
};
const DSH_OFF = { dsh: { enabled: false, degraded: true, hostRunning: false, installed: true } };
const DSH_DEGRADED = { dsh: { enabled: true, degraded: true, hostRunning: false, installed: true } };
const DSH_READY = { dsh: { enabled: true, degraded: false, hostRunning: true, installed: true } };

function evaluate(assistant, info) {
  globalThis.state.assistant = assistant;
  globalThis.state.assistantInfo = info ? { m1: info } : {};
  return {
    selfDraw: gate.assistantCapabilities(assistant, 'm1').selfDraw,
    tabVisible: gate.dshEnabled('m1'),
  };
}

test('旧 agent 不返回 dsh 块时，第三个入口保持隐藏', () => {
  const r = evaluate('dsh', OLD_AGENT);
  assert.equal(r.selfDraw, false);
  assert.equal(r.tabVisible, false, '旧 agent 下不该露出半成品入口');
});

test('新 agent 未启用 dsh 时入口保持隐藏', () => {
  const r = evaluate('dsh', DSH_OFF);
  assert.equal(r.selfDraw, false);
  assert.equal(r.tabVisible, false);
});

test('启用但 Desktop 未运行时：入口可见（带降级横幅）但不允许自绘写操作', () => {
  const r = evaluate('dsh', DSH_DEGRADED);
  assert.equal(r.tabVisible, true, '降级时入口要保留并提示，而不是整个消失');
  assert.equal(r.selfDraw, false, 'Desktop 没跑时不能放开发送/审批');
});

test('启用且就绪时：入口可见且允许自绘', () => {
  const r = evaluate('dsh', DSH_READY);
  assert.equal(r.tabVisible, true);
  assert.equal(r.selfDraw, true);
});

test('codex 与 claude 的能力判定不受 dsh 能力块影响', () => {
  const codex = evaluate('codex', DSH_READY);
  assert.equal(codex.selfDraw, true);

  const claude = evaluate('claude', DSH_READY);
  assert.equal(claude.selfDraw, false, 'Claude 仍走终端，不得自绘');

  // 注意两个轴是正交的：selfDraw 看的是"当前这个 assistant 能不能写"，
  // tabVisible 看的是"这台 Mac 上 dsh 是否可用"，与当前选中谁无关。
  const unknown = evaluate('gemini', DSH_READY);
  assert.equal(unknown.selfDraw, false, '未知 assistant 不该获得任何能力');
  assert.equal(unknown.tabVisible, true, 'tab 显隐只看该 Mac 的 dsh 可用性，不看当前选中谁');
});


// ---- 文案归属：能力化重构解决的是"分派"，不是"措辞" ----

function extractConst(name) {
  const re = new RegExp(`const ${name} = [^;]+;`);
  const match = appSrc.match(re);
  assert.ok(match, `app.js 里找不到常量 ${name}`);
  return match[0];
}

test('自绘对话的"连接中"文案随 assistant 走', () => {
  const consts = [extractConst('ASSISTANTS'), extractConst('ASSISTANT_LABELS')].join('\n');
  const fns = ['normalizeAssistant', 'assistantLabel', 'assistantConnectingText']
    .map(extractFunction)
    .join('\n');
  const api = new Function(`${consts}\n${fns}\nreturn { assistantConnectingText, assistantLabel };`)();

  assert.match(api.assistantConnectingText('codex'), /Codex app-server/);
  assert.equal(api.assistantConnectingText('dsh'), '正在连接 DeepSeek Harness…');
  // DSH 连的是 Desktop 已启动的 harness host，不是 app-server —— 术语不能串。
  assert.ok(!api.assistantConnectingText('dsh').includes('Codex'));
  assert.ok(!api.assistantConnectingText('claude').includes('Codex'));

  assert.equal(api.assistantLabel('dsh'), 'DeepSeek');
  assert.equal(api.assistantLabel('codex'), 'Codex');
});

test('共享对话界面里不再有写死的 Codex 文案', () => {
  // 这些字符串都曾在 DeepSeek tab 上原样出现过（用户实际抓到的是连接中那一句）。
  const forbidden = [
    '给 Codex 发送消息',
    'Codex 需要你的回答',
    "'Codex 会话'",
    "'Codex 图片'",
    "'Codex 未返回有效的会话 ID'",
  ];
  for (const literal of forbidden) {
    assert.ok(!appSrc.includes(literal), `仍有写死的 Codex 文案：${literal}`);
  }
  // "连接中"这句只允许作为字面量出现在它自己的函数里，不允许散在渲染代码中。
  // 只数带引号的形态：注释里提到这句话是正常的（本条注释就提到了）。
  const occurrences = appSrc.split("'正在连接 Codex app-server…'").length - 1;
  assert.equal(occurrences, 1, '连接中文案应只在 assistantConnectingText 内出现一次');
});
