(function (host) {
  'use strict';
  function create(target) {
    const document = target.document;
    const canvas = document.querySelector('.auth-stars');
    const context = canvas?.getContext('2d');
    if (!context) return { destroy() {} };
    const fine = target.matchMedia('(hover: hover) and (pointer: fine)');
    const reduced = target.matchMedia('(prefers-reduced-motion: reduce)');
    let frame = null, previousTime = null, lastMove = 0, tracking = false;
    let latest = null, particles = [], nextEmission = 0, color;
    const follow = (elapsed, duration) => 1 - Math.exp(-elapsed / duration);
    const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
    const rgba = alpha => `rgba(${color.join(',')},${alpha})`;

    function clear() {
      context.clearRect(0, 0, target.innerWidth, target.innerHeight);
      canvas.hidden = true;
    }
    function hide() {
      if (frame !== null) target.cancelAnimationFrame(frame);
      frame = null; previousTime = null; latest = null;
      particles = []; nextEmission = 0;
      clear();
    }
    function palette() {
      const hex = target.getComputedStyle(document.documentElement).getPropertyValue('--accent').trim();
      color = [1, 3, 5].map(index => parseInt(hex.slice(index, index + 2), 16));
    }
    function resize() {
      hide();
      const ratio = Math.min(target.devicePixelRatio || 1, 2);
      canvas.width = Math.round(target.innerWidth * ratio);
      canvas.height = Math.round(target.innerHeight * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
    }
    function spawn(now) {
      const edge = Math.floor(Math.random() * 4);
      const x = edge === 1 ? target.innerWidth : edge === 3 ? 0 : Math.random() * target.innerWidth;
      const y = edge === 0 ? 0 : edge === 2 ? target.innerHeight : Math.random() * target.innerHeight;
      const dx = latest.x - x, dy = latest.y - y, distance = Math.max(Math.hypot(dx, dy), 1);
      const speed = Math.max(.18, distance / (1600 + Math.random() * 650));
      particles.push({ x, y, vx: dx / distance * speed, vy: dy / distance * speed, speed,
        born: now - 16.67, expires: now + 2800, radius: .65 + Math.random() * .4 });
    }
    function draw(particle, now, distance) {
      const alpha = .36 * clamp((now - particle.born) / 180, 0, 1)
        * clamp((particle.expires - now) / 320, 0, 1) * clamp((distance - 10) / 50, 0, 1);
      const glow = context.createRadialGradient(particle.x, particle.y, 0, particle.x, particle.y, 6);
      glow.addColorStop(0, rgba(alpha * .22)); glow.addColorStop(1, rgba(0));
      context.fillStyle = glow;
      context.beginPath(); context.arc(particle.x, particle.y, 6, 0, Math.PI * 2); context.fill();
      context.fillStyle = rgba(alpha);
      context.beginPath(); context.arc(particle.x, particle.y, particle.radius, 0, Math.PI * 2); context.fill();
    }
    function tick(now) {
      frame = null;
      if (!tracking || !latest) return;
      const elapsed = Math.min(previousTime === null ? 16.67 : now - previousTime, 40);
      previousTime = now;
      if (now >= nextEmission && particles.length < 6) {
        spawn(now);
        nextEmission = now + (now - lastMove < 750 ? 300 + Math.random() * 250 : 650 + Math.random() * 350);
      }
      context.clearRect(0, 0, target.innerWidth, target.innerHeight);
      canvas.hidden = false;
      particles = particles.filter(particle => {
        const dx = latest.x - particle.x, dy = latest.y - particle.y;
        const distance = Math.hypot(dx, dy);
        if (distance < 10 || now >= particle.expires) return false;
        const turn = follow(elapsed, 190);
        particle.vx += (dx / distance * particle.speed - particle.vx) * turn;
        particle.vy += (dy / distance * particle.speed - particle.vy) * turn;
        particle.x += particle.vx * elapsed; particle.y += particle.vy * elapsed;
        draw(particle, now, Math.hypot(latest.x - particle.x, latest.y - particle.y));
        return true;
      });
      frame = target.requestAnimationFrame(tick);
    }
    function move(event) {
      if (document.hidden || event.pointerType === 'touch') return;
      latest = { x: event.clientX, y: event.clientY };
      lastMove = target.performance.now();
      if (frame === null) frame = target.requestAnimationFrame(tick);
    }
    function sync() {
      const enabled = fine.matches && !reduced.matches && !document.hidden;
      if (enabled === tracking) return;
      tracking = enabled;
      if (enabled) document.addEventListener('pointermove', move, { passive: true });
      else { document.removeEventListener('pointermove', move); hide(); }
    }
    function destroy() {
      tracking = false;
      document.removeEventListener('pointermove', move);
      document.removeEventListener('pointerleave', hide);
      document.removeEventListener('visibilitychange', sync);
      target.removeEventListener('resize', resize);
      fine.removeEventListener('change', sync);
      reduced.removeEventListener('change', sync);
      observer.disconnect();
      hide();
    }
    const observer = new target.MutationObserver(palette);
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
    document.addEventListener('pointerleave', hide);
    document.addEventListener('visibilitychange', sync);
    target.addEventListener('resize', resize, { passive: true });
    fine.addEventListener('change', sync);
    reduced.addEventListener('change', sync);
    palette(); resize(); sync();
    return { destroy };
  }
  host.FleetAuthEffects = { create };
  host.addEventListener?.('DOMContentLoaded', () => {
    const effect = create(host);
    host.addEventListener?.('pagehide', () => effect.destroy(), { once: true });
  });
})(globalThis);
