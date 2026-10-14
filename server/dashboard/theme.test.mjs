import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('./theme.js', import.meta.url), 'utf8');
const authSource = await readFile(new URL('./auth-client.js', import.meta.url), 'utf8');
const styleSource = await readFile(new URL('./style.css', import.meta.url), 'utf8');
function fixture(stored = null, light = false, blocked = false, savedPalette = null, network = null, pathname = '/account') {
  const listeners = {}, media = { matches: light, addEventListener: (type, fn) => { listeners.media = fn; } };
  const values = new Map();
  if (stored !== null) values.set('fleet-theme', stored);
  if (savedPalette !== null) values.set('fleet-theme-palette-v1', JSON.stringify(savedPalette));
  const styleValues = {};
  const attrs = {}, meta = {}, controls = ['light', 'dark', 'system'].map(themeChoice => ({dataset:{themeChoice}, setAttribute(key,value) { this[key]=value; }}));
  const host = { location: { pathname }, document: { documentElement: {style:{setProperty:(key,value)=>{styleValues[key]=value;}},setAttribute: (key,value) => {attrs[key]=value;}}, querySelector: () => meta, querySelectorAll: () => controls, addEventListener() {} },
    localStorage: {getItem(key) {if (blocked) throw Error('blocked'); return values.get(key) ?? null;}, setItem(key,value) {if(blocked) throw Error('blocked'); values.set(key,value);}, removeItem(key) {if(blocked) throw Error('blocked'); values.delete(key);}},
    matchMedia: () => media, addEventListener: (type,fn) => {listeners[type]=fn;} };
  if (network) Object.assign(host,{fetch:network,AbortController,setTimeout,clearTimeout});
  vm.createContext(host); vm.runInContext(source, host);
  return {host, attrs, meta, media, listeners, controls, styleValues, values, stored: () => values.get('fleet-theme') ?? null};
}
test('new users start light even when their OS is dark', () => {
  const f=fixture(); assert.equal(f.attrs['data-theme'],'light'); assert.equal(f.meta.content,'#FAFAFA');
  f.media.matches=false; f.listeners.media(); assert.equal(f.attrs['data-theme'],'light');
});
test('saved dark survives reload and system is an explicit persistent choice', () => {
  const f=fixture('dark'); assert.equal(f.attrs['data-theme'],'dark');
  f.host.FleetTheme.setPreference('system'); assert.equal(f.stored(),'system');
  f.media.matches=true; f.listeners.media(); assert.equal(f.attrs['data-theme'],'light');
  f.host.FleetTheme.setPreference('dark'); f.listeners.media(); assert.equal(f.attrs['data-theme'],'dark');
  assert.equal(f.controls[1]['aria-pressed'],'true');
  assert.equal(fixture('system', true).attrs['data-theme'],'light');
});
test('cross-tab preference changes update chrome and controls without another write', () => {
  const f=fixture(); f.listeners.storage({key:'fleet-theme',newValue:'dark'});
  assert.equal(f.attrs['data-theme'],'dark'); assert.equal(f.meta.content,'#0B1210'); assert.equal(f.stored(),null);
  f.listeners.storage({key:'fleet-theme',newValue:null}); assert.equal(f.attrs['data-theme'],'light');
});
test('storage restrictions and invalid choices still allow in-memory theme selection', () => {
  const f=fixture(null,false,true); f.host.FleetTheme.setPreference('dark'); assert.equal(f.attrs['data-theme'],'dark');
  f.host.FleetTheme.setPreference('bad'); assert.equal(f.attrs['data-theme'],'dark');
});
test('palette defaults use the approved four seed colors for both modes', () => {
  const f=fixture();
  assert.deepEqual(JSON.parse(JSON.stringify(f.host.FleetTheme.paletteDefaults)), {
    light: {canvas:'#FAFAFA',accent:'#356B5B',highlight:'#C99A2E',text:'#202923'},
    dark: {canvas:'#0B1210',accent:'#78B59D',highlight:'#E0B84F',text:'#F1EBDD'},
  });
  assert.equal(f.styleValues['--bg'], '#FAFAFA');
  assert.equal(f.styleValues['--accent'], '#356B5B');
  assert.equal(f.styleValues['--highlight'], '#C99A2E');
  assert.equal(f.styleValues['--text'], '#202923');
  assert.match(f.styleValues['--surface'], /^color-mix\(in oklch,/);
  assert.equal(f.styleValues['--session-list-bg'], '#FAFAFA');
  assert.equal(f.styleValues['--session-detail-bg'], '#FAFAFA');
  assert.match(styleSource, /:root, \[data-theme="light"\][\s\S]*?--accent:#356B5B;[\s\S]*?--highlight:#C99A2E;/);
  assert.match(styleSource, /\[data-theme="dark"\][\s\S]*?--bg:#0B1210;[\s\S]*?--accent:#78B59D;[\s\S]*?--highlight:#E0B84F;/);
});
test('custom palette persists valid seeds, derives secondary tokens and follows theme changes', () => {
  const f=fixture();
  const custom = {
    light: {canvas:'#fffdfa',accent:'#145DA0',highlight:'#FFEA00',text:'#202124'},
    dark: {canvas:'#020304',accent:'#62D9C0',highlight:'#F4FF45',text:'#F7F8F8'},
  };
  f.host.FleetTheme.setPalette(custom);
  assert.equal(f.meta.content, '#FFFDFA');
  assert.equal(f.styleValues['--session-list-bg'], '#FFFDFA');
  assert.equal(f.styleValues['--session-detail-bg'], '#FFFDFA');
  assert.equal(f.styleValues['--accent'], '#145DA0');
  assert.match(f.styleValues['--surface-2'], /#FFFDFA 90%, #145DA0/);
  assert.match(f.styleValues['--surface-hover'], /#FFFDFA 84%, #145DA0/);
  assert.deepEqual(JSON.parse(f.values.get('fleet-theme-palette-v1')).light, {canvas:'#FFFDFA',accent:'#145DA0',highlight:'#FFEA00',text:'#202124'});
  f.host.FleetTheme.setPreference('dark');
  assert.equal(f.meta.content, '#020304');
  assert.equal(f.styleValues['--accent'], '#62D9C0');
  assert.equal(f.styleValues['--session-list-bg'], '#020304');
  assert.equal(f.styleValues['--session-detail-bg'], '#020304');
  assert.match(f.styleValues['--surface-2'], /#020304 86%, #62D9C0/);
});
test('invalid stored palette fields fall back independently and reset removes the override', () => {
  const f=fixture(null,false,false,{light:{canvas:'bad',accent:'#123456'},dark:{text:'#ABCDEF'}});
  assert.equal(f.host.FleetTheme.getPalette().light.canvas, '#FAFAFA');
  assert.equal(f.host.FleetTheme.getPalette().light.accent, '#123456');
  assert.equal(f.host.FleetTheme.getPalette().dark.text, '#ABCDEF');
  f.host.FleetTheme.resetPalette();
  assert.equal(f.values.has('fleet-theme-palette-v1'), false);
  assert.equal(f.styleValues['--accent'], '#356B5B');
});

function paletteNetwork(f, handle) {
  Object.assign(f.host, {AbortController, setTimeout, clearTimeout});
  const calls=[];
  f.host.fetch=async (url,options={})=>{
    calls.push({url,...options,body:options.body ? JSON.parse(options.body) : undefined});
    const result=await handle(calls.at(-1));
    return {ok:result.status ? result.status===200 : true,status:result.status || 200,json:async()=>result.body || result};
  };
  return calls;
}
test('server palette replaces a stale browser cache without uploading it', async () => {
  const f=fixture(null,false,false,{light:{canvas:'#111111'}});
  const server=f.host.FleetTheme.paletteDefaults;
  server.light.canvas='#EEEEEE';
  const calls=paletteNetwork(f,()=>({palette:server}));
  await f.host.FleetTheme.refreshPalette();
  assert.equal(calls.length,1);
  assert.equal(calls[0].method,undefined);
  assert.equal(f.host.FleetTheme.getPalette().light.canvas,'#EEEEEE');
  assert.equal(JSON.parse(f.values.get('fleet-theme-palette-v1')).light.canvas,'#EEEEEE');
});
test('legacy palette imports only into an empty server and accepts a concurrent winner', async () => {
  const f=fixture(null,false,false,{light:{canvas:'#EEEEEE'}});
  const winner=f.host.FleetTheme.paletteDefaults;
  winner.light.accent='#345678';
  const calls=paletteNetwork(f,request=>request.method ? {palette:winner} : {palette:null});
  await f.host.FleetTheme.refreshPalette();
  assert.equal(calls.length,2);
  assert.equal(calls[1].method,'PUT');
  assert.equal(calls[1].body.ifAbsent,true);
  assert.equal(calls[1].body.palette.light.canvas,'#EEEEEE');
  assert.equal(f.host.FleetTheme.getPalette().light.accent,'#345678');
  const fresh=fixture();
  const reads=paletteNetwork(fresh,()=>({palette:null}));
  await fresh.host.FleetTheme.refreshPalette();
  assert.equal(reads.length,1);
  assert.equal(fresh.values.has('fleet-theme-palette-v1'),false,'unset defaults must not claim migration priority');
});
test('saving waits for server acknowledgement and failures preserve the last palette', async () => {
  const f=fixture();
  const next=f.host.FleetTheme.paletteDefaults;
  next.light.canvas='#123456';
  let complete;
  const calls=paletteNetwork(f,()=>new Promise(resolve=>{complete=resolve;}));
  const save=f.host.FleetTheme.savePalette(next);
  await Promise.resolve();
  assert.equal(f.host.FleetTheme.getPalette().light.canvas,'#FAFAFA');
  complete({palette:next});
  await save;
  assert.equal(calls[0].method,'PUT');
  assert.equal(f.host.FleetTheme.getPalette().light.canvas,'#123456');
  paletteNetwork(f,()=>({status:500,body:{error:'write failed'}}));
  next.light.canvas='#654321';
  await assert.rejects(f.host.FleetTheme.savePalette(next),/write failed/);
  assert.equal(f.host.FleetTheme.getPalette().light.canvas,'#123456');
  assert.equal(JSON.parse(f.values.get('fleet-theme-palette-v1')).light.canvas,'#123456');
});
test('palette reads and saves serialize so a delayed read cannot overwrite a save', async () => {
  const f=fixture();
  let complete;
  const old=f.host.FleetTheme.paletteDefaults;
  const next=f.host.FleetTheme.paletteDefaults;
  next.dark.canvas='#223344';
  const calls=paletteNetwork(f,request=>request.method ? {palette:request.body.palette} : new Promise(resolve=>{complete=resolve;}));
  const refresh=f.host.FleetTheme.refreshPalette();
  const save=f.host.FleetTheme.savePalette(next);
  await Promise.resolve();
  assert.equal(calls.length,1);
  complete({palette:old});
  await refresh; await save;
  assert.equal(f.host.FleetTheme.getPalette().dark.canvas,'#223344');
});
test('malformed server palette fails instead of replacing valid cached values', async () => {
  const f=fixture(null,false,false,{light:{canvas:'#EEEEEE'}});
  paletteNetwork(f,()=>({palette:{light:{canvas:'bad'}}}));
  await assert.rejects(f.host.FleetTheme.refreshPalette());
  assert.equal(f.host.FleetTheme.getPalette().light.canvas,'#EEEEEE');
});

test('standalone pages load server colors at startup and refresh on focus without polling', async () => {
  const payload={palette:{light:{canvas:'#EEEEEE',accent:'#356B5B',highlight:'#C99A2E',text:'#18211F'},dark:{canvas:'#222222',accent:'#78B59D',highlight:'#E0B84F',text:'#F4F7F6'}}};
  let calls=0;
  const f=fixture(null,false,false,null,async()=>{calls++;return {ok:true,json:async()=>payload};});
  await f.host.FleetTheme.refreshPalette();
  assert.equal(calls,1,'startup and manual refresh should coalesce');
  assert.equal(f.styleValues['--bg'],'#EEEEEE');
  payload.palette.light.canvas='#AABBCC';
  await f.listeners.focus();
  assert.equal(calls,2);
  assert.equal(f.styleValues['--bg'],'#AABBCC');
});


test('authentication pages retain local colors without requesting private settings at startup or focus', async () => {
  for (const path of ['/auth', '/auth/', '/auth/register', '/auth/verify', '/auth/setup', '/auth/recovery']) {
    const requests = [];
    const f = fixture('dark', false, false, {dark: {canvas: '#123456'}}, async url => {
      requests.push(url);
      return {ok: false, json: async () => ({error: 'unauthorized'})};
    }, path);
    await f.host.FleetTheme.refreshPalette();
    await f.listeners.focus();
    await f.host.FleetTheme.refreshPalette();
    assert.deepEqual(requests, [], path);
    assert.equal(f.styleValues['--bg'], '#123456');
    f.host.FleetTheme.setPreference('light');
    assert.equal(f.attrs['data-theme'], 'light');
  }
});

test('focus after the password step preserves the pending challenge and permits OTP verification', async () => {
  const f = fixture(null, false, false, null, async () => Response.json({palette: null}), '/auth');
  await f.host.FleetTheme.refreshPalette();
  const sandbox = {URL, Headers};
  vm.runInNewContext(authSource, sandbox);
  const redirects = [], requests = [];
  const location = {origin: 'https://fleet.test', pathname: '/auth', search: '?next=%2Faccount', replace: url => redirects.push(url)};
  const client = sandbox.FleetAuth.createClient({location, fetch: async url => {
    requests.push(url);
    if (url === '/api/auth/login') return Response.json({state: 'challenge'});
    if (url === '/api/auth/verify') return Response.json({user: {id: 'alice'}});
    return new Response('', {status: 401});
  }});
  f.host.fetch = client.fetch;
  f.host.location = location;
  await client.json('/api/auth/login', {method: 'POST', body: {email: 'alice@example.com', password: 'test-password'}});
  location.pathname = '/auth/verify';
  await f.listeners.focus();
  await f.host.FleetTheme.refreshPalette();
  assert.deepEqual(redirects, []);
  assert.equal(location.pathname, '/auth/verify');
  assert.equal(location.search, '?next=%2Faccount');
  await client.json('/api/auth/verify', {method: 'POST', body: {code: '123456'}});
  assert.deepEqual(requests, ['/api/auth/login', '/api/auth/verify']);
  assert.equal(client.user.id, 'alice');
});
