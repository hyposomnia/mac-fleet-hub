import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const source = await readFile(new URL('./device_hover.js', import.meta.url), 'utf8');
class Node {
  constructor(className = '') { this.className = className; this.dataset = {}; this.style = {setProperty(k,v){this[k]=v;}}; this.attrs = {}; this.events = {}; this.children = []; this.isConnected = true; }
  append(...nodes) { for(const node of nodes) {node.parent = this; this.children.push(node);} }
  addEventListener(name, fn) {this.events[name]=fn;}
  setAttribute(k,v) {this.attrs[k]=v;}
  getAttribute(k) {return this.attrs[k] ?? null;}
  removeAttribute(k) {delete this.attrs[k];}
  contains(node) {return this === node || this.children.some(c=>c.contains(node));}
  closest(selector) {return selector === '.host[data-mac]' && this.dataset.mac ? this : this.parent?.closest(selector);}
  querySelector(selector) {return this.children.find(c=>'.'+c.className===selector) || null;}
  cloneNode() {const n=new Node(this.className); n.dataset={...this.dataset}; n.attrs={...this.attrs}; n.textContent=this.textContent; n.append(...this.children.map(c=>c.cloneNode())); return n;}
  getBoundingClientRect() {return {left:12,top:180,width:48,height:42};}
  remove() {this.isConnected=false; if(this.parent) this.parent.children=this.parent.children.filter(c=>c!==this);}
  focus() {this.focused=true;}
}
function setup({desktop=true,collapsed=true}={}) {
 const app=new Node(), nav=new Node(), rail=new Node(), body=new Node(); app.dataset.railCollapsed=String(collapsed);
 const row=new Node('host'); row.dataset.mac='m1'; row.attrs['aria-current']='true'; row.attrs['aria-label']='Mac Mini M4';
 for(const c of ['device-icon','nm','ct','i']) {const child=new Node(c);child.textContent=c==='nm'?'Mac Mini M4':'';row.append(child);} nav.append(row);
 const docEvents={}, frames=[], timers=new Map(), observers=[]; let next=0;
 const media={matches:desktop,addEventListener(name,fn){this.events??={};this.events[name]=fn;}};
 const context={document:{body,querySelector:s=>({'#app':app,'#rail':rail,'#host-list':nav})[s],createElement:()=>new Node(),addEventListener:(n,f)=>docEvents[n]=f},
 matchMedia:()=>media,getComputedStyle:()=>({getPropertyValue:()=> '260px'}), innerWidth:1280,
 requestAnimationFrame:f=>frames.push(f),setTimeout:f=>{timers.set(++next,f);return next;},clearTimeout:id=>timers.delete(id),
 addEventListener:(n,f)=>docEvents[n]=f,MutationObserver:class{constructor(f){observers.push(f);}observe(){}}};
 vm.runInNewContext(source,context); const selected=[], settings=[];
 const api=context.FleetDeviceHover.init({onSelect:id=>selected.push(id),onSettings:id=>settings.push(id)});
 return {app,nav,row,body,docEvents,media,selected,settings,api, frames:()=>frames.splice(0).forEach(f=>f()), timers:()=>{const pending=[...timers.values()];timers.clear();pending.forEach(f=>f());},mutate:()=>observers.forEach(f=>f()),panel:()=>body.children[0]};
}
function enter(s) {s.nav.events.pointerover({target:s.row,pointerType:'mouse'});s.frames();s.frames();}
test('collapsed device hover reuses its current icon, name and selection in an anchored expandable row',()=>{
 const s=setup();enter(s);const p=s.panel();assert.equal(p.dataset.open,'true');assert.equal(p.style.left,'12px');assert.equal(p.style.top,'180px');
 assert.equal(p.style['--device-hover-width'],'236px');assert.equal(p.children[0].getAttribute('aria-current'),'true');
 assert.equal(p.children[0].querySelector('.nm').textContent,'Mac Mini M4');assert.equal(p.children[0].querySelector('.i'),null);
 assert.equal(p.children[1].getAttribute('aria-label'),'Mac Mini M4 设置');
});
test('moving from the original item onto settings keeps it open and settings does not select the device',()=>{
 const s=setup();enter(s);const p=s.panel();s.nav.events.pointerout({target:s.row,relatedTarget:p.children[1]});assert.equal(p.dataset.open,'true');
 p.children[1].events.click();assert.deepEqual(s.settings,['m1']);assert.deepEqual(s.selected,[]);assert.equal(s.panel(),undefined);
});
test('clicking the expanded item selects the originating device and leaving animates closed',()=>{
 const s=setup();enter(s);s.panel().children[0].events.click();assert.deepEqual(s.selected,['m1']);
 enter(s);s.panel().events.pointerleave({relatedTarget:null});assert.equal(s.panel().dataset.open,'false');s.timers();assert.equal(s.panel(),undefined);
});
test('expanded sidebar and touch events do not create a hover panel',()=>{
 for(const options of [{desktop:false},{collapsed:false}]){const s=setup(options);enter(s);assert.equal(s.panel(),undefined);}
 const s=setup();s.nav.events.pointerover({target:s.row,pointerType:'touch'});s.frames();assert.equal(s.panel(),undefined);
});
test('escape, scrolling, source removal and sidebar expansion clear stale hover actions',()=>{
 for(const action of [s=>s.docEvents.keydown({key:'Escape'}),s=>s.docEvents.scroll(),s=>{s.row.isConnected=false;s.mutate();},s=>{s.app.dataset.railCollapsed='false';s.mutate();}]){
 const s=setup();enter(s);action(s);assert.equal(s.panel(),undefined);
 }
 const s=setup();enter(s);s.docEvents.pointerdown({target:new Node()});assert.equal(s.panel(),undefined);
});

test('leaving before animation starts cannot reopen a dismissed item',()=>{
 const s=setup();s.nav.events.pointerover({target:s.row,pointerType:'mouse'});
 s.nav.events.pointerout({target:s.row,relatedTarget:null});s.frames();s.frames();
 assert.equal(s.panel().dataset.open,'false');s.timers();assert.equal(s.panel(),undefined);
});
