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

  function init({onRead = () => {}, onResize = () => {}} = {}) {
    const doc = root.document;
    const win = doc.querySelector('#win'), pane = doc.querySelector('#chat-pane');
    const input = doc.querySelector('#chat-input'), composer = doc.querySelector('#chat-composer');
    const output = doc.querySelector('#chat-preview-output');
    if (!win || !pane || !input || !composer || !output || composer.dataset.compactReady) return;
    composer.dataset.compactReady = 'true';
    const label = doc.querySelector('#chat-preview-output-text');
    const icon = doc.querySelector('#chat-preview-output-icon');
    const header = doc.querySelector('#chat-preview-header'), close = doc.querySelector('#chat-preview-close');
    const scroll = doc.querySelector('#chat-scroll');
    let snapshot = {};
    const preview = () => win.dataset.workspacePreview === 'true' && !pane.hidden;
    const isExpanded = () => preview() && win.dataset.compactComposerExpanded === 'true';
    function expanded(value, restoreFocus = false) {
      const wasExpanded = isExpanded();
      win.dataset.compactComposerExpanded = String(preview() && value);
      sync();
      onResize();
      if (isExpanded() && !wasExpanded && scroll) scroll.scrollTop = scroll.scrollHeight;
      if (restoreFocus && !output.hidden) output.focus();
    }
    function render() {
      const summary = outputSummary(snapshot);
      output.hidden = !preview() || isExpanded() || !summary.text;
      if (header) header.hidden = !isExpanded();
      if (scroll) scroll.inert = preview() && !isExpanded();
      output.setAttribute('aria-expanded', String(isExpanded()));
      output.dataset.state = summary.status;
      label.textContent = summary.text;
      label.title = summary.text;
      icon.className = `session-state-dot ${summary.status === 'complete' ? 'completed' : summary.status}`;
      const statusLabel = summary.status === 'running' ? '正在输出' : summary.status === 'unread' ? '输出完成，未读' : '输出完成';
      output.setAttribute('aria-label', `${statusLabel}：${summary.text}。展开会话`);
      output.title = `${statusLabel} · 点击展开会话`;
    }
    function sync() {
      if (!preview()) win.dataset.compactComposerExpanded = 'false';
      if ((!preview() || isExpanded()) && !pane.hidden && !doc.hidden && snapshot.unread) {
        snapshot = {...snapshot, unread: false};
        onRead();
      }
      render();
    }
    input.addEventListener('focus', () => expanded(true));
    output.addEventListener('click', () => { expanded(true); scroll?.focus({preventScroll: true}); });
    close?.addEventListener('click', () => expanded(false, true));
    function dismissOutside(event) {
      if (isExpanded() && !pane.contains(event.target)) expanded(false);
    }
    doc.addEventListener('pointerdown', dismissOutside);
    doc.addEventListener('focusin', dismissOutside);
    doc.addEventListener('keydown', event => {
      if (event.key !== 'Escape' || event.defaultPrevented || !isExpanded()) return;
      if (doc.querySelector('.chat-approval-trigger[aria-expanded="true"], .chat-options-trigger[aria-expanded="true"], #chat-skill-menu:not([hidden])')) return;
      event.preventDefault();
      expanded(false, true);
    });
    root.addEventListener?.('blur', () => {
      if (doc.activeElement?.tagName === 'IFRAME' && !pane.contains(doc.activeElement)) expanded(false);
    });
    doc.addEventListener?.('visibilitychange', sync);
    if (root.MutationObserver) {
      const observer = new root.MutationObserver(sync);
      observer.observe(win, {attributes: true, attributeFilter: ['data-workspace-preview']});
      observer.observe(pane, {attributes: true, attributeFilter: ['hidden']});
    }
    sync();
    return {update(next = {}) {
      if (!next.model || next.sessionKey !== snapshot.sessionKey) win.dataset.compactComposerExpanded = 'false';
      snapshot = next;
      sync();
    }};
  }
  root.FleetCompactComposer = {outputSummary, init};
})(typeof globalThis !== 'undefined' ? globalThis : window);
