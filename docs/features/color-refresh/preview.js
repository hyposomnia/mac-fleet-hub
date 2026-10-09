const root = document.documentElement;
const themeButton = document.querySelector('#theme-toggle');
const metaTheme = document.querySelector('meta[name="theme-color"]');
const noteTitle = document.querySelector('#palette-note-title');
const noteCopy = document.querySelector('#palette-note-copy');
const paletteName = document.querySelector('.palette-name');

const palettes = {
  clear: {
    name: '01 澄蓝',
    copy: '五档里最偏蓝。白底主题色 #116B98，黑底主题色 #56BDEB；酸性荧光黄保持固定。',
    theme: { light: '#ffffff', dark: '#000000' },
  },
  bay: {
    name: '02 海湾蓝',
    copy: '亮度提高并轻微偏绿。白底主题色 #00728F，黑底主题色 #43C8E2；酸性荧光黄保持固定。',
    theme: { light: '#ffffff', dark: '#000000' },
  },
  ocean: {
    name: '03 青海蓝',
    copy: '蓝绿比例居中。白底主题色 #007989，黑底主题色 #36CFD6；酸性荧光黄保持固定。',
    theme: { light: '#ffffff', dark: '#000000' },
  },
  peacock: {
    name: '04 孔雀蓝',
    copy: '明显偏绿但仍读作蓝。白底主题色 #007E7E，黑底主题色 #43D3C8；酸性荧光黄保持固定。',
    theme: { light: '#ffffff', dark: '#000000' },
  },
  turquoise: {
    name: '05 绿松蓝',
    copy: '已选方向。白底主题色进一步增绿至 #087F65，黑底主题色保持 #5BD7B7；酸性荧光黄保持固定。',
    theme: { light: '#ffffff', dark: '#000000' },
  },
};

function sync() {
  const palette = palettes[root.dataset.palette];
  const dark = root.dataset.theme === 'dark';
  document.querySelectorAll('[data-palette-choice]').forEach((button) => {
    button.setAttribute('aria-selected', String(button.dataset.paletteChoice === root.dataset.palette));
  });
  themeButton.textContent = dark ? '浅色' : '深色';
  themeButton.setAttribute('aria-pressed', String(dark));
  metaTheme.content = palette.theme[root.dataset.theme];
  noteTitle.textContent = palette.name;
  noteCopy.textContent = palette.copy;
  paletteName.textContent = palette.name;
  const url = new URL(location.href);
  url.searchParams.set('palette', root.dataset.palette);
  url.searchParams.set('theme', root.dataset.theme);
  history.replaceState(null, '', url);
}

document.querySelectorAll('[data-palette-choice]').forEach((button) => {
  button.addEventListener('click', () => {
    root.dataset.palette = button.dataset.paletteChoice;
    sync();
  });
});

themeButton.addEventListener('click', () => {
  root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
  sync();
});

const params = new URLSearchParams(location.search);
if (palettes[params.get('palette')]) root.dataset.palette = params.get('palette');
if (['light', 'dark'].includes(params.get('theme'))) root.dataset.theme = params.get('theme');
sync();
