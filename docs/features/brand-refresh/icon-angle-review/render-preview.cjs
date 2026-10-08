const fs = require('node:fs');
const path = require('node:path');
const sharp = require('sharp');
const dir = __dirname;
const source = fs.readFileSync(path.join(dir, '../../../../server/dashboard/icons/favicon.svg'), 'utf8');
const base = /d="([^"]+)"/.exec(source)[1];
const elevation = 41.5 * Math.PI / 180;
const originalElevation = Math.asin(1 / Math.sqrt(3));
const topRatio = Math.sin(elevation) / Math.sin(originalElevation);
const verticalRatio = Math.cos(elevation) / Math.cos(originalElevation);
function sideBase() {
  // Preserve outer corners while reducing the side-face stroke by six percent.
  const thin = .6, dx = Math.sqrt(3) / 2 * thin;
  return base.replace('69.22 32.75Q67.92 33.5 66.62 32.75', `${69.22+dx} ${32.75-thin/2}Q${67.92+dx} ${33.5-thin/2} ${66.62+dx} ${32.75-thin/2}`)
    .replace('49.5 22.87Q48 22 46.5 22.87', `49.5 ${22.87-thin}Q48 ${22-thin} 46.5 ${22.87-thin}`)
    .replace('29.38 32.75Q28.08 33.5 26.78 32.75', `${29.38-dx} ${32.75-thin/2}Q${28.08-dx} ${33.5-thin/2} ${26.78-dx} ${32.75-thin/2}`);
}
function project(d, face) {
  const tokens = d.match(/[MLQZ]|-?\d+(?:\.\d+)?/g), result = [];
  for (let i=0; i<tokens.length;) {
    if (/[MLQZ]/.test(tokens[i])) { result.push(tokens[i++]); continue; }
    let x=Number(tokens[i++])-48, y=Number(tokens[i++])-48;
    const rotation = face * Math.PI * 2/3;
    [x,y] = [x*Math.cos(rotation)-y*Math.sin(rotation), x*Math.sin(rotation)+y*Math.cos(rotation)];
    if (!face) y *= topRatio;
    else y = y*verticalRatio + Math.abs(x)/Math.sqrt(3)*(verticalRatio-topRatio);
    result.push([x+48,y+48]);
  }
  return result;
}
const faces = [0,1,2].map(face => project(face ? sideBase() : base,face));
// Keep the top/bottom footprint and center identical for a fair optical comparison.
const minY=48-36*topRatio, maxY=48+36*verticalRatio;
const fit=72/(maxY-minY), center=(minY+maxY)/2;
const revised = faces.map(points => '<path d="'+points.map(item=>Array.isArray(item)?`${(48+(item[0]-48)*fit).toFixed(3)} ${(48+(item[1]-center)*fit).toFixed(3)}`:item).join(' ')+'"/>').join('');
const original = source.match(/<g[^>]*>([\s\S]*)<\/g>/)[1];
const mark = body => `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 96 96"><g fill="#2C5D87">${body}</g></svg>`;
fs.writeFileSync(path.join(dir,'logo-raised-view.svg'), mark(revised));
fs.writeFileSync(path.join(dir,'app-icon-raised-view.svg'), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512"><rect width="512" height="512" fill="#EDF2F6"/><g transform="translate(88 88) scale(3.5)" fill="#2C5D87">${revised}</g></svg>`);
const put = (body,x,y,size,color='#2C5D87')=>`<g transform="translate(${x} ${y}) scale(${size/96})" fill="${color}">${body}</g>`;
let board = `<svg xmlns="http://www.w3.org/2000/svg" width="1100" height="660" viewBox="0 0 1100 660"><rect width="1100" height="660" fill="#F7F9FB"/><g fill="#263549" font-family="PingFang SC, sans-serif" font-size="24" text-anchor="middle"><text x="275" y="65">原版</text><text x="825" y="65">调整后</text></g>`;
for(const [x,body] of [[275,original],[825,revised]]) {
 board += put(body,x-168,92,336);
 board += `<rect x="${x-166}" y="463" width="88" height="88" rx="20" fill="#EDF2F6"/>`+put(body,x-151,478,58);
 board += put(body,x-32,493,28)+put(body,x+26,485,44)+put(body,x+101,479,56);
}
board += `<g font-family="PingFang SC, sans-serif" text-anchor="middle" font-size="16" fill="#62748A"><text x="275" y="603">现有正六边形比例</text><text x="825" y="603">顶部放大 · 侧面收薄 · 俯视角略提高</text></g></svg>`;
fs.writeFileSync(path.join(dir,'comparison.svg'),board);
sharp(Buffer.from(board)).png().toFile(path.join(dir,'comparison.png')).then(()=>console.log('Preview saved; production assets untouched.'));
