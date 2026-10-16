(function (root) {
  'use strict';

  const PREVIEW_EXTENSIONS = new Set([
    '.md', '.markdown', '.html', '.htm',
    '.txt', '.log', '.json', '.jsonl', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx',
    '.go', '.py', '.rb', '.sh', '.zsh', '.yaml', '.yml', '.toml', '.xml', '.csv', '.ini', '.conf', '.env', '.css',
    '.pdf',
    '.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif', '.svg', '.heic', '.heif',
    '.mp4', '.m4v', '.webm', '.mov', '.ogv', '.mpeg', '.mpg',
    '.mp3', '.m4a', '.aac', '.wav', '.ogg', '.flac', '.opus', '.aif', '.aiff',
  ]);
  const TEXT_EXTENSIONS = new Set([
    '.txt', '.log', '.json', '.jsonl', '.js', '.mjs', '.cjs', '.ts', '.tsx', '.jsx',
    '.go', '.py', '.rb', '.sh', '.zsh', '.yaml', '.yml', '.toml', '.xml', '.csv', '.ini', '.conf', '.env', '.css',
  ]);
  const TEXT_WRAP_KEY = 'fleet-preview-text-wrap';
  const TEXT_MODE_BY_EXTENSION = Object.freeze({
    '.json': 'application/json', '.jsonl': 'application/json',
    '.js': 'text/javascript', '.mjs': 'text/javascript', '.cjs': 'text/javascript', '.jsx': 'text/jsx',
    '.ts': 'text/typescript', '.tsx': 'text/typescript-jsx',
    '.go': 'text/x-go', '.py': 'text/x-python', '.rb': 'text/x-ruby',
    '.sh': 'text/x-sh', '.zsh': 'text/x-sh', '.env': 'text/x-sh',
    '.yaml': 'text/x-yaml', '.yml': 'text/x-yaml', '.toml': 'text/x-toml',
    '.xml': 'application/xml', '.css': 'text/css',
    '.ini': 'text/x-properties', '.conf': 'text/x-properties',
  });
  const RESOURCE_EXTENSIONS = new Set([...PREVIEW_EXTENSIONS, '.vtt']);
  const HTML_TEXT_RESOURCE_EXTENSIONS = new Set(['.css', '.js', '.mjs', '.cjs']);
  const HTML_RUNTIME_RESOURCE_EXTENSIONS = new Set([
    '.json', '.txt', '.xml', '.csv',
    '.png', '.jpg', '.jpeg', '.gif', '.webp', '.avif', '.svg', '.heic', '.heif',
    '.mp4', '.m4v', '.webm', '.mov', '.ogv', '.mpeg', '.mpg',
    '.mp3', '.m4a', '.aac', '.wav', '.ogg', '.flac', '.opus', '.aif', '.aiff', '.vtt',
    '.woff', '.woff2', '.ttf', '.otf', '.eot',
  ]);
  const HTML_RESOURCE_REQUEST = 'fleet-preview-resource-request-v1';
  const HTML_RESOURCE_RESPONSE = 'fleet-preview-resource-response-v1';
  const HTML_INLINE_ASSET_LIMIT = 16 << 20;
  const HTML_RUNTIME_ASSET_LIMIT = 256 << 20;
  const HTML_MIME_BY_EXTENSION = Object.freeze({
    '.json': 'application/json', '.txt': 'text/plain', '.xml': 'application/xml', '.csv': 'text/csv',
    '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif',
    '.webp': 'image/webp', '.avif': 'image/avif', '.svg': 'image/svg+xml', '.heic': 'image/heic', '.heif': 'image/heif',
    '.mp4': 'video/mp4', '.m4v': 'video/x-m4v', '.webm': 'video/webm', '.mov': 'video/quicktime',
    '.ogv': 'video/ogg', '.mpeg': 'video/mpeg', '.mpg': 'video/mpeg',
    '.mp3': 'audio/mpeg', '.m4a': 'audio/mp4', '.aac': 'audio/aac', '.wav': 'audio/wav',
    '.ogg': 'audio/ogg', '.flac': 'audio/flac', '.opus': 'audio/opus', '.aif': 'audio/aiff', '.aiff': 'audio/aiff',
    '.vtt': 'text/vtt', '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf',
    '.otf': 'font/otf', '.eot': 'application/vnd.ms-fontobject',
  });
  const RESOURCE_ATTRS = [
    ['img', 'src'], ['video', 'src'], ['video', 'poster'], ['audio', 'src'], ['source', 'src'], ['track', 'src'],
  ];
  let textEditor = null;
  let heicDecoderPromise = null;
  let imagePreview = null;
  let htmlPreview = null;
  let previewRequestVersion = 0;
  let annotationSelectionChanged = () => {};

  function loadHEICDecoder() {
    if (typeof root.HeicTo === 'function') return Promise.resolve(root.HeicTo);
    if (!heicDecoderPromise) {
      heicDecoderPromise = new Promise((resolve, reject) => {
        const script = document.createElement('script');
        script.src = '/vendor/heic-to.js?v=1.6.5';
        script.async = true;
        const timer = root.setTimeout(() => fail(), 20000);
        const fail = () => {
          root.clearTimeout(timer);
          script.remove();
          reject(new Error('无法载入 HEIC 解码器。'));
        };
        script.onerror = fail;
        script.onload = () => {
          if (typeof root.HeicTo !== 'function') { fail(); return; }
          root.clearTimeout(timer);
          resolve(root.HeicTo);
        };
        document.head.append(script);
      }).catch((error) => {
        heicDecoderPromise = null;
        throw error;
      });
    }
    return heicDecoderPromise;
  }

  function clearImagePreview() {
    if (!imagePreview) return;
    imagePreview.controller.abort();
    root.clearTimeout(imagePreview.timer);
    imagePreview.image.onload = null;
    imagePreview.image.onerror = null;
    if (imagePreview.objectURL) root.URL.revokeObjectURL(imagePreview.objectURL);
    imagePreview = null;
  }

  function renderImagePreview(image, meta, source) {
    const preview = { image, controller: new root.AbortController(), objectURL: '', timer: null };
    imagePreview = preview;
    const current = () => imagePreview === preview && !preview.controller.signal.aborted;
    const heic = ['.heic', '.heif'].includes(extensionOf(meta.path)) || /^image\/hei[cf](?:$|;)/i.test(meta.mime || '');
    image.alt = meta.name || '图片';
    image.onload = () => {
      root.clearTimeout(preview.timer);
      if (current()) setPreviewState('image');
    };
    const fail = () => {
      root.clearTimeout(preview.timer);
      if (current()) showPreviewError(heic
        ? 'HEIC 图片转换失败，仍可下载原文件。'
        : '无法显示这张图片，仍可下载原文件。');
    };
    image.onerror = async () => {
      if (!current()) return;
      if (!heic) { fail(); return; }
      // Safari 可直接显示 HEIC；其它浏览器才下载解码器并在 worker 中转换。
      image.onerror = fail;
      setPreviewState('loading');
      preview.timer = root.setTimeout(() => {
        fail();
        preview.controller.abort();
      }, 60000);
      try {
        const response = await root.fetch(source, { cache: 'no-store', signal: preview.controller.signal });
        if (!response.ok) throw new Error(`HTTP ${response.status}`);
        const blob = await response.blob();
        const decode = await loadHEICDecoder();
        if (!current()) return;
        const converted = await decode({ blob, type: 'image/jpeg', quality: 0.9 });
        if (!current()) return;
        preview.objectURL = root.URL.createObjectURL(converted);
        image.src = preview.objectURL;
      } catch (_) {
        fail();
      }
    };
    setPreviewState('loading');
    image.src = source;
  }

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

  function textPreviewMode(value) {
    return TEXT_MODE_BY_EXTENSION[extensionOf(String(value || ''))] || null;
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
    textEditor?.setOption('lineWrapping', value);
    updateTextWrapButton(root.document?.querySelector('#preview-wrap'), value);
    if (persist) {
      try { root.localStorage?.setItem(TEXT_WRAP_KEY, value ? '1' : '0'); } catch (_) {}
    }
    return value;
  }

  function ensureTextEditor() {
    if (textEditor) return textEditor;
    const textarea = root.document?.querySelector('#preview-code');
    if (!textarea || typeof root.CodeMirror?.fromTextArea !== 'function') return null;
    textEditor = root.CodeMirror.fromTextArea(textarea, {
      lineNumbers: true,
      lineWrapping: textWrapEnabled(),
      readOnly: true,
      cursorBlinkRate: -1,
      tabSize: 2,
      indentUnit: 2,
      viewportMargin: 10,
    });
    textEditor.on('cursorActivity', () => root.setTimeout(annotationSelectionChanged, 0));
    return textEditor;
  }

  function renderTextPreview(meta) {
    const target = root.document?.querySelector('#preview-text');
    const textarea = root.document?.querySelector('#preview-code');
    const content = String(meta.content || '');
    const editor = ensureTextEditor();
    if (editor) {
      target?.classList.remove('is-fallback');
      editor.setOption('mode', textPreviewMode(meta.path));
      editor.setValue(content);
      editor.setOption('lineWrapping', textWrapEnabled());
      root.requestAnimationFrame?.(() => editor.refresh());
      return;
    }
    if (textarea) textarea.value = content;
    target?.classList.add('is-fallback');
  }

  function localPathValue(source, extensions = PREVIEW_EXTENSIONS) {
    let value = String(source || '').trim();
    if (!value || value.startsWith('#') || value.startsWith('//')) return '';
    if (/^(?:https?:|mailto:|tel:|data:|blob:|javascript:)/i.test(value)) return '';
    if (/^file:\/\//i.test(value)) {
      try { value = new URL(value).pathname; } catch (_) { return ''; }
    }
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
      encoding: options.encoding || '',
    }, !!options.absolute);
  }

  function dirname(path) {
    const value = String(path || '');
    const slash = value.lastIndexOf('/');
    return slash > 0 ? value.slice(0, slash) : '/';
  }

  function previewPathSegments(path) {
    const value = String(path || '').trim();
    if (!value) return [];
    const absolute = value.startsWith('/');
    const parts = [];
    for (const part of value.split('/')) {
      if (!part || part === '.') continue;
      if (part === '..') parts.pop();
      else parts.push(part);
    }
    const segments = absolute ? [{ label: '/', path: '/', kind: 'folder' }] : [];
    let current = '';
    parts.forEach((part, index) => {
      current = absolute ? `${current}/${part}` : (current ? `${current}/${part}` : part);
      segments.push({
        label: part,
        path: current,
        kind: index === parts.length - 1 ? 'file' : 'folder',
      });
    });
    return segments;
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
    const download = document.querySelector('#preview-download');
    if (download?.href && !download.hidden && document.createElement) {
      const action = document.createElement('a');
      action.href = download.href;
      action.setAttribute('download', '');
      action.textContent = '下载原文件';
      action.className = 'btn';
      error.append(action);
    }
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

  function rewriteSrcset(value, context) {
    return String(value || '').split(',').map((candidate) => {
      const match = candidate.trim().match(/^(\S+)(\s+.+)?$/);
      if (!match) return '';
      const resolved = resourceURL(match[1], context);
      return resolved ? `${resolved}${match[2] || ''}` : '';
    }).filter(Boolean).join(', ');
  }

  function htmlRuntimeResourcePath(source) {
    return localPathValue(source, HTML_RUNTIME_RESOURCE_EXTENSIONS);
  }

  function htmlMimeForPath(path, fallback = 'application/octet-stream') {
    return HTML_MIME_BY_EXTENSION[extensionOf(path)] || fallback;
  }

  function clearHTMLPreview() {
    if (!htmlPreview) return;
    htmlPreview.controller.abort();
    if (htmlPreview.frame) htmlPreview.frame.srcdoc = '';
    htmlPreview = null;
  }

  async function replaceAsync(value, pattern, replacer) {
    const source = String(value || '');
    const matches = [...source.matchAll(pattern)];
    if (!matches.length) return source;
    const replacements = await Promise.all(matches.map((match) => replacer(...match)));
    let result = '';
    let cursor = 0;
    matches.forEach((match, index) => {
      result += source.slice(cursor, match.index) + replacements[index];
      cursor = match.index + match[0].length;
    });
    return result + source.slice(cursor);
  }

  async function loadHTMLTextResource(source, context, extensions = HTML_TEXT_RESOURCE_EXTENSIONS) {
    const path = localPathValue(source, extensions);
    if (!path) throw new Error(`不支持的 HTML 依赖：${source}`);
    const response = await root.fetch(fileEndpoint('preview', context.macId, path, {
      cwd: context.cwd || '', encoding: 'utf-8',
    }), { cache: 'no-store', signal: context.signal });
    if (!response.ok) throw new Error(`无法载入 HTML 依赖 ${source}（HTTP ${response.status}）`);
    const meta = await response.json();
    return { content: String(meta.content || ''), path: meta.path || path };
  }

  function arrayBufferDataURL(buffer, mime) {
    const bytes = new Uint8Array(buffer);
    let binary = '';
    for (let offset = 0; offset < bytes.length; offset += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + 0x8000, bytes.length)));
    }
    return `data:${mime || 'application/octet-stream'};base64,${root.btoa(binary)}`;
  }

  async function loadHTMLAssetDataURL(source, context) {
    const path = htmlRuntimeResourcePath(source);
    if (!path) return '';
    const response = await root.fetch(fileEndpoint('content', context.macId, path, {
      cwd: context.cwd || '', download: true,
    }), { cache: 'no-store', signal: context.signal });
    if (!response.ok) throw new Error(`无法载入 HTML 资源 ${source}（HTTP ${response.status}）`);
    const declaredSize = Number(response.headers?.get?.('content-length')) || 0;
    if (declaredSize > HTML_INLINE_ASSET_LIMIT) throw new Error(`HTML 样式资源过大：${source}`);
    const buffer = await response.arrayBuffer();
    if (buffer.byteLength > HTML_INLINE_ASSET_LIMIT) throw new Error(`HTML 样式资源过大：${source}`);
    const responseType = String(response.headers?.get?.('content-type') || '').split(';')[0];
    const mime = responseType && responseType !== 'application/octet-stream'
      ? responseType
      : htmlMimeForPath(path);
    return arrayBufferDataURL(buffer, mime);
  }

  async function inlineCSSResources(css, context, seen = new Set()) {
    const imports = /@import\s+(?:url\(\s*)?(['"])([^'"]+)\1\s*\)?\s*([^;]*);/gi;
    let result = await replaceAsync(css, imports, async (whole, _quote, source, media) => {
      const path = localPathValue(source, new Set(['.css']));
      if (!path) return '';
      const key = `${context.cwd || ''}\n${path}`;
      if (seen.has(key)) return '';
      seen.add(key);
      const imported = await loadHTMLTextResource(source, context, new Set(['.css']));
      const nested = await inlineCSSResources(imported.content, {
        ...context, cwd: dirname(imported.path),
      }, seen);
      const condition = String(media || '').trim();
      return condition ? `@media ${condition}{${nested}}` : nested;
    });
    result = await replaceAsync(result, /url\(\s*(['"]?)([^'"\)]+)\1\s*\)/gi, async (whole, _quote, source) => {
      const value = String(source || '').trim();
      if (!value || value.startsWith('#') || /^(?:data:|blob:)/i.test(value)) return whole;
      const dataURL = await loadHTMLAssetDataURL(value, context);
      return dataURL ? `url("${dataURL}")` : 'url("")';
    });
    return result;
  }

  function htmlPreviewRuntime() {
    const requestType = 'fleet-preview-resource-request-v1';
    const responseType = 'fleet-preview-resource-response-v1';
    const pending = new Map();
    const objectURLs = new Set();
    const resourceURLs = new Map();
    const tokens = new WeakMap();
    let nextID = 1;

    function localSource(source) {
      const value = String(source || '').trim();
      return value && !value.startsWith('#') && !value.startsWith('//') &&
        !/^(?:https?:|mailto:|tel:|javascript:)/i.test(value);
    }

    function requestResource(source) {
      return new Promise((resolve, reject) => {
        const id = nextID++;
        pending.set(id, { resolve, reject });
        parent.postMessage({ type: requestType, id, source: String(source || '') }, '*');
      });
    }

    function objectURL(source, payload) {
      if (resourceURLs.has(source)) return resourceURLs.get(source);
      const url = URL.createObjectURL(new Blob([payload.buffer], { type: payload.mime || 'application/octet-stream' }));
      objectURLs.add(url);
      resourceURLs.set(source, url);
      return url;
    }

    function nextToken(node, attr) {
      let values = tokens.get(node);
      if (!values) { values = new Map(); tokens.set(node, values); }
      const token = (values.get(attr) || 0) + 1;
      values.set(attr, token);
      return [values, token];
    }

    function applyResource(node, attr, source) {
      const value = String(source || '').trim();
      if (!value) return;
      if (/^(?:data:|blob:)/i.test(value)) {
        Element.prototype.setAttribute.call(node, attr, value);
        return;
      }
      if (!localSource(value)) {
        Element.prototype.removeAttribute.call(node, attr);
        return;
      }
      const [values, token] = nextToken(node, attr);
      Element.prototype.removeAttribute.call(node, attr);
      requestResource(value).then((payload) => {
        if (values.get(attr) !== token) return;
        Element.prototype.setAttribute.call(node, attr, objectURL(value, payload));
        if (node instanceof HTMLMediaElement) node.load();
      }).catch(() => {});
    }

    async function applySrcset(node, source) {
      const candidates = String(source || '').split(',').map((candidate) => {
        const match = candidate.trim().match(/^(\S+)(\s+.+)?$/);
        return match ? { source: match[1], descriptor: match[2] || '' } : null;
      }).filter(Boolean);
      const [values, token] = nextToken(node, 'srcset');
      Element.prototype.removeAttribute.call(node, 'srcset');
      try {
        const resolved = await Promise.all(candidates.map(async (candidate) => {
          if (/^(?:data:|blob:)/i.test(candidate.source)) return candidate.source + candidate.descriptor;
          if (!localSource(candidate.source)) return '';
          const payload = await requestResource(candidate.source);
          return objectURL(candidate.source, payload) + candidate.descriptor;
        }));
        if (values.get('srcset') === token) {
          Element.prototype.setAttribute.call(node, 'srcset', resolved.filter(Boolean).join(', '));
        }
      } catch (_) {}
    }

    async function applyStyle(node, source) {
      const [values, token] = nextToken(node, 'style');
      const matches = [...String(source || '').matchAll(/url\(\s*(['"]?)([^'"\)]+)\1\s*\)/gi)];
      if (!matches.length) return;
      const replacements = await Promise.all(matches.map(async (match) => {
        const value = String(match[2] || '').trim();
        if (/^(?:data:|blob:)/i.test(value)) return match[0];
        if (!localSource(value)) return 'url("")';
        const payload = await requestResource(value);
        return `url("${objectURL(value, payload)}")`;
      }));
      if (values.get('style') !== token) return;
      let result = '';
      let cursor = 0;
      matches.forEach((match, index) => {
        result += source.slice(cursor, match.index) + replacements[index];
        cursor = match.index + match[0].length;
      });
      Element.prototype.setAttribute.call(node, 'style', result + source.slice(cursor));
    }

    function scanElement(node) {
      if (!(node instanceof Element)) return;
      for (const attr of ['src', 'poster']) {
        const staged = node.getAttribute(`data-fleet-preview-${attr}`);
        if (staged !== null) {
          Element.prototype.removeAttribute.call(node, `data-fleet-preview-${attr}`);
          applyResource(node, attr, staged);
        } else if (node.hasAttribute(attr)) {
          const value = node.getAttribute(attr);
          if (!/^(?:data:|blob:)/i.test(value || '')) applyResource(node, attr, value);
        }
      }
      const stagedSrcset = node.getAttribute('data-fleet-preview-srcset');
      if (stagedSrcset !== null) {
        Element.prototype.removeAttribute.call(node, 'data-fleet-preview-srcset');
        applySrcset(node, stagedSrcset);
      } else if (node.hasAttribute('srcset')) {
        const value = node.getAttribute('srcset');
        if (!/^(?:data:|blob:)/i.test(value || '')) applySrcset(node, value);
      }
      if (node.hasAttribute('style')) {
        const style = node.getAttribute('style') || '';
        const unresolved = [...style.matchAll(/url\(\s*(['"]?)([^'"\)]+)\1\s*\)/gi)]
          .some((match) => !/^(?:data:|blob:)/i.test(String(match[2] || '').trim()));
        if (unresolved) applyStyle(node, style);
      }
    }

    function scanNode(node) {
      if (!(node instanceof Element)) return;
      scanElement(node);
      node.querySelectorAll?.('*').forEach(scanElement);
    }

    window.addEventListener('message', (event) => {
      const data = event.data || {};
      if (event.source !== parent || data.type !== responseType || !pending.has(data.id)) return;
      const task = pending.get(data.id);
      pending.delete(data.id);
      if (!data.ok || !(data.buffer instanceof ArrayBuffer)) task.reject(new Error(data.error || 'resource unavailable'));
      else task.resolve(data);
    });

    const nativeFetch = window.fetch?.bind(window);
    if (nativeFetch) {
      window.fetch = (input, init) => {
        const source = typeof input === 'string' || input instanceof URL ? String(input) : String(input?.url || '');
        if (!localSource(source)) return Promise.reject(new TypeError('Network access is disabled in HTML preview'));
        return requestResource(source).then((payload) => new Response(payload.buffer, {
          status: 200, headers: { 'content-type': payload.mime || 'application/octet-stream' },
        }));
      };
    }

    const observer = new MutationObserver((records) => {
      records.forEach((record) => {
        if (record.type === 'childList') record.addedNodes.forEach(scanNode);
        else scanElement(record.target);
      });
    });
    observer.observe(document.documentElement, {
      subtree: true, childList: true, attributes: true,
      attributeFilter: ['src', 'srcset', 'poster', 'style'],
    });
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', () => scanNode(document.documentElement), { once: true });
    else scanNode(document.documentElement);
    window.addEventListener('pagehide', () => {
      observer.disconnect();
      pending.forEach((task) => task.reject(new Error('preview closed')));
      pending.clear();
      objectURLs.forEach((url) => URL.revokeObjectURL(url));
    }, { once: true });
  }

  async function safeHTMLDocument(source, context) {
    if (!root.DOMPurify || typeof DOMParser === 'undefined') return '';
    const sourceDoc = new DOMParser().parseFromString(String(source || ''), 'text/html');
    const resourceContext = {
      macId: context.macId, cwd: dirname(context.path), signal: context.signal,
    };

    for (const link of [...sourceDoc.querySelectorAll('link')]) {
      if (String(link.getAttribute('rel') || '').toLowerCase() !== 'stylesheet') {
        link.remove();
        continue;
      }
      const stylesheet = await loadHTMLTextResource(link.getAttribute('href'), resourceContext, new Set(['.css']));
      const style = sourceDoc.createElement('style');
      if (link.getAttribute('media')) style.setAttribute('media', link.getAttribute('media'));
      style.textContent = await inlineCSSResources(stylesheet.content, {
        ...resourceContext, cwd: dirname(stylesheet.path),
      });
      link.replaceWith(style);
    }
    for (const style of [...sourceDoc.querySelectorAll('style')]) {
      style.textContent = await inlineCSSResources(style.textContent, resourceContext);
    }
    for (const node of [...sourceDoc.querySelectorAll('[style]')]) {
      node.setAttribute('style', await inlineCSSResources(node.getAttribute('style'), resourceContext));
    }

    const scripts = [];
    for (const script of [...sourceDoc.querySelectorAll('script')]) {
      let content = script.textContent || '';
      if (script.hasAttribute('src')) {
        const loaded = await loadHTMLTextResource(script.getAttribute('src'), resourceContext, new Set(['.js', '.mjs', '.cjs']));
        content = loaded.content;
      }
      const type = String(script.getAttribute('type') || '').trim();
      const executable = !type || /^(?:application|text)\/javascript$/i.test(type) || type === 'module';
      const placeholder = sourceDoc.createElement(script.parentElement === sourceDoc.head ? 'meta' : 'span');
      if (executable) {
        const index = scripts.push({ content, type }) - 1;
        placeholder.setAttribute('data-fleet-preview-script', String(index));
        if (placeholder.tagName === 'SPAN') placeholder.hidden = true;
        script.replaceWith(placeholder);
      } else {
        script.remove();
      }
    }

    const eventAttrs = new Set();
    sourceDoc.querySelectorAll('*').forEach((node) => {
      [...node.attributes].forEach((attr) => {
        if (/^on[a-z]+$/i.test(attr.name)) eventAttrs.add(attr.name.toLowerCase());
      });
    });
    const clean = root.DOMPurify.sanitize(sourceDoc.documentElement.outerHTML, {
      WHOLE_DOCUMENT: true,
      SANITIZE_DOM: false,
      FORBID_TAGS: ['iframe', 'frame', 'object', 'embed', 'base'],
      FORBID_ATTR: ['srcdoc'],
      ADD_ATTR: [...eventAttrs],
    });
    const doc = new DOMParser().parseFromString(clean, 'text/html');

    for (const [selector, attr] of RESOURCE_ATTRS) {
      doc.querySelectorAll(`${selector}[${attr}]`).forEach((node) => {
        const value = node.getAttribute(attr) || '';
        if (/^data:/i.test(value)) return;
        if (htmlRuntimeResourcePath(value)) node.setAttribute(`data-fleet-preview-${attr}`, value);
        node.removeAttribute(attr);
      });
    }
    doc.querySelectorAll('[srcset]').forEach((node) => {
      const value = node.getAttribute('srcset') || '';
      node.setAttribute('data-fleet-preview-srcset', value);
      node.removeAttribute('srcset');
    });
    doc.querySelectorAll('link').forEach((link) => link.remove());
    doc.querySelectorAll('meta[http-equiv]').forEach((meta) => meta.remove());
    doc.querySelectorAll('form').forEach((form) => form.removeAttribute('action'));
    doc.querySelectorAll('a[href]').forEach((link) => {
      const href = link.getAttribute('href') || '';
      if (href.startsWith('#')) return;
      const local = resolveLocalLink(href, { macId: context.macId, cwd: dirname(context.path) });
      if (local) link.setAttribute('href', `${root.location.origin}${local}`);
      else if (!/^https?:\/\//i.test(href)) link.removeAttribute('href');
      link.setAttribute('target', '_blank');
      link.setAttribute('rel', 'noopener noreferrer');
    });

    const csp = doc.createElement('meta');
    csp.setAttribute('http-equiv', 'Content-Security-Policy');
    csp.setAttribute('content', [
      "default-src 'none'", "script-src 'unsafe-inline'", "connect-src 'none'", "object-src 'none'", "frame-src 'none'",
      "form-action 'none'", "base-uri 'none'", "img-src data: blob:", "media-src data: blob:",
      "style-src 'unsafe-inline' data: blob:", "font-src data: blob:", "worker-src 'none'",
    ].join('; '));
    doc.head.prepend(csp);
    const runtime = doc.createElement('script');
    runtime.textContent = `(${htmlPreviewRuntime.toString()})();`;
    csp.after(runtime);
    doc.querySelectorAll('[data-fleet-preview-script]').forEach((placeholder) => {
      const item = scripts[Number(placeholder.getAttribute('data-fleet-preview-script'))];
      if (!item) { placeholder.remove(); return; }
      const script = doc.createElement('script');
      if (item.type) script.setAttribute('type', item.type);
      script.textContent = item.content.replace(/<\/script/gi, '<\\/script');
      placeholder.replaceWith(script);
    });
    return '<!doctype html>\n' + doc.documentElement.outerHTML;
  }

  async function handleHTMLResourceMessage(event) {
    const active = htmlPreview;
    const message = event.data || {};
    if (!active || event.source !== active.frame.contentWindow || message.type !== HTML_RESOURCE_REQUEST) return;
    const id = Number(message.id);
    const path = htmlRuntimeResourcePath(message.source);
    const respond = (payload, transfer) => {
      if (htmlPreview !== active || active.controller.signal.aborted) return;
      active.frame.contentWindow.postMessage({ type: HTML_RESOURCE_RESPONSE, id, ...payload }, '*', transfer || []);
    };
    if (!Number.isSafeInteger(id) || id < 1 || !path) {
      respond({ ok: false, error: 'resource rejected' });
      return;
    }
    try {
      const response = await root.fetch(fileEndpoint('content', active.macId, path, {
        cwd: active.cwd, download: true,
      }), { cache: 'no-store', signal: active.controller.signal });
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      const declaredSize = Number(response.headers?.get?.('content-length')) || 0;
      if (declaredSize > HTML_RUNTIME_ASSET_LIMIT) throw new Error('resource too large');
      const buffer = await response.arrayBuffer();
      if (buffer.byteLength > HTML_RUNTIME_ASSET_LIMIT) throw new Error('resource too large');
      const responseType = String(response.headers?.get?.('content-type') || '').split(';')[0];
      const mime = responseType && responseType !== 'application/octet-stream'
        ? responseType
        : htmlMimeForPath(path);
      respond({ ok: true, mime, buffer }, [buffer]);
    } catch (error) {
      if (error?.name !== 'AbortError') respond({ ok: false, error: 'resource unavailable' });
    }
  }

  function renderPreviewPath(node, value, request) {
    if (!node) return;
    const segments = previewPathSegments(value);
    const revealFile = request.embed && root.parent !== root
      ? root.parent?.FleetWorkspaceTabs?.revealFile
      : null;
    if (!segments.length || typeof revealFile !== 'function' || typeof node.replaceChildren !== 'function') {
      node.textContent = value;
      return;
    }
    node.replaceChildren();
    segments.forEach((segment, index) => {
      if (index > 0 && segments[index - 1].path !== '/') {
        const separator = document.createElement('span');
        separator.className = 'preview-path-separator';
        separator.setAttribute('aria-hidden', 'true');
        separator.textContent = '/';
        node.append(separator);
      }
      const button = document.createElement('button');
      const action = segment.kind === 'file' ? '选中' : '打开';
      button.type = 'button';
      button.className = 'preview-path-segment';
      button.textContent = segment.label;
      button.title = `在文件管理器中${action} ${segment.path}`;
      button.setAttribute('aria-label', button.title);
      button.onclick = () => root.parent.FleetWorkspaceTabs?.revealFile?.({
        macId: request.macId, path: segment.path, kind: segment.kind,
      });
      node.append(button);
    });
    root.requestAnimationFrame?.(() => { node.scrollLeft = node.scrollWidth; });
  }

  async function renderPreview(meta, request) {
    clearImagePreview();
    clearHTMLPreview();
    const title = document.querySelector('#preview-title');
    const path = document.querySelector('#preview-path');
    const download = document.querySelector('#preview-download');
    if (title) title.textContent = meta.name || '文件预览';
    renderPreviewPath(path, meta.path || request.path, request);
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
      const controller = new root.AbortController();
      htmlPreview = {
        frame, controller, macId: request.macId, cwd: dirname(meta.path || request.path),
      };
      const documentSource = await safeHTMLDocument(meta.content || '', {
        macId: request.macId, path: meta.path || request.path, signal: controller.signal,
      });
      if (controller.signal.aborted || htmlPreview?.controller !== controller) return;
      frame.srcdoc = documentSource;
      setPreviewState('html');
      return;
    }
    if (meta.kind === 'text') {
      const wrap = document.querySelector('#preview-wrap');
      renderTextPreview(meta);
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
      renderImagePreview(document.querySelector('#preview-image'), meta, source);
      return;
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
    const version = ++previewRequestVersion;
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
    const download = document.querySelector('#preview-download');
    if (download) {
      download.href = fileEndpoint('content', request.macId, request.path, { cwd: request.cwd, download: true });
      download.setAttribute('download', '');
      download.hidden = false;
    }
    let encoding = document.querySelector('#preview-encoding');
    const textFile = isTextPreviewPath(request.path) || ['.md', '.markdown', '.html', '.htm'].includes(extensionOf(request.path));
    if (!encoding && textFile && download?.parentNode && document.createElement) {
      const encodingControl = document.createElement('span');
      encodingControl.className = 'preview-encoding-control';
      encoding = document.createElement('select');
      encoding.id = 'preview-encoding';
      encoding.className = 'preview-encoding-select';
      encoding.setAttribute('aria-label', '文本编码');
      for (const [value, label] of [['auto', '自动识别编码'], ['utf-8', 'UTF-8'], ['utf-16le', 'UTF-16 LE'], ['utf-16be', 'UTF-16 BE'], ['gb18030', 'GB18030 / GBK'], ['big5', 'Big5'], ['shift_jis', 'Shift JIS'], ['windows-1252', 'Windows-1252']]) {
        const option = document.createElement('option');
        option.value = value; option.textContent = label; encoding.append(option);
      }
      encoding.onchange = () => initRoute();
      encodingControl.append(encoding);
      if (request.embed && page?.insertBefore) page.insertBefore(encodingControl, document.querySelector('#preview-stage'));
      else download.parentNode.insertBefore(encodingControl, download);
    }
    try {
      const response = await fetch(fileEndpoint('preview', request.macId, request.path, { cwd: request.cwd, encoding: encoding?.value }), { cache: 'no-store' });
      if (!response.ok) {
        let message = `无法预览文件（HTTP ${response.status}）`;
        try { const body = await response.json(); if (body?.message) message = body.message; } catch (_) {}
        throw new Error(message);
      }
      const meta = await response.json();
      if (version !== previewRequestVersion) return;
      await renderPreview(meta, request);
      document.dispatchEvent(new Event('fleet:preview-rendered'));
    } catch (error) {
      if (version !== previewRequestVersion) return;
      showPreviewError(error?.message || '无法预览文件。');
    }
  }


  function annotationSelection() {
    if (document.querySelector('#preview-text')?.hidden) return null;
    const textarea = document.querySelector('#preview-code');
    const text = textEditor ? textEditor.getValue() : textarea?.value || '';
    const from = textEditor ? textEditor.indexFromPos(textEditor.getCursor('from')) : textarea?.selectionStart;
    const to = textEditor ? textEditor.indexFromPos(textEditor.getCursor('to')) : textarea?.selectionEnd;
    if (from == null || from === to) return null;
    const start = textEditor ? textEditor.cursorCoords(textEditor.posFromIndex(from), 'window') : textarea.getBoundingClientRect();
    const end = textEditor ? textEditor.cursorCoords(textEditor.posFromIndex(to), 'window') : start;
    return {text: text.slice(from,to), locator: {version:root.FleetAnnotations.textVersion(text), startOffset:from,endOffset:to,exact:text.slice(from,to),
      prefix:text.slice(Math.max(0,from-64),from),suffix:text.slice(to,to+64)}, rect:{left:start.left,right:end.right,top:start.top,bottom:end.bottom}};
  }
  function annotationRects(locator) {
    const markdown = document.querySelector('#preview-markdown');
    if (markdown && !markdown.hidden) {
      const range = root.FleetAnnotationUI.rangeFor(markdown,locator);
      return range ? [...range.getClientRects()].map(rect=>({left:rect.left,right:rect.right,top:rect.top,bottom:rect.bottom})) : [];
    }
    if (!textEditor || document.querySelector('#preview-text')?.hidden) return [];
    const text = textEditor.getValue(), at = root.FleetAnnotations.locate(text,locator);
    if (at < 0) return [];
    const from=textEditor.posFromIndex(at), to=textEditor.posFromIndex(at+locator.exact.length), viewport=textEditor.getViewport();
    const rects=[];
    const bounds=textEditor.getWrapperElement().getBoundingClientRect();
    for(let line=Math.max(from.line,viewport.from);line<=Math.min(to.line,viewport.to-1);line++) {
      const start=textEditor.cursorCoords({line,ch:line===from.line?from.ch:0},'window');
      const end=textEditor.cursorCoords({line,ch:line===to.line?to.ch:textEditor.getLine(line).length},'window');
      rects.push({left:Math.max(bounds.left,start.left),right:Math.min(bounds.right,end.right),top:Math.max(bounds.top,start.top),bottom:Math.min(bounds.bottom,end.bottom)});
    }
    return rects.filter(rect=>rect.right>rect.left&&rect.bottom>rect.top);
  }
  function revealAnnotation(locator) {
    const markdown=document.querySelector('#preview-markdown');
    if (markdown && !markdown.hidden) {
      const range=root.FleetAnnotationUI.rangeFor(markdown,locator);
      if(!range)return false;
      range.startContainer.parentElement?.scrollIntoView({block:'center'});
      if(root.CSS?.highlights && root.Highlight) {
        root.CSS.highlights.set('fleet-annotation-reveal',new root.Highlight(range));
        root.setTimeout(()=>root.CSS.highlights.delete('fleet-annotation-reveal'),1600);
      }
      return true;
    }
    if(!textEditor || document.querySelector('#preview-text')?.hidden)return false;
    const at=root.FleetAnnotations.locate(textEditor.getValue(),locator);if(at<0)return false;
    const from=textEditor.posFromIndex(at),to=textEditor.posFromIndex(at+locator.exact.length);
    textEditor.scrollIntoView({from,to},80);
    const mark=textEditor.markText(from,to,{className:'annotation-text-reveal'});
    root.setTimeout(()=>mark.clear(),1600);return true;
  }

  root.FleetPreview = {
    annotationSelection, annotationRects, revealAnnotation,
    onAnnotationSelection(callback) { annotationSelectionChanged = callback; },
    clearAnnotationSelection() { if(textEditor)textEditor.setCursor(textEditor.getCursor()); },
    resolveLocalLink, resourceURL, fileEndpoint, formatBytes, isPreviewRoute, previewRequest, previewPathSegments,
    safeHTMLDocument, rewriteCSSURLs, isTextPreviewPath, textPreviewMode, textWrapEnabled,
    htmlRuntimeResourcePath, updateTextWrapButton, setTextWrap, initRoute,
  };
  root.addEventListener?.('message', handleHTMLResourceMessage);
  root.addEventListener?.('pagehide', () => { clearImagePreview(); clearHTMLPreview(); });
})(typeof globalThis !== 'undefined' ? globalThis : window);
