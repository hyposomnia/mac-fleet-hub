import test from 'node:test';
import assert from 'node:assert/strict';
import { createRecordsPager } from './record_pager.js';

const page = (id, next = '') => ({ messages: [{ message_id: id }], next_cursor: next, has_more: !!next, filter_fields: [{ name: 'status', type: 'enum' }] });

test('records load one summary page at a time and preserve navigation after errors', async () => {
  const calls = [];
  let fail = false;
  const pager = createRecordsPager(async (params) => {
    calls.push(new URLSearchParams(params));
    if (fail) throw new Error('gateway unavailable');
    return params.get('cursor') ? page('older') : page('newer', 'cursor-2');
  });
  assert.equal(calls.length, 0);
  await pager.reset(new URLSearchParams({ q: 'hello', limit: '25' }));
  assert.equal(calls.length, 1);
  assert.equal(calls[0].get('view'), 'summary');
  assert.equal(calls[0].get('include_filters'), '1');
  assert.equal(pager.state.page, 1);
  fail = true;
  await pager.next();
  assert.equal(pager.state.page, 1);
  assert.equal(pager.state.messages[0].message_id, 'newer');
  assert.equal(pager.state.error, 'gateway unavailable');
  fail = false;
  await pager.next();
  assert.equal(pager.state.page, 2);
  assert.equal(calls.at(-1).get('cursor'), 'cursor-2');
  assert.equal(calls.at(-1).get('include_filters'), null);
  await pager.next();
  assert.equal(calls.length, 3, 'last page must not request again');
  await pager.previous();
  assert.equal(pager.state.page, 1);
  assert.equal(calls.at(-1).get('cursor'), null);
  await pager.reset(new URLSearchParams({ q: 'changed', limit: '50' }));
  assert.equal(pager.state.page, 1);
  assert.equal(calls.at(-1).get('cursor'), null);
  assert.equal(calls.at(-1).get('q'), 'changed');
});

test('changing filters aborts the old request and discards late results', async () => {
  const calls = [];
  const pager = createRecordsPager((params, { signal }) => new Promise((resolve) => calls.push({ params, signal, resolve })));
  const old = pager.reset(new URLSearchParams({ q: 'old' }));
  const latest = pager.reset(new URLSearchParams({ q: 'new' }));
  assert.equal(calls[0].signal.aborted, true);
  calls[1].resolve(page('new-result'));
  await latest;
  calls[0].resolve(page('old-result', 'old-cursor'));
  await old;
  assert.equal(pager.state.messages[0].message_id, 'new-result');
  assert.equal(pager.state.nextCursor, '');
  assert.equal(pager.state.loading, false);
  pager.dispose();
});

test('malformed responses show an error rather than reporting an empty success', async () => {
  const pager = createRecordsPager(async () => ({ messages: [] }));
  await pager.reset(new URLSearchParams());
  assert.match(pager.state.error, /分页/);
  assert.equal(pager.state.loading, false);
});
