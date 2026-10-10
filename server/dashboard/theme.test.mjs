import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('./theme.js', import.meta.url), 'utf8');
const styleSource = await readFile(new URL('./style.css', import.meta.url), 'utf8');
function fixture(stored = null, light = false, blocked = false, savedPalette = null) {
  const listeners = {}, media = { matches: light, addEventListener: (type, fn) => { listeners.media = fn; } };
  const values = new Map();
  if (stored !== null) values.set('fleet-theme', stored);
  if (savedPalette !== null) values.set('fleet-theme-palette-v1', JSON.stringify(savedPalette));
  const styleValues = {};
  const attrs = {}, meta = {}, controls = ['light', 'dark', 'system'].map(themeChoice => ({dataset:{themeChoice}, setAttribute(key,value) { this[key]=value; }}));
  const host = { document: { documentElement: {style:{setProperty:(key,value)=>{styleValues[key]=value;}},setAttribute: (key,value) => {attrs[key]=value;}}, querySelector: () => meta, querySelectorAll: () => controls, addEventListener() {} },
    localStorage: {getItem(key) {if (blocked) throw Error('blocked'); return values.get(key) ?? null;}, setItem(key,value) {if(blocked) throw Error('blocked'); values.set(key,value);}, removeItem(key) {if(blocked) throw Error('blocked'); values.delete(key);}},
    matchMedia: () => media, addEventListener: (type,fn) => {listeners[type]=fn;} };
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
