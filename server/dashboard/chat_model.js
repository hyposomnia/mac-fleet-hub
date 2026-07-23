(function (root) {
  'use strict';

  function createChatState() {
    return { phase: 'idle', activeTurnId: '', messages: [], items: {}, approvals: {}, turnUsage: {}, error: null };
  }

  function cloneState(state) {
    return {
      phase: state.phase || 'idle',
      activeTurnId: state.activeTurnId || '',
      messages: (state.messages || []).slice(),
      items: { ...(state.items || {}) },
      approvals: { ...(state.approvals || {}) },
      turnUsage: { ...(state.turnUsage || {}) },
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

  function asObject(value) {
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
  }

  function chatPhase(value) {
    const raw = typeof value === 'object' && value ? value.type : value;
    const phase = String(raw || '').trim().toLowerCase();
    if (['active', 'running', 'inprogress', 'in_progress', 'started'].includes(phase)) return 'running';
    if (['systemerror', 'system_error', 'error', 'failed'].includes(phase)) return 'error';
    if (['idle', 'notloaded', 'not_loaded', 'completed', 'interrupted', 'cancelled'].includes(phase)) return 'idle';
    return phase || 'idle';
  }

  function firstString(...values) {
    for (const value of values) {
      if (typeof value === 'string' && value.trim()) return value.trim();
    }
    return '';
  }

  function firstNumber(...values) {
    for (const value of values) {
      if (value === undefined || value === null || value === '') continue;
      const n = Number(value);
      if (Number.isFinite(n)) return n;
    }
    return null;
  }

  function timeMs(...values) {
    for (const value of values) {
      if (value === undefined || value === null || value === '') continue;
      if (typeof value === 'number' || /^[0-9]+(\.[0-9]+)?$/.test(String(value))) {
        const n = Number(value);
        if (Number.isFinite(n) && n > 0) return n < 100000000000 ? Math.round(n * 1000) : Math.round(n);
      }
      const parsed = Date.parse(String(value));
      if (Number.isFinite(parsed)) return parsed;
    }
    return null;
  }

  function uuidV7TimeMs(id) {
    const compact = String(id || '').replaceAll('-', '');
    if (!/^[0-9a-fA-F]{12}/.test(compact)) return null;
    const n = Number.parseInt(compact.slice(0, 12), 16);
    if (!Number.isFinite(n) || n < 946684800000 || n > 4102444800000) return null;
    return n;
  }

  function mergeMessageTiming(item, data, kind) {
    const payload = asObject(data);
    const nestedItem = asObject(payload.item);
    const nestedTurn = asObject(payload.turn);
    const sentAt = timeMs(
      payload.sentAtMs, payload.sent_at_ms,
      payload.createdAtMs, payload.created_at_ms, payload.createdAt, payload.created_at,
      payload.timestampMs, payload.timestamp_ms, payload.timestamp,
      nestedItem.sentAtMs, nestedItem.sent_at_ms,
      nestedItem.createdAtMs, nestedItem.created_at_ms, nestedItem.createdAt, nestedItem.created_at,
      nestedItem.timestampMs, nestedItem.timestamp_ms, nestedItem.timestamp,
    );
    if (kind === 'user' && sentAt) item.sentAtMs = sentAt;

    const startedAt = timeMs(
      payload.startedAtMs, payload.started_at_ms, payload.startedAt, payload.started_at,
      nestedItem.startedAtMs, nestedItem.started_at_ms, nestedItem.startedAt, nestedItem.started_at,
      nestedTurn.startedAtMs, nestedTurn.started_at_ms, nestedTurn.startedAt, nestedTurn.started_at,
    );
    if (kind === 'assistant' && startedAt && !item.startedAtMs) item.startedAtMs = startedAt;

    const completedAt = timeMs(
      payload.completedAtMs, payload.completed_at_ms, payload.completedAt, payload.completed_at,
      payload.finishedAtMs, payload.finished_at_ms, payload.finishedAt, payload.finished_at,
      nestedItem.completedAtMs, nestedItem.completed_at_ms, nestedItem.completedAt, nestedItem.completed_at,
      nestedTurn.completedAtMs, nestedTurn.completed_at_ms, nestedTurn.completedAt, nestedTurn.completed_at,
      nestedTurn.finishedAtMs, nestedTurn.finished_at_ms, nestedTurn.finishedAt, nestedTurn.finished_at,
    );
    if (kind === 'assistant' && completedAt) item.completedAtMs = completedAt;
    if (kind === 'assistant' && item.startedAtMs && item.completedAtMs && item.completedAtMs >= item.startedAtMs) {
      item.durationMs = item.completedAtMs - item.startedAtMs;
    }
  }

  function normalizeTokenUsage(data) {
    const payload = asObject(data);
    const containers = [
      asObject(payload.usage), asObject(payload.tokenUsage), asObject(payload.token_usage),
      asObject(asObject(payload.item).usage), asObject(asObject(payload.item).tokenUsage), asObject(asObject(payload.item).token_usage),
      asObject(asObject(payload.turn).usage), asObject(asObject(payload.turn).tokenUsage), asObject(asObject(payload.turn).token_usage),
      asObject(asObject(payload.response).usage),
    ];
    const roots = [payload];
    for (const container of containers) {
      roots.push(
        asObject(container.last),
        asObject(container.lastTokenUsage),
        asObject(container.last_token_usage),
        container,
      );
    }
    const input = firstNumber(...roots.flatMap((r) => [
      r.inputTokens, r.input_tokens, r.promptTokens, r.prompt_tokens,
      r.totalInputTokens, r.total_input_tokens,
    ]));
    const output = firstNumber(...roots.flatMap((r) => [
      r.outputTokens, r.output_tokens, r.completionTokens, r.completion_tokens,
      r.totalOutputTokens, r.total_output_tokens,
    ]));
    if (input === null && output === null) return null;
    return { inputTokens: input, outputTokens: output };
  }

  function mergeAssistantMetadata(item, data, ev) {
    const payload = asObject(data);
    const nestedItem = asObject(payload.item);
    const nestedTurn = asObject(payload.turn);
    item.turnId = firstString(ev && ev.turnId, payload.turnId, payload.turn_id, nestedTurn.id, item.turnId);
    item.model = firstString(
      payload.model, payload.modelName, payload.model_name,
      nestedItem.model, nestedItem.modelName, nestedItem.model_name,
      nestedTurn.model, nestedTurn.modelName, nestedTurn.model_name,
      asObject(nestedTurn.options).model,
      item.model,
    );
    item.effort = firstString(
      payload.reasoningEffort, payload.reasoning_effort, payload.effort,
      nestedItem.reasoningEffort, nestedItem.reasoning_effort, nestedItem.effort,
      nestedTurn.reasoningEffort, nestedTurn.reasoning_effort, nestedTurn.effort,
      asObject(nestedTurn.options).effort,
      item.effort,
    );
    const usage = normalizeTokenUsage(payload);
    if (usage) item.usage = { ...(item.usage || {}), ...usage };
    mergeMessageTiming(item, payload, 'assistant');
    return item;
  }

  function mergeTurnMetadata(next, data, ev, complete = false) {
    const turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, asObject(data.turn).id);
    for (let i = next.messages.length - 1; i >= 0; i -= 1) {
      const item = next.items[next.messages[i]];
      if (!item || item.type !== 'assistant') continue;
      if (turnId && item.turnId && item.turnId !== turnId) continue;
      mergeAssistantMetadata(item, data, ev);
      if (complete) item.turnComplete = true;
      return;
    }
  }

  function mergePendingTurnUsage(next, item) {
    if (!item || !item.turnId || !next.turnUsage[item.turnId]) return;
    mergeAssistantMetadata(item, next.turnUsage[item.turnId], { turnId: item.turnId });
  }

  function historicalAssistantCompletesTurn(data, item) {
    const payload = asObject(data);
    const phase = firstString(payload.phase, asObject(payload.item).phase).toLowerCase();
    return phase === 'final_answer' || phase === 'final' || !!item.completedAtMs;
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
    next.items[itemId] = { id: itemId, type: 'user', text: text || '', images: (images || []).slice(), sentAtMs: Date.now() };
    next.messages.push(itemId);
    return next;
  }

  function prependHistory(state, events) {
    let history = createChatState();
    for (const ev of (events || [])) {
      const data = dataOf(ev);
      history = reduceChatEvent(history, { ...(ev || {}), data: { ...data, __history: true } });
    }
    const next = cloneState(state || createChatState());
    const existing = new Set(next.messages);
    next.messages = history.messages.filter((id) => !existing.has(id)).concat(next.messages);
    next.items = { ...history.items, ...next.items };
    next.approvals = { ...history.approvals, ...next.approvals };
    next.turnUsage = { ...history.turnUsage, ...next.turnUsage };
    return next;
  }

  function applyToolData(item, data) {
    item.kind = data.kind || item.kind || (data.command ? 'commandExecution' : 'tool');
    const defaultTitle = item.kind === 'commandExecution' ? '运行命令' : (item.kind === 'mcpToolCall' ? 'MCP 工具' : '工具调用');
    item.title = data.title || item.title || (data.command ? '运行命令' : defaultTitle);
    item.summary = data.summary || data.command || item.summary || '';
    item.meta = data.meta || data.cwd || item.meta || '';
    if (data.detail !== undefined) item.detail = data.detail || '';
    if (data.output !== undefined) item.output = data.output || '';
    if (data.status) item.status = data.status;
    if (data.durationMs !== undefined && data.durationMs !== null) item.durationMs = data.durationMs;
    if (data.exitCode !== undefined && data.exitCode !== null) item.exitCode = data.exitCode;
    return item;
  }

  function reduceChatEvent(state, ev) {
    const next = cloneState(state || createChatState());
    const data = dataOf(ev);
    const itemId = ev && ev.itemId ? ev.itemId : (data.itemId || data.requestId || ('event-' + next.messages.length));

    switch (ev && ev.type) {
      case 'thread_status':
        next.phase = chatPhase(data.status || data.state || next.phase);
        if (next.phase === 'running') {
          next.activeTurnId = firstString(data.activeTurnId, data.active_turn_id, next.activeTurnId);
          if (next.activeTurnId) {
            for (const item of Object.values(next.items)) {
              if (item?.type === 'assistant' && item.turnId === next.activeTurnId) item.turnComplete = false;
            }
          }
        } else {
          next.activeTurnId = '';
        }
        return next;
      case 'turn_started':
        next.phase = 'running';
        next.activeTurnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, asObject(data.turn).id, next.activeTurnId);
        return next;
      case 'assistant_delta': {
        next.phase = 'running';
        next.activeTurnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, next.activeTurnId);
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'assistant', text: '', done: false }));
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
        mergeAssistantMetadata(item, data, ev);
        mergePendingTurnUsage(next, item);
        if (!item.startedAtMs) item.startedAtMs = uuidV7TimeMs(item.turnId) || Date.now();
        item.text = (item.text || '') + (data.delta || '');
        return next;
      }
      case 'assistant_done': {
        const finalText = data.text || (data.item && data.item.text) || '';
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'assistant', text: '', done: false }));
        if (finalText) item.text = finalText;
        mergeAssistantMetadata(item, data, ev);
        mergePendingTurnUsage(next, item);
        const historyTurnComplete = data.__history && historicalAssistantCompletesTurn(data, item);
        if (!item.completedAtMs) item.completedAtMs = data.__history ? uuidV7TimeMs(item.turnId || (ev && ev.turnId)) : Date.now();
        if (!item.startedAtMs) item.startedAtMs = uuidV7TimeMs(item.turnId || (ev && ev.turnId)) || item.completedAtMs;
        if (item.startedAtMs && item.completedAtMs && item.completedAtMs >= item.startedAtMs) item.durationMs = item.completedAtMs - item.startedAtMs;
        item.done = true;
        if (data.__history) item.turnComplete = historyTurnComplete;
        return next;
      }
      case 'user_done': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'user', text: '', images: [] }));
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
        item.text = data.text || item.text || '';
        item.images = (data.images || item.images || []).slice();
        mergeMessageTiming(item, data, 'user');
        if (!item.sentAtMs) item.sentAtMs = uuidV7TimeMs(item.turnId || (ev && ev.turnId));
        return next;
      }
      case 'tool_delta': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'tool', title: '', summary: '', meta: '', output: '', progress: '', stream: data.stream || 'stdout' }));
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
        applyToolData(item, data);
        if (data.delta) item.output = (item.output || '') + data.delta;
        if (data.message) item.progress = data.message;
        item.stream = data.stream || item.stream;
        return next;
      }
      case 'tool_update':
      case 'tool_done': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'tool', title: '', summary: '', meta: '', detail: '', output: '', progress: '', stream: 'stdout' }));
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
        applyToolData(item, data);
        return next;
      }
      case 'diff_update': {
        const item = upsertItem(next, itemId, () => ({ id: itemId, type: 'diff', files: [], raw: null }));
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
        item.files = normalizeDiffFiles(data);
        item.raw = data;
        return next;
      }
      case 'approval_request': {
        const requestId = String(data.requestId || data.approvalId || itemId);
        const item = upsertItem(next, requestId, () => ({ id: requestId, type: 'approval', status: 'pending' }));
        item.requestId = requestId;
        item.itemId = data.itemId || ev.itemId || '';
        item.turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, item.turnId);
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
      case 'turn_usage': {
        const turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id);
        if (turnId) next.turnUsage[turnId] = data;
        mergeTurnMetadata(next, data, ev);
        return next;
      }
      case 'turn_done': {
        next.phase = 'idle';
        next.activeTurnId = '';
        mergeTurnMetadata(next, data, ev, true);
        const turnId = firstString(ev && ev.turnId, data.turnId, data.turn_id, asObject(data.turn).id);
        if (turnId) delete next.turnUsage[turnId];
        return next;
      }
      case 'error':
        next.error = data.message || data.error || '自绘会话出错';
        next.phase = 'error';
        return next;
      default:
        return next;
    }
  }

  const api = { createChatState, appendUserMessage, prependHistory, reduceChatEvent, normalizeDiffFiles, chatPhase, uuidV7TimeMs };
  root.FleetChatModel = api;
  if (typeof module !== 'undefined') module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : window);
