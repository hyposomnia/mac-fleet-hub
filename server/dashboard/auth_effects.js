(function (host) {
  'use strict';
  function create(target) {
    const document = target.document;
    const probe = document.querySelector('.auth-probe');
    const coordinates = probe?.querySelector('span');
    const fine = target.matchMedia?.('(hover: hover) and (pointer: fine)');
    const reduced = target.matchMedia?.('(prefers-reduced-motion: reduce)');
    let frame = null;
    let latest = null;
    let tracking = false;
    function hide() {
      if (frame !== null) target.cancelAnimationFrame(frame);
      frame = null;
      latest = null;
      if (probe) probe.hidden = true;
    }
    function move(event) {
      if (document.hidden || event.pointerType === 'touch') return;
      latest = [Math.round(event.clientX), Math.round(event.clientY)];
      if (frame !== null) return;
      frame = target.requestAnimationFrame(() => {
        frame = null;
        if (!latest) return;
        probe.style.transform = `translate3d(${latest[0]}px, ${latest[1]}px, 0)`;
        if (coordinates) coordinates.textContent = `${String(latest[0]).padStart(4, '0')} : ${String(latest[1]).padStart(4, '0')}`;
        probe.hidden = false;
      });
    }
    function sync() {
      const enabled = Boolean(probe && fine?.matches && !reduced?.matches && !document.hidden);
      if (enabled === tracking) return;
      tracking = enabled;
      if (enabled) document.addEventListener('pointermove', move, { passive: true });
      else { document.removeEventListener('pointermove', move); hide(); }
    }
    function visibility() { if (document.hidden) hide(); sync(); }
    function destroy() {
      document.removeEventListener('pointermove', move);
      document.removeEventListener('pointerleave', hide);
      document.removeEventListener('visibilitychange', visibility);
      fine?.removeEventListener?.('change', sync);
      reduced?.removeEventListener?.('change', sync);
      hide();
    }
    fine?.addEventListener?.('change', sync);
    reduced?.addEventListener?.('change', sync);
    document.addEventListener('pointerleave', hide);
    document.addEventListener('visibilitychange', visibility);
    sync();
    return { destroy };
  }
  host.FleetAuthEffects = { create };
  host.addEventListener?.('DOMContentLoaded', () => {
    const effect = create(host);
    host.addEventListener?.('pagehide', () => effect.destroy(), { once: true });
  });
})(globalThis);
