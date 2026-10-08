import { readFile, writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

export function renderTitaniumCSS(design) {
  const common = {
    ...Object.fromEntries(Object.entries(design.typography).map(([name, value]) => [`t-${name}`, `${value}px`])),
    'rail-w': `${design.navigation.deviceWidth}px`, 'sess-w': `${design.navigation.sessionWidth}px`,
    'rail-compact-w': `${design.navigation.compactDeviceWidth}px`, 'sess-compact-w': `${design.navigation.compactSessionWidth}px`,
    ...Object.fromEntries(Object.entries(design.radii).map(([name, value]) => [`radius-${name}`, `${value}px`])),
    'r-sm': 'var(--radius-compact)', r: 'var(--radius-control)', 'r-lg': 'var(--radius-card)', 'r-xl': 'var(--radius-panel)',
    border: 'transparent', 'border-1': 'transparent', 'border-strong': 'transparent', 'accent-line': 'transparent',
    'accent-soft': 'color-mix(in srgb,var(--accent) 11%,transparent)',
    'online-soft': 'color-mix(in srgb,var(--online) 10%,transparent)',
    'wait-soft': 'color-mix(in srgb,var(--wait) 10%,transparent)',
    'danger-soft': 'color-mix(in srgb,var(--danger) 10%,transparent)',
    'brand-grad': 'var(--accent)', ring: '0 0 0 3px var(--accent-soft)', glow: 'none',
    'chat-bg': 'var(--surface)', 'chat-surface': 'var(--surface-1)', 'chat-surface-1': 'var(--surface-1)',
    'chat-surface-2': 'var(--surface-2)', 'chat-surface-hover': 'var(--surface-hover)',
    'chat-text': 'var(--text)', 'chat-text-1': 'var(--text-1)', 'chat-text-2': 'var(--text-2)', 'chat-text-3': 'var(--text-3)',
    'chat-link': 'var(--accent-text)', 'chat-code-bg': 'var(--surface-2)', 'chat-user-bg': 'var(--surface-2)',
    'chat-composer-bg': 'var(--surface-2)', 'chat-popover-shadow': 'var(--e3)',
    'chat-border': 'transparent', 'chat-border-light': 'transparent', 'chat-border-strong': 'transparent',
  };
  const block = (selector, tokens, scheme) => `${selector} {\n${Object.entries(tokens).map(([name, value]) => `  --${name}: ${value};`).join('\n')}${scheme ? `\n  color-scheme: ${scheme};` : ''}\n}\n`;
  const devices = Object.entries(design.deviceColors).map(([name, palette]) =>
    block(`[data-device-color="${name}"]`, { 'device-ink': palette.light, 'device-dark': palette.dark })).join('\n');
  return block(':root', common) + '\n' + block(':root, [data-theme="light"]', design.light, 'light') + '\n' +
    block('[data-theme="dark"]', design.dark, 'dark') + '\n' + devices +
    '\n[data-theme="dark"] [data-device-color] { --device-ink: var(--device-dark); }\n';
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const design = JSON.parse(await readFile(new URL('../server/dashboard/titanium.json', import.meta.url), 'utf8'));
  const rendered = renderTitaniumCSS(design);
  const target = new URL('../server/dashboard/titanium.css', import.meta.url);
  if (process.argv.includes('--check')) {
    if (await readFile(target, 'utf8') !== rendered) throw new Error('Titanium tokens are out of sync');
    process.stdout.write('Titanium shared tokens are in sync\n');
  } else if (process.argv.includes('--write')) {
    await writeFile(target, rendered);
  } else { process.stdout.write(rendered); }
}
