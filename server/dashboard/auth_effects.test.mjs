import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./auth_effects.js', import.meta.url), 'utf8');
function fixture({ fine = true, reduced = false, random = .4 } = {}) {
  const listeners = new Map(), windowListeners = new Map(), frames = new Map(), fills = [], arcs = [];
  let nextFrame = 0, now = 0;
  const context = {
    setTransform() {}, clearRect() {}, beginPath() {},
    arc(x, y, radius) { arcs.push({ x, y, radius }); },
    fill() { fills.push(this.fillStyle); },
    createRadialGradient: () => ({ addColorStop() {} }),
  };
  const canvas = { hidden: true, getContext: () => context };
  const media = [fine, reduced].map(matches => ({ matches, addEventListener(_, callback) { this.change = callback; }, removeEventListener() { this.change = null; } }));
  const document = {
    hidden: false, documentElement: {}, querySelector: selector => selector === '.auth-stars' ? canvas : null,
    addEventListener: (name, callback) => listeners.set(name, callback), removeEventListener: name => listeners.delete(name),
  };
  let observer;
  const target = {
    Math: Object.assign(Object.create(Math), { random: () => random }),
    document, innerWidth: 1440, innerHeight: 900, devicePixelRatio: 2, accent: '#2C5D87',
    performance: { now: () => now },
    getComputedStyle: () => ({ getPropertyValue: () => target.accent }),
    matchMedia: query => media[query.includes('reduced') ? 1 : 0],
    requestAnimationFrame: callback => { const id = ++nextFrame; frames.set(id, callback); return id; },
    cancelAnimationFrame: id => frames.delete(id),
    addEventListener: (name, callback) => windowListeners.set(name, callback), removeEventListener: name => windowListeners.delete(name),
    MutationObserver: class { constructor(callback) { this.callback = callback; observer = this; } observe() {} disconnect() { this.disconnected = true; } },
  };
  vm.runInNewContext(source, target);
  const effect = target.FleetAuthEffects.create(target);
  function flush() {
    now += 16.67;
    const callbacks = [...frames.values()]; frames.clear();
    callbacks.forEach(callback => callback(now));
  }
  const move = (x, y, pointerType = 'mouse') => listeners.get('pointermove')?.({ clientX: x, clientY: y, pointerType });
  return { effect, document, canvas, context, listeners, windowListeners, frames, fills, arcs, media, target, observer, flush, move };
}

test('touch and reduced motion never attach pointer tracking', () => {
  for (const options of [{ fine: false }, { reduced: true }]) {
    const current = fixture(options);
    assert.equal(current.listeners.has('pointermove'), false);
    assert.equal(current.canvas.hidden, true);
    assert.equal(current.frames.size, 0);
  }
});

test('fine-pointer input coalesces into one frame and starts stars while pointing at the form', () => {
  const current = fixture();
  current.move(680, 350); current.move(760, 380);
  assert.equal(current.frames.size, 1);
  current.flush();
  assert.ok(current.fills.length > 0, 'the form area must not suppress the stars');
  assert.equal(current.canvas.hidden, false);
  assert.equal(current.canvas.width, 2880);
  assert.equal(current.context.globalCompositeOperation, undefined, 'no destination-out form mask');
});

test('stars originate at each screen edge and move toward the pointer', () => {
  for (const random of [.1, .35, .6, .85]) {
    const current = fixture({ random }); current.move(720, 450); current.flush();
    const first = current.arcs.at(-1);
    assert.ok(first, 'an edge must emit a star');
    assert.ok(Math.min(first.x, first.y, 1440 - first.x, 900 - first.y) < 30);
    const distance = Math.hypot(first.x - 720, first.y - 450);
    for (let count = 0; count < 12; count++) current.flush();
    const later = current.arcs.at(-1);
    assert.ok(Math.hypot(later.x - 720, later.y - 450) < distance);
  }
});

test('a stationary pointer receives sparse stars without accumulating particles', () => {
  const current = fixture(); current.move(720, 450);
  let drawn = 0;
  for (let count = 0; count < 600; count++) {
    const before = current.arcs.length; current.flush();
    const inFrame = current.arcs.length - before;
    assert.ok(inFrame <= 12, 'at most six points and their small halos per frame');
    drawn += inFrame;
  }
  assert.ok(drawn > 0);
  assert.equal(current.frames.size, 1);
  current.effect.destroy(); assert.equal(current.frames.size, 0);
});

test('touch events on a hybrid device do not start animation', () => {
  const current = fixture(); current.move(200, 200, 'touch');
  assert.equal(current.frames.size, 0);
  assert.equal(current.canvas.hidden, true);
});

test('pointer leave and hidden pages cancel pending animation', () => {
  const current = fixture(); current.move(200, 200);
  current.listeners.get('pointerleave')();
  assert.equal(current.frames.size, 0);
  current.move(200, 200); current.document.hidden = true;
  current.listeners.get('visibilitychange')();
  assert.equal(current.frames.size, 0);
  assert.equal(current.listeners.has('pointermove'), false);
  assert.equal(current.canvas.hidden, true);
  current.document.hidden = false; current.listeners.get('visibilitychange')();
  assert.equal(current.listeners.has('pointermove'), true);
  assert.equal(current.frames.size, 0);
});

test('changed pointer or motion preferences cancel animation immediately', () => {
  for (const index of [0, 1]) {
    const current = fixture(); current.move(200, 200);
    current.media[index].matches = index === 1; current.media[index].change();
    assert.equal(current.frames.size, 0);
    assert.equal(current.listeners.has('pointermove'), false);
    assert.equal(current.canvas.hidden, true);
  }
});

test('theme changes update the star color and teardown removes all active work', () => {
  const current = fixture();
  current.target.accent = '#B8D9FF'; current.observer.callback();
  current.move(200, 200); current.move(300, 240); current.flush();
  assert.match(current.fills.find(fill => typeof fill === 'string'), /^rgba\(184,217,255,/);
  current.effect.destroy();
  assert.equal(current.frames.size, 0);
  assert.equal(current.listeners.has('pointermove'), false);
  assert.equal(current.windowListeners.has('resize'), false);
  assert.equal(current.observer.disconnected, true);
  assert.equal(current.canvas.hidden, true);
});
