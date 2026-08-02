(function (root) {
  'use strict';

  const PREVIEW_EXTENSIONS = new Set([
    '.md', '.markdown', '.html', '.htm',
    '.txt', '.log', '.json', '.jsonl', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx',
    '.go', '.py', '.rb', '.sh', '.zsh', '.yaml', '.yml', '.toml', '.xml', '.csv', '.ini', '.conf', '.env',
    '.pdf',
    '.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif',
    '.mp4', '.m4v', '.webm', '.mov',
    '.mp3', '.m4a', '.aac', '.wav', '.ogg', '.flac',
  ]);
  const TEXT_EXTENSIONS = new Set([
    '.txt', '.log', '.json', '.jsonl', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx',
    '.go', '.py', '.rb', '.sh', '.zsh', '.yaml', '.yml', '.toml', '.xml', '.csv', '.ini', '.conf', '.env', '.css',
  ]);
  const TEXT_WRAP_KEY = 'fleet-preview-text-wrap';
  const RESOURCE_EXTENSIONS = new Set([...PREVIEW_EXTENSIONS, '.css']);
  const RESOURCE_ATTRS = [
    ['img', 'src'], ['video', 'src'], ['video', 'poster'], ['audio', 'src'], ['source', 'src'],
  ];

  function decodedPath(value) {
    try { return decodeURIComponent(value); } catch (_) { return value; }
  }

  function pathWithoutLocationSuffix(value) {
    return value.replace(/:\d+(?::\d+)?$/, '');
  }

  function extensionOf(value) {
    const clean = pathWithoutLocationSuffix(value).toLowerCase();
    const slash = clean.lastIndexOf('/');
    const dot = clean.lastIndexOf('.');
    return dot > slash ? clean.slice(dot) : '';
  }

  function isTextPreviewPath(value) {
    return TEXT_EXTENSIONS.has(extensionOf(String(value || '')));
  }

  function textWrapEnabled() {
    try { return root.localStorage?.getItem(TEXT_WRAP_KEY) === '1'; } catch (_) { return false; }
  }

  function updateTextWrapButton(button, enabled) {
    if (!button) return;
    const label = enabled ? '关闭自动换行' : '开启自动换行';
    button.setAttribute('aria-pressed', String(enabled));
    button.setAttribute('aria-label', label);
    button.title = label;
  }

  function setTextWrap(enabled, persist = true) {
    const value = !!enabled;
    root.document?.querySelector('#preview-text')?.classList.toggle('is-wrapped', value);
    updateTextWrapButton(root.document?.querySelector('#preview-wrap'), value);
    if (persist) {
      try { root.localStorage?.setItem(TEXT_WRAP_KEY, value ? '1' : '0'); } catch (_) {}
    }
    return value;
  }

  function localPathValue(source, extensions = PREVIEW_EXTENSIONS) {
    let value = String(source || '').trim();
    if (!value || value.startsWith('#') || value.startsWith('//')) return '';
    if (/^(?:https?:|mailto:|tel:|data:|blob:|javascript:)/i.test(value)) return '';
    const hashAt = value.indexOf('#');
    if (hashAt >= 0) value = value.slice(0, hashAt);
    const queryAt = value.indexOf('?');
    if (queryAt >= 0) value = value.slice(0, queryAt);
    value = decodedPath(value);
    return extensions.has(extensionOf(value)) ? value : '';
  }

  function queryURL(pathname, values, absolute = false) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(values || {})) {
      if (value !== undefined && value !== null && value !== '') params.set(key, String(value));
    }
    const relative = `${pathname}?${params.toString()}`;
    return absolute && root.location ? `${root.location.origin}${relative}` : relative;
  }

  function resolveLocalLink(source, context = {}) {
    const path = localPathValue(source);
    if (!path || !/^m\d+$/.test(String(context.macId || ''))) return '';
    return queryURL('/view', { mac: context.macId, path, cwd: context.cwd || '' });
  }

  function fileEndpoint(kind, macId, path, options = {}) {
    if (!/^m\d+$/.test(String(macId || '')) || !path) return '';
    return queryURL(`/${macId}/api/file/${kind}`, {
      path,
      cwd: options.cwd || '',
      download: options.download ? '1' : '',
    }, !!options.absolute);
  }

  function dirname(path) {
    const value = String(path || '');
    const slash = value.lastIndexOf('/');
    return slash > 0 ? value.slice(0, slash) : '/';
  }

  function resourceURL(source, context = {}) {
    const value = String(source || '').trim();
    if (/^(?:data:|blob:)/i.test(value)) return value;
    const path = localPathValue(value, RESOURCE_EXTENSIONS);
    if (!path) return '';
    return fileEndpoint('content', context.macId, path, {
      cwd: context.cwd || '', absolute: !!context.absolute,
    });
  }

  function formatBytes(bytes) {
    const n = Math.max(0, Number(bytes) || 0);
    if (n < 1024) return `${Math.round(n)} B`;
    if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
    if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`;
    return `${(n / 1024 / 1024 / 1024).toFixed(1)} GB`;
  }

  function isPreviewRoute(pathname = root.location?.pathname || '') {
    return pathname.replace(/\/+$/, '') === '/view';
  }

  function previewRequest(search = root.location?.search || '') {
    const params = new URLSearchParams(search);
    const macId = params.get('mac') || '';
    const path = params.get('path') || '';
    if (!/^m\d+$/.test(macId) || !path) return null;
    return { macId, path, cwd: params.get('cwd') || '', embed: params.get('embed') === '1' };
  }

  function setPreviewState(kind) {
    const stage = document.querySelector('#preview-stage');
    if (!stage) return;
    stage.dataset.kind = kind;
    stage.querySelectorAll('[data-preview-kind]').forEach((node) => {
      node.hidden = node.dataset.previewKind !== kind;
    });
  }

  function showPreviewError(message) {
    const error = document.querySelector('#preview-error');
    if (!error) return;
    error.textContent = message || '无法预览文件。';
    setPreviewState('error');
  }

  function rewriteCSSURLs(css, context) {
    return String(css || '').replace(/url\(\s*(['"]?)([^'"\)]+)\1\s*\)/gi, (whole, _quote, source) => {
      const value = String(source || '').trim();
      if (!value || value.startsWith('#') || /^(?:data:|blob:)/i.test(value)) return whole;
      const resolved = resourceURL(value, context);
      return resolved ? `url("${resolved.replaceAll('"', '%22')}")` : 'url("")';
    });
  }

  function safeHTMLDocument(source, context) {
    if (!root.DOMPurify || typeof DOMParser === 'undefined') return '';
    const clean = root.DOMPurify.sanitize(String(source || ''), {
      WHOLE_DOCUMENT: true,
      FORBID_TAGS: ['script', 'iframe', 'frame', 'object', 'embed', 'form', 'input', 'button', 'textarea', 'select', 'option', 'base'],
      FORBID_ATTR: ['srcdoc'],
    });
    const doc = new DOMParser().parseFromString(clean, 'text/html');
    const resourceContext = { macId: context.macId, cwd: dirname(context.path), absolute: true };

    for (const [selector, attr] of RESOURCE_ATTRS) {
      doc.querySelectorAll(`${selector}[${attr}]`).forEach((node) => {
        const resolved = resourceURL(node.getAttribute(attr), resourceContext);
        if (resolved) node.setAttribute(attr, resolved);
        else node.removeAttribute(attr);
      });
    }
    doc.querySelectorAll('[srcset]').forEach((node) => node.removeAttribute('srcset'));
    doc.querySelectorAll('link').forEach((link) => {
      if (String(link.getAttribute('rel') || '').toLowerCase() !== 'stylesheet') {
        link.remove();
        return;
      }
      const resolved = resourceURL(link.getAttribute('href'), resourceContext);
      if (resolved) link.setAttribute('href', resolved);
      else link.remove();
    });
    doc.querySelectorAll('style').forEach((style) => {
      style.textContent = rewriteCSSURLs(style.textContent, resourceContext);
    });
    doc.querySelectorAll('[style]').forEach((node) => {
      node.setAttribute('style', rewriteCSSURLs(node.getAttribute('style'), resourceContext));
    });
    doc.querySelectorAll('a[href]').forEach((link) => {
      const href = link.getAttribute('href') || '';
      if (href.startsWith('#')) return;
      const local = resolveLocalLink(href, { macId: context.macId, cwd: dirname(context.path) });
      if (local) link.setAttribute('href', `${root.location.origin}${local}`);
      else if (!/^https?:\/\//i.test(href)) link.removeAttribute('href');
      link.setAttribute('target', '_blank');
      link.setAttribute('rel', 'noopener noreferrer');
    });

    const contentSource = `${root.location.origin}/${context.macId}/api/file/content`;
    const csp = doc.createElement('meta');
    csp.setAttribute('http-equiv', 'Content-Security-Policy');
    csp.setAttribute('content', [
      "default-src 'none'", "script-src 'none'", "connect-src 'none'", "object-src 'none'", "frame-src 'none'",
      "form-action 'none'", "base-uri 'none'", `img-src data: blob: ${contentSource}`,
      `media-src data: blob: ${contentSource}`, `style-src 'unsafe-inline' ${contentSource}`, "font-src data:",
    ].join('; '));
    doc.head.prepend(csp);
    return '<!doctype html>\n' + doc.documentElement.outerHTML;
  }

  function renderPreview(meta, request) {
    const title = document.querySelector('#preview-title');
    const path = document.querySelector('#preview-path');
    const kind = document.querySelector('#preview-kind');
    const size = document.querySelector('#preview-size');
    const download = document.querySelector('#preview-download');
    const previewMeta = document.querySelector('#preview-meta');
    if (title) title.textContent = meta.name || '文件预览';
    if (path) path.textContent = meta.path || request.path;
    if (kind) kind.textContent = ({
      markdown: 'Markdown', text: '文本', html: 'HTML', pdf: 'PDF',
      image: '图片', video: '视频', audio: '音频',
    })[meta.kind] || meta.kind;
    if (size) size.textContent = formatBytes(meta.size);
    if (previewMeta) previewMeta.hidden = false;
    document.title = `${meta.name || '文件'} - fleet hub`;
    if (download) {
      download.href = fileEndpoint('content', request.macId, meta.path, { download: true });
      download.setAttribute('download', meta.name || 'download');
      download.hidden = false;
    }

    const baseContext = { macId: request.macId, cwd: dirname(meta.path), absolute: false };
    if (meta.kind === 'markdown') {
      const target = document.querySelector('#preview-markdown');
      target.replaceChildren(root.FleetMarkdown.renderMarkdown(
        meta.content || '',
        (source) => resourceURL(source, baseContext),
        (source) => resolveLocalLink(source, baseContext) || source,
      ));
      setPreviewState('markdown');
      return;
    }
    if (meta.kind === 'html') {
      const frame = document.querySelector('#preview-html');
      frame.srcdoc = safeHTMLDocument(meta.content || '', { macId: request.macId, path: meta.path });
      setPreviewState('html');
      return;
    }
    if (meta.kind === 'text') {
      const target = document.querySelector('#preview-text');
      const wrap = document.querySelector('#preview-wrap');
      target.textContent = meta.content || '';
      if (wrap) wrap.hidden = false;
      setTextWrap(textWrapEnabled(), false);
      setPreviewState('text');
      return;
    }
    const source = fileEndpoint('content', request.macId, meta.path);
    if (meta.kind === 'pdf') {
      const frame = document.querySelector('#preview-pdf');
      frame.src = source;
    } else if (meta.kind === 'image') {
      const image = document.querySelector('#preview-image');
      image.alt = meta.name || '图片';
      image.onerror = () => showPreviewError('浏览器无法显示这张图片。');
      image.src = source;
    } else if (meta.kind === 'video') {
      const video = document.querySelector('#preview-video');
      video.onerror = () => showPreviewError('浏览器不支持该视频的封装或编码。');
      video.src = source;
      video.load();
    } else if (meta.kind === 'audio') {
      const audio = document.querySelector('#preview-audio');
      audio.onerror = () => showPreviewError('浏览器不支持该音频的封装或编码。');
      audio.src = source;
      audio.load();
    }
    setPreviewState(meta.kind);
  }

  async function initRoute() {
    const request = previewRequest();
    const app = document.querySelector('#app');
    const page = document.querySelector('#preview-page');
    if (app) app.hidden = true;
    if (page) page.hidden = false;
    if (!request) {
      showPreviewError('预览链接缺少主机或文件路径。');
      return;
    }
    document.documentElement.dataset.previewEmbed = request.embed ? 'true' : 'false';
    const wrap = document.querySelector('#preview-wrap');
    if (wrap) {
      wrap.onclick = () => setTextWrap(wrap.getAttribute('aria-pressed') !== 'true');
    }
    const host = document.querySelector('#preview-host');
    if (host) host.textContent = request.macId.toUpperCase();
    try {
      const response = await fetch(fileEndpoint('preview', request.macId, request.path, { cwd: request.cwd }), { cache: 'no-store' });
      if (!response.ok) {
        let message = `无法预览文件（HTTP ${response.status}）`;
        try { const body = await response.json(); if (body?.message) message = body.message; } catch (_) {}
        throw new Error(message);
      }
      renderPreview(await response.json(), request);
    } catch (error) {
      showPreviewError(error?.message || '无法预览文件。');
    }
  }

  root.FleetPreview = {
    resolveLocalLink, resourceURL, fileEndpoint, formatBytes, isPreviewRoute, previewRequest,
    safeHTMLDocument, rewriteCSSURLs, isTextPreviewPath, textWrapEnabled,
    updateTextWrapButton, setTextWrap, initRoute,
  };
})(typeof globalThis !== 'undefined' ? globalThis : window);
