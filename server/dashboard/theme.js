/* Shared synchronous theme bootstrap: light by default, configurable seed palette. */
(function (host) {
  'use strict';
  const THEME_KEY = 'fleet-theme';
  const PALETTE_KEY = 'fleet-theme-palette-v1';
  const PALETTE_FIELDS = ['canvas', 'accent', 'highlight', 'text'];
  const PALETTE_DEFAULTS = Object.freeze({
    light: Object.freeze({ canvas: '#FAFAFA', accent: '#356B5B', highlight: '#C99A2E', text: '#202923' }),
    dark: Object.freeze({ canvas: '#0B1210', accent: '#78B59D', highlight: '#E0B84F', text: '#F1EBDD' }),
  });
  const validPreference = value => ['light', 'dark', 'system'].includes(value);
  const clone = value => JSON.parse(JSON.stringify(value));
  const normalizedHex = value => {
    const match = String(value || '').trim().match(/^#([0-9a-f]{6})$/i);
    return match ? `#${match[1].toUpperCase()}` : null;
  };
  function normalizePalette(value) {
    const result = clone(PALETTE_DEFAULTS);
    for (const mode of ['light', 'dark']) {
      for (const field of PALETTE_FIELDS) result[mode][field] = normalizedHex(value?.[mode]?.[field]) || result[mode][field];
    }
    return result;
  }
  function contrastColor(hex) {
    const values = hex.slice(1).match(/../g).map(value => parseInt(value, 16) / 255)
      .map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4);
    const luminance = .2126 * values[0] + .7152 * values[1] + .0722 * values[2];
    const white = 1.05 / (luminance + .05);
    const black = (luminance + .05) / .05;
    return white >= black ? '#FFFFFF' : '#000000';
  }
  const mix = (base, color, baseWeight) => `color-mix(in oklch, ${base} ${baseWeight}%, ${color})`;
  function derivedTokens(mode, seeds) {
    const light = mode === 'light';
    const { canvas, accent, highlight, text } = seeds;
    return {
      bg: canvas,
      surface: mix(canvas, accent, light ? 96 : 93),
      'surface-1': mix(canvas, accent, light ? 98 : 96),
      'surface-2': mix(canvas, accent, light ? 90 : 86),
      'surface-hover': mix(canvas, accent, light ? 84 : 78),
      text,
      'text-1': mix(text, canvas, light ? 78 : 82),
      'text-2': mix(text, canvas, light ? 66 : 70),
      'text-3': mix(text, canvas, light ? 56 : 58),
      accent,
      'accent-1': mix(accent, text, 82),
      'accent-text': mix(accent, text, 84),
      'accent-contrast': contrastColor(accent),
      highlight,
      'session-list-bg': canvas,
      'session-detail-bg': canvas,
    };
  }
  function createController(target) {
    let preference = 'light';
    let palette = normalizePalette();
    let media;
    const subscribers = new Set();
    function readPalette() {
      let saved;
      try { saved = JSON.parse(target.localStorage.getItem(PALETTE_KEY) || 'null'); } catch (_) {}
      palette = normalizePalette(saved);
      return palette;
    }
    function resolvedTheme() { return preference === 'system' ? (media?.matches ? 'light' : 'dark') : preference; }
    function syncControls() {
      target.document.querySelectorAll('[data-theme-choice]').forEach(button => {
        button.setAttribute('aria-pressed', String(button.dataset.themeChoice === preference));
      });
    }
    function applyPalette(theme) {
      const seeds = palette[theme] || palette.light;
      const style = target.document.documentElement.style;
      if (style?.setProperty) {
        for (const [name, value] of Object.entries(derivedTokens(theme, seeds))) style.setProperty(`--${name}`, value);
      }
      return seeds;
    }
    function apply(theme = resolvedTheme()) {
      target.document.documentElement.setAttribute('data-theme', theme);
      const seeds = applyPalette(theme);
      const meta = target.document.querySelector('meta[name="theme-color"]');
      if (meta) meta.content = seeds.canvas;
      syncControls();
      subscribers.forEach(listener => listener(theme));
    }
    const onSystemChange = () => { if (preference === 'system') apply(); };
    function refresh() {
      let saved;
      try { saved = target.localStorage.getItem(THEME_KEY); } catch (_) {}
      preference = validPreference(saved) ? saved : 'light';
      readPalette();
      media?.removeEventListener?.('change', onSystemChange);
      media = target.matchMedia?.('(prefers-color-scheme: light)');
      media?.addEventListener?.('change', onSystemChange);
      apply();
    }
    function setPreference(value) {
      if (!validPreference(value)) return;
      preference = value;
      try { target.localStorage.setItem(THEME_KEY, value); } catch (_) {}
      apply();
    }
    function setPalette(value) {
      palette = normalizePalette(value);
      try { target.localStorage.setItem(PALETTE_KEY, JSON.stringify(palette)); } catch (_) {}
      apply();
      return clone(palette);
    }
    function resetPalette() {
      palette = normalizePalette();
      try { target.localStorage.removeItem(PALETTE_KEY); } catch (_) {}
      apply();
      return clone(palette);
    }
    target.addEventListener?.('storage', event => {
      if (event.key === PALETTE_KEY) { readPalette(); apply(); return; }
      if (event.key !== THEME_KEY && event.key !== null) return;
      if (event.key === null) { refresh(); return; }
      preference = validPreference(event.newValue) ? event.newValue : 'light';
      apply();
    });
    target.document.addEventListener('DOMContentLoaded', () => {
      syncControls();
      target.document.querySelectorAll('.account-theme [data-theme-choice]').forEach(button => {
        button.addEventListener('click', () => setPreference(button.dataset.themeChoice));
      });
    });
    refresh();
    return {
      refresh, apply, syncControls, setPreference, resolvedTheme, setPalette, resetPalette,
      normalizeColor: normalizedHex,
      getPalette: () => clone(palette),
      get paletteDefaults() { return clone(PALETTE_DEFAULTS); },
      get paletteStorageKey() { return PALETTE_KEY; },
      get preference() { return preference; },
      subscribe(listener) { subscribers.add(listener); return () => subscribers.delete(listener); },
    };
  }
  host.FleetTheme = createController(host);
})(globalThis);
