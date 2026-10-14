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
  const validPalette = value => ['light', 'dark'].every(mode => PALETTE_FIELDS.every(field => normalizedHex(value?.[mode]?.[field])));
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
    let hasSavedPalette = false;
    let media;
    const subscribers = new Set();
    function paletteKey() {
      if (!target.FleetAuth) return target.document.documentElement.dataset?.auth ? 'fleet-theme-palette-anonymous' : PALETTE_KEY;
      return target.FleetAuth.user ? target.FleetAuth.storageKey(PALETTE_KEY) : 'fleet-theme-palette-anonymous';
    }
    function readPalette() {
      let saved;
      try { saved = JSON.parse(target.localStorage.getItem(paletteKey()) || 'null'); } catch (_) {}
      hasSavedPalette = ['light', 'dark'].some(mode => PALETTE_FIELDS.some(field => normalizedHex(saved?.[mode]?.[field])));
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
      hasSavedPalette = true;
      try { target.localStorage.setItem(paletteKey(), JSON.stringify(palette)); } catch (_) {}
      apply();
      return clone(palette);
    }
    function resetPalette() {
      palette = normalizePalette();
      hasSavedPalette = false;
      try { target.localStorage.removeItem(paletteKey()); } catch (_) {}
      apply();
      return clone(palette);
    }
    let operations = Promise.resolve(), refreshTask;
    function enqueue(operation) {
      const task = operations.then(operation);
      operations = task.catch(() => {});
      return task;
    }
    async function requestPalette(update) {
      const identity = paletteKey();
      const controller = new target.AbortController();
      const timeout = target.setTimeout(() => controller.abort(), 10000);
      try {
        const response = await target.fetch('/api/settings', {
          cache: 'no-store', signal: controller.signal,
          ...(update ? {method: 'PUT', headers: {'content-type': 'application/json'}, body: JSON.stringify(update)} : {}),
        });
        const data = await response.json();
        if (identity !== paletteKey()) throw new Error('账号已切换，忽略旧配色响应');
        if (!response.ok) throw new Error(data?.error?.message || data?.error || `HTTP ${response.status}`);
        if (data.palette !== null && !validPalette(data.palette)) throw new Error('服务器未返回有效配色');
        return data.palette;
      } finally { target.clearTimeout(timeout); }
    }
    function refreshPalette() {
      if (refreshTask) return refreshTask;
      refreshTask = enqueue(async () => {
        // Authentication steps have no full session yet; keep their local palette.
        if (/^\/auth(?:\/|$)/.test(target.location?.pathname || '') || ((target.FleetAuth || target.document.documentElement.dataset?.auth) && !target.FleetAuth?.user)) return clone(palette);
        const identity = paletteKey();
        let server = await requestPalette();
        if (server === null && hasSavedPalette && !target.FleetAuth) server = await requestPalette({palette: clone(palette), ifAbsent: true});
        if (identity !== paletteKey()) throw new Error('账号已切换，忽略旧配色响应');
        if (server === null) { palette = normalizePalette(); apply(); }
        else setPalette(server);
        return clone(palette);
      }).finally(() => { refreshTask = undefined; });
      return refreshTask;
    }
    function savePalette(value) {
      if (!validPalette(value)) return Promise.reject(new Error('配色格式错误'));
      const next = normalizePalette(value);
      const identity = paletteKey();
      return enqueue(async () => {
        if (identity !== paletteKey()) throw new Error('账号已切换，请重新保存配色');
        const server = await requestPalette({palette: next});
        if (identity !== paletteKey()) throw new Error('账号已切换，忽略旧配色响应');
        if (server === null) throw new Error('服务器未确认保存配色');
        return setPalette(server);
      });
    }
    target.addEventListener?.('storage', event => {
      if (event.key === paletteKey()) { readPalette(); apply(); return; }
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
    if (typeof target.fetch === 'function') {
      // Cached seeds render immediately; an offline read never discards them.
      refreshPalette().catch(() => {});
      target.addEventListener?.('focus', () => refreshPalette().catch(() => {}));
    }
    return {
      refresh, apply, syncControls, setPreference, resolvedTheme, setPalette, resetPalette, refreshPalette, savePalette,
      normalizeColor: normalizedHex,
      getPalette: () => clone(palette),
      get paletteDefaults() { return clone(PALETTE_DEFAULTS); },
      get paletteStorageKey() { return paletteKey(); },
      get preference() { return preference; },
      subscribe(listener) { subscribers.add(listener); return () => subscribers.delete(listener); },
    };
  }
  host.FleetTheme = createController(host);
})(globalThis);
