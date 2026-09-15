(function (root) {
  'use strict';

  // 文件上传队列：纯状态，不碰 DOM、不碰网络。
  // 语义要点：同一时刻只允许一个条目处于 uploading（串行泵），其余排队等待；
  // 单个文件失败只影响它自己，队列继续往下走。

  function createQueue() {
    return { items: [], activeId: '', seq: 0 };
  }

  function basename(path) {
    const parts = String(path || '').replace(/\/+$/, '').split('/').filter(Boolean);
    return parts.length ? parts[parts.length - 1] : '';
  }

  function find(queue, id) {
    return (queue.items || []).find((item) => item.id === id) || null;
  }

  // specs: [{ name, size }]（File/FileList 已拍平成纯数据，File 对象由调用方挂回 item.file）
  // target: { macId, path }；路径与设备在入队时固定，之后切目录/切设备不影响已排队的条目。
  function addFiles(queue, specs, target) {
    const created = [];
    for (const spec of specs || []) {
      const name = String(spec?.name || '');
      if (!name) continue;
      queue.seq += 1;
      const item = {
        id: `u${queue.seq}`,
        name,
        size: Math.max(0, Number(spec?.size) || 0),
        macId: String(target?.macId || ''),
        path: String(target?.path || ''),
        folder: basename(target?.path),
        status: 'pending',
        loaded: 0,
        progress: 0,
        error: '',
      };
      queue.items.push(item);
      created.push(item);
    }
    return created;
  }

  // 串行闸门：已有条目在传时返回 null，调用方据此停手。
  function nextPending(queue) {
    if (queue.activeId) return null;
    return queue.items.find((item) => item.status === 'pending') || null;
  }

  function beginItem(queue, id) {
    const item = find(queue, id);
    if (!item || item.status !== 'pending' || queue.activeId) return false;
    item.status = 'uploading';
    item.loaded = 0;
    item.progress = 0;
    queue.activeId = id;
    return true;
  }

  function setProgress(queue, id, loaded, total) {
    const item = find(queue, id);
    if (!item || item.status !== 'uploading') return false;
    const bytes = Math.max(0, Number(loaded) || 0);
    // 浏览器偶尔给不出 total（lengthComputable=false），用入队时记下的文件大小兜底。
    const full = Number(total) > 0 ? Number(total) : item.size;
    item.loaded = bytes;
    item.progress = full > 0 ? Math.min(100, Math.max(0, Math.round((bytes / full) * 100))) : 0;
    return true;
  }

  function release(queue, id) {
    if (queue.activeId === id) queue.activeId = '';
  }

  function finishItem(queue, id) {
    const item = find(queue, id);
    if (!item) return false;
    item.status = 'done';
    item.progress = 100;
    item.loaded = item.size || item.loaded;
    item.error = '';
    release(queue, id);
    return true;
  }

  function failItem(queue, id, message) {
    const item = find(queue, id);
    if (!item) return false;
    item.status = 'error';
    item.error = String(message || '上传失败。');
    release(queue, id);
    return true;
  }

  function removeItem(queue, id) {
    const index = queue.items.findIndex((item) => item.id === id);
    if (index < 0) return false;
    queue.items.splice(index, 1);
    release(queue, id);
    return true;
  }

  // 只摘掉已完成的行；失败行留着让用户看清原因。
  function clearSettled(queue) {
    queue.items = queue.items.filter((item) => item.status !== 'done');
    return queue;
  }

  function stateText(item) {
    if (item.status === 'pending') return '等待';
    if (item.status === 'uploading') return item.progress > 0 ? `${item.progress}%` : '上传中';
    if (item.status === 'done') return '完成';
    return item.error || '上传失败。';
  }

  function tone(item) {
    if (item.status === 'uploading') return 'active';
    if (item.status === 'done') return 'done';
    if (item.status === 'error') return 'error';
    return 'pending';
  }

  function rows(queue) {
    return (queue.items || []).map((item) => ({
      id: item.id,
      name: item.name,
      folder: item.folder,
      macId: item.macId,
      size: item.size,
      status: item.status,
      // 只有正在传的条目画进度条：整队里一根动着的条，比每行都挂一根更清楚。
      percent: item.status === 'uploading' ? item.progress : (item.status === 'done' ? 100 : 0),
      stateText: stateText(item),
      tone: tone(item),
      error: item.error,
    }));
  }

  function summary(queue) {
    const items = queue.items || [];
    const done = items.filter((item) => item.status === 'done').length;
    const failed = items.filter((item) => item.status === 'error').length;
    const active = items.find((item) => item.status === 'uploading') || null;
    const pending = items.filter((item) => item.status === 'pending').length;
    return {
      total: items.length,
      counter: `${done}/${items.length}`,
      activeName: active ? active.name : '',
      percent: active ? active.progress : 0,
      busy: !!active || pending > 0,
      failed,
      hasRows: items.length > 0,
    };
  }

  const api = {
    createQueue, addFiles, nextPending, beginItem, setProgress,
    finishItem, failItem, removeItem, clearSettled, rows, summary, basename,
  };
  root.FleetUploadModel = api;
  if (typeof module !== 'undefined') module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : window);
