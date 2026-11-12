(() => {
  'use strict';

  // ---------------------------------------------------------------- helpers

  const $ = (id) => document.getElementById(id);
  const FA = '۰۱۲۳۴۵۶۷۸۹';
  const fa = (v) => String(v).replace(/[0-9]/g, (d) => FA[+d]);
  const faNum = (v, digits = 0) => fa(Number(v).toFixed(digits));
  const clamp = (v, a, b) => Math.min(b, Math.max(a, v));
  const lerp = (a, b, t) => a + (b - a) * t;
  const pad2 = (n) => String(n).padStart(2, '0');
  const clock = (d) => fa(`${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`);
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));

  // ------------------------------------------------------------------ state

  const S = {
    city: null,
    cfg: null,
    world: null,
    drivers: new Map(),
    pickup: null,
    dropoff: null,
    active: 'pickup',
    requestId: null,
    offer: null,
    trip: null,
    candidates: [],
    candidatesAt: 0,
    rings: [],
    logs: [],
    evtCount: 0,
    settings: { autoAccept: true, autoDemand: false },
    lastStatsAt: 0,
    adopting: false,
  };

  // ------------------------------------------------------------- projection

  function metersFromLatLon(lat, lon) {
    const kx = 111320 * Math.cos((S.city.center.lat * Math.PI) / 180);
    const ky = 110540;
    return {
      x: (lon - S.city.center.lon) * kx,
      y: (S.city.center.lat - lat) * ky,
    };
  }

  function latLonFromMeters(x, y) {
    const kx = 111320 * Math.cos((S.city.center.lat * Math.PI) / 180);
    const ky = 110540;
    return {
      lat: S.city.center.lat - y / ky,
      lon: S.city.center.lon + x / kx,
    };
  }

  function makePoint(lat, lon) {
    const m = metersFromLatLon(lat, lon);
    return { lat, lon, x: m.x, y: m.y };
  }

  function pointAt(x, y) {
    const ll = latLonFromMeters(x, y);
    return makePoint(ll.lat, ll.lon);
  }

  function distance(a, b) {
    return Math.hypot(a.x - b.x, a.y - b.y);
  }

  // ----------------------------------------------------------------- canvas

  const canvas = $('map');
  const ctx = canvas.getContext('2d');
  const baseCanvas = document.createElement('canvas');
  const bctx = baseCanvas.getContext('2d');

  let DPR = 1;
  let W = 0;
  let H = 0;
  let baseDirty = true;

  const V = { zoom: 1, panX: 0, panY: 0, scale: 0.05, ox: 0, oy: 0 };

  const sx = (x) => V.ox + x * V.scale;
  const sy = (y) => V.oy + y * V.scale;
  const wx = (px) => (px - V.ox) / V.scale;
  const wy = (py) => (py - V.oy) / V.scale;

  function layout() {
    if (!S.world) return;
    const pad = 28;
    const fit = Math.min((W - pad * 2) / S.world.w, (H - pad * 2) / S.world.h);
    V.scale = fit * V.zoom;
    V.ox = W / 2 + V.panX - S.world.cx * V.scale;
    V.oy = H / 2 + V.panY - S.world.cy * V.scale;
    updateScale();
  }

  function resize() {
    const rect = canvas.parentElement.getBoundingClientRect();
    DPR = Math.min(window.devicePixelRatio || 1, 2);
    W = Math.max(1, Math.floor(rect.width));
    H = Math.max(1, Math.floor(rect.height));
    for (const c of [canvas, baseCanvas]) {
      c.width = Math.floor(W * DPR);
      c.height = Math.floor(H * DPR);
    }
    layout();
    baseDirty = true;
  }

  function roundRect(c, x, y, w, h, r) {
    const rr = Math.min(r, w / 2, h / 2);
    c.beginPath();
    c.moveTo(x + rr, y);
    c.arcTo(x + w, y, x + w, y + h, rr);
    c.arcTo(x + w, y + h, x, y + h, rr);
    c.arcTo(x, y + h, x, y, rr);
    c.arcTo(x, y, x + w, y, rr);
    c.closePath();
  }

  // -------------------------------------------------------------- basemap

  function drawBase() {
    if (!baseDirty || !S.world) return;

    const c = bctx;
    c.setTransform(1, 0, 0, 1, 0, 0);
    c.scale(DPR, DPR);
    c.clearRect(0, 0, W, H);

    c.fillStyle = '#e6e6df';
    c.fillRect(0, 0, W, H);

    const x0 = wx(-30);
    const x1 = wx(W + 30);
    const y0 = wy(-30);
    const y1 = wy(H + 30);

    const minorStep = 340;
    const majorStep = 1020;
    const minorW = clamp(V.scale * 26, 0.8, 2.6);
    const majorW = clamp(V.scale * 105, 2.6, 9);

    c.lineCap = 'butt';

    // Deterministic per-street offsets: the city looks organic instead of a
    // perfect lattice, but the same block is drawn identically on every frame.
    const jitterX = (i) => Math.abs((Math.sin(i * 12.9898) * 43758.5453) % 1) * 46;
    const jitterY = (j) => Math.abs((Math.sin(j * 78.233) * 43758.5453) % 1) * 46;

    // minor streets
    c.strokeStyle = '#f4f4ee';
    c.lineWidth = minorW;
    for (let i = Math.floor(x0 / minorStep) - 1; i <= Math.ceil(x1 / minorStep) + 1; i++) {
      const X = sx(i * minorStep + jitterX(i));
      c.beginPath();
      c.moveTo(X, 0);
      c.lineTo(X, H);
      c.stroke();
    }
    for (let j = Math.floor(y0 / minorStep) - 1; j <= Math.ceil(y1 / minorStep) + 1; j++) {
      const Y = sy(j * minorStep + jitterY(j));
      c.beginPath();
      c.moveTo(0, Y);
      c.lineTo(W, Y);
      c.stroke();
    }

    // City blocks: a slightly darker mass inset from every minor street. This
    // is what turns a wireframe into something that reads as a map.
    const blockPx = minorStep * V.scale;
    if (blockPx > 6.5) {
      c.fillStyle = '#e0e0d8';
      const inset = clamp(blockPx * 0.11, 1.2, 3.4);
      for (let i = Math.floor(x0 / minorStep) - 1; i <= Math.ceil(x1 / minorStep) + 1; i++) {
        const xa = sx(i * minorStep + jitterX(i)) + inset;
        const xb = sx((i + 1) * minorStep + jitterX(i + 1)) - inset;
        if (xb - xa < 4) continue;
        for (let j = Math.floor(y0 / minorStep) - 1; j <= Math.ceil(y1 / minorStep) + 1; j++) {
          const ya = sy(j * minorStep + jitterY(j)) + inset;
          const yb = sy((j + 1) * minorStep + jitterY(j + 1)) - inset;
          if (yb - ya < 4) continue;
          roundRect(c, xa, ya, xb - xa, yb - ya, 1.5);
          c.fill();
        }
      }
    }

    // major avenues
    c.strokeStyle = '#fbfbf7';
    c.lineWidth = majorW;
    for (let i = Math.floor(x0 / majorStep) - 1; i <= Math.ceil(x1 / majorStep) + 1; i++) {
      const jitter = Math.abs((Math.sin(i * 45.331) * 43758.5453) % 1) * 90;
      const X = sx(i * majorStep + jitter);
      c.beginPath();
      c.moveTo(X, 0);
      c.lineTo(X, H);
      c.stroke();
    }
    for (let j = Math.floor(y0 / majorStep) - 1; j <= Math.ceil(y1 / majorStep) + 1; j++) {
      const jitter = Math.abs((Math.sin(j * 91.71) * 43758.5453) % 1) * 90;
      const Y = sy(j * majorStep + jitter);
      c.beginPath();
      c.moveTo(0, Y);
      c.lineTo(W, Y);
      c.stroke();
    }

    // two diagonal highways
    c.strokeStyle = '#fcfcf8';
    c.lineWidth = majorW * 1.35;
    c.beginPath();
    c.moveTo(sx(-16000), sy(9200));
    c.lineTo(sx(16000), sy(-7800));
    c.stroke();
    c.lineWidth = majorW * 1.1;
    c.beginPath();
    c.moveTo(sx(-15000), sy(-4600));
    c.lineTo(sx(15000), sy(6400));
    c.stroke();

    // parks
    const parks = [
      { x: -3350, y: -2950, w: 2000, h: 1500 },
      { x: -900, y: 3050, w: 1250, h: 950 },
      { x: 3050, y: 1500, w: 900, h: 700 },
    ];
    for (const p of parks) {
      const X = sx(p.x);
      const Y = sy(p.y);
      const w = p.w * V.scale;
      const h = p.h * V.scale;
      c.fillStyle = '#dbe5d1';
      roundRect(c, X, Y, w, h, Math.min(18, w * 0.08));
      c.fill();

      c.fillStyle = '#cfdcc1';
      for (let k = 0; k < 22; k++) {
        const rx = X + 8 + Math.abs(Math.sin(k * 3.7 + p.x) * 0.8) * Math.max(1, w - 16);
        const ry = Y + 8 + Math.abs(Math.cos(k * 5.1 + p.y) * 0.8) * Math.max(1, h - 16);
        c.beginPath();
        c.arc(rx, ry, Math.max(1.5, 2.6 * Math.min(1.4, V.scale * 22)), 0, Math.PI * 2);
        c.fill();
      }
    }

    // lake
    const lakeX = sx(-2500);
    const lakeY = sy(-2350);
    const lakeW = 620 * V.scale;
    const lakeH = 420 * V.scale;
    if (lakeW > 4) {
      c.fillStyle = '#cbd8da';
      c.beginPath();
      c.ellipse(lakeX, lakeY, lakeW / 2, lakeH / 2, -0.2, 0, Math.PI * 2);
      c.fill();
      c.strokeStyle = '#bccbcd';
      c.lineWidth = 1;
      c.stroke();
    }

    // labels
    c.textAlign = 'center';
    c.textBaseline = 'middle';

    const drawn = (text, x, y, size, color, opacity) => {
      c.font = `${size}px "Segoe UI", Tahoma, sans-serif`;
      c.lineWidth = 3;
      c.strokeStyle = `rgba(255,255,255,${opacity})`;
      c.fillStyle = color;
      c.strokeText(text, sx(x), sy(y));
      c.fillText(text, sx(x), sy(y));
    };

    if (V.scale > 0.028) {
      c.globalAlpha = 0.9;
      drawn('منطقه ۶', 1400, -1250, 11, '#9a9a92', 0.55);
      drawn('منطقه ۷', -2300, 1900, 11, '#9a9a92', 0.55);
      drawn('منطقه ۳', 3200, 2700, 11, '#9a9a92', 0.55);
      drawn('منطقه ۱۰', -3900, -3100, 11, '#9a9a92', 0.55);
      c.globalAlpha = 1;
    }

    if (V.scale > 0.045) {
      c.save();
      c.translate(sx(-1130), sy(600));
      c.rotate(-Math.PI / 2);
      c.font = '10px "Segoe UI", Tahoma, sans-serif';
      c.fillStyle = '#a3a39b';
      c.textAlign = 'center';
      c.fillText('خیابان ولیعصر', 0, 0);
      c.restore();

      drawn('بلوار کشاورز', -600, 1180, 10, '#a3a39b', 0.6);
      drawn('خیابان انقلاب', 500, -2260, 10, '#a3a39b', 0.6);
      drawn('اتوبان مدرس', 2350, 0, 10, '#a6a69e', 0.55);
    }

    baseDirty = false;
  }

  // ---------------------------------------------------------------- layers

  function strokePath(points, color, width, dash) {
    if (points.length < 2) return;
    ctx.save();
    ctx.lineJoin = 'round';
    ctx.lineCap = 'round';
    ctx.setLineDash(dash || []);
    ctx.beginPath();
    points.forEach((p, i) => {
      const X = sx(p.x);
      const Y = sy(p.y);
      if (i === 0) ctx.moveTo(X, Y);
      else ctx.lineTo(X, Y);
    });
    ctx.strokeStyle = color;
    ctx.lineWidth = width;
    ctx.stroke();
    ctx.restore();
  }

  function elbow(a, b) {
    if (Math.abs(b.x - a.x) >= Math.abs(b.y - a.y)) return { x: b.x, y: a.y };
    return { x: a.x, y: b.y };
  }

  function orthoRoute(a, b) {
    return [a, elbow(a, b), b];
  }

  function drawRings(now) {
    // On a phone the search animation competes with the cars for space and
    // quickly turns into a green disc; the map is clearer without it.
    if (!S.rings.length || W < 520) return;
    const cell = (S.cfg && S.cfg.cellMeters) || 450;
    S.rings = S.rings.filter((r) => now - r.at < 900);
    const last = S.rings[S.rings.length - 1];
    if (!last) return;

    // A single faint radar sweep is easier to read than a dozen nested circles.
    const age = clamp((now - last.at) / 900, 0, 1);
    const cx = sx(last.center.x);
    const cy = sy(last.center.y);
    const radius = Math.max(5, (last.ring + age) * cell * V.scale);
    ctx.beginPath();
    ctx.arc(cx, cy, radius, 0, Math.PI * 2);
    ctx.strokeStyle = `rgba(19,107,76,${0.22 * (1 - age)})`;
    ctx.lineWidth = 1.1;
    ctx.stroke();
  }

  function drawCandidates(now) {
    if (!S.candidates.length || !S.requestId) return;
    const age = now - S.candidatesAt;
    if (age > 12000) return;
    const alpha = clamp(1 - age / 12000, 0.15, 0.85);
    const top = S.candidates.slice(0, 4);

    top.forEach((cand, idx) => {
      const d = S.drivers.get(cand.driverId);
      if (!d) return;
      const X = sx(d.rx);
      const Y = sy(d.ry);
      const chosen = S.offer && S.offer.driverId === cand.driverId;

      ctx.beginPath();
      ctx.arc(X, Y, chosen ? 15 : 12, 0, Math.PI * 2);
      ctx.strokeStyle = chosen ? `rgba(19,107,76,${alpha})` : `rgba(19,107,76,${alpha * 0.45})`;
      ctx.lineWidth = chosen ? 2 : 1.2;
      ctx.setLineDash([3, 3]);
      ctx.stroke();
      ctx.setLineDash([]);

      if (!chosen) {
        ctx.font = '9px "JetBrains Mono", Consolas, monospace';
        ctx.fillStyle = `rgba(19,107,76,${alpha * 0.85})`;
        ctx.textAlign = 'center';
        ctx.fillText(fa(idx + 1), X, Y - 17);
      } else {
        ctx.beginPath();
        ctx.arc(X, Y, 15, 0, Math.PI * 2);
        ctx.fillStyle = 'rgba(19,107,76,0.08)';
        ctx.fill();
      }
    });
  }

  function drawCar(d) {
    const X = sx(d.rx);
    const Y = sy(d.ry);
    if (X < -24 || Y < -24 || X > W + 24 || Y > H + 24) return;

    const colors = {
      available: '#2f7a5b',
      reserved: '#b4761f',
      on_trip: '#49565f',
    };
    const color = colors[d.state] || '#49565f';

    ctx.save();
    ctx.translate(X, Y);

    if (d.state === 'reserved') {
      ctx.beginPath();
      ctx.arc(0, 0, 10, 0, Math.PI * 2);
      ctx.fillStyle = 'rgba(169,112,31,0.14)';
      ctx.fill();
    }

    ctx.rotate(((d.heading || 0) * Math.PI) / 180);
    const carLen = clamp(V.scale * 250, 8, 16);
    const carW = carLen * 0.5;
    roundRect(ctx, -carW / 2, -carLen / 2, carW, carLen, carLen * 0.18);
    ctx.fillStyle = color;
    ctx.fill();
    ctx.strokeStyle = 'rgba(255,255,255,0.92)';
    ctx.lineWidth = Math.max(0.9, carLen * 0.09);
    ctx.stroke();

    ctx.fillStyle = 'rgba(255,255,255,0.55)';
    roundRect(ctx, -carW * 0.3, -carLen * 0.34, carW * 0.6, carLen * 0.28, 1);
    ctx.fill();

    ctx.restore();
  }

  function drawPin(p, kind, label, now) {
    const X = sx(p.x);
    const Y = sy(p.y);

    if (kind === 'pickup') {
      const pulse = 10 + ((now / 22) % 24);
      ctx.beginPath();
      ctx.arc(X, Y, pulse, 0, Math.PI * 2);
      ctx.fillStyle = 'rgba(19,107,76,0.055)';
      ctx.fill();

      ctx.beginPath();
      ctx.arc(X, Y, 6.5, 0, Math.PI * 2);
      ctx.fillStyle = '#ffffff';
      ctx.fill();
      ctx.lineWidth = 3;
      ctx.strokeStyle = '#136b4c';
      ctx.stroke();
    } else {
      ctx.save();
      ctx.translate(X, Y);
      ctx.rotate(Math.PI / 4);
      roundRect(ctx, -5.6, -5.6, 11.2, 11.2, 2);
      ctx.fillStyle = '#15171a';
      ctx.fill();
      ctx.strokeStyle = '#fff';
      ctx.lineWidth = 1.6;
      ctx.stroke();
      ctx.restore();
    }

    // label chip
    ctx.font = '9.5px "Segoe UI", Tahoma, sans-serif';
    const text = label;
    const tw = ctx.measureText(text).width;
    const bx = X - tw / 2 - 5;
    const by = Y - 30;
    ctx.fillStyle = 'rgba(255,255,255,0.94)';
    roundRect(ctx, bx, by, tw + 10, 16, 4);
    ctx.fill();
    ctx.strokeStyle = 'rgba(21,23,26,0.10)';
    ctx.lineWidth = 1;
    ctx.stroke();
    ctx.fillStyle = '#3a3f45';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(text, X, by + 8.5);
  }

  function draw(now) {
    drawBase();

    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, W, H);
    ctx.drawImage(baseCanvas, 0, 0, W, H);
    ctx.setTransform(DPR, 0, 0, DPR, 0, 0);

    // active trip route
    if (S.trip) {
      const car = S.drivers.get(S.trip.driverId);
      if (S.trip.phase === 'to_pickup' && car) {
        const approach = orthoRoute({ x: car.rx, y: car.ry }, S.trip._pickup);
        strokePath(approach, 'rgba(19,107,76,0.18)', 5);
        strokePath(approach, '#136b4c', 1.8, [5, 5]);
      }
      const pts = orthoRoute(S.trip._pickup, S.trip._dropoff);
      strokePath(pts, 'rgba(255,255,255,0.92)', 7.5);
      strokePath(pts, '#22262b', 3);
    } else if (S.dropoff && S.pickup) {
      // preview while selecting
      const pts = orthoRoute(S.pickup, S.dropoff);
      strokePath(pts, 'rgba(255,255,255,0.8)', 6);
      strokePath(pts, 'rgba(34,38,43,0.38)', 2);
    }

    // offer approach line
    if (S.offer && !S.trip) {
      const d = S.drivers.get(S.offer.driverId);
      if (d && S.pickup) {
        const pts = orthoRoute({ x: d.rx, y: d.ry }, S.pickup);
        strokePath(pts, 'rgba(19,107,76,0.22)', 5.5);
        strokePath(pts, '#136b4c', 2, [6, 5]);
      }
    }

    drawRings(now);
    drawCandidates(now);

    const inView = [];
    for (const d of S.drivers.values()) inView.push(d);
    for (const d of inView) drawCar(d);

    if (S.pickup) drawPin(S.pickup, 'pickup', 'مبدا', now);
    if (S.dropoff) drawPin(S.dropoff, 'dropoff', 'مقصد', now);
  }

  // -------------------------------------------------------------- scale bar

  function updateScale() {
    const el = $('scaleLabel');
    const bar = document.querySelector('.scalebar i');
    if (!el || !bar || !V.scale) return;
    const target = 90 / V.scale;
    const steps = [100, 200, 500, 1000, 2000, 3000, 5000, 10000, 20000];
    const nice = steps.find((v) => v >= target) || 20000;
    bar.style.width = `${Math.round(nice * V.scale)}px`;
    el.textContent = nice >= 1000 ? `${fa(nice / 1000)} کیلومتر` : `${fa(nice)} متر`;
  }

  // ---------------------------------------------------------------- logging

  function log(tag, text, kind = '') {
    S.logs.unshift({ t: new Date(), tag, text, kind });
    if (S.logs.length > 7) S.logs.pop();
    S.evtCount++;
    renderLog();
  }

  function renderLog() {
    const el = $('log');
    el.innerHTML = S.logs.map((l) => `
      <li>
        <time>${clock(l.t)}</time>
        <span class="tag ${l.kind}">${esc(l.tag)}</span>
        <p title="${esc(l.text)}">${esc(l.text)}</p>
      </li>`).join('');
    $('eventMeta').textContent = `${fa(S.evtCount)} evt`;
  }

  function notice(msg) {
    const el = $('notice');
    if (!msg) {
      el.hidden = true;
      el.textContent = '';
      return;
    }
    el.hidden = false;
    el.textContent = msg;
  }

  function badge(text, kind = '') {
    const el = $('panelBadge');
    el.textContent = text;
    el.className = `badge ${kind}`.trim();
  }

  // ------------------------------------------------------------- rendering

  function setFieldValue(which, point) {
    const el = $(which === 'pickup' ? 'pickupLabel' : 'dropoffLabel');
    if (!point) {
      el.textContent = 'روی نقشه انتخاب کنید';
      el.classList.add('empty');
      return;
    }
    el.classList.remove('empty');
    const lat = point.lat.toFixed(4);
    const lon = point.lon.toFixed(4);
    el.textContent = `${lat} , ${lon}`;
    el.style.direction = 'ltr';
    el.style.textAlign = 'right';
  }

  function setActive(which) {
    S.active = which;
    $('fieldPickup').classList.toggle('active', which === 'pickup');
    $('fieldDropoff').classList.toggle('active', which === 'dropoff');
    document.querySelectorAll('.map-modes .chip').forEach((b) => {
      b.classList.toggle('active', b.dataset.mode === which);
    });
  }

  function updateRequestButton() {
    const busy = !!S.requestId || !!S.offer || !!S.trip;
    $('requestBtn').disabled = !(S.pickup && S.dropoff) || busy;
  }

  function renderCandidates(list, chosenId) {
    const el = $('candidates');
    el.innerHTML = '';
    list.slice(0, 5).forEach((c, i) => {
      const li = document.createElement('li');
      if (chosenId && c.driverId === chosenId) li.className = 'chosen';
      const etaMin = c.etaSec / 60;
      li.innerHTML = `
        <span class="rank">${fa(i + 1)}</span>
        <span>
          <span class="c-name">${esc(c.name)}</span>
          <span class="c-sub">${esc(c.car)}</span>
          <span class="c-sub">${faNum(c.distanceM / 1000, 1)} کیلومتر · امتیاز ${fa(Math.round(c.cost))}</span>
        </span>
        <span class="c-eta">${faNum(etaMin, 1)}<small>دقیقه</small></span>`;
      el.appendChild(li);
    });
  }

  function showFlow(title) {
    $('flow').hidden = false;
    $('flowTitle').textContent = title;
    const bar = $('progress');
    bar.classList.add('indeterminate');
    bar.firstElementChild.style.width = '';
  }

  function settleFlow(title, meta) {
    $('flow').hidden = false;
    $('flowTitle').textContent = title;
    $('flowMeta').textContent = meta || '';
    const bar = $('progress');
    bar.classList.remove('indeterminate');
    bar.firstElementChild.style.width = '100%';
  }

  function hideFlow() {
    $('flow').hidden = true;
    $('candidates').innerHTML = '';
    $('flowMeta').textContent = '';
    const bar = $('progress');
    bar.classList.add('indeterminate');
  }

  function showOffer(o) {
    S.offer = {
      ...o,
      expiresAt: Date.parse(o.expiresAt),
      createdAt: Date.parse(o.createdAt),
    };
    $('offerCard').hidden = false;
    $('offerAvatar').textContent = (o.driverName || 'ر').trim().charAt(0);
    $('offerName').textContent = o.driverName;
    $('offerCar').textContent = o.car;
    $('offerPlate').textContent = o.plate;
    $('offerRating').textContent = faNum(o.rating, 2);
    $('offerEta').textContent = faNum(o.etaSec / 60, 1);
    $('offerDistance').textContent = `${faNum(o.distanceM / 1000, 1)} کیلومتر تا شما`;
    $('ttlSec').textContent = fa(Math.round((S.offer.expiresAt - S.offer.createdAt) / 1000));
    $('offerBar').style.width = '100%';
    badge('ارسال پیشنهاد', 'busy');
  }

  function hideOffer() {
    S.offer = null;
    $('offerCard').hidden = true;
  }

  function startTrip(trip) {
    S.trip = {
      ...trip,
      _pickup: makePoint(trip.pickup.lat, trip.pickup.lon),
      _dropoff: makePoint(trip.dropoff.lat, trip.dropoff.lon),
    };
    hideOffer();
    $('tripCard').hidden = false;
    $('tripBadge').textContent = 'در سفر';
    $('tripId').textContent = trip.id;
    const d = S.drivers.get(trip.driverId);
    $('tripDriver').textContent = d ? d.name : trip.driverId;
    $('tripSub').textContent = d ? `${d.car} · ${d.plate}` : '';
    $('tripBar').style.width = '0%';
    $('tripEta').textContent = '—';
    $('tripFare').textContent = `${fa(trip.fare.toLocaleString('en-US'))} تومان`;
    settleFlow('سفر آغاز شد', '');
    hideFlow();
    badge('در سفر', 'ok');
    updateRequestButton();
  }

  function finishTrip(data) {
    if (!S.trip || data.tripId !== S.trip.id) return;
    $('tripBadge').textContent = 'پایان سفر';
    $('tripBar').style.width = '100%';
    $('tripEta').textContent = 'رسید';
    $('tripSub').textContent = 'سفر با موفقیت به پایان رسید';
    $('tripFare').textContent = `${fa(Number(data.fare).toLocaleString('en-US'))} تومان`;
    badge('آماده', 'ok');
    log('trip', `پایان سفر ${S.trip.id} — ${fa(Number(data.fare).toLocaleString('en-US'))} تومان`, 'ok');
    S.requestId = null;
    S.candidates = [];
    S.rings = [];
    S.trip = null;
    updateRequestButton();
  }

  // --------------------------------------------------------------- statistics

  function applyStats(st) {
    $('statAvailable').textContent = fa(st.available ?? 0);
    $('statOnTrip').textContent = fa(st.onTrip ?? 0);
    // Sub-100 µs intervals are below the clock resolution of some virtualised
    // hosts, so a zero sample is reported as "under 0.1 ms" instead of "0.00".
    const search = st.requests > 0
      ? ((st.avgSearchMs ?? 0) >= 0.01 ? fa(Number(st.avgSearchMs).toFixed(2)) : 'زیر ۰.۱')
      : '—';
    $('statSearch').textContent = search;
    $('statAccept').textContent = fa(Math.round((st.acceptRate ?? 0) * 100));
    $('mapSubtitle').textContent = `${fa(st.available ?? 0)} راننده آماده · ${fa(st.drivers ?? 0)} در ناوگان`;
  }

  async function refreshStats() {
    try {
      const res = await fetch('/api/stats', { cache: 'no-store' });
      if (!res.ok) return;
      applyStats(await res.json());
    } catch (e) {
      /* transient */
    }
  }

  // ------------------------------------------------------------------- data

  async function loadState() {
    const res = await fetch('/api/state', { cache: 'no-store' });
    const data = await res.json();
    S.city = data.city;
    S.cfg = data.config;
    S.settings = data.settings;

    const nw = metersFromLatLon(data.city.maxLat, data.city.minLon);
    const se = metersFromLatLon(data.city.minLat, data.city.maxLon);
    S.world = {
      minX: nw.x,
      maxX: se.x,
      minY: nw.y,
      maxY: se.y,
      w: se.x - nw.x,
      h: se.y - nw.y,
      cx: (nw.x + se.x) / 2,
      cy: (nw.y + se.y) / 2,
    };

    S.drivers.clear();
    for (const d of data.drivers) {
      const p = metersFromLatLon(d.pos.lat, d.pos.lon);
      S.drivers.set(d.id, {
        ...d,
        x: p.x,
        y: p.y,
        rx: p.x,
        ry: p.y,
        heading: d.heading || 0,
      });
    }

    $('autoAccept').checked = !!data.settings.autoAccept;
    $('autoDemand').checked = !!data.settings.autoDemand;
    applyStats(data.stats);
    layout();
    baseDirty = true;
  }

  // ------------------------------------------------------------------- UI

  function resetSelection(keepTrip = false) {
    S.requestId = null;
    S.candidates = [];
    S.rings = [];
    hideOffer();
    hideFlow();
    notice('');
    if (!keepTrip) {
      S.trip = null;
      $('tripCard').hidden = true;
    }
    badge('آماده', '');
    updateRequestButton();
  }

  function clearAll() {
    S.pickup = null;
    S.dropoff = null;
    setFieldValue('pickup', null);
    setFieldValue('dropoff', null);
    setActive('pickup');
    resetSelection(false);
  }

  function sample() {
    const span = S.world;
    const randPoint = () => {
      // stay inside the central 70% so the demo looks like a city centre
      const x = span.cx + (Math.random() - 0.5) * span.w * 0.62;
      const y = span.cy + (Math.random() - 0.5) * span.h * 0.62;
      return pointAt(x, y);
    };
    S.pickup = randPoint();
    S.dropoff = randPoint();
    let guard = 0;
    while (distance(S.pickup, S.dropoff) < 1800 && guard++ < 20) {
      S.dropoff = randPoint();
    }
    setFieldValue('pickup', S.pickup);
    setFieldValue('dropoff', S.dropoff);
    setActive('pickup');
    notice('');
    updateRequestButton();
  }

  async function submitRequest() {
    if (!S.pickup || !S.dropoff) return;
    notice('');
    S.candidates = [];
    S.rings = [];
    S.trip = null;
    $('tripCard').hidden = true;
    $('candidates').innerHTML = '';
    badge('در جست‌وجو', 'busy');
    showFlow('در حال ثبت درخواست…');
    $('requestBtn').disabled = true;
    S.adopting = true;

    try {
      const res = await fetch('/api/request', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          pickup: { lat: S.pickup.lat, lon: S.pickup.lon },
          dropoff: { lat: S.dropoff.lat, lon: S.dropoff.lon },
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        S.adopting = false;
        notice(data.error || 'ثبت درخواست ناموفق بود');
        badge('خطا', 'err');
        hideFlow();
        updateRequestButton();
        return;
      }
      S.requestId = data.requestId;
      S.adopting = false;
      showFlow('در حال جست‌وجوی راننده…');
      log('req', `درخواست ${data.requestId} ثبت شد`);
    } catch (e) {
      S.adopting = false;
      notice('ارتباط با سرور قطع شد');
      badge('خطا', 'err');
      hideFlow();
      updateRequestButton();
    }
  }

  async function acceptOffer() {
    if (!S.offer) return;
    const id = S.offer.id;
    $('acceptBtn').disabled = true;
    try {
      const res = await fetch(`/api/offers/${encodeURIComponent(id)}/accept`, { method: 'POST' });
      const data = await res.json();
      if (!res.ok) {
        notice(data.error || 'پذیرش پیشنهاد ناموفق بود');
        return;
      }
      if (data.trip) startTrip(data.trip);
    } catch (e) {
      notice('ارتباط با سرور قطع شد');
    } finally {
      $('acceptBtn').disabled = false;
    }
  }

  async function declineOffer() {
    if (!S.offer) return;
    const id = S.offer.id;
    try {
      await fetch(`/api/offers/${encodeURIComponent(id)}/decline`, { method: 'POST' });
      hideOffer();
      showFlow('در حال جست‌وجوی راننده جایگزین…');
      badge('در جست‌وجو', 'busy');
    } catch (e) {
      notice('ارتباط با سرور قطع شد');
    }
  }

  // ------------------------------------------------------------------ events

  function connect() {
    const es = new EventSource('/api/events');
    const conn = $('conn');

    es.onopen = () => {
      conn.classList.add('on');
      conn.classList.remove('off');
      $('connLabel').textContent = 'اتصال زنده';
    };
    es.onerror = () => {
      conn.classList.remove('on');
      conn.classList.add('off');
      $('connLabel').textContent = 'قطع ارتباط';
    };

    const on = (name, fn) => es.addEventListener(name, (e) => {
      let data = null;
      try {
        data = JSON.parse(e.data);
      } catch (err) {
        return;
      }
      fn(data);
    });

    const adopt = (d) => {
      if (!S.requestId && S.adopting && d && d.requestId) {
        S.requestId = d.requestId;
        S.adopting = false;
        showFlow('در حال جست‌وجوی راننده…');
      }
    };

    on('search.ring', (d) => {
      adopt(d);
      if (d.requestId !== S.requestId || !S.pickup) return;
      // Keep only the newest ring: the SSE stream emits one event per ring
      // within a millisecond, and storing all of them paints a green blob.
      S.rings = [{ ring: d.ring, at: performance.now(), center: S.pickup }];
    });

    on('search.done', (d) => {
      adopt(d);
      if (d.requestId !== S.requestId) return;
      S.candidates = d.candidates || [];
      S.candidatesAt = performance.now();
      renderCandidates(S.candidates, null);
      settleFlow(
        'راننده‌های کاندید پیدا شدند',
        `${fa(d.cells)} خانه · ${fa(d.scanned)} راننده · ${d.ms > 0 ? `${fa(Number(d.ms).toFixed(2))}ms` : 'زیر ۰.۱ms'}`,
      );
      log('idx', `جست‌وجو: ${fa(d.cells)} خانه، ${fa(d.scanned)} راننده، ${d.ms > 0 ? `${fa(Number(d.ms).toFixed(2))}ms` : 'زیر ۰.۱ms'}`);
      if (!S.candidates.length) {
        showFlow('ناوگانی در محدوده نیست؛ تلاش مجدد…');
      }
    });

    on('offer.created', (o) => {
      adopt(o);
      if (o.requestId !== S.requestId) return;
      showOffer(o);
      renderCandidates(S.candidates, o.driverId);
      settleFlow('پیشنهاد برای راننده ارسال شد', `${fa(Math.round(o.etaSec))} ثانیه تا رسیدن`);
      log('offer', `پیشنهاد ${o.id} برای ${o.driverName}`, 'warn');
    });

    on('offer.accepted', (d) => {
      if (d.requestId !== S.requestId) return;
      log('offer', `${d.driverName} پیشنهاد را پذیرفت`, 'ok');
    });

    on('offer.declined', (d) => {
      if (d.requestId !== S.requestId) return;
      hideOffer();
      showFlow('راننده رد کرد؛ جست‌وجوی مجدد…');
      log('offer', 'پیشنهاد رد شد؛ بازگشت به صف', 'warn');
    });

    on('offer.expired', (d) => {
      if (d.requestId !== S.requestId) return;
      hideOffer();
      showFlow('پیشنهاد منقضی شد؛ جست‌وجوی مجدد…');
      log('offer', 'پیشنهاد بدون پاسخ ماند', 'warn');
    });

    on('request.retry', (d) => {
      adopt(d);
      if (d.requestId !== S.requestId) return;
      showFlow('تلاش مجدد برای یافتن راننده…');
      log('req', `تلاش ${d.attempt} — ${d.reason}`);
    });

    on('request.unmatched', (d) => {
      adopt(d);
      if (d.requestId !== S.requestId) return;
      hideFlow();
      hideOffer();
      S.requestId = null;
      badge('ناموفق', 'err');
      notice('در این محدوده راننده آزادی پیدا نشد. دوباره تلاش کنید.');
      updateRequestButton();
      log('err', `درخواست ناموفق: ${d.reason}`, 'err');
    });

    on('trip.started', (t) => {
      if (t.requestId !== S.requestId) return;
      startTrip(t);
      log('trip', `سفر ${t.id} آغاز شد`, 'ok');
    });

    on('trip.picked_up', (d) => {
      if (!S.trip || d.tripId !== S.trip.id) return;
      S.trip.phase = 'to_dropoff';
      $('tripSub').textContent = 'مسافر سوار شد؛ در مسیر مقصد';
      log('trip', 'مسافر سوار شد', 'ok');
    });

    on('trip.finished', (d) => {
      finishTrip(d);
    });

    on('batch', (d) => {
      if (!S.requestId && !S.trip) return;
      const took = d.assignmentMs > 0 ? `${fa(Number(d.assignmentMs).toFixed(2))}ms` : 'زیر ۰.۱ms';
      log('assign', `تخصیص ${fa(d.requests)} درخواست / ${fa(d.drivers)} راننده در ${took}`);
    });

    on('fleet', (list) => {
      let available = 0;
      for (const item of list) {
        const d = S.drivers.get(item.i);
        if (!d) continue;
        const p = metersFromLatLon(item.a, item.o);
        d.x = p.x;
        d.y = p.y;
        d.heading = item.h;
        d.state = item.s;
        if (item.s === 'available') available++;
      }
      if (available !== undefined) {
        $('mapSubtitle').textContent = `${fa(available)} راننده آماده · ${fa(S.drivers.size)} در ناوگان`;
      }
    });

    on('fleet.reset', () => {
      log('sys', 'ناوگان بازنشانی شد', 'ok');
      clearAll();
      loadState().then(() => {
        layout();
        baseDirty = true;
      });
    });

    on('demand.injected', () => {
      log('req', 'یک درخواست خودکار به صف اضافه شد');
    });
  }

  // ------------------------------------------------------------------ wiring

  function wire() {
    $('fieldPickup').addEventListener('click', () => setActive('pickup'));
    $('fieldDropoff').addEventListener('click', () => setActive('dropoff'));

    document.querySelectorAll('.map-modes .chip').forEach((btn) => {
      btn.addEventListener('click', () => setActive(btn.dataset.mode));
    });

    $('requestBtn').addEventListener('click', submitRequest);
    $('clearBtn').addEventListener('click', clearAll);
    $('sampleBtn').addEventListener('click', sample);
    $('acceptBtn').addEventListener('click', acceptOffer);
    $('declineBtn').addEventListener('click', declineOffer);

    $('resetBtn').addEventListener('click', async () => {
      try {
        await fetch('/api/reset', { method: 'POST' });
        clearAll();
        await loadState();
        log('sys', 'ناوگان تازه‌سازی شد', 'ok');
      } catch (e) {
        notice('بازنشانی ناموفق بود');
      }
    });

    const pushSettings = async () => {
      try {
        await fetch('/api/settings', {
          method: 'POST',
          headers: { 'content-type': 'application/json' },
          body: JSON.stringify({
            autoAccept: $('autoAccept').checked,
            autoDemand: $('autoDemand').checked,
          }),
        });
      } catch (e) {
        /* ignore */
      }
    };
    $('autoAccept').addEventListener('change', pushSettings);
    $('autoDemand').addEventListener('change', pushSettings);

    $('zoomIn').addEventListener('click', () => zoomAt(W / 2, H / 2, 1.35));
    $('zoomOut').addEventListener('click', () => zoomAt(W / 2, H / 2, 1 / 1.35));

    wireMap();
    window.addEventListener('resize', resize);
    if ('ResizeObserver' in window) {
      new ResizeObserver(() => resize()).observe(document.querySelector('.map-shell'));
    }
  }

  let dragging = false;
  let dragMoved = false;
  let dragStart = null;
  let panStart = null;

  function wireMap() {
    canvas.addEventListener('pointerdown', (e) => {
      dragging = true;
      dragMoved = false;
      dragStart = { x: e.clientX, y: e.clientY };
      panStart = { x: V.panX, y: V.panY };
      canvas.setPointerCapture(e.pointerId);
    });

    canvas.addEventListener('pointermove', (e) => {
      if (!dragging) return;
      const dx = e.clientX - dragStart.x;
      const dy = e.clientY - dragStart.y;
      if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
        dragMoved = true;
        canvas.classList.add('dragging');
      }
      if (dragMoved) {
        V.panX = panStart.x + dx;
        V.panY = panStart.y + dy;
        layout();
        baseDirty = true;
      }
    });

    const endDrag = (e) => {
      if (!dragging) return;
      dragging = false;
      canvas.classList.remove('dragging');
      if (dragMoved) return;
      const rect = canvas.getBoundingClientRect();
      const p = pointAt(wx(e.clientX - rect.left), wy(e.clientY - rect.top));
      handleMapClick(p);
    };

    canvas.addEventListener('pointerup', endDrag);
    canvas.addEventListener('pointercancel', () => {
      dragging = false;
      canvas.classList.remove('dragging');
    });

    canvas.addEventListener('wheel', (e) => {
      e.preventDefault();
      const rect = canvas.getBoundingClientRect();
      zoomAt(e.clientX - rect.left, e.clientY - rect.top, Math.exp(-e.deltaY * 0.0014));
    }, { passive: false });
  }

  function zoomAt(mx, my, factor) {
    const before = { x: wx(mx), y: wy(my) };
    V.zoom = clamp(V.zoom * factor, 0.65, 6);
    layout();
    const after = { x: wx(mx), y: wy(my) };
    V.panX += (after.x - before.x) * V.scale;
    V.panY += (after.y - before.y) * V.scale;
    layout();
    baseDirty = true;
  }

  function handleMapClick(point) {
    if (S.requestId || S.offer) {
      notice('درخواست در حال انجام است؛ ابتدا آن را تکمیل یا پاک کنید.');
      return;
    }
    notice('');
    if (S.active === 'pickup') {
      S.pickup = point;
      setFieldValue('pickup', point);
      if (!S.dropoff) setActive('dropoff');
    } else {
      S.dropoff = point;
      setFieldValue('dropoff', point);
      if (!S.pickup) setActive('pickup');
    }
    if (S.trip) {
      S.trip = null;
      $('tripCard').hidden = true;
    }
    updateRequestButton();
  }

  // ------------------------------------------------------------------- loop

  let lastFrame = performance.now();

  function frame(now) {
    const dt = Math.min(0.06, Math.max(0.001, (now - lastFrame) / 1000));
    lastFrame = now;

    const k = 1 - Math.pow(0.002, dt);
    for (const d of S.drivers.values()) {
      d.rx = lerp(d.rx, d.x, k);
      d.ry = lerp(d.ry, d.y, k);
    }

    draw(now);
    updateOfferCountdown(now);
    updateTripProgress(now);

    requestAnimationFrame(frame);
  }

  let lastAux = 0;

  function updateOfferCountdown(now) {
    if (!S.offer) return;
    const ttl = Math.max(1, S.offer.expiresAt - S.offer.createdAt);
    const left = S.offer.expiresAt - Date.now();
    const pct = clamp(left / ttl, 0, 1);
    $('offerBar').style.width = `${pct * 100}%`;
    $('offerBar').style.background = pct < 0.28 ? '#a5412f' : '#136b4c';
    $('ttlSec').textContent = fa(Math.max(0, Math.ceil(left / 1000)));
  }

  function updateTripProgress(now) {
    if (!S.trip || now - lastAux < 220) return;
    lastAux = now;
    const d = S.drivers.get(S.trip.driverId);
    if (!d) return;
    const total = Math.max(50, distance(S.trip._pickup, S.trip._dropoff));
    const remaining = distance({ x: d.rx, y: d.ry }, S.trip._dropoff);
    const pct = clamp(1 - remaining / total, 0, 1);
    $('tripBar').style.width = `${(pct * 100).toFixed(1)}%`;
    $('tripEta').textContent = `${fa((remaining / 1000).toFixed(1))} کیلومتر باقی‌مانده`;
  }

  // ------------------------------------------------------------------- boot

  async function boot() {
    await loadState();
    resize();
    wire();
    connect();
    setActive('pickup');
    updateRequestButton();
    log('sys', 'کنسول توزیع آماده است', 'ok');
    requestAnimationFrame(frame);
    setInterval(refreshStats, 2500);
  }

  boot().catch((err) => {
    console.error(err);
    notice('بارگذاری کنسول ناموفق بود');
  });
})();
