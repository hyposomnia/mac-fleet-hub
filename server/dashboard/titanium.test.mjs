import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
import { renderTitaniumCSS } from '../../scripts/titanium-design.mjs';

const design = JSON.parse(await readFile(new URL('./titanium.json', import.meta.url), 'utf8'));
const css = await readFile(new URL('./titanium.css', import.meta.url), 'utf8');
const appSource = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const themeSource = await readFile(new URL('./theme.js', import.meta.url), 'utf8');

test('web tokens match the shared native design resource', () => {
  assert.equal(css, renderTitaniumCSS(design));
  assert.deepEqual(design.radii, { compact: 8, control: 12, card: 16, panel: 20 });
  assert.deepEqual(design.typography, { caption: 11, secondary: 12, body: 13, title: 15, control: 16, display: 17, page: 28 });
  assert.deepEqual(design.navigation, { mobileBreakpoint: 860, compactDesktopBreakpoint: 1180, deviceWidth: 260, sessionWidth: 330, compactDeviceWidth: 220, compactSessionWidth: 310, collapsedDeviceWidth: 72 });
  assert.equal(design.light.accent, '#2C5D87');
  assert.equal(design.dark.accent, '#B8D9FF');
  const colorNames = Object.keys(design.light).filter((name) => design.light[name].startsWith('#'));
  for (const name of colorNames) {
    for (const palette of [design.light, design.dark]) assert.match(palette[name], /^#[0-9A-F]{6}$/);
  }
});

test('new clients default to light and explicit theme choices persist', () => {
  for (const saved of [null, 'invalid', 'light', 'dark', 'system']) {
    const storage = new Map(saved ? [['fleet-theme', saved]] : []);
    const attributes = {};
    const context = {
      document: { addEventListener() {}, querySelectorAll: () => [], documentElement: { setAttribute: (name, value) => { attributes[name] = value; }, getAttribute: (name) => attributes[name] }, querySelector: () => ({}) },
      getComputedStyle: () => ({ getPropertyValue: () => '#F7F9FB' }),
      localStorage: { getItem: (name) => storage.get(name), setItem: (name, value) => storage.set(name, value) },
      matchMedia: () => ({ matches: false, addEventListener() {} }), state: { pool: [] }, $$: () => [],
    };
    vm.createContext(context);
    vm.runInContext(themeSource, context);
    context.FleetTheme.refresh();
    assert.equal(attributes['data-theme'], saved === 'dark' || saved === 'system' ? 'dark' : 'light');
    context.FleetTheme.setPreference('system');
    assert.equal(storage.get('fleet-theme'), 'system');
    context.FleetTheme.setPreference('light');
    assert.equal(attributes['data-theme'], 'light');
  }
});

test('PWA branding and cache references include the Titanium resources', async () => {
  const manifest = JSON.parse(await readFile(new URL('./manifest.webmanifest', import.meta.url), 'utf8'));
  assert.equal(manifest.background_color, design.light.bg);
  assert.equal(manifest.theme_color, design.light.surface);
  const worker = await readFile(new URL('./sw.js', import.meta.url), 'utf8');
  assert.match(worker, /titanium\.css\?v=\d+/);
  assert.match(worker, /icons\/logo\.svg/);
  const icon = await readFile(new URL('./icons/icon.svg', import.meta.url), 'utf8');
  assert.match(icon, /<svg/);
  assert.doesNotMatch(icon, /linearGradient|rx=/);
});

test('an empty composer does not expand from placeholder wrapping in a collapsed WebView', () => {
  const input = { value: '', scrollHeight: 360, style: {} };
  const context = { $: selector => selector === '#chat-input' ? input : null };
  vm.createContext(context);
  vm.runInContext(appSource.slice(appSource.indexOf('function resizeChatInput()'), appSource.indexOf('function chatComposerAction(')), context);
  context.resizeChatInput();
  assert.equal(input.style.height, '0px');
  assert.equal(input.style.overflowY, 'hidden');
  input.value = 'A long draft';
  context.resizeChatInput();
  assert.equal(input.style.height, '180px');
  assert.equal(input.style.overflowY, 'auto');
  input.scrollHeight = 72;
  context.resizeChatInput();
  assert.equal(input.style.height, '72px');
  assert.equal(input.style.overflowY, 'hidden');
});
