import { createRecordsPager } from './record_pager.js?v=234';

const $ = (selector) => document.querySelector(selector);
const form = $('#records-filters');
const conditions = $('#records-conditions');
const list = $('#records-list');
const statusNames = { queued: '队列中', running: '执行中', completed: '完成', failed: '失败', pending: '待回调', retrying: '重试中', delivered: '已投递', delivery_failed: '投递失败', true: '是', false: '否' };
const operatorNames = { eq: '等于', contains: '包含', gte: '大于或等于', lte: '小于或等于', empty: '未设置', not_empty: '已设置' };
const detailsControllers = new Set();
let renderedMessages;
let schema;

function node(tag, props = {}, ...children) {
  const element = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (key === 'text') element.textContent = value;
    else if (key === 'class') element.className = value;
    else element.setAttribute(key, value);
  }
  element.append(...children);
  return element;
}
function date(value) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';
}
async function readJSON(params, options = {}) {
  const response = await fetch(`/api/automation/message-records?${params}`, { ...options, cache: 'no-store' });
  if (response.redirected && new URL(response.url).pathname.startsWith('/auth')) throw new Error('登录已过期，请返回 fleet hub 重新登录');
  const body = await response.json();
  if (!response.ok) throw new Error(body?.error?.message || body?.error || `HTTP ${response.status}`);
  return body;
}
function fillOptions(select, options) {
  const selected = select.value;
  while (select.options.length > 1) select.remove(1);
  for (const { value, label } of options) select.append(node('option', { value, text: label || value }));
  select.value = selected;
}
function addCondition() {
  if (conditions.children.length >= 30) return;
  const field = node('select', { class: 'input', 'aria-label': '筛选字段' });
  for (const item of pager.state.fields) field.append(node('option', { value: item.name, text: item.label }));
  const op = node('select', { class: 'input', 'aria-label': '筛选操作' });
  const valueContainer = node('span', { class: 'records-condition-value' });
  const remove = node('button', { class: 'btn sm', type: 'button', text: '移除', 'aria-label': '移除筛选条件' });
  const row = node('div', { class: 'records-condition' }, field, op, valueContainer, remove);
  function updateValue() {
    const description = pager.state.fields.find((item) => item.name === field.value);
    let value;
    if (description.type === 'enum' || description.type === 'boolean') {
      value = node('select', { class: 'input', 'aria-label': `${description.label}筛选值` });
      for (const option of description.options) value.append(node('option', { value: option, text: statusNames[option] || option }));
    } else {
      value = node('input', { class: 'input', 'aria-label': `${description.label}筛选值`, type: description.type === 'time' ? 'datetime-local' : description.type === 'number' ? 'number' : 'text' });
      if (description.type === 'number') { value.min = '0'; value.step = '1'; }
      if (description.type === 'time') value.step = '1';
      value.maxLength = 2048;
    }
    value.required = true;
    valueContainer.replaceChildren(value);
    updateOperator();
  }
  function updateOperator() {
    const value = valueContainer.firstElementChild;
    value.disabled = op.value === 'empty' || op.value === 'not_empty';
    valueContainer.hidden = value.disabled;
  }
  field.onchange = () => {
    const description = pager.state.fields.find((item) => item.name === field.value);
    op.replaceChildren(...description.operators.map((item) => node('option', { value: item, text: operatorNames[item] })));
    updateValue();
  };
  op.onchange = updateOperator;
  remove.onclick = () => row.remove();
  field.onchange();
  conditions.append(row);
}

function filterQuery() {
  const data = new FormData(form);
  const filters = [];
  for (const field of ['status', 'device_id', 'ai_client', 'access_key_id', 'project_name', 'session_name']) {
    const value = data.get(field).trim();
    if (!value) continue;
    filters.push({ field, op: ['project_name', 'session_name'].includes(field) ? 'contains' : field === 'access_key_id' && value === '__legacy__' ? 'empty' : 'eq', value: value === '__legacy__' ? '' : value });
  }
  for (const [name, op] of [['created_from', 'gte'], ['created_to', 'lte']]) {
    if (data.get(name)) filters.push({ field: 'created_at', op, value: new Date(data.get(name)).toISOString() });
  }
  for (const row of conditions.children) {
    const [field, op] = row.querySelectorAll('select');
    const input = row.querySelector('.records-condition-value').firstElementChild;
    const description = pager.state.fields.find((item) => item.name === field.value);
    const value = input.disabled ? '' : description.type === 'time' ? new Date(input.value).toISOString() : input.value.trim();
    filters.push({ field: field.value, op: op.value, value });
  }
  const params = new URLSearchParams({ limit: $('#records-page-size').value });
  if (data.get('q').trim()) params.set('q', data.get('q').trim());
  if (filters.length) params.set('filters', JSON.stringify(filters));
  return params;
}

function recordSection(label, content) {
  return node('div', { class: 'message-record-section' }, node('div', { class: 'message-record-label', text: label }), node('pre', { class: 'message-record-copy', text: content || '—' }));
}
function renderDetails(item) {
  const root = node('div', { class: 'message-record-body' });
  for (const [label, content] of [
    ['消息 ID', item.message_id], ['用户消息', item.message], ['AI 回复', item.ai_message?.content],
    ['设备', `${item.device?.name || '—'} · ${item.device?.id || '—'}`], ['AI 客户端', item.ai_client],
    ['项目', `${item.project_name || '—'}\n${item.project || '—'}`], ['会话', `${item.session_name || '—'}\n${item.session_id || '尚未创建'}`],
    ['访问密钥', `${item.access_key?.name || '旧记录'} · ${item.access_key?.id || '—'}`], ['调用别名', item.alias],
    ['Turn ID', item.turn_id], ['投递队列 ID', item.agent_queue_id], ['幂等键', item.idempotency_key],
    ['执行时间', `创建：${date(item.created_at)}\n开始：${date(item.started_at)}\n完成：${date(item.completed_at)}\n失败：${date(item.failed_at)}`],
  ]) {
    if (content !== undefined) root.append(recordSection(label, content));
  }
  if (item.error) root.append(recordSection('错误', `${item.error.code}\n${item.error.message}\n可重试：${item.error.retryable ? '是' : '否'}`));
  if (item.callback_url) root.append(recordSection('回调', `${item.callback_url}\n状态：${statusNames[item.callback?.status] || item.callback?.status || '—'}\n尝试次数：${item.callback?.attempts || 0}\n最近尝试：${date(item.callback?.last_attempt_at)}\n下次尝试：${date(item.callback?.next_attempt_at)}\n最近错误：${item.callback?.last_error || '—'}`));
  return root;
}
function renderRecords(messages) {
  for (const controller of detailsControllers) controller.abort();
  detailsControllers.clear();
  list.replaceChildren();
  for (const item of messages) {
    const details = node('details', { class: 'message-record' });
    const summary = node('summary', {},
      node('span', { class: `message-status ${['queued', 'running', 'completed', 'failed'].includes(item.status) ? item.status : ''}`, text: statusNames[item.status] || item.status }),
      node('span', { class: 'message-record-main' }, node('div', { class: 'message-record-title', text: item.message_preview || '(空消息)' }), node('div', { class: 'message-record-meta', text: `${item.device?.name || item.device?.id || '未知设备'} · ${item.ai_client || '—'} · ${item.project_name || '—'} · ${item.access_key?.name || '旧记录'}` })),
      node('time', { class: 'message-record-time', text: date(item.created_at) }));
    const body = node('div');
    details.append(summary, body);
    let loaded = false;
    let loading = false;
    const load = async () => {
      if (loaded || loading) return;
      loading = true;
      const controller = new AbortController();
      detailsControllers.add(controller);
      body.replaceChildren(node('p', { class: 'message-record-empty', text: '正在加载正文…' }));
      try {
        const result = await readJSON(new URLSearchParams({ detail: item.message_id }), { signal: controller.signal });
        if (!details.isConnected) return;
        if (!result.message) throw new Error('服务器返回的记录详情无效');
        body.replaceChildren(renderDetails(result.message));
        loaded = true;
      } catch (error) {
        if (controller.signal.aborted || !details.isConnected) return;
        const retry = node('button', { type: 'button', class: 'btn sm', text: '重试' });
        retry.onclick = load;
        body.replaceChildren(node('div', { class: 'message-record-empty' }, node('p', { text: error.message }), retry));
      } finally { loading = false; detailsControllers.delete(controller); }
    };
    details.ontoggle = () => { if (details.open) load(); };
    list.append(details);
  }
}

const pager = createRecordsPager(readJSON, (state) => {
  list.setAttribute('aria-busy', String(state.loading));
  $('#records-previous').disabled = state.loading || state.page === 1;
  $('#records-next').disabled = state.loading || !state.nextCursor;
  $('#records-refresh').disabled = state.loading;
  $('#records-page-number').textContent = `第 ${state.page} 页`;
  $('#records-status').textContent = state.loading ? '正在加载…' : state.error ? '加载失败，可重试或重新筛选' : state.messages.length ? `本页 ${state.messages.length} 条${state.nextCursor ? '' : ' · 已到末页'}` : '没有符合条件的消息记录';
  $('#records-error').hidden = !state.error;
  $('#records-error').textContent = state.error;
  if (renderedMessages !== state.messages) { renderRecords(state.messages); renderedMessages = state.messages; }
  if (state.fields.length && schema !== state.fields) {
    schema = state.fields;
    fillOptions(form.elements.device_id, Object.entries(state.devices).map(([value, label]) => ({ value, label: `${label || value} (${value})` })));
    fillOptions(form.elements.access_key_id, state.keys.map((key) => ({ value: key.id, label: key.name })));
    $('#records-add-condition').disabled = false;
  }
});

function applyFilters() {
  if (form.reportValidity()) pager.reset(filterQuery());
}
form.onsubmit = (event) => { event.preventDefault(); applyFilters(); };
$('#records-reset').onclick = () => { conditions.replaceChildren(); form.reset(); applyFilters(); };
$('#records-add-condition').onclick = addCondition;
$('#records-refresh').onclick = applyFilters;
$('#records-page-size').onchange = applyFilters;
$('#records-next').onclick = () => pager.next();
$('#records-previous').onclick = () => pager.previous();
window.addEventListener('pagehide', () => { pager.dispose(); for (const controller of detailsControllers) controller.abort(); });
window.addEventListener('pageshow', (event) => { if (event.persisted) applyFilters(); });
applyFilters();

if ('serviceWorker' in navigator) navigator.serviceWorker.register('/sw.js').then((registration) => registration.update()).catch(() => {});
