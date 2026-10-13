export function createRecordsPager(fetchPage, onChange = () => {}) {
  const state = { messages: [], fields: [], keys: [], devices: {}, page: 1, nextCursor: '', loading: false, error: '' };
  let params = new URLSearchParams();
  let cursors = [''];
  let controller;
  let sequence = 0;

  async function load(cursor, page) {
    const current = ++sequence;
    controller?.abort();
    controller = new AbortController();
    const query = new URLSearchParams(params);
    query.set('view', 'summary');
    if (cursor) query.set('cursor', cursor);
    if (!state.fields.length) query.set('include_filters', '1');
    state.loading = true;
    state.error = '';
    onChange(state);
    try {
      const result = await fetchPage(query, { signal: controller.signal });
      if (current !== sequence) return;
      if (!Array.isArray(result?.messages) || typeof result.has_more !== 'boolean' || (result.has_more && !result.next_cursor)) {
        throw new Error('服务器返回的分页数据无效，请刷新后重试');
      }
      state.messages = result.messages;
      state.nextCursor = result.has_more ? result.next_cursor : '';
      state.page = page;
      cursors[page - 1] = cursor;
      if (result.filter_fields) state.fields = result.filter_fields;
      if (result.access_keys) state.keys = result.access_keys;
      if (result.devices) state.devices = result.devices;
    } catch (error) {
      if (current !== sequence) return;
      state.error = error.message;
    } finally {
      if (current === sequence) {
        state.loading = false;
        onChange(state);
      }
    }
  }
  return {
    state,
    reset(query) {
      params = new URLSearchParams(query);
      state.messages = [];
      state.page = 1;
      state.nextCursor = '';
      cursors = [''];
      return load('', 1);
    },
    next() { if (!state.loading && state.nextCursor) return load(state.nextCursor, state.page + 1); },
    previous() { if (!state.loading && state.page > 1) return load(cursors[state.page - 2], state.page - 1); },
    dispose() { sequence++; controller?.abort(); },
  };
}
