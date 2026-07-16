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

  function appendUserMessage(state, text, id) {
    const next = cloneState(state);
    const itemId = id || ('user-' + Date.now());
    next.items[itemId] = { id: itemId, type: 'user', text: text || '' };
    next.messages.push(itemId);
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
      case 'tool_delta': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'tool', title: data.command || '命令执行', cwd: data.cwd || '', output: '', stream: data.stream || 'stdout' }));
        item.output = (item.output || '') + (data.delta || '');
        item.stream = data.stream || item.stream;
        return next;
      }
      case 'diff_update': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'diff', files: [], raw: null }));
        item.files = data.files || data.changes || item.files;
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

  const api = { createChatState, appendUserMessage, reduceChatEvent };
  root.FleetChatModel = api;
  if (typeof module !== 'undefined') module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : window);
