import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const source=await readFile(new URL('./preview.js',import.meta.url),'utf8');
test('network links retain the owning host for loopback, LAN, IPv6, hostnames and VPN-only names',()=>{
 const ctx={URL,URLSearchParams,location:{origin:'https://fleet.example.test'}};vm.createContext(ctx);vm.runInContext(source,ctx);
 for(const target of ['http://127.0.0.2:8788/?v=3#demo','http://localhost:3000','http://192.168.1.30','http://10.1.2.3','http://172.20.0.4','http://[fd00::5]:8080','http://build.local:8788','https://vpn-only.corp.example/']){
  const href=ctx.FleetPreview.resolveNetworkLink(target,{macId:'m3'});const url=new URL(href,ctx.location.origin);
  assert.equal(url.pathname,'/host-view');assert.equal(url.searchParams.get('mac'),'m3');assert.equal(url.searchParams.get('url'),new URL(target).href);
 }
 assert.equal(ctx.FleetPreview.resolveNetworkLink('https://fleet.example.test/account',{macId:'m3'}),'');
 assert.equal(ctx.FleetPreview.resolveNetworkLink('https://example.test',{macId:'unknown'}),'');
 assert.equal(ctx.FleetPreview.resolveNetworkLink('/Users/test/doc.md',{macId:'m3'}),'');
});
test('oversized preview retains a direct download action',async()=>{
 const nodes=new Map();
 const node=()=>({hidden:true,dataset:{},children:[],append(child){this.children.push(child)},setAttribute(){},querySelectorAll(){return[];}});
 for(const id of ['#app','#preview-page','#preview-stage','#preview-error','#preview-download']) nodes.set(id,node());
 const document={createElement:node,querySelector:id=>nodes.get(id),documentElement:{dataset:{}}};
 const ctx={document,URLSearchParams,location:{pathname:'/view',search:'?mac=m1&path=large.log&cwd=%2Ftmp'},fetch:async()=>({ok:false,status:413,json:async()=>({error:'preview_too_large',message:'文件过大，不支持预览'})})};
 vm.createContext(ctx);vm.runInContext(source,ctx);await ctx.FleetPreview.initRoute();
 assert.match(nodes.get('#preview-error').textContent,/文件过大/);
 assert.equal(nodes.get('#preview-download').hidden,false);
 assert.equal(nodes.get('#preview-error').children[0].textContent,'下载原文件');
 assert.equal(nodes.get('#preview-error').children[0].href,nodes.get('#preview-download').href);
 const url=new URL(nodes.get('#preview-download').href,'http://localhost');
 assert.equal(url.pathname,'/m1/api/file/content');assert.equal(url.searchParams.get('cwd'),'/tmp');assert.equal(url.searchParams.get('download'),'1');
});
