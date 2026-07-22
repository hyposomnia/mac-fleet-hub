(function (root) {
  'use strict';

  const ALLOWED_TAGS = ['p', 'br', 'strong', 'em', 'del', 'blockquote', 'ul', 'ol', 'li', 'pre', 'code', 'a',
    'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'table', 'thead', 'tbody', 'tr', 'th', 'td'];

  function renderMarkdown(text) {
    const body = document.createElement('div');
    body.className = 'chat-markdown';
    const source = text || '…';
    if (!root.marked || !root.DOMPurify) {
      body.textContent = source;
      return body;
    }
    try {
      const parsed = root.marked.parse(source, { gfm: true, breaks: true });
      body.innerHTML = root.DOMPurify.sanitize(parsed, {
        ALLOWED_TAGS,
        ALLOWED_ATTR: ['href', 'title', 'start'],
      });
      body.querySelectorAll('a[href]').forEach((link) => {
        link.target = '_blank';
        link.rel = 'noopener noreferrer';
      });
      body.querySelectorAll('a:not([href])').forEach((link) => link.replaceWith(...link.childNodes));
    } catch (_) {
      body.textContent = source;
    }
    return body;
  }

  root.FleetMarkdown = { renderMarkdown };
})(typeof globalThis !== 'undefined' ? globalThis : window);
