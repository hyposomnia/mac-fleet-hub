import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
const source = await readFile(new URL('./auth_effects.js', import.meta.url), 'utf8').catch(() => '');
function fixture({ fine = true, reduced = false } = {}) {
  assert.ok(source, 'auth effects module missing');
  const listeners = new Map();
  const frames = new Map();
  const probe = { hidden: true, style: {}, querySelector: () => ({ textContent: '' }) };
  const document = { hidden: false, querySelector: () => probe, addEventListener: (name, callback) => listeners.set(name, callback),
    removeEventListener: name => listeners.delete(name) };
  const target = { document, matchMedia: query => ({ matches: query.includes('reduced') ? reduced : fine, addEventListener() {}, removeEventListener() {} }),
    requestAnimationFrame: callback => { const id = frames.size + 1; frames.set(id, callback); return id; },
    cancelAnimationFrame: id => frames.delete(id), addEventListener() {} };
  vm.runInNewContext(source, target);
  const effect = target.FleetAuthEffects.create(target);
  return { effect, document, probe, listeners, frames };
}
test('touch and reduced motion never attach pointer tracking', () => {
  for (const options of [{ fine: false }, { reduced: true }]) {
    const current = fixture(options);
    assert.equal(current.listeners.has('pointermove'), false);
    assert.equal(current.probe.hidden, true);
  }
});
test('fine pointers update one compositor transform per animation frame', () => {
  const current = fixture();
  current.listeners.get('pointermove')({ clientX: 10, clientY: 20 });
  current.listeners.get('pointermove')({ clientX: 30, clientY: 40 });
  assert.equal(current.frames.size, 1);
  const frame = [...current.frames.values()][0];
  current.frames.clear();
  frame();
  assert.equal(current.probe.style.transform, 'translate3d(30px, 40px, 0)');
  assert.equal(current.probe.hidden, false);
  current.listeners.get('pointerleave')();
  assert.equal(current.probe.hidden, true);
});
test('hidden pages and teardown cancel pending animation', () => {
  const current = fixture();
  current.listeners.get('pointermove')({ clientX: 10, clientY: 20 });
  current.document.hidden = true;
  current.listeners.get('visibilitychange')();
  assert.equal(current.frames.size, 0);
  current.effect.destroy();
  assert.equal(current.listeners.has('pointermove'), false);
});
