(() => {
  'use strict';
  const stage = document.querySelector('.lab-stage');
  const canvas = document.querySelector('#effect-canvas');
  const context = canvas.getContext('2d');
  const choices = [...document.querySelectorAll('[data-demo]')];
  const autoButton = document.querySelector('#auto-preview');
  const motionButton = document.querySelector('#motion-toggle');
  const status = document.querySelector('#motion-status');
  const fine = matchMedia('(hover: hover) and (pointer: fine)');
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const descriptions = [
    '柔光追随：一束宽而柔的钢蓝光，轻轻落后于鼠标。最安静的选择。',
    '磁性点阵：鼠标拨开点阵，附近的点微微亮起，再归位。推荐先试这个。',
    '精密光环：小型双层光环，有轻微弹性；没有坐标，也不替换系统鼠标。',
    '金属掠光：细密的银蓝曲线随鼠标起伏，像光掠过钛金属表面。',
    '弹性丝带：移动时拉出一条细丝，转向有弹性，停下后自然消散。'
  ];
  const parameters = new URLSearchParams(location.search);
  let mode = Math.min(5, Math.max(1, parseInt(parameters.get('demo'), 10) || 5));
  let width = 0, height = 0;
  let enabled = true, auto = false, frame = null, lastFrame = 0, autoStart = 0;
  let inside = false;
  let lastMove = 0, alpha = 0, motionX = 0, motionY = 0;
  let target = { x: 0, y: 0 }, cursor = { x: 0, y: 0 };
  let ribbon = [];
  let dark = parameters.get('theme') === 'dark';
  let ink = [44, 93, 135], silver = [88, 130, 164];
  const rgba = (color, opacity) => `rgba(${color.join(',')},${opacity})`;
  const clamp = (value, low, high) => Math.max(low, Math.min(high, value));
  const follow = (elapsed, duration) => 1 - Math.exp(-elapsed / duration);

  function updateURL() {
    const query = new URLSearchParams({ demo: String(mode), theme: dark ? 'dark' : 'light' });
    history.replaceState(null, '', `${location.pathname}?${query}`);
  }

  function theme(value) {
    dark = value === 'dark';
    document.documentElement.dataset.theme = value;
    document.querySelectorAll('[data-theme-choice]').forEach(button => {
      button.setAttribute('aria-pressed', String(button.dataset.themeChoice === value));
    });
    ink = dark ? [184, 217, 255] : [44, 93, 135];
    silver = dark ? [124, 161, 194] : [88, 130, 164];
    updateURL();
    draw(performance.now());
  }

  function resize() {
    const bounds = stage.getBoundingClientRect();
    width = bounds.width; height = bounds.height;
    const ratio = Math.min(devicePixelRatio || 1, 2);
    canvas.width = Math.round(width * ratio); canvas.height = Math.round(height * ratio);
    context.setTransform(ratio, 0, 0, ratio, 0, 0);
    if (!inside && !auto) park();
    draw(performance.now());
  }

  function park() {
    target = { x: width * .77, y: height * .54 };
    cursor = { ...target };
    ribbon = Array.from({ length: 26 }, () => ({ ...cursor }));
  }

  function select(value) {
    mode = value;
    choices.forEach(button => {
      const selected = Number(button.dataset.demo) === mode;
      button.setAttribute('aria-selected', String(selected));
      button.tabIndex = selected ? 0 : -1;
    });
    stage.setAttribute('aria-labelledby', `choice-${mode}`);
    document.querySelector('#effect-description').textContent = descriptions[mode - 1];
    ribbon = Array.from({ length: 26 }, () => ({ ...cursor }));
    updateURL();
    draw(performance.now());
    if (auto || inside) wake();
  }

  function trackAllowed() { return enabled && !reduced.matches && !document.hidden; }

  function syncStatus() {
    autoButton.disabled = !trackAllowed();
    motionButton.setAttribute('aria-pressed', String(enabled));
    motionButton.textContent = enabled ? '效果已开启' : '效果已关闭';
    autoButton.setAttribute('aria-pressed', String(auto));
    autoButton.textContent = auto ? '停止演示' : '自动演示';
    status.textContent = reduced.matches ? '已遵循“减少动态效果”，仅显示静态背景。'
      : !enabled ? '效果已关闭，可以比较没有装饰时的页面。'
      : auto ? '自动演示中。移动鼠标即可接管。'
      : !fine.matches ? '触屏不跟随。可点“自动演示”比较五种效果。'
      : '在上方空白处移动鼠标，比较跟随手感。';
  }

  function stopAuto() { auto = false; syncStatus(); }
  function cancel() { if (frame !== null) cancelAnimationFrame(frame); frame = null; lastFrame = 0; }
  function wake() { if (frame === null && trackAllowed()) frame = requestAnimationFrame(tick); }

  function tick(now) {
    frame = null;
    if (!trackAllowed()) return;
    const elapsed = Math.min(lastFrame ? now - lastFrame : 16.67, 40);
    lastFrame = now;
    if (auto) {
      const t = (now - autoStart) / 1000;
      target.x = width * (.77 + .14 * Math.sin(t * 1.15));
      target.y = height * (.52 + .27 * Math.sin(t * 1.65 + .5));
      lastMove = now;
    }
    const durations = [130, 85, 55, 220, 45];
    const step = follow(elapsed, durations[mode - 1]);
    const previous = { ...cursor };
    cursor.x += (target.x - cursor.x) * step;
    cursor.y += (target.y - cursor.y) * step;
    motionX = (cursor.x - previous.x) / elapsed;
    motionY = (cursor.y - previous.y) / elapsed;
    const idle = now - lastMove;
    const desiredAlpha = (auto || inside) ? (mode === 5 ? clamp(1 - (idle - 160) / 750, 0, 1) : 1) : 0;
    alpha += (desiredAlpha - alpha) * follow(elapsed, 120);
    ribbon[0] = { ...cursor };
    for (let index = 1; index < ribbon.length; index++) {
      const amount = follow(elapsed, 32 + index * 1.6);
      ribbon[index].x += (ribbon[index - 1].x - ribbon[index].x) * amount;
      ribbon[index].y += (ribbon[index - 1].y - ribbon[index].y) * amount;
    }
    draw(now);
    const distance = Math.hypot(target.x - cursor.x, target.y - cursor.y);
    if (auto || distance > .1 || Math.abs(alpha - desiredAlpha) > .002 || (mode === 5 && idle < 1300)) wake();
    else lastFrame = 0;
  }

  function dot(x, y, radius, color, opacity) {
    context.fillStyle = rgba(color, opacity);
    context.beginPath(); context.arc(x, y, radius, 0, Math.PI * 2); context.fill();
  }

  function glow(x, y, radius, strength) {
    const gradient = context.createRadialGradient(x, y, 0, x, y, radius);
    gradient.addColorStop(0, rgba(ink, strength));
    gradient.addColorStop(.3, rgba(silver, strength * .65));
    gradient.addColorStop(1, rgba(silver, 0));
    context.fillStyle = gradient; context.fillRect(x - radius, y - radius, radius * 2, radius * 2);
  }

  function softLight() {
    const amount = .28 + alpha * .72;
    glow(cursor.x, cursor.y, 340, (dark ? .15 : .13) * amount);
    glow(cursor.x - 38, cursor.y - 32, 158, (dark ? .08 : .065) * amount);
    context.save(); context.translate(cursor.x, cursor.y); context.rotate(-.52);
    context.scale(1.5, .62); glow(0, 0, 140, .08 * amount); context.restore();
  }

  function magneticDots() {
    const pitch = 32, radius = 178;
    for (let y = 20; y < height; y += pitch) {
      for (let x = 20; x < width; x += pitch) {
        const dx = x - cursor.x, dy = y - cursor.y, distance = Math.hypot(dx, dy);
        const influence = Math.pow(clamp(1 - distance / radius, 0, 1), 2) * alpha;
        const displacement = influence * 28;
        dot(x + dx / Math.max(distance, 1) * displacement, y + dy / Math.max(distance, 1) * displacement,
          1 + influence * 1.3, ink, (dark ? .11 : .085) + influence * .5);
      }
    }
    if (alpha > .01) {
      glow(cursor.x, cursor.y, 136, .06 * alpha);
      context.strokeStyle = rgba(ink, .18 * alpha); context.lineWidth = .8;
      context.beginPath(); context.arc(cursor.x, cursor.y, 32, 0, Math.PI * 2); context.stroke();
    }
  }

  function preciseRing() {
    context.strokeStyle = rgba(ink, dark ? .025 : .018); context.lineWidth = .5;
    context.beginPath();
    for (let x = 0; x < width; x += 64) { context.moveTo(x, 0); context.lineTo(x, height); }
    for (let y = 0; y < height; y += 64) { context.moveTo(0, y); context.lineTo(width, y); }
    context.stroke();
    if (alpha < .005) return;
    const speed = Math.min(Math.hypot(motionX, motionY), 1.5);
    context.save(); context.translate(cursor.x, cursor.y); context.rotate(Math.atan2(motionY, motionX));
    context.scale(1 + speed * .16, 1 - speed * .08);
    context.lineWidth = 1; context.strokeStyle = rgba(ink, .5 * alpha);
    context.beginPath(); context.arc(0, 0, 18, 0, Math.PI * 2); context.stroke();
    context.strokeStyle = rgba(ink, .3 * alpha);
    for (let side = 0; side < 2; side++) {
      context.beginPath(); context.arc(0, 0, 27, side * Math.PI - .45, side * Math.PI + .45); context.stroke();
    }
    dot(0, 0, 1.6, ink, .7 * alpha); context.restore();
  }

  function metallicLight() {
    const amount = .25 + alpha * .75;
    glow(cursor.x, cursor.y, 230, .065 * amount);
    for (let line = -14; line <= 14; line++) {
      const offset = line * 12;
      const gradient = context.createLinearGradient(0, 0, width, 0);
      gradient.addColorStop(0, rgba(silver, 0));
      gradient.addColorStop(.25, rgba(silver, dark ? .07 : .045));
      gradient.addColorStop(clamp(cursor.x / width, .3, .85), rgba(ink, (dark ? .32 : .24) * amount));
      gradient.addColorStop(1, rgba(silver, 0));
      context.strokeStyle = gradient; context.lineWidth = line % 4 === 0 ? 1.15 : .65;
      context.beginPath();
      for (let x = -20; x <= width + 20; x += 12) {
        const envelope = Math.exp(-Math.pow((x - cursor.x) / 235, 2));
        const arch = -Math.cos(offset / 180) * 42 * envelope * amount;
        const response = (cursor.y - height * .57) * envelope * .6 * amount;
        const y = height * .57 + offset + Math.sin(x / 240 + line * .1) * 14 + arch + response;
        if (x === -20) context.moveTo(x, y); else context.lineTo(x, y);
      }
      context.stroke();
    }
  }

  function elasticRibbon() {
    if (alpha < .005) return;
    const speed = clamp(Math.hypot(motionX, motionY), 0, 2);
    for (let strand = -1; strand <= 1; strand++) {
      for (let index = 1; index < ribbon.length - 1; index++) {
        const previous = ribbon[index - 1], point = ribbon[index], next = ribbon[index + 1];
        const dx = next.x - previous.x, dy = next.y - previous.y, distance = Math.max(Math.hypot(dx, dy), 1);
        const normalX = -dy / distance * strand * (2.2 + speed), normalY = dx / distance * strand * (2.2 + speed);
        const taper = 1 - index / ribbon.length;
        context.strokeStyle = rgba(strand === 0 ? ink : silver, alpha * taper * (strand === 0 ? .56 : .25));
        context.lineWidth = strand === 0 ? 1.1 + taper * speed * 1.4 : .8;
        context.lineCap = 'round';
        context.beginPath();
        context.moveTo((previous.x + point.x) / 2 + normalX, (previous.y + point.y) / 2 + normalY);
        context.quadraticCurveTo(point.x + normalX, point.y + normalY, (point.x + next.x) / 2 + normalX, (point.y + next.y) / 2 + normalY);
        context.stroke();
      }
    }
    glow(cursor.x, cursor.y, 38, .06 * alpha);
  }

  function draw() {
    context.clearRect(0, 0, width, height);
    if (!enabled || !width) return;
    [softLight, magneticDots, preciseRing, metallicLight, elasticRibbon][mode - 1]();
  }

  stage.addEventListener('pointermove', event => {
    if (!fine.matches || event.pointerType === 'touch' || !trackAllowed()) return;
    if (auto) stopAuto();
    const box = stage.getBoundingClientRect();
    target = { x: event.clientX - box.left, y: event.clientY - box.top };
    inside = true;
    lastMove = performance.now(); wake();
  }, { passive: true });
  stage.addEventListener('pointerleave', () => { inside = false; wake(); });
  choices.forEach(button => {
    button.addEventListener('click', () => select(Number(button.dataset.demo)));
    button.addEventListener('keydown', event => {
      const keys = { ArrowRight: 1, ArrowLeft: -1, Home: 'first', End: 'last' };
      if (!(event.key in keys)) return;
      event.preventDefault();
      const next = keys[event.key] === 'first' ? 1 : keys[event.key] === 'last' ? 5 : (mode - 1 + keys[event.key] + 5) % 5 + 1;
      select(next); choices[next - 1].focus();
    });
  });
  document.querySelectorAll('[data-theme-choice]').forEach(button => button.addEventListener('click', () => theme(button.dataset.themeChoice)));
  autoButton.addEventListener('click', () => {
    auto = !auto;
    if (auto) { autoStart = performance.now(); }
    syncStatus(); wake();
  });
  motionButton.addEventListener('click', () => {
    enabled = !enabled;
    if (!enabled) { auto = false; alpha = 0; cancel(); }
    syncStatus(); draw();
  });
  const feedback = () => {
    document.querySelector('#form-feedback').textContent = '这里只预览动效；登录、注册和恢复账户请回到服务网页。';
    resize();
  };
  document.querySelector('#preview-form').addEventListener('submit', event => { event.preventDefault(); feedback(); });
  document.querySelectorAll('[data-preview-action]').forEach(button => button.addEventListener('click', feedback));
  function preferences() {
    auto = false; inside = false; alpha = 0; cancel(); park(); syncStatus(); draw();
  }
  reduced.addEventListener('change', preferences);
  fine.addEventListener('change', preferences);
  document.addEventListener('visibilitychange', () => {
    if (document.hidden) { auto = false; inside = false; alpha = 0; cancel(); }
    else { park(); draw(); }
    syncStatus();
  });
  const observer = new ResizeObserver(resize);
  observer.observe(stage);
  addEventListener('pagehide', () => { cancel(); observer.disconnect(); }, { once: true });
  resize(); theme(dark ? 'dark' : 'light'); select(mode); syncStatus();
})();
