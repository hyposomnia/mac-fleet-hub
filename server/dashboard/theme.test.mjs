import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import { readFile } from 'node:fs/promises';
const source = await readFile(new URL('./theme.js', import.meta.url), 'utf8');
function fixture(stored = null, light = false, blocked = false) {
  const listeners = {}, media = { matches: light, addEventListener: (type, fn) => { listeners.media = fn; } };
  const attrs = {}, meta = {}, controls = ['light', 'dark', 'system'].map(themeChoice => ({dataset:{themeChoice}, setAttribute(key,value) { this[key]=value; }}));
  const host = { document: { documentElement: {setAttribute: (key,value) => {attrs[key]=value;}}, querySelector: () => meta, querySelectorAll: () => controls, addEventListener() {} },
    localStorage: {getItem() {if (blocked) throw Error('blocked'); return stored;}, setItem(key,value) {if(blocked) throw Error('blocked'); stored=value;}},
    matchMedia: () => media, addEventListener: (type,fn) => {listeners[type]=fn;} };
  vm.createContext(host); vm.runInContext(source, host);
  return {host, attrs, meta, media, listeners, controls, stored: () => stored};
}
test('new users start light even when their OS is dark', () => {
  const f=fixture(); assert.equal(f.attrs['data-theme'],'light'); assert.equal(f.meta.content,'#FFFFFF');
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
  assert.equal(f.attrs['data-theme'],'dark'); assert.equal(f.meta.content,'#10141B'); assert.equal(f.stored(),null);
  f.listeners.storage({key:'fleet-theme',newValue:null}); assert.equal(f.attrs['data-theme'],'light');
});
test('storage restrictions and invalid choices still allow in-memory theme selection', () => {
  const f=fixture(null,false,true); f.host.FleetTheme.setPreference('dark'); assert.equal(f.attrs['data-theme'],'dark');
  f.host.FleetTheme.setPreference('bad'); assert.equal(f.attrs['data-theme'],'dark');
});
