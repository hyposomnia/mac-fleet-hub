import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./native_appearance.js', import.meta.url), 'utf8');

function harness(native = true, pathname = '/') {
  const messages = [];
  const colors = { '--accent': '#123456', '--bg': '#FAFAFA', '--surface': 'color-mix(in oklch, white 90%, black)' };
  let mutation;
  const context = {
    __fleetNativeVersion: native ? 1 : 0,
    webkit: { messageHandlers: { fleet: { postMessage: value => messages.push(value) } } },
    location: { pathname }, CSS: { supports: (_, value) => /^#[0-9a-f]{6}$/i.test(value) || value.startsWith('color-mix(') },
    getComputedStyle: element => element.style ? ({
      color: colors[element.style.color.slice(4, -1)]?.startsWith('color-mix(') ? '#E2E2E2' : colors[element.style.color.slice(4, -1)],
    }) : ({ getPropertyValue: name => colors[name] || '' }),
    document: { documentElement: { appendChild() {} }, createElement: tag => tag === 'span'
      ? { style: {}, setAttribute() {} } : ({ getContext: () => ({
      clearRect() {}, fillRect() {}, fillStyle: '#000000',
      getImageData() { return { data: [...this.fillStyle.slice(1).match(/../g).map(channel => parseInt(channel, 16)), 255] }; },
    }) }) },
    MutationObserver: class { constructor(callback) { mutation = callback; } observe() {} },
    addEventListener() {},
  };
  context.top = context;
  vm.createContext(context);
  vm.runInContext(source, context);
  return { context, colors, messages, mutate: () => mutation() };
}

test('native colors are resolved once, updated on palette changes, and can be republished after reset', () => {
  const { context, colors, messages, mutate } = harness();
  assert.equal(messages[0].type, 'appearance');
  assert.equal(messages[0].tokens.accent, '#123456');
  assert.equal(messages[0].tokens.surface, '#E2E2E2');
  mutate();
  assert.equal(messages.length, 1);
  colors['--accent'] = '#abcdef';
  mutate();
  assert.equal(messages[1].tokens.accent, '#ABCDEF');
  context.FleetNativeAppearance.publish();
  assert.equal(messages.length, 3);
  vm.runInContext(source, context);
  assert.equal(messages.length, 3);
});

test('normal Web and auxiliary pages never post native appearance messages', () => {
  assert.equal(harness(false).messages.length, 0);
  assert.equal(harness(true, '/account').messages.length, 0);
  assert.equal(harness(true, '/auth').messages.length, 0);
});
