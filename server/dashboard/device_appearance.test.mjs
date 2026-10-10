import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const source = await readFile(new URL('./device_appearance.js', import.meta.url), 'utf8').catch(() => '');
const appSource = await readFile(new URL('./app.js', import.meta.url), 'utf8');
const indexHTML = await readFile(new URL('./index.html', import.meta.url), 'utf8');
const styleCSS = await readFile(new URL('./style.css', import.meta.url), 'utf8');
function setup(initial = '{}', blocked = false, fetch) {
  let stored = initial;
  const element = tag => ({tag, attributes:{}, children:[], setAttribute(k,v){this.attributes[k]=v;}, appendChild(n){this.children.push(n);}});
  const target = {document:{createElement:element, createElementNS:(_,tag)=>element(tag)}, fetch, AbortController, setTimeout, clearTimeout,
    localStorage:{getItem(){if(blocked) throw Error('blocked'); return stored;},setItem(k,v){if(blocked) throw Error('blocked'); stored=v;}}};
  vm.createContext(target); vm.runInContext(source,target);
  return {api:target.FleetDeviceAppearance, stored:()=>stored};
}
test('unknown preferences and corrupt storage fall back to a steel computer', () => {
  const {api} = setup('broken');
  assert.deepEqual(JSON.parse(JSON.stringify(api.get('m1'))), {icon:'monitor',color:'steel'});
  assert.deepEqual(JSON.parse(JSON.stringify(api.normalize({icon:'<svg>',color:'url(secret)'}))), {icon:'monitor',color:'steel'});
});
test('device appearance persists independently and ignores unsupported device IDs', () => {
  const {api,stored} = setup();
  assert.equal(api.set('m2',{icon:'laptop',color:'violet'}),true);
  assert.equal(api.get('m1').icon,'monitor');
  assert.equal(api.get('m2').color,'violet');
  assert.equal(setup(stored()).api.get('m2').icon,'laptop');
  assert.equal(api.set('__proto__',{icon:'server',color:'coral'}),false);
});
test('blocked storage preserves in-page preferences and reports persistence failure', () => {
  const {api} = setup('{}',true);
  assert.equal(api.set('m1',{icon:'server',color:'coral'}),false);
  assert.equal(api.get('m1').icon,'server');
});
test('previewing a draft never changes saved appearance', () => {
  const {api} = setup();
  const icon=api.createIcon({icon:'laptop',color:'teal'});
  assert.equal(icon.attributes['data-device-color'],'teal');
  assert.equal(api.get('m1').icon,'monitor');
  assert.equal(icon.children[0].tag,'svg');
  assert.equal(icon.children[0].attributes.viewBox,'0 0 24 24');
  assert.ok(icon.children[0].children[0].attributes.d);
});
test('all six icon choices have SVG geometry and eight colors are distinct', () => {
  const {api} = setup();
  assert.equal(api.icons.length,6); assert.equal(api.colors.length,8);
  for (const choice of api.icons) assert.ok(api.createIcon({icon:choice.id}).children[0].children.length);
  assert.equal(new Set(api.colors.map(color=>color.id)).size,8);
});
test('one to four ASCII letters or digits preserve case and leading zeros and reject invalid text', () => {
  const {api}=setup();
  for(const [text,expected] of [['m','m'],['mb','mb'],[' A ','A'],[' aB ','aB'],['4','4'],['04','04'],['a1','a1'],['1b','1b'],['mbp','mbp'],['MbP','MbP'],['009','009'],['a1b','a1b'],['A1b','A1b'],['home','home'],['Mac4','Mac4'],['0009','0009']]) {
    assert.deepEqual(JSON.parse(JSON.stringify(api.normalize({icon:'text',text,color:'violet'}))), {icon:'text',text:expected,color:'violet'});
  }
  for(const text of ['', 'ABCDE', '12345', 'M?', 'A 1', '中', '<', 'é', 'ß', 'ſ', 'ı', '４', '٤']) assert.equal(api.normalize({icon:'text',text}).icon,'monitor');
});
test('letter icons persist per device, invalid saves retain prior preference and SVG selection clears text',()=>{
  const {api,stored}=setup();
  assert.equal(api.set('m1',{icon:'text',text:'mb',color:'teal'}),true);
  assert.equal(setup(stored()).api.get('m1').text,'mb');
  assert.equal(api.get('m2').icon,'monitor');
  assert.equal(api.set('m1',{icon:'text',text:'M?',color:'rose'}),false);
  assert.equal(api.get('m1').text,'mb');
  api.set('m1',{icon:'mini',text:'MB',color:'teal'});
  assert.equal(api.get('m1').icon,'mini');assert.equal(api.get('m1').text,undefined);
});
test('alphanumeric icons persist per device and keep their text when recoloured',()=>{
  const {api,stored}=setup();
  for(const [text,expected] of [['4','4'],['04','04'],['a1','a1'],['mbp','mbp'],['MbP','MbP'],['MBP','MBP'],['009','009'],['a1b','a1b'],['A1b','A1b']]) {
    assert.equal(api.set('m1',{icon:'text',text,color:'teal'}),true);
    assert.equal(setup(stored()).api.get('m1').text,expected);
    assert.equal(api.set('m1',{...api.get('m1'),color:'violet'}),true);
    const restored=setup(stored()).api.get('m1');
    assert.equal(restored.text,expected);assert.equal(restored.color,'violet');
    assert.equal(api.get('m2').icon,'monitor');
  }
});
test('alphanumeric previews are plain text in the same coloured icon container and do not mutate preferences',()=>{
  const {api}=setup();
  for(const [text,expected] of [['ab','ab'],['4','4'],['04','04'],['a1','a1'],['www','www'],['WWW','WWW'],['009','009'],['a1b','a1b'],['A1b','A1b'],['home','home'],['Mac4','Mac4']]) {
    const icon=api.createIcon({icon:'text',text,color:'coral'});
    assert.equal(icon.children.length,0);assert.equal(icon.textContent,expected);
    assert.equal(icon.attributes['data-device-color'],'coral');
    assert.match(icon.className,/device-icon-letters/);assert.equal(icon.attributes['data-length'],String(expected.length));
  }
  assert.equal(api.get('m1').icon,'monitor');
});

test('letter editor shows entered case, accepts four characters, and sizes all lengths without forced uppercase', () => {
  assert.match(indexHTML, /id="hm-letter-input"[^>]*maxlength="4"[^>]*pattern="\[A-Za-z0-9\]\{1,4\}"/);
  assert.match(indexHTML, /1–4 个英文字母或数字，保留输入的大小写。/);
  assert.doesNotMatch(styleCSS, /\.device-letter-input\s*\{[^}]*text-transform:\s*uppercase/);
  for (const length of [1, 2, 3, 4]) assert.match(styleCSS, new RegExp(`\\.device-icon-letters\\[data-length="${length}"\\]`));
});

test('device connectivity uses original colour online and conspicuous grayscale offline without corner dots', () => {
  assert.doesNotMatch(appSource, /device-status-mark/);
  assert.doesNotMatch(styleCSS, /\.device-status-mark/);
  const offlineRule = styleCSS.match(/\.device-icon\.is-offline\s*\{([^}]*)\}/)?.[1] || '';
  assert.match(offlineRule, /filter:\s*grayscale\(1\)/);
  assert.match(offlineRule, /opacity:\s*\.4[0-9]/);
});

function appearanceServer(initial = {}) {
  let data = structuredClone(initial);
  const calls = [];
  const fetch = async (url, options = {}) => {
    calls.push({url, ...options});
    if (options.method === 'PATCH') {
      const {id, appearance, ifAbsent} = JSON.parse(options.body);
      if (!ifAbsent || !Object.hasOwn(data, id)) data[id] = appearance;
    }
    const result = structuredClone(data);
    return {ok:true, status:200, json:async()=>({deviceAppearance:result})};
  };
  return {fetch, calls, data:()=>data};
}

test('device appearance saved to the server restores in a browser with empty local storage', async () => {
  const server = appearanceServer();
  const first = setup('{}', false, server.fetch).api;
  assert.equal(typeof first.save, 'function');
  await first.save('m1', {icon:'text', text:'aB04', color:'violet'});
  const second = setup('{}', false, server.fetch).api;
  await second.refresh();
  assert.equal(second.get('m1').text, 'aB04');
  assert.equal(second.get('m1').color, 'violet');
  assert.equal(server.calls[0].method, 'PATCH');
  assert.equal(server.calls[0].url, '/api/settings');
});

test('legacy local appearances migrate only when a server value is absent', async () => {
  const server = appearanceServer({m1:{icon:'text',text:'A',color:'violet'}});
  const {api,stored} = setup(JSON.stringify({m1:{icon:'mini',color:'coral'},m2:{icon:'text',text:'0009',color:'teal'}}), false, server.fetch);
  await api.refresh();
  assert.equal(api.get('m1').text, 'A');
  assert.equal(server.data().m2.text, '0009');
  const patch = server.calls.filter(call=>call.method==='PATCH');
  assert.equal(patch.length, 1);
  assert.equal(JSON.parse(patch[0].body).ifAbsent, true);
  assert.equal(JSON.parse(stored()).m1.text, 'A');
});

test('failed server saves do not change the saved icon or claim local persistence', async () => {
  const initial = JSON.stringify({m1:{icon:'text',text:'A',color:'violet'}});
  const {api,stored} = setup(initial, false, async()=>({ok:false,status:500}));
  await assert.rejects(api.save('m1',{icon:'mini',color:'coral'}), /500/);
  assert.equal(api.get('m1').text, 'A');
  assert.equal(stored(), initial);
});

test('blocked local storage cannot prevent server persistence and restoration', async () => {
  const server = appearanceServer();
  const {api} = setup('{}', true, server.fetch);
  await api.save('m1', {icon:'text',text:'Mac4',color:'teal'});
  const second = setup('{}', true, server.fetch).api;
  await second.refresh();
  assert.equal(second.get('m1').text, 'Mac4');
});

test('refresh failures preserve cached appearance and later retry imports remaining devices', async () => {
  const server = appearanceServer();
  let fail = true;
  const {api} = setup(JSON.stringify({m2:{icon:'text',text:'009',color:'teal'}}), false, async (...args)=> {
    if (fail) throw new Error('offline');
    return server.fetch(...args);
  });
  await assert.rejects(api.refresh(), /offline/);
  assert.equal(api.get('m2').text, '009');
  fail = false;
  await api.refresh();
  assert.equal(server.data().m2.text, '009');
});

test('saved defaults remain authoritative over stale browser overrides', async () => {
  const server = appearanceServer({m1:{icon:'monitor',color:'steel'}});
  const {api} = setup(JSON.stringify({m1:{icon:'text',text:'Old',color:'violet'}}), false, server.fetch);
  await api.refresh();
  assert.equal(api.get('m1').icon, 'monitor');
  assert.equal(server.calls.length, 1);
});

test('saving one device retains other legacy appearances until they can migrate', async () => {
  const server = appearanceServer();
  const {api} = setup(JSON.stringify({m2:{icon:'text',text:'009',color:'teal'}}), false, server.fetch);
  await api.save('m1',{icon:'mini',color:'violet'});
  assert.equal(api.get('m2').text,'009');
  await api.refresh();
  assert.equal(server.data().m2.text,'009');
});

test('the host editor awaits server persistence and explains the shared storage', () => {
  const saveHost = appSource.match(/async function saveHost\(\)[\s\S]*?\n}\n/)?.[0] || '';
  assert.match(saveHost, /await FleetDeviceAppearance\.save\(id,/);
  assert.doesNotMatch(saveHost, /FleetDeviceAppearance\.set\(/);
  assert.match(saveHost, /设备外观未保存到服务器/);
  assert.match(indexHTML, /图标、文字与颜色保存在服务器，不同浏览器共享/);
});

function accountCache(initial, fetch) {
  const stored = new Map(Object.entries(initial));
  const target = {FleetAuth:{}, fetch, AbortController, setTimeout, clearTimeout,
    localStorage:{getItem:key=>stored.get(key) ?? null, setItem:(key,value)=>stored.set(key,value)}};
  vm.createContext(target); vm.runInContext(source,target);
  return {api:target.FleetDeviceAppearance, stored};
}

test('account-bound appearance caches never reuse another account and reset clears memory', () => {
  const {api} = accountCache({
    'fleet-user:1:appearance':JSON.stringify({m1:{icon:'text',text:'A1',color:'teal'}}),
    'fleet-user:2:appearance':JSON.stringify({m2:{icon:'mini',color:'violet'}}),
    'fleet-device-appearance-v1':JSON.stringify({m9:{icon:'server',color:'coral'}}),
  });
  assert.equal(api.get('m9').icon,'monitor','unscoped cache stays hidden before account binding');
  api.bindAccount('fleet-user:1:appearance');
  assert.equal(api.get('m1').text,'A1');
  api.bindAccount('fleet-user:2:appearance');
  assert.equal(api.get('m1').icon,'monitor');
  assert.equal(api.get('m2').icon,'mini');
  api.reset();
  assert.equal(api.get('m2').icon,'monitor');
});

test('legacy browser appearances migrate only for devices owned by the current account', async () => {
  const server = appearanceServer();
  const {api,stored} = accountCache({'fleet-device-appearance-v1':JSON.stringify({
    m1:{icon:'text',text:'A1',color:'teal'},m2:{icon:'server',color:'coral'},
  })},server.fetch);
  api.bindAccount('fleet-user:1:appearance');
  await api.refresh('/api/settings',['m1']);
  assert.deepEqual(server.calls.filter(call=>call.method==='PATCH').map(call=>JSON.parse(call.body).id),['m1']);
  assert.equal(api.get('m1').text,'A1');
  assert.equal(api.get('m2').icon,'monitor');
  assert.equal(JSON.parse(stored.get('fleet-user:1:appearance')).m1.text,'A1');
});

test('late saves cannot repopulate appearance state after logout or an account switch', async () => {
  let finish, started;
  const requestStarted = new Promise(resolve=>{started=resolve;});
  const {api,stored} = accountCache({},()=>{
    started();
    return new Promise(resolve=>{finish=()=>resolve({ok:true,status:200,json:async()=>({deviceAppearance:{m1:{icon:'mini',color:'teal'}}})});});
  });
  api.bindAccount('fleet-user:1:appearance');
  const save = api.save('m1',{icon:'mini',color:'teal'});
  await requestStarted;
  api.reset();
  api.bindAccount('fleet-user:2:appearance');
  finish();
  await assert.rejects(save,/账号已变更/);
  assert.equal(api.get('m1').icon,'monitor');
  assert.equal(stored.has('fleet-user:2:appearance'),false);
});
