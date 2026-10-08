const $=s=>document.querySelector(s), $$=s=>[...document.querySelectorAll(s)];
const params=new URLSearchParams(location.search);
const initialTheme=params.get('theme');if(['dark','light'].includes(initialTheme))document.documentElement.dataset.theme=initialTheme;
const initialPalette=params.get('palette');if(['champagne','jade','platinum'].includes(initialPalette))document.documentElement.dataset.palette=initialPalette;
const direction=window.FLEET_VARIANTS?.find(v=>v.id===params.get('variant'));
if(direction){
 document.documentElement.dataset.palette='champagne';
 document.querySelectorAll('.logo').forEach(e=>e.innerHTML='<svg viewBox="0 0 96 96" aria-hidden="true">'+direction.mark+'</svg>');
 const select=$('#review-palette');select.replaceChildren();
 const original=document.createElement('option');original.value='original';original.textContent='原版 · 香槟金';select.append(original);
 for(const v of window.FLEET_VARIANTS){const option=document.createElement('option');option.value=v.id;option.textContent=v.number+' '+v.name;select.append(option)}
 select.value=direction.id;
 const back=$('.review-bar a');back.href='compare.html';back.textContent='← 五版对比';
 $('.review-caption').textContent=direction.name+' · 原布局';
}else{const select=$('#review-palette');const group=document.createElement('optgroup');group.label='五版新方向';for(const v of window.FLEET_VARIANTS){const option=document.createElement('option');option.value=v.id;option.textContent=v.number+' '+v.name;group.append(option)}select.append(group);select.value=document.documentElement.dataset.palette}

function updateTheme(){const light=document.documentElement.dataset.theme==='light';$('#review-theme').textContent=light?'切换深色':'切换浅色';document.querySelector('meta[name="theme-color"]').content=direction?direction.palettes[light?'light':'dark'].bg:light?'#F4F1E9':'#111310'}
updateTheme();$('#review-theme').onclick=()=>{document.documentElement.dataset.theme=document.documentElement.dataset.theme==='dark'?'light':'dark';updateTheme()};
$('#review-palette').onchange=e=>{if(direction||window.FLEET_VARIANTS.some(v=>v.id===e.target.value)){const query=new URLSearchParams({theme:document.documentElement.dataset.theme});if(e.target.value!=='original')query.set('variant',e.target.value);location.href='dashboard.html?'+query}else{document.documentElement.dataset.palette=e.target.value}};
let toastTimer;function toast(text){const t=$('#review-toast');t.textContent=text;t.hidden=false;clearTimeout(toastTimer);toastTimer=setTimeout(()=>t.hidden=true,3200)}
function mode(next){$('#app').dataset.mode=next;$('#file-browser').hidden=next!=='files';$('#app').classList.remove('term-open');$$('[data-mode]').filter(e=>e.tagName==='BUTTON').forEach(e=>e.setAttribute('aria-selected',String(e.dataset.mode===next)))}
$$('button[data-mode]').forEach(e=>e.onclick=()=>mode(e.dataset.mode));
$$('[data-demo-session]').forEach(e=>e.onclick=()=>{$$('[data-demo-session]').forEach(x=>{x.classList.toggle('sel',x===e);x.setAttribute('aria-pressed',String(x===e))});$('#app').classList.add('term-open');const title=$('.win-head .ttl')||$('.win-head .tt');title.textContent=e.querySelector('.t').textContent});
$$('[data-demo-back]').forEach(e=>e.onclick=()=>$('#app').classList.remove('term-open'));
$('#session-search').oninput=e=>{const term=e.target.value.toLowerCase();let n=0;$$('[data-demo-session]').forEach(x=>{x.hidden=!x.textContent.toLowerCase().includes(term);if(!x.hidden)n++});$('#demo-empty').hidden=n>0};
$('#file-search').oninput=e=>$$('[data-demo-file]').forEach(x=>x.hidden=!x.dataset.demoFile.toLowerCase().includes(e.target.value.toLowerCase()));
$$('[data-demo-host]').forEach(e=>e.onclick=()=>{$$('[data-demo-host]').forEach(x=>x.setAttribute('aria-current',String(x===e)));toast('已切换预览设备范围')});
$$('[data-demo-file]').forEach(e=>e.onclick=()=>toast(e.dataset.demoFile+' · 这是本地视觉预览'));
$('#chat-input').oninput=()=>$('#chat-send').disabled=!$('#chat-input').value.trim();
$('#chat-composer').onsubmit=e=>{e.preventDefault();const text=$('#chat-input').value.trim();if(!text)return;const row=document.createElement('div');row.className='chat-row user';const wrap=document.createElement('div');wrap.className='chat-user-wrap';const card=document.createElement('div');card.className='chat-card';card.textContent=text;wrap.append(card);row.append(wrap);$('#chat-scroll .chat-stack').append(row);$('#chat-input').value='';$('#chat-send').disabled=true;$('#chat-scroll').scrollTop=$('#chat-scroll').scrollHeight;toast('消息仅保留在本地预览，不发送给设备')};
$$('button').filter(e=>!e.onclick&&e.id!=='chat-send').forEach(e=>e.onclick=()=>toast('视觉预览 · 此操作未连接真实服务'));
document.addEventListener('keydown',e=>{if(e.key==='Escape'){$('#app').classList.remove('term-open');$('#review-toast').hidden=true}});
