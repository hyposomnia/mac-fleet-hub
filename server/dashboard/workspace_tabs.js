/* File previews share the conversation workspace without disposing its writer. */
(function (root) {
  'use strict';
  function previewTarget(href) {
    try {
      const url = new URL(href, root.location.origin);
      if (url.origin !== root.location.origin || !root.FleetPreview.isPreviewRoute(url.pathname)) return null;
      const request = root.FleetPreview.previewRequest(url.search);
      if (!request) return null;
      const path = request.path.replace(/:\d+(?::\d+)?$/, '');
      // Keep relative paths scoped to their originating device and working directory.
      const absolute = path.startsWith('/') ? path : `${request.cwd || ''}/${path}`;
      const parts = [];
      for (const part of absolute.split('/')) {
        if (part === '..') parts.pop();
        else if (part && part !== '.') parts.push(part);
      }
      const key = JSON.stringify([request.macId, !path.startsWith('/') && !request.cwd ? 'relative' : '', path.startsWith('~') ? request.cwd : '', parts]);
      url.searchParams.delete('embed');
      return {key, url: url.pathname + url.search, name: path.split('/').filter(Boolean).pop() || '文件',
        detail: absolute};
    } catch (_) { return null; }
  }

  function createModel() {
    const tabs = [];
    let active = 'chat';
    return {
      get tabs() { return tabs.slice(); }, get active() { return active; },
      open(target) {
        if (!tabs.some(tab => tab.key === target.key)) tabs.push(target);
        active = target.key;
      },
      select(key) {
        if (key === 'chat' || tabs.some(tab => tab.key === key)) active = key;
      },
      close(key) {
        const index = tabs.findIndex(tab => tab.key === key);
        if (index < 0) return;
        tabs.splice(index, 1);
        if (active === key) active = tabs[Math.max(0, index - 1)]?.key || 'chat';
      },
      reset() { tabs.length = 0; active = 'chat'; },
    };
  }

  function init({onOpen = () => {}} = {}) {
    const doc = root.document, win = doc.querySelector('#win');
    const strip = doc.querySelector('#workspace-tabs'), stage = doc.querySelector('#workspace-preview');
    if (!win || !strip || !stage || strip.dataset.ready) return;
    strip.dataset.ready = 'true';
    const model = createModel(), frames = new Map();
    const controls = new Map();
    const tooltip = doc.createElement('div');
    tooltip.id = 'workspace-tab-preview'; tooltip.className = 'workspace-tab-preview';
    tooltip.setAttribute('role', 'tooltip'); tooltip.hidden = true;
    const tipTitle = element('strong', 'workspace-tab-preview-title');
    const tipText = element('div', 'workspace-tab-preview-text');
    const tipPath = element('div', 'workspace-tab-preview-path');
    tooltip.append(tipTitle, tipText, tipPath); doc.body.append(tooltip);
    let tipTimer, tipAnchor;
    function hideTip() {
      root.clearTimeout(tipTimer); tooltip.hidden = true;
      tipAnchor?.removeAttribute('aria-describedby'); tipAnchor = null;
    }
    function chatTitle() {
      return (doc.querySelector('#win-title .ttl') || doc.querySelector('#win-title'))?.textContent?.trim() || '会话';
    }
    function excerpt(tab) {
      try {
        const content = tab.key === 'chat' ? doc.querySelector('#chat-scroll') : frames.get(tab.key)?.contentDocument;
        const nodes = content?.querySelectorAll(tab.key === 'chat'
          ? '.chat-row.assistant .chat-card, .chat-row.user .chat-card'
          : '.preview-markdown .chat-markdown, .preview-text .CodeMirror-code, .preview-text > textarea');
        const node = tab.key === 'chat' ? nodes?.[nodes.length - 1] : nodes?.[0];
        return (node?.value || node?.innerText || node?.textContent || '').trim().replace(/\n\s*\n/g, '\n').slice(0, 400);
      } catch (_) { return ''; }
    }
    function showTip(tab, button) {
      hideTip(); tipAnchor = button;
      tipTimer = root.setTimeout(() => {
        tipTitle.textContent = tab.key === 'chat' ? chatTitle() : tab.name;
        tipText.textContent = excerpt(tab); tipText.hidden = !tipText.textContent;
        tipPath.textContent = tab.key === 'chat' ? doc.querySelector('#win-meta')?.textContent || '' : tab.detail;
        tipPath.hidden = !tipPath.textContent;
        tooltip.hidden = false; button.setAttribute('aria-describedby', tooltip.id);
        const rect = button.getBoundingClientRect(), card = tooltip.getBoundingClientRect();
        tooltip.style.left = `${Math.max(8, Math.min(rect.left, root.innerWidth - card.width - 8))}px`;
        tooltip.style.top = `${Math.max(8, rect.bottom + card.height + 8 > root.innerHeight ? rect.top - card.height - 6 : rect.bottom + 6)}px`;
      }, 350);
    }
    function leaveTip() { root.clearTimeout(tipTimer); tipTimer = root.setTimeout(hideTip, 100); }
    tooltip.onpointerenter = () => root.clearTimeout(tipTimer);
    tooltip.onpointerleave = leaveTip;
    doc.addEventListener?.('keydown', event => { if (event.key === 'Escape') hideTip(); });
    strip.addEventListener('scroll', hideTip);
    root.addEventListener('resize', hideTip);
    function syncComposerHeight() {
      const pane = doc.querySelector('#chat-pane'), composer = doc.querySelector('#chat-composer');
      const height = pane && !pane.hidden ? composer?.getBoundingClientRect().height || 0 : 0;
      stage.style.setProperty('--workspace-composer-height', `${height}px`);
    }
    const composer = doc.querySelector('#chat-composer');
    if (composer && root.ResizeObserver) new root.ResizeObserver(syncComposerHeight).observe(composer);
    function syncHeader() {
      const pane = doc.querySelector('#chat-pane');
      const visible = !!(pane && !pane.hidden) || model.tabs.length > 0;
      win.dataset.workspaceTabs = String(visible);
      strip.hidden = !visible;
    }
    const pane = doc.querySelector('#chat-pane');
    if (pane && root.MutationObserver) new root.MutationObserver(syncHeader)
      .observe(pane, {attributes: true, attributeFilter: ['hidden']});
    const title = doc.querySelector('#win-title');
    if (title && root.MutationObserver) new root.MutationObserver(() => {
      const label = controls.get('chat')?.querySelector('.workspace-tab-label');
      if (label) label.textContent = chatTitle();
      hideTip();
    }).observe(title, {childList: true, subtree: true, characterData: true});
    function element(tag, className, text) {
      const node = doc.createElement(tag);
      node.className = className;
      if (text) node.textContent = text;
      return node;
    }
    function pause(frame) {
      try { frame?.contentDocument?.querySelectorAll('audio, video').forEach(media => media.pause()); } catch (_) {}
    }
    function render({focus = false} = {}) {
      hideTip();
      const showing = model.active !== 'chat';
      syncComposerHeight();
      win.dataset.workspacePreview = String(showing);
      syncHeader(); stage.hidden = !showing;
      for (const selector of ['#chat-scroll', '#chat-turn-pin', '#frames', '#frame']) {
        const content = doc.querySelector(selector);
        if (content) content.inert = showing;
      }
      for (const [key, frame] of frames) {
        if (key !== model.active) pause(frame);
        frame.hidden = key !== model.active;
      }
      strip.replaceChildren(); controls.clear();
      for (const tab of [{key: 'chat', name: chatTitle(), detail: ''}, ...model.tabs]) {
        const wrap = element('div', 'workspace-tab');
        const button = element('button', 'workspace-tab-select');
        const icon = element('span', 'workspace-tab-icon');
        icon.setAttribute('aria-hidden', 'true');
        // Static SVG only; document titles and excerpts always use textContent.
        icon.innerHTML = tab.key === 'chat'
          ? '<svg viewBox="0 0 24 24"><path d="M21 11.5a8.4 8.4 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.4 8.4 0 0 1-3.8-.9L3 21l1.9-5.7a8.4 8.4 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.4 8.4 0 0 1 3.8-.9h.5a8.5 8.5 0 0 1 8 8v.5Z"/></svg>'
          : '<svg viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"/><path d="M14 2v6h6"/></svg>';
        button.append(icon, element('span', 'workspace-tab-label', tab.name));
        button.onpointerenter = event => { if (event.pointerType !== 'touch') showTip(tab, button); };
        button.onpointerleave = leaveTip;
        button.onfocus = () => { if (button.matches(':focus-visible')) showTip(tab, button); };
        button.onblur = hideTip;
        const selected = tab.key === model.active;
        button.type = 'button';
        button.setAttribute('role', 'tab');
        button.setAttribute('aria-selected', String(selected));
        button.setAttribute('aria-controls', tab.key === 'chat' ? 'chat-pane' : frames.get(tab.key).id);
        button.tabIndex = selected ? 0 : -1;
        wrap.dataset.selected = String(selected);
        button.onclick = () => { model.select(tab.key); render({focus: true}); };
        button.onkeydown = event => {
          const keys = ['chat', ...model.tabs.map(item => item.key)];
          const index = keys.indexOf(tab.key);
          let next;
          if (event.key === 'ArrowRight') next = keys[(index + 1) % keys.length];
          if (event.key === 'ArrowLeft') next = keys[(index + keys.length - 1) % keys.length];
          if (event.key === 'Home') next = 'chat';
          if (event.key === 'End') next = keys[keys.length - 1];
          if (next) { event.preventDefault(); model.select(next); render({focus: true}); }
          if (event.key === 'Delete' && tab.key !== 'chat') { event.preventDefault(); close(tab.key); }
        };
        wrap.append(button); controls.set(tab.key, button);
        if (tab.key !== 'chat') {
          const closeButton = element('button', 'workspace-tab-close', '×');
          closeButton.type = 'button';
          closeButton.setAttribute('aria-label', `关闭 ${tab.name}`);
          closeButton.onclick = () => close(tab.key);
          wrap.append(closeButton);
        }
        strip.append(wrap);
      }
      if (focus) controls.get(model.active)?.focus({preventScroll: true});
    }
    function close(key) {
      pause(frames.get(key)); frames.get(key)?.remove(); frames.delete(key);
      model.close(key); render({focus: true});
    }
    function open(href) {
      const target = previewTarget(href);
      if (!target) return false;
      onOpen();
      if (!frames.has(target.key)) {
        const frame = element('iframe', 'workspace-preview-frame');
        frame.id = `workspace-file-${++init.sequence}`;
        frame.title = `文件预览：${target.name}`;
        frame.src = target.url;
        frame.onload = () => {
          // The trusted /view wrapper retains its existing scriptless HTML sandbox.
          try {
            const content = frame.contentDocument;
            if (content.documentElement) content.documentElement.dataset.workspaceTab = 'true';
            content.addEventListener('click', intercept);
          } catch (_) {}
          if (model.active !== target.key) pause(frame);
        };
        frames.set(target.key, frame); stage.append(frame);
      }
      model.open(target); render({focus: true});
      return true;
    }
    function intercept(event) {
      if (event.defaultPrevented || event.button > 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
      const link = event.target.closest?.('a[href]');
      if (!link || link.hasAttribute('download')) return;
      if (open(link.href)) event.preventDefault();
    }
    doc.querySelector('#chat-scroll')?.addEventListener('click', intercept);
    doc.querySelector('#file-preview-open')?.addEventListener('click', intercept);
    Object.assign(root.FleetWorkspaceTabs, {
      open, showChat: () => { model.select('chat'); render(); },
      reset: () => { for (const frame of frames.values()) { pause(frame); frame.remove(); }
        frames.clear(); model.reset(); render();
        tipTitle.textContent = tipText.textContent = tipPath.textContent = ''; },
    });
    render();
  }
  init.sequence = 0;
  root.FleetWorkspaceTabs = {init, previewTarget, createModel, showChat: () => {}, reset: () => {}};
})(globalThis);
