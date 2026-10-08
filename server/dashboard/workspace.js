'use strict';

(function (root) {
  const listeners = new Set();
  let timer = null;
  let previous = '';
  let terminalOpening = false;
  const overlayIDs = ['host-modal', 'fleet-settings-modal', 'settings-modal', 'automation-modal'];

  function ready() {
    return !!document.querySelector('#app') && state.mode != null &&
      (!root.FleetAuth || !!root.FleetAuth.user) &&
      document.documentElement.dataset.auth !== 'expired';
  }

  function projectGroups() {
    return FleetCore.orderedSessionGroups(state.sessionResults || [], state.sessionProjects || [], state.sessionSearch || '');
  }

  function snapshot() {
    const authenticated = ready();
    const sessions = authenticated ? state.sessionResults.map((session) => {
      const status = sessionStatus(session, !!session.pty || isSessionRunning(session, session.macId) ||
        FleetChatModel.chatPhase(session.status) === 'running');
      return {
        sessionId: session.sessionId, macId: session.macId, assistant: session.assistant || state.assistant,
        title: session.title || '', cwd: sessionProjectInfo(session).cwd, project: sessionProjectInfo(session).name,
        projectKey: sessionProjectInfo(session).key, mtime: Number(session.mtime) || 0,
        live: !!session.live, pinned: !!session.pinned, unread: sessionIsUnread(session),
        running: status.className === 'running', waiting: status.className === 'waiting',
        status: status.label, actions: sessionMenuActions(session),
      };
    }) : [];
    const nativeDSH = authenticated ? dshNativeButtonState() : {};
    return {
      version: 1, theme: document.documentElement.dataset.theme || 'light',
      themePreference: root.FleetTheme?.preference || 'light',
      ready: authenticated, identity: authenticated ? String(root.FleetAuth?.user?.id || 'gateway') : '',
      email: authenticated ? root.FleetAuth?.user?.email || '' : '',
      admin: authenticated && root.FleetAuth?.user?.role === 'admin',
      devices: authenticated ? MACS.map((device) => ({
        id: device.id, name: macName(device.id), online: !!state.nodes[device.id],
        ...root.FleetDeviceAppearance?.get(device.id), count: state.counts?.[device.id] || 0,
      })) : [],
      sessions,
      projects: authenticated ? projectGroups().map((group) => ({
        id: group.key, title: group.name, cwd: group.cwd, projectless: group.projectless,
        macId: state.sessionMacId === 'all' ? group.arr[0]?.macId || group.macId || state.macId : state.sessionMacId,
        sessionIDs: group.arr.map((session) => `${session.macId}/${session.assistant || state.assistant}/${session.sessionId}`),
        collapsed: state.collapsed?.has(group.key) || false,
      })) : [],
      assistants: authenticated ? ['codex', 'claude', ...(Object.keys(state.assistantInfo || {}).some(dshEnabled) ? ['dsh'] : [])] : [],
      dshNativeURL: nativeDSH.enabled ? `/${nativeDSH.macId}/dsh/` : null,
      dshNativeHint: nativeDSH.title || '', degraded: authenticated && dshDegraded(),
      mode: authenticated ? state.mode : 'sessions',
      view: authenticated ? state.sessionView || 'project' : 'project',
      search: authenticated ? state.sessionSearch || '' : '',
      deviceScope: authenticated ? (state.mode === 'files' ? state.fileMacId : state.sessionMacId) || 'all' : 'all',
      assistant: authenticated ? state.assistant : 'codex',
      archived: authenticated && state.scope === 'all',
      loading: authenticated && document.querySelector('#session-groups')?.getAttribute('aria-busy') === 'true',
      hasMore: authenticated && sessionHasMore(),
      errors: authenticated ? Object.keys(state.sessionErrors || {}).map((id) => `${macName(id)} 暂时无法连接`) : [],
      gatewayDown: !!state.gatewayDown,
      selectedID: authenticated && state.selectedSid ? `${state.selectedSessionMacId}/${state.selectedSessionAssistant || state.assistant}/${state.selectedSid}` : null,
      title: authenticated ? state.chat?.title || state.curTitle || '选择一个会话' : 'Fleet Hub',
      renderer: authenticated && state.mode === 'files' ? 'files' : authenticated && state.current && !state.chat ? 'terminal' : 'chat',
      overlay: authenticated ? overlayIDs.find((id) => document.getElementById(id)?.hidden === false) || null : null,
    };
  }

  function publish() {
    const value = snapshot(), signature = JSON.stringify(value);
    if (signature === previous) return;
    previous = signature;
    for (const listener of listeners) listener(value);
  }

  function subscribe(listener) {
    listeners.add(listener);
    listener(snapshot());
    if (!timer) timer = setInterval(publish, 750);
    return () => {
      listeners.delete(listener);
      if (!listeners.size) { clearInterval(timer); timer = null; previous = ''; }
    };
  }

  function concreteDevice(id) {
    if (!MACS.some((device) => device.id === id) || !state.nodes[id]) throw new Error('设备离线或没有访问权限');
    return id;
  }

  function selectAssistant(assistant, allowTerminal) {
    const choices = snapshot().assistants;
    if (!choices.includes(assistant) || (assistant === 'claude' && !allowTerminal)) throw new Error('不支持的助手');
    if (assistant === state.assistant) return;
    setAssistant(assistant);
  }

  async function openTerminal(session, create = false) {
    if (terminalOpening) throw new Error('终端正在连接，请稍候');
    terminalOpening = true;
    try {
      root.FleetWorkspaceTabs?.showChat();
      const existing = !create && poolFind(session.macId, session.sessionId, session.assistant);
      if (existing) {
        state.macId = session.macId;
        activateSession(session);
        poolShow(existing);
        return;
      }
      const result = await FleetCore.attachTerminal({
        api, macId: session.macId, assistant: session.assistant, sessionId: session.sessionId,
        cwd: session.cwd, origin: location.origin, create,
      });
      state.macId = session.macId;
      activateSession(session);
      poolAdd(session.macId, session.assistant, session.sessionId || null, result.sid, result.url,
        session.title || '新会话', session.cwd || '', 'default');
    } finally { terminalOpening = false; }
  }

  async function execute(message, { allowTerminal = false } = {}) {
    if (!ready()) throw new Error('请先登录');
    let opened = false, url = null;
    switch (message.command) {
      case 'device':
        if (message.device !== 'all' && !MACS.some((device) => device.id === message.device)) throw new Error('设备不存在');
        if (state.mode === 'files' && message.device === 'all') throw new Error('浏览文件需要选择一台设备');
        selectMac(message.device);
        break;
      case 'assistant':
        if (state.mode !== 'sessions') setMode('sessions');
        selectAssistant(message.assistant, allowTerminal);
        break;
      case 'view':
        if (!['recent', 'project'].includes(message.view)) throw new Error('不支持的会话视图');
        state.sessionView = message.view;
        updateSessionFilterUI(); persistUIState(); renderSessionResults();
        break;
      case 'collapse-project':
        if (!projectGroups().some((group) => group.key === message.project)) throw new Error('项目已变化');
        if (state.collapsed.has(message.project)) state.collapsed.delete(message.project);
        else state.collapsed.add(message.project);
        renderSessionResults();
        break;
      case 'search':
        state.sessionSearch = String(message.search || '').slice(0, 500).trim();
        state.sessionCursors = {};
        await loadSessions({ clear: true });
        break;
      case 'theme': setThemePreference(message.preference); break;
      case 'archive': toggleArchivedSessions(); break;
      case 'more': await loadSessions({ append: true }); break;
      case 'refresh': await refreshNodes(); if (state.mode === 'sessions') await loadSessions(); break;
      case 'open': {
        if (state.mode !== 'sessions') setMode('sessions');
        const session = state.sessionResults.find((item) => item.sessionId === message.sessionId && item.macId === message.macId &&
          (item.assistant || state.assistant) === message.assistant);
        if (!session) throw new Error('会话已变化，请刷新列表');
        concreteDevice(session.macId);
        const terminal = session.assistant === 'claude' || message.terminal === true;
        if (terminal && !allowTerminal) throw new Error('此客户端未启用终端');
        if (terminal) await openTerminal(session);
        else {
          activateSession(session);
          if (!canSelfDrawChat(session.assistant, session.macId)) {
            selectSes(session.sessionId, session.macId, session.assistant);
            break;
          }
          await openChatSession(session);
        }
        opened = true;
        break;
      }
      case 'new': {
        const macId = concreteDevice(message.macId);
        let cwd = String(message.cwd || '').trim();
        if (message.project) {
          const project = projectGroups().find((group) => group.key === message.project);
          if (!project || project.projectless || !project.cwd ||
              !(project.arr.some((session) => session.macId === macId) || project.macId === macId ||
                (state.sessionMacId === macId && project.cwd))) throw new Error('项目已变化，请刷新列表');
          cwd = project.cwd;
        }
        if (cwd && !cwd.startsWith('/')) throw new Error('请填写 Mac 上的绝对路径');
        if (state.mode !== 'sessions') setMode('sessions');
        if (state.assistant === 'claude') {
          if (!allowTerminal) throw new Error('此客户端未启用终端');
          await openTerminal({ macId, assistant: 'claude', cwd, title: '新 Claude 会话' }, true);
        } else {
          if (!canSelfDrawChat(state.assistant, macId)) throw new Error('请先启动这台 Mac 的桌面助手');
          await newSessionIn(cwd, { macId, unscoped: !cwd });
        }
        opened = true;
        break;
      }
      case 'action': {
        const session = state.sessionResults.find((item) => item.macId === message.macId &&
          item.sessionId === message.sessionId && (item.assistant || state.assistant) === message.assistant);
        if (!session || !sessionMenuActions(session).includes(message.action)) throw new Error('不支持此会话操作');
        concreteDevice(session.macId);
        const value = String(message.value || '').trim();
        if (message.action === 'rename' && !value) throw new Error('会话名称不能为空');
        await mutateSession(session, message.action, value, { throwOnError: true });
        break;
      }
      case 'host':
        if (!MACS.some((device) => device.id === message.macId)) throw new Error('设备不存在');
        await openHostModal(message.macId); opened = true;
        break;
      case 'settings': openSettings(); opened = true; break;
      case 'automation': await openAutomation(); opened = true; break;
      case 'dismiss-overlay':
        overlayIDs.forEach(closeOverlay); break;
      case 'back': backToList(); break;
      case 'files': {
        const device = message.macId || state.fileMacId || state.macId || MACS.find((item) => state.nodes[item.id])?.id;
        if (!device || !MACS.some((item) => item.id === device)) throw new Error('请先选择一台设备');
        setFileDevice(device); setMode('files'); opened = true;
        break;
      }
      case 'sessions': setMode('sessions'); break;
      case 'page':
        if (!['account', 'add-device', 'admin', 'guide', 'dsh'].includes(message.page)) throw new Error('不支持的页面');
        if (message.page === 'admin' && root.FleetAuth?.user?.role !== 'admin') throw new Error('需要管理员权限');
        url = message.page === 'dsh' ? snapshot().dshNativeURL : {
          account: '/account', 'add-device': '/account#add-device', admin: '/admin', guide: '/automation-guide.html',
        }[message.page];
        if (!url) throw new Error(snapshot().dshNativeHint || '此页面暂时不可用');
        break;
      case 'logout': await root.FleetAuth.logout(); break;
      default: throw new Error('不支持的操作');
    }
    publish();
    return { opened, url };
  }

  root.FleetWorkspace = Object.freeze({ snapshot, subscribe, execute, publish });
})(globalThis);
