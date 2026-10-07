import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('./settings_dialog.js', import.meta.url), 'utf8').catch(() => '');
function element(dataset = {}) {
  return { dataset, hidden: true, textContent: '', children: [], attributes: {}, controls: [],
    setAttribute(name, value) { this.attributes[name] = String(value); },
    querySelectorAll() { return this.controls; }, replaceChildren(...children) { this.children = children; },
    append(...children) { this.children.push(...children); }, focus() { this.focused = true; }, addEventListener() {} };
}
function fixture(load = async () => null, confirm = () => true, inline = false) {
  assert.ok(source, 'unified settings controller missing');
  const sandbox = {};
  vm.runInNewContext(source, sandbox);
  const pages = ['account', 'add-device', 'automation', 'sessions'];
  const panels = pages.map(page => element({ settingsPanel: page }));
  const buttons = pages.map(page => element({ settingsPage: page }));
  const overlay = element();
  const status = element();
  const document = { createElement: () => element(), addEventListener() {}, activeElement: element() };
  const discardPrompt = inline ? { container: element(), cancel: element(), discard: element() } : null;
  const dialog = sandbox.FleetSettingsDialog.create({ document, overlay, panels, buttons, title: element(), status, confirm, load, discardPrompt });
  return { dialog, panels, buttons, overlay, status, discardPrompt };
}

test('all settings share one overlay and select only the requested panel', async () => {
  const current = fixture();
  await current.dialog.open('add-device');
  assert.equal(current.overlay.hidden, false);
  assert.deepEqual(current.panels.map(panel => panel.hidden), [true, false, true, true]);
  assert.equal(current.buttons[1].attributes['aria-selected'], 'true');
  assert.equal(current.dialog.close(), true);
  assert.equal(current.overlay.hidden, true);
});

test('late account loads never overwrite a newer page and close invalidates pending loads', async () => {
  let finish;
  const current = fixture(page => page === 'account' ? new Promise(resolve => { finish = resolve; }) : Promise.resolve(null));
  const loading = current.dialog.open('account');
  await current.dialog.open('add-device');
  finish({ canLeave: () => false });
  await loading;
  assert.equal(current.dialog.page, 'add-device');
  assert.equal(current.dialog.close(), true);
  assert.deepEqual(current.panels[0].children, []);
});

test('security recovery flow blocks closing and switching until acknowledged', async () => {
  let acknowledged = false;
  const current = fixture(async () => ({ canLeave: () => acknowledged }));
  await current.dialog.open('account');
  assert.equal(current.dialog.close(), false);
  await current.dialog.open('automation');
  assert.equal(current.dialog.page, 'account');
  assert.match(current.status.textContent, /恢复码/);
  acknowledged = true;
  assert.equal(current.dialog.close(), true);
});

test('edited settings are not discarded when confirmation is declined', async () => {
  const current = fixture(async () => null, () => false);
  const input = { value: '', checked: false };
  current.panels[3].controls.push(input);
  await current.dialog.open('sessions');
  input.value = '5';
  assert.equal(current.dialog.close(), false);
  await current.dialog.open('automation');
  assert.equal(current.dialog.page, 'sessions');
  current.dialog.reset();
  assert.equal(current.overlay.hidden, true);
});

test('escape and tab are trapped before dashboard shortcuts and close restores focus', async () => {
  const current = fixture();
  const trigger = element();
  await current.dialog.open('sessions', trigger);
  let prevented = false;
  let stopped = false;
  current.dialog.keydown({ key: 'Escape', preventDefault() { prevented = true; }, stopImmediatePropagation() { stopped = true; } });
  assert.equal(prevented, true);
  assert.equal(stopped, true);
  assert.equal(trigger.focused, true);
});

test('inline discard prompt never invokes a blocking native browser confirmation', async () => {
  const current = fixture(async () => null, () => { throw Error('native confirm should not run'); }, true);
  const input = { value: '', checked: false };
  current.panels[3].controls.push(input);
  await current.dialog.open('sessions');
  input.value = '7';
  assert.equal(current.dialog.close(), false);
  assert.equal(current.discardPrompt.container.hidden, false);
  current.discardPrompt.cancel.onclick();
  assert.equal(current.discardPrompt.container.hidden, true);
  assert.equal(current.dialog.page, 'sessions');
  assert.equal(current.dialog.close(), false);
  await current.discardPrompt.discard.onclick();
  assert.equal(current.overlay.hidden, true);
});
