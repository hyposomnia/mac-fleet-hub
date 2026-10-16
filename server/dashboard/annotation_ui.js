/* Selection/editing UI. Only the app and registered same-origin /view wrappers
   can contribute selections; sandboxed documents cannot operate the composer. */
(function (root) {
  'use strict';
  const A = root.FleetAnnotations;
  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text != null) node.textContent = text;
    return node;
  };
  const button = (label, action, className = '') => {
    const node = el('button', className, label);
    node.type = 'button';
    node.onclick = action;
    return node;
  };
  const keyFor = (chat) => (chat ? `${chat.macId}\n${chat.assistant}\n${chat.sessionId}` : '');
  const mobile = () => root.matchMedia('(max-width: 700px)').matches;
  const excluded =
    'button:not(.annotation-reference), .chat-code-toolbar, .chat-code-line-number, .annotation-owned, [aria-hidden="true"]';
  const blocks = new Set([
    'P',
    'DIV',
    'LI',
    'PRE',
    'BLOCKQUOTE',
    'H1',
    'H2',
    'H3',
    'H4',
    'H5',
    'H6',
    'TR',
    'BR',
  ]);

  function snapshot(node) {
    let text = '';
    const entries = [];
    function walk(current) {
      if (current.nodeType === 3) {
        entries.push({ node: current, start: text.length, end: text.length + current.data.length });
        text += current.data;
        return;
      }
      if (current.nodeType !== 1 && current.nodeType !== 11) return;
      if (current.matches?.(excluded)) return;
      for (const child of current.childNodes) walk(child);
      if (blocks.has(current.tagName) && text && !text.endsWith('\n')) text += '\n';
    }
    walk(node);
    return { text, entries };
  }

  function rangeFor(node, locator) {
    const view = snapshot(node),
      at = A.locate(view.text, locator);
    if (at < 0) return null;
    const first = view.entries.find((entry) => entry.end > at);
    const last = view.entries.find(
      (entry) => entry.end >= at + locator.exact.length && entry.start < at + locator.exact.length
    );
    if (!first || !last) return null;
    const range = node.ownerDocument.createRange();
    range.setStart(first.node, Math.max(0, at - first.start));
    range.setEnd(last.node, at + locator.exact.length - last.start);
    return range;
  }

  function selectionFragments(doc, range, selector) {
    const fragments = [];
    // Paragraph/whole-block selections can end on an ancestor or at the start
    // of the next block. Use the text actually covered, not endpoint parents.
    for (const sourceNode of doc.querySelectorAll(selector)) {
      if (!range.intersectsNode(sourceNode)) continue;
      const view = snapshot(sourceNode);
      const selected = view.entries.flatMap((entry) => {
        if (!range.intersectsNode(entry.node)) return [];
        const from = range.startContainer === entry.node ? range.startOffset : 0;
        const to = range.endContainer === entry.node ? range.endOffset : entry.node.data.length;
        return to > from ? [{ from: entry.start + from, to: entry.start + to }] : [];
      });
      if (!selected.length) continue;
      const from = selected[0].from, to = selected[selected.length - 1].to;
      const text = view.text.slice(from, to);
      if (!text.trim()) continue;
      fragments.push({ sourceNode, note: { text, locator: {
        version: A.textVersion(view.text), startOffset: from, endOffset: to, exact: text,
        prefix: view.text.slice(Math.max(0, from - 64), from), suffix: view.text.slice(to, to + 64),
      } } });
    }
    return fragments;
  }

  function init({ getChat, onChange, notify = () => {} }) {
    const frames = new Map();
    const activePointers = new Set();
    const dock = document.querySelector('#chat-annotations');
    let selection = null,
      editing = null,
      panel = null,
      returnFocus = null,
      hoverTimer;
    let lastChat,
      lastScope,
      composing = false,
      restoringFocus = false;
    const menu = el('div', 'annotation-owned annotation-selection-menu');
    menu.hidden = true;
    menu.append(button('添加到对话', () => openEditor(selection), 'annotation-add'));
    menu.onpointerdown = (event) => event.preventDefault();
    const editor = el('div', 'annotation-owned annotation-editor');
    editor.hidden = true;
    const comment = el('textarea');
    comment.rows = 1;
    comment.placeholder = '添加注释';
    comment.setAttribute('aria-label', '注释内容');
    const save = button('✓', saveEditor, 'annotation-save');
    save.setAttribute('aria-label', '保存注释');
    editor.append(comment, save);
    const layer = el('div', 'annotation-owned annotation-marker-layer');
    document.body.append(menu, editor, layer);

    function current(chat, scope = keyFor(chat)) {
      return chat && getChat() === chat && scope === keyFor(getChat());
    }
    function clearNative() {
      root.getSelection()?.removeAllRanges();
      for (const { frame } of frames.values()) {
        try {
          frame.contentWindow.getSelection()?.removeAllRanges();
          frame.contentWindow.FleetPreview?.clearAnnotationSelection?.();
        } catch (_) {}
      }
    }
    function closeEditor({ clear = true } = {}) {
      editing = null;
      selection = null;
      editor.hidden = menu.hidden = true;
      if (clear) clearNative();
      redraw();
    }
    function owns(target) {
      return !!target?.closest?.('.annotation-owned, .annotation-reference');
    }
    function outside(event) {
      if (owns(event.target)) return;
      // Let the browser keep its native range during multi-click selection.
      // A blank click collapses it before pointerup captures the new range.
      const selecting = event.target?.closest?.('[data-annotation-message], #preview-markdown, #preview-text');
      const had = selection || editing;
      if (had) closeEditor({ clear: !selecting });
      else if (!selecting) clearNative();
      if (panel && !panel.contains(event.target)) closePanel();
    }
    document.addEventListener('pointerdown', outside, true);
    document.addEventListener(
      'keydown',
      (event) => {
        if (
          event.key === 'Tab' &&
          selection &&
          !editing &&
          !menu.hidden &&
          !menu.contains(document.activeElement)
        ) {
          event.preventDefault();
          menu.querySelector('button').focus();
          return;
        }
        if (event.key !== 'Escape' || composing || event.isComposing || event.keyCode === 229) return;
        if (editing) {
          event.preventDefault();
          event.stopImmediatePropagation();
          if (current(editing.chat, editing.scope) && editing.savedId) {
            editing.chat.annotations = (editing.chat.annotations || []).filter(
              (note) => note.id !== editing.savedId
            );
            onChange(editing.chat);
          }
          closeEditor();
          document.querySelector('#chat-input')?.focus({ preventScroll: true });
        } else if (panel) {
          event.preventDefault();
          event.stopImmediatePropagation();
          closePanel(true);
        } else if (selection) {
          event.preventDefault();
          closeEditor();
        }
      },
      true
    );
    comment.addEventListener('compositionstart', () => {
      composing = true;
    });
    comment.addEventListener('compositionend', () => {
      composing = false;
    });
    comment.addEventListener('keydown', (event) => {
      if (
        event.key === 'Enter' &&
        !event.shiftKey &&
        !composing &&
        !event.isComposing &&
        event.keyCode !== 229
      ) {
        event.preventDefault();
        saveEditor();
      }
    });
    comment.addEventListener('input', () => {
      comment.style.height = '36px';
      comment.style.height = `${Math.min(120, comment.scrollHeight)}px`;
      place(editor, editing?.rect);
    });

    function position(rect, frame) {
      const offset = frame?.getBoundingClientRect();
      return {
        left: rect.left + (offset?.left || 0),
        right: rect.right + (offset?.left || 0),
        top: rect.top + (offset?.top || 0),
        bottom: rect.bottom + (offset?.top || 0),
      };
    }
    function place(node, rect) {
      if (!rect) return;
      const vv = root.visualViewport;
      const left = vv?.offsetLeft || 0,
        top = vv?.offsetTop || 0;
      const width = vv?.width || root.innerWidth,
        height = vv?.height || root.innerHeight;
      const size = node.getBoundingClientRect();
      node.style.left = `${Math.max(left + 12, Math.min(rect.left, left + width - size.width - 12))}px`;
      node.style.top = `${Math.max(top + 12, Math.min(rect.bottom + 8, top + height - size.height - 12))}px`;
    }
    function capture(doc, frameInfo) {
      if (activePointers.size || editing || panel) return;
      const chat = getChat();
      if (!chat || !['codex', 'dsh'].includes(chat.assistant)) return;
      if (
        frameInfo &&
        (frameInfo.key !== keyFor(chat) || frameInfo.frame.hidden || !frameInfo.frame.isConnected)
      )
        return;
      let fragments, rect;
      const file = frameInfo && frameInfo.frame.contentWindow.FleetPreview?.annotationSelection?.();
      if (file) {
        fragments = [{ note: { text: file.text, locator: file.locator } }];
        rect = position(file.rect, frameInfo.frame);
      } else {
        const sel = doc.getSelection();
        if (!sel || sel.isCollapsed || !sel.rangeCount) {
          selection = null;
          menu.hidden = true;
          redraw();
          return;
        }
        const range = sel.getRangeAt(0);
        fragments = selectionFragments(doc, range, frameInfo ? '#preview-markdown' : '[data-annotation-message]');
        rect = position(range.getBoundingClientRect(), frameInfo?.frame);
      }
      const candidates = fragments.filter(({ note }) => note.text.trim()).map(({ note, sourceNode }) => ({
        id: root.crypto.randomUUID(), text: note.text,
        fleetSource: {
          deviceId: chat.macId,
          assistant: chat.assistant,
          sessionId: chat.sessionId,
          kind: frameInfo ? 'file' : 'message',
          locator: {
            ...note.locator,
            ...(frameInfo ? { path: frameInfo.path } : { itemId: sourceNode.dataset.annotationMessage }),
          },
        },
      }));
      if (!candidates.length) {
        selection = null;
        menu.hidden = true;
        redraw();
        return;
      }
      try {
        A.validateDraft([...(chat.annotations || []), ...candidates]);
      } catch (error) {
        selection = null;
        menu.hidden = true;
        redraw();
        notify(error.message);
        return;
      }
      selection = { chat, note: candidates[0], notes: candidates, rect,
        sourceNode: fragments[0].sourceNode, sourceNodes: fragments.map(({ sourceNode }) => sourceNode).filter(Boolean) };
      menu.hidden = false;
      place(menu, rect);
      redraw();
    }
    function listen(doc, frameInfo) {
      doc.addEventListener('pointerdown', (event) => {
        if (owns(event.target)) return;
        activePointers.add(event.pointerId);
        root.clearTimeout(doc.annotationSelectionTimer);
        if (frameInfo) outside(event);
      }, true);
      doc.addEventListener('pointerup', (event) => {
        activePointers.delete(event.pointerId);
        root.setTimeout(() => capture(doc, frameInfo), 0);
      }, true);
      doc.addEventListener('pointercancel', (event) => {
        activePointers.delete(event.pointerId);
        root.clearTimeout(doc.annotationSelectionTimer);
      }, true);
      doc.addEventListener('keyup', (event) => {
        if (event.key !== 'Escape' && !owns(event.target)) capture(doc, frameInfo);
      });
      // iOS adjusts native selection handles without another pointerup.
      doc.addEventListener('selectionchange', () => {
        root.clearTimeout(doc.annotationSelectionTimer);
        if (activePointers.size) return;
        doc.annotationSelectionTimer = root.setTimeout(() => capture(doc, frameInfo), 150);
      });
      doc.addEventListener(
        'scroll',
        () => {
          if (selection && !editing) {
            menu.hidden = true;
            selection = null;
          }
          redraw();
        },
        true
      );
    }
    listen(document);

    function openEditor(candidate, savedId) {
      if (!candidate || !current(candidate.chat)) return;
      closePanel();
      returnFocus = document.activeElement;
      editing = { ...candidate, savedId, scope: keyFor(candidate.chat) };
      selection = candidate;
      menu.hidden = true;
      editor.hidden = false;
      comment.value = candidate.note.annotation || '';
      comment.style.height = '36px';
      place(editor, candidate.rect);
      comment.focus({ preventScroll: true });
      comment.dispatchEvent(new Event('input'));
      redraw();
    }
    function saveEditor() {
      if (!editing || !current(editing.chat, editing.scope)) {
        closeEditor();
        return;
      }
      const { chat, savedId } = editing;
      const value = comment.value.trim();
      const next = [...(chat.annotations || [])];
      const index = next.findIndex((item) => item.id === savedId);
      if (savedId && index < 0) {
        closeEditor();
        return;
      }
      const notes = A.copy(editing.notes || [editing.note]);
      for (const note of notes) {
        if (value) note.annotation = value;
        else delete note.annotation;
      }
      if (index < 0) next.push(...notes);
      else next[index] = notes[0];
      try {
        A.validateDraft(next);
      } catch (error) {
        notify(error.message);
        return;
      }
      chat.annotations = next;
      onChange(chat);
      closeEditor();
      render();
      document.querySelector('#chat-input')?.focus({ preventScroll: true });
    }

    function closePanel(focus = false) {
      root.clearTimeout(hoverTimer);
      if (!panel) return;
      const previous = panel;
      panel = null;
      if (previous.open) previous.close();
      previous.remove();
      if (focus && returnFocus?.isConnected) {
        restoringFocus = true;
        returnFocus.focus({ preventScroll: true });
        restoringFocus = false;
      }
    }
    function sourceLabel(note) {
      return (
        note.fleetSource?.locator?.path ||
        note.fileContext?.path ||
        (note.source || note.fleetSource ? '会话原文' : '选中文字')
      );
    }
    function canNavigate(note) {
      return !!(note.fleetSource || note.source || note.fileContext);
    }
    function showDetails(
      envelope,
      { editable = false, anchor = dock, ambiguous = false, candidates = [] } = {}
    ) {
      closeEditor({ clear: false });
      closePanel();
      returnFocus = anchor;
      panel = el(mobile() ? 'dialog' : 'section', 'annotation-owned annotation-panel');
      panel.setAttribute('aria-label', '注释详情');
      panel.setAttribute('role', 'dialog');
      const header = el('header');
      header.append(
        el('strong', '', ambiguous ? '注释来源待确认' : `${envelope.items.length} 条注释`),
        button('×', () => closePanel(true), 'annotation-close')
      );
      header.lastChild.setAttribute('aria-label', '关闭注释详情');
      panel.append(header);
      if (ambiguous)
        panel.append(
          el('p', 'annotation-source-warning', '本段包含多次输入，无法确定引用来源。以下为候选输入。')
        );
      const groups = ambiguous ? candidates : [envelope];
      for (let group = 0; group < groups.length; group++) {
        if (ambiguous) {
          panel.append(el('p', 'annotation-source-label', `输入 ${group + 1}`));
          if (groups[group].inputText) panel.append(el('p', 'annotation-comment', groups[group].inputText));
          if (!groups[group].items.length)
            panel.append(el('p', 'annotation-source-label', '此输入没有注释。'));
        }
        groups[group].items.forEach((note, index) => {
          const row = el('section', 'annotation-detail');
          const marker = el('span', 'annotation-number', groups[group].indices?.[index] || index + 1);
          row.append(marker, el('blockquote', '', note.text));
          if (note.annotation) row.append(el('p', 'annotation-comment', note.annotation));
          row.append(el('div', 'annotation-source-label', sourceLabel(note)));
          const actions = el('div', 'annotation-actions');
          if (canNavigate(note)) actions.append(button('查看原文', () => navigate(note)));
          if (!editable)
            actions.append(
              button('再次注释', () => {
                const chat = getChat();
                if (!chat) return;
                const copy = A.copy(note);
                copy.id = root.crypto.randomUUID();
                if (!A.sourceBelongsTo(copy, chat)) {
                  notify('此注释不属于当前会话。');
                  return;
                }
                const file = copy.fileContext;
                if (
                  file &&
                  typeof file.version === 'string' &&
                  Number.isSafeInteger(file.startOffset) &&
                  file.startOffset >= 0 &&
                  file.endOffset - file.startOffset === copy.text.length
                ) {
                  copy.fleetSource = {
                    deviceId: chat.macId,
                    assistant: chat.assistant,
                    sessionId: chat.sessionId,
                    kind: 'file',
                    locator: {
                      path: file.path,
                      version: file.version,
                      startOffset: file.startOffset,
                      endOffset: file.endOffset,
                      exact: copy.text,
                    },
                  };
                }
                delete copy.fileContext;
                openEditor({ chat, note: copy, rect: anchor.getBoundingClientRect() });
              })
            );
          if (editable) {
            const chat = getChat();
            actions.append(
              button('编辑', () => openEditor({ chat, note, rect: anchor.getBoundingClientRect() }, note.id)),
              button('移除', () => {
                if (!current(chat)) return;
                chat.annotations = chat.annotations.filter((item) => item.id !== note.id);
                onChange(chat);
                render();
                if (chat.annotations.length) showDetails({ items: chat.annotations }, { editable, anchor });
                else closePanel(true);
              })
            );
          }
          row.append(actions);
          panel.append(row);
        });
      }
      document.body.append(panel);
      if (mobile()) {
        panel.setAttribute('aria-modal', 'true');
        panel.showModal();
        panel.addEventListener('cancel', (event) => {
          event.preventDefault();
          closePanel(true);
        });
        panel.addEventListener('click', (event) => {
          if (event.target === panel) {
            const r = panel.getBoundingClientRect();
            if (event.clientY < r.top || event.clientX < r.left || event.clientX > r.right) closePanel(true);
          }
        });
      } else {
        panel.setAttribute('tabindex', '-1');
        place(panel, anchor.getBoundingClientRect());
      }
      if (editable || mobile()) panel.querySelector('button')?.focus({ preventScroll: true });
    }

    function resolve(note) {
      const chat = getChat();
      if (!chat) return null;
      const source = note.fleetSource;
      if (!A.sourceBelongsTo(note, chat)) return null;
      if (source?.kind === 'file' || note.fileContext) {
        const path = source?.locator.path || note.fileContext.path;
        for (const info of frames.values()) {
          if (info.path !== path || info.key !== keyFor(chat) || !info.frame.isConnected) continue;
          return { info, locator: source?.locator || { ...note.fileContext, exact: note.text } };
        }
        return null;
      }
      const id = source?.locator.itemId || note.source?.messageId;
      if (!id) return null;
      const node = [...document.querySelectorAll('[data-annotation-message]')].find(
        (node) => node.dataset.annotationMessage === id || node.dataset.annotationNativeMessage === id
      );
      if (!node) return null;
      const locator = source?.locator || { ...note.source, exact: note.text };
      const range = rangeFor(node, locator);
      return range ? { node, range } : null;
    }
    function navigate(note) {
      closePanel();
      if (!A.sourceBelongsTo(note, getChat())) {
        notify('原文不属于当前会话，已保留选文快照。');
        return;
      }
      let resolved = resolve(note);
      if (!resolved && (note.fleetSource?.kind === 'file' || note.fileContext)) {
        const chat = getChat(),
          path = note.fleetSource?.locator.path || note.fileContext.path;
        root.FleetWorkspaceTabs?.open(
          `/view?mac=${encodeURIComponent(chat.macId)}&path=${encodeURIComponent(path)}&cwd=${encodeURIComponent(chat.cwd || '')}`
        );
        // The trusted wrapper will resolve on load. No privileged file reads
        // are performed here, and text is revalidated after loading.
        pendingNavigation = { note, key: keyFor(chat) };
        return;
      }
      if (resolved?.info) {
        root.FleetWorkspaceTabs?.open(resolved.info.url);
        const ok = resolved.info.frame.contentWindow.FleetPreview?.revealAnnotation?.(resolved.locator);
        if (!ok) notify('原文已变化，已保留选文快照。');
      } else if (resolved?.range) {
        root.FleetWorkspaceTabs?.showChat();
        resolved.range.startContainer.parentElement?.scrollIntoView({
          block: 'center',
          behavior: root.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth',
        });
        selection = {
          chat: getChat(),
          note,
          rect: resolved.range.getBoundingClientRect(),
          sourceNode: resolved.node,
        };
        redraw();
        root.setTimeout(() => {
          selection = null;
          redraw();
        }, 1400);
      } else notify('原文已变化或未加载，已保留选文快照。');
    }
    let pendingNavigation;
    function registerPreview(frame, target, key) {
      const url = new URL(frame.src, root.location.origin);
      if (url.origin !== root.location.origin || url.pathname !== '/view' || !frame.isConnected || !key)
        return;
      const info = {
        frame,
        key,
        path: frame.contentDocument?.querySelector('#preview-path')?.textContent || target.detail,
        url: target.url,
      };
      const previous = frames.get(frame);
      if (previous?.doc === frame.contentDocument) previous.path = info.path;
      else {
        info.doc = frame.contentDocument;
        frames.set(frame, info);
        listen(frame.contentDocument, info);
        frame.contentWindow.FleetPreview?.onAnnotationSelection?.(() => capture(frame.contentDocument, info));
      }
      if (
        ['markdown', 'text'].includes(frame.contentDocument?.querySelector('#preview-stage')?.dataset.kind) &&
        pendingNavigation?.key === key &&
        (pendingNavigation.note.fleetSource?.locator.path || pendingNavigation.note.fileContext?.path) ===
          info.path
      ) {
        const note = pendingNavigation.note;
        pendingNavigation = null;
        root.setTimeout(() => navigate(note), 0);
      }
      redraw();
    }

    function redraw() {
      layer.replaceChildren();
      const chat = getChat();
      if (!chat) return;
      for (const frame of frames.keys()) if (!frame.isConnected) frames.delete(frame);
      const notes = [...(chat.annotations || [])];
      const savedIds = new Set(notes.map((note) => note.id));
      for (const note of selection?.notes || (selection ? [selection.note] : [])) {
        if (!notes.some((saved) => saved.id === note.id)) notes.push(note);
      }
      const pendingIds = new Set((selection?.notes || (selection ? [selection.note] : [])).map((note) => note.id));
      const occupied = [];
      notes.forEach((note, index) => {
        let resolved = resolve(note),
          rects = [];
        if (resolved?.node?.closest('#chat-scroll')?.inert) return;
        if (resolved?.range) rects = [...resolved.range.getClientRects()].map((rect) => position(rect));
        if (resolved?.info && !resolved.info.frame.hidden)
          rects =
            resolved.info.frame.contentWindow.FleetPreview?.annotationRects?.(resolved.locator)?.map((rect) =>
              position(rect, resolved.info.frame)
            ) || [];
        const bounds =
          resolved?.node?.closest('#chat-scroll')?.getBoundingClientRect() ||
          resolved?.info?.frame?.getBoundingClientRect();
        if (bounds)
          rects = rects
            .map((rect) => ({
              left: Math.max(bounds.left, rect.left),
              right: Math.min(bounds.right, rect.right),
              top: Math.max(bounds.top, rect.top),
              bottom: Math.min(bounds.bottom, rect.bottom),
            }))
            .filter((rect) => rect.right > rect.left && rect.bottom > rect.top);
        for (const rect of rects) {
          if (rect.bottom < 0 || rect.top > root.innerHeight) continue;
          const highlight = el(
            'span',
            'annotation-highlight' + (pendingIds.has(note.id) ? ' is-editing' : '')
          );
          Object.assign(highlight.style, {
            left: `${rect.left}px`,
            top: `${rect.top}px`,
            width: `${rect.right - rect.left}px`,
            height: `${rect.bottom - rect.top}px`,
          });
          layer.append(highlight);
        }
        if (!rects.length || !savedIds.has(note.id)) return;
        const rect = rects[rects.length - 1];
        if (rect.bottom < 0 || rect.top > root.innerHeight) return;
        let x = Math.min(root.innerWidth - 38, rect.right + 4),
          y = rect.top - 6;
        while (occupied.some((pos) => Math.abs(pos.x - x) < 36 && Math.abs(pos.y - y) < 36)) y += 30;
        occupied.push({ x, y });
        const marker = button(
          String(index + 1),
          () =>
            openEditor(
              { chat, note, rect },
              (chat.annotations || []).some((saved) => saved.id === note.id) ? note.id : undefined
            ),
          'annotation-number annotation-marker'
        );
        marker.setAttribute('aria-label', `注释 ${index + 1}：${note.text}`);
        Object.assign(marker.style, { left: `${x}px`, top: `${y}px` });
        layer.append(marker);
      });
    }
    function render() {
      const chat = getChat();
      if (chat !== lastChat || keyFor(chat) !== lastScope) {
        closeEditor();
        closePanel();
        lastChat = chat;
        lastScope = keyFor(chat);
      }
      if (dock) {
        dock.replaceChildren();
        dock.hidden = !chat?.annotations?.length;
        if (!dock.hidden) {
          const pill = el('div', 'annotation-chip');
          const trigger = button(
            `${chat.annotations.length} 条注释`,
            () => showDetails({ items: chat.annotations }, { editable: true, anchor: trigger }),
            'annotation-chip-open'
          );
          const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
          icon.setAttribute('viewBox', '0 0 24 24');
          icon.setAttribute('aria-hidden', 'true');
          icon.setAttribute('class', 'ic');
          icon.innerHTML =
            '<path d="M21 11.5a8.5 8.5 0 0 1-8.5 8.5 9 9 0 0 1-4-.9L3 21l1.9-5.5a9 9 0 0 1-.9-4A8.5 8.5 0 0 1 12.5 3 8.5 8.5 0 0 1 21 11.5Z"/>';
          trigger.prepend(icon);
          trigger.setAttribute('aria-haspopup', 'dialog');
          const remove = button(
            '×',
            () => {
              chat.annotations = [];
              onChange(chat);
              closePanel();
              render();
            },
            'annotation-chip-remove'
          );
          remove.setAttribute('aria-label', '移除全部注释');
          pill.append(trigger, remove);
          dock.append(pill);
        }
      }
      redraw();
    }
    function reference(protocol, index, context) {
      const namespace = protocol === 'codex' ? A.CODEX_VERSION : A.FLEET_VERSION;
      if (!context || context.version !== namespace)
        return document.createTextNode(`:${protocol}-annotation{index="${index}"}`);
      const candidates = context.candidates;
      const valid = A.canonicalIndex(index),
        ambiguous = context?.ambiguous && candidates.length > 0;
      const note =
        valid && !ambiguous && candidates.length === 1 ? candidates[0].items[Number(index) - 1] : null;
      if (!note && !ambiguous)
        return el('span', 'annotation-reference-unresolved', `注释 ${index}（无法定位）`);
      const ref = button(
        `注释 ${index}${ambiguous ? '（来源待确认）' : ''}`,
        () => {
          if (note && !mobile() && canNavigate(note)) navigate(note);
          else
            showDetails(note ? { items: [note], indices: [Number(index)] } : { items: [] }, {
              anchor: ref,
              ambiguous,
              candidates,
            });
        },
        'annotation-reference'
      );
      ref.setAttribute('aria-label', note ? `注释 ${index}：${note.text}` : `注释 ${index}：来源待确认`);
      const preview = () => {
        root.clearTimeout(hoverTimer);
        hoverTimer = root.setTimeout(
          () =>
            showDetails(note ? { items: [note], indices: [Number(index)] } : { items: [] }, {
              anchor: ref,
              ambiguous,
              candidates,
            }),
          300
        );
      };
      ref.addEventListener('pointerenter', (event) => {
        if (event.pointerType !== 'touch') preview();
      });
      ref.addEventListener('pointerleave', () => root.clearTimeout(hoverTimer));
      ref.addEventListener('focus', () => {
        if (!mobile() && !restoringFocus) preview();
      });
      return ref;
    }
    const sentAttachment = (envelope) =>
      button(
        `${envelope.items.length} 条注释`,
        (event) => showDetails(envelope, { anchor: event.currentTarget }),
        'annotation-owned annotation-sent'
      );
    root.addEventListener('resize', () => {
      redraw();
      if (editing) place(editor, editing.rect);
    });
    root.visualViewport?.addEventListener('resize', () => {
      if (editing) place(editor, editing.rect);
    });
    return {
      render,
      redraw,
      reference,
      sentAttachment,
      registerPreview,
      snapshot,
      rangeFor,
      ownsSelection: (node) =>
        !!(
          (selection?.sourceNodes || [selection?.sourceNode]).some((source) => source && node.contains(source)) ||
          (() => {
            const sel = root.getSelection();
            return sel?.rangeCount && !sel.isCollapsed && node.contains(sel.anchorNode);
          })()
        ),
      reset: () => {
        closeEditor();
        closePanel();
        frames.clear();
        pendingNavigation = null;
        layer.replaceChildren();
        if (dock) {
          dock.replaceChildren();
          dock.hidden = true;
        }
      },
      isOwned: owns,
    };
  }
  root.FleetAnnotationUI = { init, snapshot, rangeFor };
})(globalThis);
