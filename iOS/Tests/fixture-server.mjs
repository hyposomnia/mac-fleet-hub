import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const dashboard = fileURLToPath(new URL('../../server/dashboard/', import.meta.url));
const requests = [];
const control = { accessMode: 'read_write', approvalMode: 'on-request', model: 'fixture-model', turnPhase: 'idle', items: [],
  serverEpoch: 'fixture', snapshotVersion: 1, writerOwner: 'fleet', pendingRequests: 0 };
let sessions;
let names;
let dshEnabled = false;
let palette = null;
let deviceAppearance = {};
let settings;
let accessKeys;
let nextKey;
let fileEntries;
let subagentsEnabled = false;
function reset() {
  sessions = ['codex', 'claude', 'dsh'].map((assistant) => ({ sessionId: `${assistant}-session`, assistant,
    title: `${assistant} fixture`, cwd: '/Users/fixture/project', live: true, mtime: Date.now(),
    projectName: 'Fixture project', archived: false, pinned: false }));
  names = { m1: 'Fixture Mac', m2: 'Offline Mac' };
  requests.length = 0;
  dshEnabled = false;
  subagentsEnabled = false;
  palette = null;
  deviceAppearance = { m1: { icon: 'text', text: 'AI', color: 'teal' } };
  settings = { chatCacheMaxSessions: 6, autoCloseMinutes: 30, desktopMaxWindows: 8, mobileMaxWindows: 4 };
  nextKey = 1;
  accessKeys = [
    { id: 'fixture-key', name: 'Fixture automation', key: 'synthetic-only-fixture-access-key-0001', recoverable: true, rpm: 10,
      binding: { device_id: 'm1', ai_client: 'codex', project_path: '/Users/fixture/project', session_id: 'codex-session' },
      created_at: '2026-10-15T00:00:00Z', last_used_at: '2026-10-15T01:00:00Z' },
    { id: 'fixture-legacy', name: 'Fixture legacy', recoverable: false, rpm: 5, binding: null,
      created_at: '2026-10-14T00:00:00Z', last_used_at: null },
  ];
  fileEntries = [{ name: 'README.md', path: '/Users/fixture/project/README.md', kind: 'file', previewable: true, mime: 'text/markdown', size: 40 }];
  Object.assign(control, { approvalMode: 'on-request', model: 'fixture-model', snapshotVersion: 1 });
}
reset();

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, 'http://localhost:18765');
  response.setHeader('Cache-Control', 'no-store');
  function json(data, status = 200) {
    response.writeHead(status, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify(data));
  }
  try {
    if (url.pathname === '/__requests') { json(requests); return; }
    if (url.pathname === '/__reset') { reset(); json({ ok: true }); return; }
    if (url.pathname === '/__dsh') { dshEnabled = true; json({ ok: true }); return; }
    if (url.pathname === '/__subagents') { subagentsEnabled = true; json({ ok: true }); return; }
    if (url.pathname === '/__appearance') {
      palette = {
        light: { canvas: '#FAFAFA', accent: '#123456', highlight: '#C99A2E', text: '#202923' },
        dark: { canvas: '#0B1210', accent: '#ABCDEF', highlight: '#E0B84F', text: '#F1EBDD' },
      };
      json({ ok: true }); return;
    }
    let body = '';
    for await (const chunk of request) body += chunk;
    const value = body ? JSON.parse(body) : null;
    requests.push({ path: url.pathname, query: url.search, method: request.method, body: value });
    if (url.pathname === '/api/auth/me') { json({ user: { id: 'fixture-user', email: 'fixture@example.test', role: 'user' }, csrf_token: 'fixture-csrf' }); return; }
    if (url.pathname === '/api/auth/logout') { json({ ok: true }); return; }
    if (url.pathname === '/api/auth/sessions') { json({ sessions: [{ id: 'fixture-login', current: true }] }); return; }
    if (url.pathname === '/api/auth/logout-others') { json({ ok: true }); return; }
    if (url.pathname === '/enroll/client-release.json') {
      json({ version: '0.0.0-fixture', installation_protocol: 1,
        assets: { dmg: { path: '/enroll/Fixture-Fleet-Hub.dmg', sha256: '0'.repeat(64) } } }); return;
    }
    if (/^\/api\/automation\/access-keys(?:\/[A-Za-z0-9_-]+)?$/.test(url.pathname)) {
      const identifier = url.pathname.split('/')[4];
      if (!identifier && request.method === 'GET') { json({ keys: accessKeys }); return; }
      const existing = accessKeys.find((key) => key.id === identifier);
      if (identifier && !existing) { json({ error: 'access_key_not_found', message: '访问密钥不存在' }, 404); return; }
      if (identifier && request.method === 'DELETE') {
        accessKeys = accessKeys.filter((key) => key !== existing); json({ ok: true }); return;
      }
      if ((!identifier && request.method === 'POST') || (identifier && request.method === 'PATCH')) {
        const name = value?.name ?? existing?.name;
        const rpm = value?.rpm ?? existing?.rpm ?? 10;
        if (typeof name !== 'string' || !name.trim() || !Number.isInteger(rpm) || rpm < 1 || rpm > 10000 ||
            ['key', 'secret', 'token'].some((field) => Object.hasOwn(value, field))) {
          json({ error: 'invalid_access_key', message: '名称或 RPM 无效；仅支持合成密钥' }, 400); return;
        }
        const binding = Object.hasOwn(value, 'binding') ? value.binding : existing?.binding ?? null;
        if (binding !== null && (typeof binding !== 'object' || Array.isArray(binding) ||
            !['m1', 'm2'].includes(binding.device_id) ||
            (binding.ai_client && !['codex', 'deepseek'].includes(binding.ai_client)) ||
            (binding.project_path && (!binding.ai_client || !binding.project_path.startsWith('/'))) ||
            (binding.session_id && !binding.project_path))) {
          json({ error: 'invalid_binding', message: '密钥范围无效' }, 400); return;
        }
        if (existing) {
          Object.assign(existing, { name: name.trim(), rpm, binding }); json(existing);
        } else {
          const sequence = nextKey++;
          const key = { id: `fixture-created-${sequence}`, name: name.trim(), rpm, binding,
            key: `synthetic-only-created-access-key-${sequence}`, recoverable: true,
            created_at: '2026-10-15T02:00:00Z', last_used_at: null };
          accessKeys.push(key); json(key, 201);
        }
        return;
      }
      json({ error: 'method_not_allowed' }, 405); return;
    }
    if (url.pathname === '/api/devices') {
      json({ devices: [{ id: 'm1', name: names.m1, online: true }, { id: 'm2', name: names.m2, online: false }] }); return;
    }
    if (url.pathname === '/api/nodes.json') { json([{ givenName: 'mac1', online: true }, { givenName: 'mac2', online: false }]); return; }
    if (url.pathname === '/api/names') {
      if (request.method === 'POST') {
        names[value.id] = value.name;
      }
      json(names); return;
    }
    if (url.pathname === '/api/settings') {
      if (['POST', 'PUT', 'PATCH'].includes(request.method)) {
        const update = value;
        if (update.palette) palette = update.palette;
        if (update.id && update.appearance) deviceAppearance[update.id] = update.appearance;
        for (const key of Object.keys(settings)) {
          if (Object.hasOwn(update, key)) settings[key] = update[key];
        }
      }
      json({ ...settings, palette, deviceAppearance }); return;
    }
    if (url.pathname.startsWith('/m1/api/')) {
      const endpoint = url.pathname.slice('/m1/api/'.length);
      if (endpoint === 'info') { json({ meshIP: '100.64.0.10', proxy: {}, dsh: { enabled: dshEnabled, nativeUI: true, installed: true, hostRunning: true, sessionActions: ['rename', 'delete', 'archive', 'unarchive'] } }); return; }
      if (endpoint === 'sessions') {
        const assistant = url.searchParams.get('assistant') || 'codex';
        const archived = url.searchParams.get('archived') === 'true';
        const search = (url.searchParams.get('search') || '').toLowerCase();
        const filtered = sessions.filter((value) => value.assistant === assistant && (url.searchParams.get('scope') === 'all' || value.archived === archived) &&
          (value.title + value.projectName + value.cwd).toLowerCase().includes(search));
        json({ sessions: filtered, total: filtered.length }); return;
      }
      if (endpoint === 'sessions/action') {
        const action = JSON.parse(body);
        const value = sessions.find((item) => item.sessionId === action.sessionId && item.assistant === action.assistant);
        if (!value) { json({ message: '会话不存在' }, 404); return; }
        if (action.action === 'rename') value.title = action.value;
        if (action.action === 'pin' || action.action === 'unpin') value.pinned = action.action === 'pin';
        if (action.action === 'archive' || action.action === 'unarchive') value.archived = action.action === 'archive';
        if (action.action === 'delete') sessions = sessions.filter((item) => item !== value);
        json({ ok: true }); return;
      }
      if (endpoint === 'projects') { json({ projects: [{ cwd: '/Users/fixture/project', name: 'Fixture project' }, { cwd: '/Users/fixture/empty', name: 'Empty project' }] }); return; }
      if (endpoint === 'file/list') { json({ path: '/Users/fixture/project', entries: fileEntries, breadcrumbs: [] }); return; }
      if (endpoint === 'file/mkdir' && request.method === 'POST') {
        fileEntries.push({ name: value.name, path: `${value.path}/${value.name}`, kind: 'folder', size: 0 });
        json({ ok: true }); return;
      }
      if (endpoint === 'file/stat') { json({ path: url.searchParams.get('path'), name: 'README.md', mime: 'text/markdown', kind: 'file', size: 40 }); return; }
      if (endpoint === 'file/preview') { json({ path: url.searchParams.get('path'), name: 'README.md', kind: 'markdown', content: '# Fixture document\nFile preview is persistent.', size: 40 }); return; }
      if (endpoint === 'file/content') {
        response.writeHead(200, { 'Content-Type': 'text/markdown' }); response.end('# Fixture document\nFile preview is persistent.'); return;
      }
      if (endpoint === 'open' || endpoint === 'new') { json({ sid: 'fixture-term', url: '/m1/term/?arg=fixture-term', mode: 'default' }); return; }
      if (endpoint === 'chat/settings') {
        if (request.method !== 'POST') { json({ error: 'method_not_allowed' }, 405); return; }
        if (Object.hasOwn(value, 'model') && !['fixture-model', 'fixture-model-alternate'].includes(value.model)) {
          json({ error: 'invalid_model', message: '仅支持合成测试模型' }, 400); return;
        }
        if (Object.hasOwn(value, 'approvalMode')) control.approvalMode = value.approvalMode;
        if (Object.hasOwn(value, 'model')) control.model = value.model;
        control.snapshotVersion += 1;
        json(control); return;
      }
      if (endpoint === 'chat/resume') {
        json({ ...control, models: [{ value: 'fixture-model', displayName: 'Fixture model' },
          { value: 'fixture-model-alternate', displayName: 'Fixture alternate model' }],
          history: { events: [{ type: 'assistant_done', itemId: 'fixture-message', data: { text: 'Fixture reply\n\n[README.md](/view?mac=m1&path=/Users/fixture/project/README.md)' } }] } });
        return;
      }
      if (endpoint === 'chat/queue') { json(control); return; }
      if (endpoint === 'chat/subagents') {
        json({ items: subagentsEnabled ? [{ threadId: 'fixture-subagent', name: 'Fixture reviewer', status: 'running', role: 'reviewer', depth: 1 }] : [] }); return;
      }
      if (endpoint === 'chat/history') {
        json({ events: [{ type: 'assistant_done', itemId: 'fixture-subagent-message', data: { text: 'Synthetic subagent review evidence' } }] }); return;
      }
      if (endpoint === 'chat/skills') { json({ skills: [] }); return; }
      if (endpoint === 'chat/events') {
        response.writeHead(200, { 'Content-Type': 'text/event-stream', 'Connection': 'keep-alive' });
        response.write(': fixture stream\n\n');
        const timer = setInterval(() => response.write(': heartbeat\n\n'), 1000);
        response.on('close', () => clearInterval(timer));
        return;
      }
      if (endpoint === 'watch') { json({ changed: false }); return; }
      json({}); return;
    }
    if (url.pathname === '/m1/term/') {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      response.end(`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head>
        <body style="background:#171717;color:#fff;font:16px monospace"><p>Fixture ttyd connected</p><textarea aria-label="Terminal"></textarea>
        <script>window.term={options:{},focus(){},write(){},paste(text){document.querySelector('textarea').value+=text;}};</script></body></html>`);
      return;
    }
    if (url.pathname === '/m1/dsh/') {
      response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
      response.end('<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><h1>Fixture DSH UI</h1>');
      return;
    }
    const pathname = ['/', '/view'].includes(url.pathname) ? 'index.html'
      : ['/auth', '/account', '/admin'].includes(url.pathname) ? url.pathname.slice(1) + '.html'
      : decodeURIComponent(url.pathname).slice(1);
    const filename = path.resolve(dashboard, pathname);
    if (!filename.startsWith(dashboard)) { json({ error: 'forbidden' }, 403); return; }
    const contents = await readFile(filename);
    const mime = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png', '.webmanifest': 'application/manifest+json' };
    response.writeHead(200, { 'Content-Type': mime[path.extname(filename)] || 'application/octet-stream' });
    response.end(contents);
  } catch (error) { json({ error: error.code || 'fixture_error' }, 404); }
});
server.listen(18765, '127.0.0.1', () => process.stdout.write('Fleet fixture listening on http://localhost:18765\n'));
