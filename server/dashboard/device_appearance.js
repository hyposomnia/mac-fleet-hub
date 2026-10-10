/* Server-backed device identity, with a browser cache and legacy preference import. */
(function (host) {
  'use strict';
  const legacyKey = 'fleet-device-appearance-v1';
  let key = host.FleetAuth ? null : legacyKey;
  let generation = 0;
  const icons = [
    {id:'monitor',label:'显示器',path:'M4 3h16a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1ZM8 21h8M12 16v5'},
    {id:'laptop',label:'笔记本',path:'M5 4h14v12H5ZM3 16h18l1 4H2ZM10 18h4'},
    {id:'desktop',label:'工作站',path:'M5 3h14a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1ZM8 7h8M8 11h8M9 17h.01'},
    {id:'mini',label:'迷你主机',path:'M6 4h12a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2ZM8 16h5M16 16h.01'},
    {id:'server',label:'服务器',path:'M4 3h16v7H4ZM4 14h16v7H4ZM8 6.5h.01M8 17.5h.01M12 6.5h5M12 17.5h5M6 10v4M18 10v4'},
    {id:'terminal',label:'终端',path:'M4 3h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1ZM7 8l3 3-3 3M13 14h4M8 22h8'},
  ];
  const colors = [
    {id:'steel',label:'钢蓝'}, {id:'teal',label:'青碧'},
    {id:'green',label:'森林绿'}, {id:'amber',label:'琥珀'},
    {id:'coral',label:'珊瑚红'}, {id:'violet',label:'紫罗兰'},
    {id:'rose',label:'玫瑰'}, {id:'slate',label:'石墨'},
  ];
  function letterText(value) {
    const text = typeof value === 'string' ? value.trim() : '';
    return /^[A-Za-z0-9]{1,4}$/.test(text) ? text : '';
  }
  function normalize(value) {
    const color = colors.some(c=>c.id===value?.color) ? value.color : 'steel';
    const text = letterText(value?.text);
    if (value?.icon === 'text' && text) return {icon:'text',text,color};
    return {icon:icons.some(i=>i.id===value?.icon) ? value.icon : 'monitor',color};
  }
  function readCache(name) {
    const values = Object.create(null);
    try {
      const saved = JSON.parse(host.localStorage.getItem(name) || '{}');
      if (saved && typeof saved === 'object' && !Array.isArray(saved)) {
        for (const [id,value] of Object.entries(saved)) if (/^m[1-9]\d*$/.test(id)) values[id]=normalize(value);
      }
    } catch (_) {}
    return values;
  }
  let preferences = key ? readCache(key) : Object.create(null);
  let legacyPreferences = Object.create(null);
  function get(id) { return normalize(preferences[id]); }
  function set(id,value) {
    if (!key || !/^m[1-9]\d*$/.test(id) || (value?.icon === 'text' && !letterText(value.text))) return false;
    preferences[id]=normalize(value);
    try { host.localStorage.setItem(key,JSON.stringify(preferences)); return true; } catch (_) { return false; }
  }
  function replace(values) {
    preferences = Object.create(null);
    for (const [id,value] of Object.entries(values)) if (/^m[1-9]\d*$/.test(id)) preferences[id]=normalize(value);
    try { host.localStorage.setItem(key,JSON.stringify(preferences)); } catch (_) {}
    return preferences;
  }
  let operations = Promise.resolve(), refreshTask;
  function reset() {
    generation++;
    key = host.FleetAuth ? null : legacyKey;
    preferences = Object.create(null);
    legacyPreferences = Object.create(null);
    operations = Promise.resolve();
    refreshTask = undefined;
  }
  function bindAccount(storageKey) {
    reset();
    key = storageKey;
    preferences = readCache(key);
    legacyPreferences = readCache(legacyKey);
  }
  function enqueue(operation) {
    const started = generation;
    const task = operations.then(()=>{
      if (started !== generation || !key) throw new Error('账号已变更');
      return operation();
    });
    operations = task.catch(()=>{});
    return task;
  }
  async function request(url, update) {
    const started = generation;
    const controller = new host.AbortController();
    const timeout = host.setTimeout(()=>controller.abort(), 10000);
    try {
      const response = await host.fetch(url, {
        cache:'no-store', signal:controller.signal,
        ...(update ? {method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify(update)} : {}),
      });
      if (!response.ok) throw new Error('HTTP ' + response.status);
      const data = (await response.json()).deviceAppearance;
      if (started !== generation || !key) throw new Error('账号已变更');
      if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('服务器未返回设备外观');
      return data;
    } finally { host.clearTimeout(timeout); }
  }
  function save(id, value, url = '/api/settings') {
    if (!/^m[1-9]\d*$/.test(id) || (value?.icon === 'text' && !letterText(value.text))) {
      return Promise.reject(new Error('设备外观格式错误'));
    }
    const appearance = normalize(value);
    return enqueue(async()=>replace({...preferences,...await request(url, {id,appearance})}));
  }
  function refresh(url = '/api/settings', deviceIDs) {
    if (refreshTask) return refreshTask;
    const started = generation;
    const allowed = host.FleetAuth ? new Set(deviceIDs || []) : null;
    const task = enqueue(async()=>{
      const local = {...legacyPreferences,...preferences};
      let server = await request(url);
      try {
        for (const [id,appearance] of Object.entries(local)) {
          if (allowed && !allowed.has(id)) continue;
          if (!Object.hasOwn(server,id)) server = await request(url, {id,appearance,ifAbsent:true});
        }
      } catch (error) {
        if (started === generation) replace({...preferences,...server});
        throw error;
      }
      legacyPreferences = Object.create(null);
      return replace(server);
    }).finally(()=>{if (refreshTask === task) refreshTask=undefined;});
    refreshTask = task;
    return task;
  }
  function createIcon(value) {
    const preference=normalize(value), ns='http://www.w3.org/2000/svg';
    const wrapper=host.document.createElement('span');
    wrapper.className='device-icon'; wrapper.setAttribute('data-device-color',preference.color);
    if (preference.icon === 'text') {
      wrapper.className += ' device-icon-letters'; wrapper.setAttribute('data-length',String(preference.text.length));
      wrapper.textContent = preference.text; return wrapper;
    }
    const svg=host.document.createElementNS(ns,'svg');
    for (const [attr,v] of Object.entries({viewBox:'0 0 24 24',fill:'none',stroke:'currentColor','stroke-width':'1.8',
      'stroke-linecap':'round','stroke-linejoin':'round','aria-hidden':'true',focusable:'false'})) svg.setAttribute(attr,v);
    const path=host.document.createElementNS(ns,'path');
    path.setAttribute('d',icons.find(i=>i.id===preference.icon).path);
    svg.appendChild(path); wrapper.appendChild(svg); return wrapper;
  }
  host.FleetDeviceAppearance={get key(){return key;},icons,colors,letterText,normalize,get,set,save,refresh,bindAccount,reset,createIcon};
})(globalThis);
