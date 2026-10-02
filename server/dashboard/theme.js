/* Shared synchronous bootstrap: light by default, explicit system preference. */
(function (host) {
  'use strict';
  const COLORS = { light: '#EDF1F4', dark: '#10141B' };
  const valid = value => ['light', 'dark', 'system'].includes(value);
  function createController(target) {
    let preference = 'light';
    let media;
    const subscribers = new Set();
    function resolvedTheme() { return preference === 'system' ? (media?.matches ? 'light' : 'dark') : preference; }
    function syncControls() {
      target.document.querySelectorAll('[data-theme-choice]').forEach(button => {
        button.setAttribute('aria-pressed', String(button.dataset.themeChoice === preference));
      });
    }
    function apply(theme = resolvedTheme()) {
      target.document.documentElement.setAttribute('data-theme', theme);
      const meta = target.document.querySelector('meta[name="theme-color"]');
      if (meta) meta.content = COLORS[theme] || COLORS.light;
      syncControls();
      subscribers.forEach(listener => listener(theme));
    }
    const onSystemChange = () => { if (preference === 'system') apply(); };
    function refresh() {
      let saved;
      try { saved = target.localStorage.getItem('fleet-theme'); } catch (_) {}
      preference = valid(saved) ? saved : 'light';
      media?.removeEventListener?.('change', onSystemChange);
      media = target.matchMedia?.('(prefers-color-scheme: light)');
      media?.addEventListener?.('change', onSystemChange);
      apply();
    }
    function setPreference(value) {
      if (!valid(value)) return;
      preference = value;
      try { target.localStorage.setItem('fleet-theme', value); } catch (_) {}
      apply();
    }
    target.addEventListener?.('storage', event => {
      if (event.key !== 'fleet-theme' && event.key !== null) return;
      if (event.key === null) { refresh(); return; }
      preference = valid(event.newValue) ? event.newValue : 'light';
      apply();
    });
    target.document.addEventListener('DOMContentLoaded', () => {
      syncControls();
      target.document.querySelectorAll('.account-theme [data-theme-choice]').forEach(button => {
        button.addEventListener('click', () => setPreference(button.dataset.themeChoice));
      });
    });
    refresh();
    return { refresh, apply, syncControls, setPreference, resolvedTheme,
      get preference() { return preference; },
      subscribe(listener) { subscribers.add(listener); return () => subscribers.delete(listener); } };
  }
  host.FleetTheme = createController(host);
})(globalThis);
