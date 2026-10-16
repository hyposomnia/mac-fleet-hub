import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import vm from 'node:vm';
const marked = await readFile(new URL('./vendor/marked.min.js', import.meta.url), 'utf8');
const source = await readFile(new URL('./markdown.js', import.meta.url), 'utf8');
function render(value) {
  let parsed = '';
  const context = {
    document: { createElement: () => ({ append() {}, querySelectorAll: () => [], childNodes: [] }) },
    DOMPurify: {
      sanitize(html) {
        parsed += html;
        return html;
      },
    },
  };
  vm.createContext(context);
  vm.runInContext(marked, context);
  vm.runInContext(source, context);
  context.FleetMarkdown.renderMarkdown(value);
  return parsed;
}
test('A07 Markdown recognizes adjacent and multiline single-colon references', () => {
  const html = render(
    'Before :codex-annotation{index="1"}:codex-annotation{index="2"}\nAfter :fleet-annotation{index="3"}'
  );
  assert.equal((html.match(/data-fleet-annotation=/g) || []).length, 3);
  for (const index of [1, 2, 3]) assert.match(html, new RegExp(`注释 ${index}`));
});
test('A07 fenced/inline code, escaped colons and HTML attributes preserve literal syntax', () => {
  for (const value of [
    '`:codex-annotation{index="1"}`',
    '```text\n:codex-annotation{index="1"}\n```',
    '\\:codex-annotation{index="1"}',
    '<span title=\':codex-annotation{index="1"}\'>literal</span>',
  ]) {
    const html = render(value);
    assert.doesNotMatch(html, /data-fleet-annotation=/, value);
  }
});
test('A07 incomplete or nonnumeric attributes never become controls during streaming', () => {
  for (const value of [
    ':codex-annotation{index="',
    ':codex-annotation{index="1"',
    ':codex-annotation{index="<img>"}',
    ':codex-annotation{index="1" onclick="x"}',
  ])
    assert.doesNotMatch(render(value), /data-fleet-annotation=/);
});
