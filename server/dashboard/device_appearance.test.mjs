import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';

const source = await readFile(new URL('./device_appearance.js', import.meta.url), 'utf8').catch(() => '');
function setup(initial = '{}', blocked = false) {
  let stored = initial;
  const element = tag => ({tag, attributes:{}, children:[], setAttribute(k,v){this.attributes[k]=v;}, appendChild(n){this.children.push(n);}});
  const target = {document:{createElement:element, createElementNS:(_,tag)=>element(tag)},
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
test('one to three ASCII letters or digits preserve case and leading zeros and reject invalid text', () => {
  const {api}=setup();
  for(const [text,expected] of [['m','m'],['mb','mb'],[' A ','A'],[' aB ','aB'],['4','4'],['04','04'],['a1','a1'],['1b','1b'],['mbp','mbp'],['MbP','MbP'],['009','009'],['a1b','a1b'],['A1b','A1b']]) {
    assert.deepEqual(JSON.parse(JSON.stringify(api.normalize({icon:'text',text,color:'violet'}))), {icon:'text',text:expected,color:'violet'});
  }
  for(const text of ['', 'ABCD', '1234', 'M?', 'A 1', '中', '<', 'é', 'ß', 'ſ', 'ı', '４', '٤']) assert.equal(api.normalize({icon:'text',text}).icon,'monitor');
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
  for(const [text,expected] of [['ab','ab'],['4','4'],['04','04'],['a1','a1'],['www','www'],['WWW','WWW'],['009','009'],['a1b','a1b'],['A1b','A1b']]) {
    const icon=api.createIcon({icon:'text',text,color:'coral'});
    assert.equal(icon.children.length,0);assert.equal(icon.textContent,expected);
    assert.equal(icon.attributes['data-device-color'],'coral');
    assert.match(icon.className,/device-icon-letters/);assert.equal(icon.attributes['data-length'],String(expected.length));
  }
  assert.equal(api.get('m1').icon,'monitor');
});
