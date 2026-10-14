import test from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
const source=await readFile(new URL('./preview.js',import.meta.url),'utf8');
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
