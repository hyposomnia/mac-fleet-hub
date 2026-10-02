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
        detail: `${request.macId.toUpperCase()} · ${absolute}`};
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
      const showing = model.active !== 'chat';
      win.dataset.workspacePreview = String(showing);
      win.dataset.workspaceTabs = String(model.tabs.length > 0);
      strip.hidden = !model.tabs.length; stage.hidden = !showing;
      for (const selector of ['#chat-pane', '#frames', '#frame']) {
        const content = doc.querySelector(selector);
        if (content) content.inert = showing;
      }
      for (const [key, frame] of frames) {
        if (key !== model.active) pause(frame);
        frame.hidden = key !== model.active;
      }
      strip.replaceChildren(); controls.clear();
      for (const tab of [{key: 'chat', name: '会话', detail: '返回当前会话'}, ...model.tabs]) {
        const wrap = element('div', 'workspace-tab');
        const button = element('button', 'workspace-tab-select', tab.name);
        const selected = tab.key === model.active;
        button.type = 'button'; button.title = tab.detail;
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
          closeButton.type = 'button'; closeButton.title = `关闭 ${tab.name}`;
          closeButton.setAttribute('aria-label', closeButton.title);
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
      if (!model.tabs.length) doc.querySelector('#win-title')?.focus({preventScroll: true});
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
            content.querySelector('.preview-close')?.addEventListener('click', event => {
              event.preventDefault(); close(target.key);
            });
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
        frames.clear(); model.reset(); render(); },
    });
    render();
  }
  init.sequence = 0;
  root.FleetWorkspaceTabs = {init, previewTarget, createModel, showChat: () => {}, reset: () => {}};
})(globalThis);
