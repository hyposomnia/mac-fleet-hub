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
  const CODE_MODE_BY_LANGUAGE = Object.freeze({
    bash: 'text/x-sh', cjs: 'text/javascript', css: 'text/css', env: 'text/x-sh',
    go: 'text/x-go', html: 'text/html', htm: 'text/html', js: 'text/javascript',
    javascript: 'text/javascript', json: 'application/json', jsonl: 'application/json',
    jsx: 'text/jsx', py: 'text/x-python', python: 'text/x-python', rb: 'text/x-ruby',
    ruby: 'text/x-ruby', sh: 'text/x-sh', shell: 'text/x-sh', toml: 'text/x-toml',
    ts: 'text/typescript', tsx: 'text/typescript-jsx', xml: 'application/xml',
    yaml: 'text/x-yaml', yml: 'text/x-yaml', zsh: 'text/x-sh',
  });

  function languageOf(code) {
    const match = String(code.className || '').match(/(?:^|\s)language-([^\s]+)/i);
    return match ? match[1].toLowerCase() : '';
  }

  function appendHighlightedLine(target, source, mode, state) {
    if (!mode || !root.CodeMirror?.StringStream) {
      target.textContent = source || '\u200b';
      return;
    }
    const stream = new root.CodeMirror.StringStream(source, 4);
    while (!stream.eol()) {
      const style = mode.token(stream, state);
      const value = stream.current();
      if (style) {
        const span = document.createElement('span');
        span.className = style.split(/\s+/).map((name) => `cm-${name}`).join(' ');
        span.textContent = value;
        target.append(span);
      } else {
        target.append(document.createTextNode(value));
      }
      stream.start = stream.pos;
    }
    if (!target.childNodes.length) target.textContent = '\u200b';
  }

  function copyText(value) {
    if (root.navigator?.clipboard?.writeText) return root.navigator.clipboard.writeText(value);
    const textarea = document.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.append(textarea);
    textarea.select();
    const copied = document.execCommand?.('copy');
    textarea.remove();
    return copied ? Promise.resolve() : Promise.reject(new Error('copy failed'));
  }

  function enhanceCodeBlock(pre) {
    const code = pre.querySelector(':scope > code');
    if (!code) return;
    const source = code.textContent.replace(/\n$/, '');
    const language = languageOf(code);
    const viewer = document.createElement('div');
    viewer.className = 'chat-code-block';

    const toolbar = document.createElement('div');
    toolbar.className = 'chat-code-toolbar';
    const label = document.createElement('span');
    label.className = 'chat-code-language';
    label.textContent = language || 'text';
    const copy = document.createElement('button');
    copy.className = 'chat-code-copy';
    copy.type = 'button';
    copy.textContent = '复制';
    copy.setAttribute('aria-label', '复制代码');
    copy.addEventListener('click', async () => {
      try {
        await copyText(source);
        copy.textContent = '√';
        copy.classList.add('is-copied');
        copy.setAttribute('aria-label', '已复制');
        root.setTimeout(() => {
          copy.textContent = '复制';
          copy.classList.remove('is-copied');
          copy.setAttribute('aria-label', '复制代码');
        }, 1800);
      } catch (_) {
        copy.textContent = '复制失败';
      }
    });
    toolbar.append(label, copy);

    const lines = document.createElement('div');
    lines.className = 'chat-code-lines cm-s-default';
    let mode = null;
    let state = null;
    if (language && root.CodeMirror?.getMode) {
      mode = root.CodeMirror.getMode({ indentUnit: 2 }, CODE_MODE_BY_LANGUAGE[language] || language);
      if (mode?.name === 'null') mode = null;
      else state = root.CodeMirror.startState(mode);
    }
    source.split('\n').forEach((line, index) => {
      const row = document.createElement('div');
      row.className = 'chat-code-line';
      const number = document.createElement('span');
      number.className = 'chat-code-line-number';
      number.textContent = String(index + 1);
      number.setAttribute('aria-hidden', 'true');
      const content = document.createElement('span');
      content.className = 'chat-code-line-content';
      appendHighlightedLine(content, line, mode, state);
      row.append(number, content);
      lines.append(row);
    });
    viewer.append(toolbar, lines);
    pre.replaceWith(viewer);
  }

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
      ALLOWED_ATTR: ['href', 'title', 'start', 'src', 'alt', 'class'],
    });
    chunk.querySelectorAll('pre').forEach(enhanceCodeBlock);
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
