const root=document.documentElement;
const frame=document.getElementById('guide-preview');
function refresh(){
 const style=getComputedStyle(root);
 const swatches=[['背景','--bg'],['表面','--surface'],['浮层','--surface-1'],['正文','--text'],['强调','--accent']];
 const target=document.getElementById('swatches');target.replaceChildren();
 for(const [name,token] of swatches){const box=document.createElement('div');box.className='swatch';const color=style.getPropertyValue(token).trim();box.innerHTML=`<div class="swatch-color" style="background:${color}"></div><div class="swatch-meta"><span>${name}</span><code>${color.toUpperCase()}</code></div><span class="swatch-token">${token}</span>`;target.append(box)}
 document.querySelectorAll('[data-token]').forEach(e=>e.textContent=style.getPropertyValue(e.dataset.token).trim().toUpperCase());
 document.getElementById('guide-theme').textContent=root.dataset.theme==='light'?'查看深色':'查看浅色';
 const wordmark=document.querySelector('.guide-wordmark img');wordmark.src=root.dataset.theme==='light'?'assets/logo-mark-dark.svg':'assets/logo-mark-light.svg';
 if(frame.contentDocument?.documentElement){const r=frame.contentDocument.documentElement;r.dataset.theme=root.dataset.theme;r.dataset.palette=root.dataset.palette;const b=frame.contentDocument.getElementById('review-palette');if(b)b.value=root.dataset.palette;frame.contentWindow.updateTheme?.()}
}
document.querySelectorAll('[data-palette-choice]').forEach(e=>e.onclick=()=>{root.dataset.palette=e.dataset.paletteChoice;document.querySelectorAll('[data-palette-choice]').forEach(b=>b.setAttribute('aria-pressed',String(b===e)));refresh()});
document.getElementById('guide-theme').onclick=()=>{root.dataset.theme=root.dataset.theme==='light'?'dark':'light';refresh()};
frame.onload=refresh;refresh();
let timer;document.querySelectorAll('.sample-buttons button:not(:disabled)').forEach(e=>e.onclick=()=>{const t=document.getElementById('guide-toast');t.hidden=false;clearTimeout(timer);timer=setTimeout(()=>t.hidden=true,2000)});
