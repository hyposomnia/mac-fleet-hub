'use strict';

(function (root) {
  function projectInfo(session) {
    const projectless = !!session?.projectless;
    const cwd = projectless ? '' : (session?.projectCwd || session?.cwd || '');
    const projectId = session?.projectId || '';
    return {
      key: projectless ? 'projectless:' : (cwd ? `cwd:${cwd}` : (projectId ? `id:${projectId}` : 'unknown:')),
      name: projectless ? '无项目' : (session?.projectName || cwd.split('/').filter(Boolean).pop() || '(未知项目)'),
      cwd, projectless,
    };
  }

  function groupSessions(sessions, projects = [], search = '') {
    const groups = new Map();
    for (const session of sessions || []) {
      const project = projectInfo(session);
      if (!groups.has(project.key)) groups.set(project.key, { ...project, arr: [] });
      groups.get(project.key).arr.push(session);
    }
    const needle = search.toLocaleLowerCase();
    for (const project of projects || []) {
      if (!project.cwd || (needle && !`${project.name || ''}\n${project.cwd}`.toLocaleLowerCase().includes(needle))) continue;
      const key = `cwd:${project.cwd}`;
      if (groups.has(key)) groups.get(key).name = project.name || groups.get(key).name;
      else groups.set(key, { key, name: project.name || projectInfo(project).name, cwd: project.cwd,
        macId: project.macId, projectless: false, arr: [] });
    }
    return [...groups.values()];
  }

  function normalizeDevices(payload) {
    const records = Array.isArray(payload) ? payload : (payload?.devices || payload?.nodes || []);
    const devices = new Map();
    for (const record of records) {
      const match = String(record.givenName || record.name || '').toLowerCase().match(/^mac(\d+)$/);
      const scoped = /^m\d+$/.test(record.id || '');
      const id = scoped ? record.id : (match ? `m${match[1]}` : '');
      if (!id) continue;
      devices.set(id, { id, name: scoped ? record.name || '' : '', online: record.online === true || record.online === 'true' });
    }
    return [...devices.values()].sort((first, second) => Number(first.id.slice(1)) - Number(second.id.slice(1)));
  }

  function orderedSessionGroups(sessions, projects = [], search = '') {
    return groupSessions(sessions, projects, search).map((group) => {
      group.arr.sort((first, second) => Number(!!second.pinned) - Number(!!first.pinned) ||
        Number(!!second.live) - Number(!!first.live) || Number(second.mtime) - Number(first.mtime));
      return { ...group, pinned: group.arr.some((session) => session.pinned),
        last: Math.max(0, ...group.arr.map((session) => Number(session.mtime) || 0)) };
    }).sort((first, second) => Number(second.pinned) - Number(first.pinned) || second.last - first.last);
  }

  function sessionQuery({ assistant, archived = false, search = '', cursor = '', soft = false, paginated = true }) {
    const query = new URLSearchParams({ assistant });
    if (paginated) {
      query.set('archived', String(archived));
      query.set('limit', soft ? '100' : '50');
      if (search) query.set('search', search);
      if (cursor) query.set('cursor', cursor);
    } else query.set('scope', archived ? 'all' : 'active');
    return `sessions?${query.toString()}`;
  }

  async function requestJSON(fetcher, url, options, { path = url, tooLargeMessage = '文件太大' } = {}) {
    const response = await fetcher(url, options);
    if (!response.ok) {
      let message = response.status === 413 ? tooLargeMessage : `${path}: ${response.status}`;
      let code = '';
      try {
        const data = await response.json();
        if (data?.message) message = data.message;
        if (typeof data?.error === 'string') code = data.error;
      } catch (_) {}
      throw Object.assign(new Error(message), { status: response.status, code });
    }
    return response.json();
  }

  function terminalURL(value, macId, origin) {
    const url = new URL(value, origin);
    if (!/^m\d+$/.test(macId) || url.origin !== origin || url.pathname !== `/${macId}/term/` ||
        url.username || url.password || !url.searchParams.get('arg')) throw new Error('终端地址不合法');
    return url.pathname + url.search;
  }

  async function attachTerminal({ api, macId, assistant, sessionId, cwd, origin, create = false, mode = 'default' }) {
    if (!/^m\d+$/.test(macId) || !['codex', 'claude'].includes(assistant) ||
        (!create && !sessionId) || !['default', 'bypass', 'auto'].includes(mode)) throw new Error('无法连接此终端');
    const data = await api(macId, create ? 'new' : 'open', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify(create ? { assistant, cwd: cwd || '', mode } : { assistant, sessionId, mode }),
    });
    if (typeof data.sid !== 'string' || !data.sid) throw new Error('终端响应不完整');
    return { ...data, url: terminalURL(data.url, macId, origin) };
  }

  root.FleetCore = Object.freeze({ projectInfo, groupSessions, orderedSessionGroups, normalizeDevices, sessionQuery, requestJSON, terminalURL, attachTerminal });
})(globalThis);
