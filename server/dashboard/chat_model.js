(function (root) {
  'use strict';

  function createChatState() {
    return { phase: 'idle', messages: [], items: {}, approvals: {}, error: null };
  }

  function cloneState(state) {
    return {
      phase: state.phase || 'idle',
      messages: (state.messages || []).slice(),
      items: { ...(state.items || {}) },
      approvals: { ...(state.approvals || {}) },
      error: state.error || null,
    };
  }

  function dataOf(ev) {
    if (!ev || ev.data == null) return {};
    if (typeof ev.data === 'string') {
      try { return JSON.parse(ev.data); } catch (_) { return {}; }
    }
    return ev.data || {};
  }

  function upsertItem(next, id, make) {
    const item = next.items[id] || make();
    next.items[id] = item;
    if (!next.messages.includes(id)) next.messages.push(id);
    return item;
  }

  function diffLineCounts(diff) {
    let additions = 0;
    let deletions = 0;
    for (const line of String(diff || '').split('\n')) {
      if (line.startsWith('+') && !line.startsWith('+++')) additions += 1;
      else if (line.startsWith('-') && !line.startsWith('---')) deletions += 1;
    }
    return { additions, deletions };
  }

  function normalizeDiffFiles(data) {
    const source = data && (data.files || data.changes);
    const files = Array.isArray(source)
      ? source
      : (source && typeof source === 'object'
        ? Object.entries(source).map(([path, change]) => ({ path, ...(change || {}) }))
        : []);
    return files.map((file) => {
      const diff = file.diff || file.patch || file.unified_diff || '';
      const counts = diffLineCounts(diff);
      return {
        path: file.path || file.filePath || file.name || '未知文件',
        additions: Number.isFinite(file.additions) ? file.additions
          : (Number.isFinite(file.addedLines) ? file.addedLines : counts.additions),
        deletions: Number.isFinite(file.deletions) ? file.deletions
          : (Number.isFinite(file.deletedLines) ? file.deletedLines : counts.deletions),
      };
    });
  }

  function appendUserMessage(state, text, id, images) {
    const next = cloneState(state);
    const itemId = id || ('user-' + Date.now());
    next.items[itemId] = { id: itemId, type: 'user', text: text || '', images: (images || []).slice() };
    next.messages.push(itemId);
    return next;
  }

  function prependHistory(state, events) {
    let history = createChatState();
    for (const ev of (events || [])) history = reduceChatEvent(history, ev);
    const next = cloneState(state || createChatState());
    const existing = new Set(next.messages);
    next.messages = history.messages.filter((id) => !existing.has(id)).concat(next.messages);
    next.items = { ...history.items, ...next.items };
    next.approvals = { ...history.approvals, ...next.approvals };
    return next;
  }

  function reduceChatEvent(state, ev) {
    const next = cloneState(state || createChatState());
    const data = dataOf(ev);
    const itemId = ev && ev.itemId ? ev.itemId : (data.itemId || data.requestId || ('event-' + next.messages.length));

    switch (ev && ev.type) {
      case 'thread_status':
        next.phase = data.status || data.state || next.phase;
        return next;
      case 'assistant_delta': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'assistant', text: '', done: false }));
        item.text = (item.text || '') + (data.delta || '');
        return next;
      }
      case 'assistant_done': {
        const finalText = data.text || (data.item && data.item.text) || '';
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'assistant', text: '', done: false }));
        if (finalText) item.text = finalText;
        item.done = true;
        return next;
      }
      case 'user_done': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'user', text: '', images: [] }));
        item.text = data.text || item.text || '';
        item.images = (data.images || item.images || []).slice();
        return next;
      }
      case 'tool_delta': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'tool', title: data.command || '命令执行', cwd: data.cwd || '', output: '', stream: data.stream || 'stdout' }));
        item.output = (item.output || '') + (data.delta || '');
        item.stream = data.stream || item.stream;
        return next;
      }
      case 'tool_done': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'tool', title: '命令执行', cwd: '', output: '', stream: 'stdout' }));
        item.title = data.command || item.title;
        item.cwd = data.cwd || item.cwd;
        item.output = data.output || item.output;
        item.status = data.status || item.status;
        return next;
      }
      case 'diff_update': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'diff', files: [], raw: null }));
        item.files = normalizeDiffFiles(data);
        item.raw = data;
        return next;
      }
      case 'approval_request': {
        const requestId = String(data.requestId || data.approvalId || itemId);
        const item = upsertItem(next, requestId, () => ({ id: requestId, type: 'approval', status: 'pending' }));
        item.requestId = requestId;
        item.itemId = data.itemId || ev.itemId || '';
        item.kind = data.command ? 'command' : (data.permissions ? 'permission' : 'file');
        item.command = data.command || '';
        item.cwd = data.cwd || '';
        item.reason = data.reason || '';
        item.raw = data;
        next.approvals[requestId] = item;
        return next;
      }
      case 'approval_resolved':
        if (data.requestId && next.approvals[String(data.requestId)]) next.approvals[String(data.requestId)].status = 'resolved';
        return next;
      case 'turn_done':
        next.phase = 'idle';
        return next;
      case 'error':
        next.error = data.message || data.error || '自绘会话出错';
        next.phase = 'error';
        return next;
      default:
        return next;
    }
  }

  const api = { createChatState, appendUserMessage, prependHistory, reduceChatEvent, normalizeDiffFiles };
  root.FleetChatModel = api;
  if (typeof module !== 'undefined') module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : window);
