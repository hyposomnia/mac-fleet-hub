/* File tabs reuse the conversation's input and delivery controls. */
(function (root) {
  'use strict';
  function outputSummary({model, running = false, unread = false} = {}) {
    let text = '';
    for (let i = (model?.messages?.length || 0) - 1; i >= 0; i--) {
      const item = model.items[model.messages[i]];
      if (item?.type !== 'assistant' || !item.text?.trim()) continue;
      text = item.text.split(/\r?\n/).map(line => line.trim()).filter(Boolean).pop() || '';
      break;
    }
    return {text, status: running ? 'running' : unread ? 'unread' : 'complete'};
  }

  function init({onRead = () => {}, onReturn = () => {}, onResize = () => {}} = {}) {
    const doc = root.document;
    const win = doc.querySelector('#win'), pane = doc.querySelector('#chat-pane');
    const input = doc.querySelector('#chat-input'), composer = doc.querySelector('#chat-composer');
    const output = doc.querySelector('#chat-preview-output');
    if (!win || !pane || !input || !composer || !output || composer.dataset.compactReady) return;
    composer.dataset.compactReady = 'true';
    const label = doc.querySelector('#chat-preview-output-text');
    const icon = doc.querySelector('#chat-preview-output-icon');
    let snapshot = {};
    const preview = () => win.dataset.workspacePreview === 'true' && !pane.hidden;
    function expanded(value) {
      win.dataset.compactComposerExpanded = String(preview() && value);
      onResize();
    }
    function render() {
      const summary = outputSummary(snapshot);
      output.hidden = !preview() || !summary.text;
      output.dataset.state = summary.status;
      label.textContent = summary.text;
      label.title = summary.text;
      icon.className = `session-state-dot ${summary.status === 'complete' ? 'completed' : summary.status}`;
      const statusLabel = summary.status === 'running' ? '正在输出' : summary.status === 'unread' ? '输出完成，未读' : '输出完成';
      output.setAttribute('aria-label', `${statusLabel}：${summary.text}。返回会话`);
      output.title = `${statusLabel} · 点击返回会话`;
    }
    function sync() {
      if (!preview()) expanded(false);
      if (!preview() && !pane.hidden && !doc.hidden && snapshot.unread) {
        snapshot = {...snapshot, unread: false};
        onRead();
      }
      render();
    }
    input.addEventListener('focus', () => expanded(true));
    input.addEventListener('blur', () => expanded(false));
    output.addEventListener('click', onReturn);
    doc.addEventListener?.('visibilitychange', sync);
    if (root.MutationObserver) {
      const observer = new root.MutationObserver(sync);
      observer.observe(win, {attributes: true, attributeFilter: ['data-workspace-preview']});
      observer.observe(pane, {attributes: true, attributeFilter: ['hidden']});
    }
    sync();
    return {update(next = {}) { snapshot = next; sync(); }};
  }
  root.FleetCompactComposer = {outputSummary, init};
})(typeof globalThis !== 'undefined' ? globalThis : window);
