import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';

const app = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const markup = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const harnessSource = `
  const BASE = '';
  let automationAccessKeys = [];
  const calls = { edits: [], clipboard: [], prompts: [] };
  const $ = selector => document.querySelector(selector);
  const toast = () => {};
  const settingsDate = value => value || '—';
  const accessKeyBindingLabel = binding => binding?.label || '范围：全部设备 · 全部客户端 · 全部项目 · 全部会话';
  const openAccessKeyForm = key => calls.edits.push(key.id);
  ${app.slice(app.indexOf('function h('), app.indexOf('// 内联 SVG'))}
  ${app.slice(app.indexOf('function renderAccessKeys('), app.indexOf('function accessKeyClientName('))}
  ${app.slice(app.indexOf('async function copyAccessKeyValue('), app.indexOf('function formatBytes('))}
  return { calls, accessKeyCurl, render(keys) { automationAccessKeys = keys; renderAccessKeys(); } };
`;

function syntheticKey(overrides = {}) {
  return {
    id: 'synthetic-card', name: '自动化测试', recoverable: true,
    key: 'mfh_live_synthetic_only_not_a_real_credential_1234', rpm: 25,
    created_at: '2026-10-15', last_used_at: null, ...overrides,
  };
}

class Element {
  constructor(tag = 'div') { this.tagName = tag; this.nodeType = 1; this.children = []; this.attributes = {}; this.dataset = {}; }
  setAttribute(name, value) { this.attributes[name] = String(value); }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  set textContent(value) { this.children = [String(value)]; }
  get textContent() { return this.children.map(child => typeof child === 'string' ? child : child.textContent).join(''); }
}

function fixture() {
  const root = new Element();
  const document = { querySelector: () => root, createElement: tag => new Element(tag), createTextNode: value => String(value) };
  const clipboard = [];
  const navigator = { clipboard: { writeText: async value => { clipboard.push(value); } } };
  const prompts = [];
  const window = { location: { origin: 'https://fleet.example.test' }, prompt: (...args) => { prompts.push(args); } };
  return { ...new Function('document', 'navigator', 'window', harnessSource)(document, navigator, window), root, clipboard, prompts, navigator };
}

test('cards show name, last use and actions without credentials or scope metadata', async () => {
  const current = fixture();
  const key = syntheticKey();
  current.render([key]);
  const card = current.root.children[0];
  const [head, row] = card.children;
  assert.equal(head.children[0].textContent, key.name);
  assert.equal(head.children[1].textContent, '编辑');
  assert.deepEqual(row.children.map(child => child.tagName), ['span', 'button', 'a']);
  assert.equal(row.children[0].textContent, '最近使用：—');
  assert.equal(row.children[1].textContent, '复制密钥');
  assert.match(row.children[1].className, /automation-doc-link/);
  assert.doesNotMatch(card.textContent, /mfh_live_|••••••|范围：|RPM：|创建：/);
  assert.ok(!JSON.stringify(card).includes(key.key), 'full credential must stay outside the DOM');
  await row.children[1].onclick();
  await row.children[2].onclick({ preventDefault() {} });
  assert.ok(current.clipboard[0] === key.key, 'copy must retain the full credential');
  assert.ok(current.clipboard[1] === current.accessKeyCurl(key, 'https://fleet.example.test'), 'request copy must retain the full curl command');
  assert.ok(current.clipboard[1].includes(`Authorization: Bearer ${key.key}`), 'curl must authorize with the full credential');
  head.children[1].children[0].onclick();
  assert.deepEqual(current.calls.edits, [key.id]);
});

test('unrecoverable or missing values disable both copies and preserve editing', () => {
  const current = fixture();
  for (const key of [syntheticKey({ recoverable: false }), syntheticKey({ key: '' })]) {
    current.render([key]);
    const card = current.root.children[0];
    const [, copy, request] = card.children[1].children;
    assert.match(copy.attributes.title, /原文不可恢复/);
    assert.equal(copy.attributes.disabled, '');
    assert.equal(request.attributes['aria-disabled'], 'true');
    assert.equal(request.attributes.tabindex, '-1');
    copy.onclick();
    request.onclick({ preventDefault() {} });
    card.children[0].children[1].children[0].onclick();
  }
  assert.equal(current.clipboard.length, 0);
  assert.equal(current.calls.edits.length, 2);
  current.render([]);
  assert.equal(current.root.textContent, '尚未创建访问密钥');
});

test('clipboard fallback keeps the full value and alias request with shell quoting', async () => {
  const current = fixture();
  const key = syntheticKey({ name: "release's automation", binding: {
    device_id: 'm2', ai_client: 'codex', project_path: '/synthetic/project', session_id: 'synthetic-session',
  } });
  current.navigator.clipboard.writeText = async () => { throw new Error('clipboard unavailable'); };
  current.render([key]);
  const row = current.root.children[0].children[1];
  await row.children[1].onclick();
  await row.children[2].onclick({ preventDefault() {} });
  assert.ok(current.prompts[0][1] === key.key, 'fallback must copy the complete credential');
  const request = current.prompts[1][1];
  assert.ok(request.includes(`Authorization: Bearer ${key.key}`), 'fallback request must retain authorization');
  assert.ok(request.includes('release\'\\\'\'s automation'), 'alias must remain shell quoted');
  assert.ok(request.includes('/api/v1/messages'), 'request must target the message API');
});

test('browser: cards fit 320/390/430/1440 widths and copy complete synthetic values', { skip: !process.env.ACCESS_KEY_PLAYWRIGHT_MODULE }, async () => {
  const { chromium } = await import(pathToFileURL(process.env.ACCESS_KEY_PLAYWRIGHT_MODULE).href);
  const modal = markup.slice(markup.indexOf('<div id="fleet-settings-modal"'), markup.indexOf('<div id="device-modal"'));
  const assets = new Map(await Promise.all(['style.css', 'account.css'].map(async name =>
    [`/${name}`, await readFile(new URL(`./${name}`, import.meta.url), 'utf8')])));
  const server = createServer((request, response) => {
    if (assets.has(request.url)) {
      response.writeHead(200, { 'Content-Type': 'text/css' });
      response.end(assets.get(request.url));
    } else {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      response.end(`<!doctype html><html lang="zh-CN"><head><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/account.css"></head><body>${modal}</body></html>`);
    }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  let browser;
  try {
    browser = await chromium.launch({ executablePath: process.env.ACCESS_KEY_CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', headless: true });
    const page = await browser.newPage();
    const errors = [];
    page.on('pageerror', () => errors.push('pageerror'));
    const key = syntheticKey({ name: 'LongUnbrokenName'.repeat(20), binding: { label: `范围：${'LongUnbrokenScope/'.repeat(40)}` } });
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.evaluate(source => {
      const clipboard = { writeText: async value => { window.cardHarness.calls.clipboard.push(value); } };
      const testWindow = { location: window.location, prompt: (...args) => { window.cardHarness.calls.prompts.push(args); } };
      window.cardHarness = new Function('document', 'navigator', 'window', source)(document, { clipboard }, testWindow);
      document.querySelector('#fleet-settings-modal').hidden = false;
      document.querySelector('#automation-modal').hidden = false;
    }, harnessSource);
    await page.evaluate(key => window.cardHarness.render([key, { ...key, id: 'legacy', recoverable: false }]), key);
    for (const theme of ['light', 'dark']) {
      await page.evaluate(theme => document.documentElement.dataset.theme = theme, theme);
      for (const width of [320, 390, 430, 1440]) {
        await page.setViewportSize({ width, height: 900 });
        const layout = await page.evaluate(() => {
          const card = document.querySelector('.automation-key-card');
          const head = card.querySelector('.automation-key-head');
          const row = card.querySelector('.automation-key-copy-row');
          const title = head.querySelector('strong').getBoundingClientRect();
          const edit = head.querySelector('button').getBoundingClientRect();
          const children = [...row.children].map(element => element.getBoundingClientRect());
          const scroller = document.querySelector('.fleet-settings-scroll');
          return {
            fits: [document.documentElement, scroller, ...document.querySelectorAll('.automation-key-card')].every(element => element.scrollWidth <= element.clientWidth + 1),
            headFits: title.right <= edit.left && Math.abs(title.top + title.height / 2 - edit.top - edit.height / 2) < 2,
            rowFits: children.every(rect => Math.abs(rect.top + rect.height / 2 - children[0].top - children[0].height / 2) < 2) && children.every((rect, index) => !index || children[index - 1].right <= rect.left),
            order: head.getBoundingClientRect().bottom <= row.getBoundingClientRect().top,
          };
        });
        for (const [check, passed] of Object.entries(layout)) assert.ok(passed, `${theme} ${width}px: ${check}`);
        assert.ok(await page.evaluate(secret => !document.documentElement.outerHTML.includes(secret), key.key), 'full credential must not enter DOM text or attributes');
      }
    }
    const card = page.locator('.automation-key-card').first();
    await card.locator('.automation-key-copy-row button').click();
    await card.locator('.automation-key-request-link').click();
    await card.locator('.automation-key-edit').click();
    const copied = await page.evaluate(key => ({
      value: window.cardHarness.calls.clipboard[0] === key.key,
      request: window.cardHarness.calls.clipboard[1] === window.cardHarness.accessKeyCurl(key, location.origin),
      edit: window.cardHarness.calls.edits[0] === key.id,
    }), key);
    assert.ok(copied.value && copied.request && copied.edit, 'browser copy/request/edit actions must preserve their payloads');
    assert.equal(errors.length, 0);
  } finally {
    if (browser) await browser.close();
    await new Promise(resolve => server.close(resolve));
  }
});
