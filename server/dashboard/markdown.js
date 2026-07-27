(function (root) {
  'use strict';

  const ALLOWED_TAGS = ['p', 'br', 'strong', 'em', 'del', 'blockquote', 'ul', 'ol', 'li', 'pre', 'code', 'a', 'img',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td'];
  const DIRECTIVE_LABELS = {
    'created-thread': '已创建任务',
    'git-create-branch': '已创建分支',
    'git-stage': '已暂存更改',
    'git-commit': '已提交代码',
    'git-push': '已推送代码',
    'git-create-pr': '已创建拉取请求',
    'code-comment': '代码批注',
  };

  function parseDirectiveAttrs(source) {
    const attrs = {};
    const pattern = /([A-Za-z][A-Za-z0-9_-]*)\s*=\s*(?:"((?:\\.|[^"\\])*)"|([^\s}]+))/g;
    let cursor = 0;
    let match;
    while ((match = pattern.exec(source))) {
      if (source.slice(cursor, match.index).trim()) return null;
      if (match[2] !== undefined) {
        try {
          attrs[match[1]] = JSON.parse(`"${match[2]}"`);
        } catch (_) {
          return null;
        }
      } else if (match[3] === 'true' || match[3] === 'false') {
        attrs[match[1]] = match[3] === 'true';
      } else {
        attrs[match[1]] = match[3];
      }
      cursor = pattern.lastIndex;
    }
    return source.slice(cursor).trim() ? null : attrs;
  }

  function parseCodexDirective(line) {
    const match = String(line || '').trim().match(/^::([a-z][a-z0-9-]*)\{(.*)\}$/);
    if (!match || !DIRECTIVE_LABELS[match[1]]) return null;
    const attrs = parseDirectiveAttrs(match[2]);
    if (!attrs) return null;
    return { name: match[1], label: DIRECTIVE_LABELS[match[1]], attrs };
  }

  function splitCodexContent(text) {
    const parts = [];
    let markdown = [];
    let fence = '';
    const flushMarkdown = () => {
      const value = markdown.join('\n').trim();
      if (value) parts.push({ type: 'markdown', value });
      markdown = [];
    };
    for (const line of String(text || '…').split(/\r?\n/)) {
      const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
      if (fence) {
        markdown.push(line);
        if (fenceMatch && fenceMatch[1][0] === fence[0] && fenceMatch[1].length >= fence.length) fence = '';
        continue;
      }
      if (fenceMatch) {
        fence = fenceMatch[1];
        markdown.push(line);
        continue;
      }
      const directive = line.startsWith('::') ? parseCodexDirective(line) : null;
      if (!directive) {
        markdown.push(line);
        continue;
      }
      flushMarkdown();
      parts.push({ type: 'directive', value: directive });
    }
    flushMarkdown();
    return parts;
  }

  function appendMarkdown(body, source, resolveMedia, resolveLink) {
    if (!root.marked || !root.DOMPurify) {
      const fallback = document.createElement('p');
      fallback.textContent = source;
      body.append(fallback);
      return;
    }
    const parsed = root.marked.parse(source, { gfm: true, breaks: true });
    const chunk = document.createElement('div');
    chunk.innerHTML = root.DOMPurify.sanitize(parsed, {
      ALLOWED_TAGS,
      ALLOWED_ATTR: ['href', 'title', 'start', 'src', 'alt'],
    });
    chunk.querySelectorAll('a[href]').forEach((link) => {
      const original = link.getAttribute('href');
      const resolved = typeof resolveLink === 'function' ? resolveLink(original) : original;
      if (resolved) link.setAttribute('href', resolved);
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
    });
    chunk.querySelectorAll('a:not([href])').forEach((link) => link.replaceWith(...link.childNodes));
    chunk.querySelectorAll('img[src]').forEach((img) => {
      const resolved = typeof resolveMedia === 'function' ? resolveMedia(img.getAttribute('src')) : img.getAttribute('src');
      if (!resolved) {
        img.replaceWith(document.createTextNode(img.alt || '图片'));
        return;
      }
      img.src = resolved;
      img.loading = 'lazy';
      img.decoding = 'async';
    });
    body.append(...chunk.childNodes);
  }

  function directiveDetail(directive) {
    const attrs = directive.attrs || {};
    if (directive.name === 'git-create-branch' || directive.name === 'git-push') return attrs.branch || '';
    if (directive.name === 'git-create-pr') {
      return /^https?:\/\//i.test(String(attrs.url || '')) ? attrs.url : (attrs.branch || '');
    }
    if (directive.name === 'created-thread') return attrs.threadId || attrs.clientThreadId || '';
    if (directive.name === 'code-comment') return attrs.file || '';
    if (!attrs.cwd) return '';
    const path = String(attrs.cwd).replace(/[\\/]+$/, '');
    return path.split(/[\\/]/).pop() || path;
  }

  function renderDirective(directive) {
    const row = document.createElement('div');
    row.className = `chat-directive ${directive.name}`;
    row.setAttribute('role', 'status');

    const icon = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    icon.setAttribute('class', 'chat-directive-icon');
    icon.setAttribute('viewBox', '0 0 24 24');
    icon.setAttribute('aria-hidden', 'true');
    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
    circle.setAttribute('cx', '12');
    circle.setAttribute('cy', '12');
    circle.setAttribute('r', '9');
    const check = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    check.setAttribute('d', 'm8 12 2.6 2.6L16.5 9');
    icon.append(circle, check);

    const label = document.createElement('span');
    label.className = 'chat-directive-label';
    label.textContent = directive.label;
    row.append(icon, label);

    const detail = directiveDetail(directive);
    if (detail) {
      const detailNode = directive.name === 'git-create-pr' && /^https?:\/\//i.test(String(directive.attrs.url || ''))
        ? document.createElement('a')
        : document.createElement('span');
      detailNode.className = 'chat-directive-detail mono';
      detailNode.textContent = detail;
      if (detailNode.tagName === 'A') {
        detailNode.href = directive.attrs.url;
        detailNode.target = '_blank';
        detailNode.rel = 'noopener noreferrer';
      }
      if (directive.attrs.cwd) detailNode.title = directive.attrs.cwd;
      row.append(detailNode);
    }
    return row;
  }

  function renderMarkdown(text, resolveMedia, resolveLink) {
    const body = document.createElement('div');
    body.className = 'chat-markdown';
    try {
      for (const part of splitCodexContent(text)) {
        if (part.type === 'directive') body.append(renderDirective(part.value));
        else appendMarkdown(body, part.value, resolveMedia, resolveLink);
      }
    } catch (_) {
      body.textContent = text || '…';
    }
    return body;
  }

  root.FleetMarkdown = { renderMarkdown, parseCodexDirective, splitCodexContent };
})(typeof globalThis !== 'undefined' ? globalThis : window);
