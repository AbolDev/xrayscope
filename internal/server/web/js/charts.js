// Hand-written SVG/CSS charts: no dependencies, light on the browser.
import { h, svg, t, num, fmtTime, fmtDay, fmtDate, fmtWeekday, isFa, debounce, dur } from './util.js';

const NS = 'http://www.w3.org/2000/svg';

function niceMax(v) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / p;
  const n = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10;
  return n * p;
}

function attachTip(wrap) {
  const tip = h('div', { class: 'tip' });
  wrap.append(tip);
  return {
    show(x, y, rows, title) {
      tip.replaceChildren();
      if (title) tip.append(h('b', null, title));
      for (const r of rows) tip.append(h('div', { class: 'r' }, r.color ? h('i', { style: { background: r.color } }) : null, r.text));
      tip.style.left = Math.min(Math.max(x, 60), wrap.clientWidth - 60) + 'px';
      tip.style.top = Math.max(y, 0) + 'px';
      tip.classList.add('show');
    },
    hide() { tip.classList.remove('show'); },
  };
}

// observe redraws a chart when its container width changes.
function observe(wrap, draw) {
  let last = 0;
  const run = () => { const w = wrap.clientWidth; if (w && w !== last) { last = w; draw(); } };
  if (typeof ResizeObserver !== 'undefined') new ResizeObserver(debounce(run, 60)).observe(wrap);
  requestAnimationFrame(run);
}

/**
 * Time series with stacked bars (few buckets) or stacked areas (many).
 * opts: {from, to, bucket, series:[{label,color,points:[{t,v}]}], height, yFmt}
 */
export function timeChart(host, opts) {
  const H = opts.height || 230;
  const M = { l: 52, r: 10, t: 10, b: 24 };
  const wrap = h('div', { class: 'chart', style: { height: H + 'px' } });
  host.append(wrap);
  const tip = attachTip(wrap);
  const yFmt = opts.yFmt || ((v) => num(v));
  const bucket = opts.bucket || 3600;
  const start = Math.floor(opts.from / bucket) * bucket;
  const steps = Math.max(1, Math.min(1500, Math.ceil((opts.to - start) / bucket)));
  const vals = opts.series.map((s) => {
    const arr = new Array(steps).fill(0);
    for (const p of s.points) {
      const i = Math.floor((p.t - start) / bucket);
      if (i >= 0 && i < steps) arr[i] += p.v;
    }
    return arr;
  });
  const totals = new Array(steps).fill(0);
  vals.forEach((arr) => arr.forEach((v, i) => { totals[i] += v; }));
  const max = niceMax(Math.max(...totals, 0));

  function draw() {
    const W = Math.max(260, wrap.clientWidth);
    const pw = W - M.l - M.r, ph = H - M.t - M.b;
    wrap.querySelectorAll('svg').forEach((n) => n.remove());
    const s = svg('svg', { width: W, height: H, viewBox: `0 0 ${W} ${H}` });
    const y = (v) => M.t + ph - (v / max) * ph;
    for (let i = 0; i <= 4; i++) {
      const v = (max / 4) * i;
      s.append(svg('line', { class: 'grid-line', x1: M.l, x2: W - M.r, y1: y(v), y2: y(v) }));
      s.append(svg('text', { x: M.l - 8, y: y(v) + 4, 'text-anchor': 'end' }, yFmt(v)));
    }
    const bw = pw / steps;
    const barMode = steps <= 130;
    const acc = new Array(steps).fill(0);
    vals.forEach((arr, si) => {
      const color = opts.series[si].color;
      if (barMode) {
        const gap = bw > 6 ? 1.5 : 0.5;
        arr.forEach((v, i) => {
          if (v <= 0) return;
          const y0 = y(acc[i]), y1 = y(acc[i] + v);
          s.append(svg('rect', { x: M.l + i * bw + gap / 2, y: y1, width: Math.max(1, bw - gap), height: Math.max(0.5, y0 - y1), fill: color, rx: bw > 8 ? 2 : 0 }));
          acc[i] += v;
        });
      } else {
        const top = [], bot = [];
        arr.forEach((v, i) => {
          const x = M.l + (i + 0.5) * bw;
          bot.push([x, y(acc[i])]);
          acc[i] += v;
          top.push([x, y(acc[i])]);
        });
        const d = 'M' + top.map((p) => p.join(',')).join('L') + 'L' + bot.reverse().map((p) => p.join(',')).join('L') + 'Z';
        s.append(svg('path', { d, fill: color, 'fill-opacity': 0.55, stroke: color, 'stroke-width': 1 }));
      }
    });
    // x axis
    const every = Math.max(1, Math.ceil(steps / Math.max(2, Math.floor(pw / 78))));
    let prevDay = '';
    const shift = opts.labelShift || 0;
    for (let i = 0; i < steps; i += every) {
      const ts = start + i * bucket + shift;
      let label;
      if (bucket >= 86400) label = fmtDay(ts);
      else {
        const d = fmtDay(ts);
        label = fmtTime(ts);
        if (d !== prevDay && (every * bucket >= 3600 * 3 || prevDay === '')) label = fmtDay(ts) + ' ' + label;
        prevDay = d;
      }
      s.append(svg('text', { x: M.l + (i + 0.5) * bw, y: H - 6, 'text-anchor': 'middle' }, label));
    }
    // hover
    const hit = svg('rect', { x: M.l, y: M.t, width: pw, height: ph, fill: 'transparent' });
    hit.addEventListener('mousemove', (e) => {
      const r = s.getBoundingClientRect();
      const i = Math.min(steps - 1, Math.max(0, Math.floor((e.clientX - r.left - M.l) / bw)));
      const ts = start + i * bucket + (opts.labelShift || 0);
      const rows = opts.series.map((sr, si) => ({ color: sr.color, text: sr.label + ': ' + yFmt(vals[si][i]) }));
      const title = bucket >= 86400 ? fmtDate(ts) : fmtDay(ts) + ' ' + fmtTime(ts);
      tip.show(M.l + (i + 0.5) * bw, y(totals[i]) - 8, rows, title);
    });
    hit.addEventListener('mouseleave', tip.hide);
    s.append(hit);
    wrap.prepend(s);
  }
  observe(wrap, draw);
  if (opts.series.length > 1 || opts.legend) {
    host.append(h('div', { class: 'legend' }, opts.series.map((s) => h('span', null, h('i', { style: { background: s.color } }), s.label))));
  }
  return wrap;
}

/** Horizontal bars. items: [{label, value, color, text}] */
export function barList(items, opts = {}) {
  const max = Math.max(...items.map((i) => i.value), 1);
  return h('div', { class: 'bars' }, items.map((it) => h('div', { class: 'bar-row' },
    h('div', { class: 'name', title: it.label }, it.link ? h('a', { href: it.link }, it.label) : it.label),
    h('div', { class: 'track' }, h('i', { style: { width: Math.max(1.5, (it.value / max) * 100) + '%', background: it.color || 'var(--primary)' } })),
    h('div', { class: 'val' }, it.text != null ? it.text : (opts.fmt || num)(it.value)))));
}

/** Donut chart with a centre label. items: [{label, value, color}] */
export function donut(items, opts = {}) {
  const size = opts.size || 170, r = size / 2 - 12, c = size / 2, sw = 22;
  const total = items.reduce((a, b) => a + b.value, 0);
  const s = svg('svg', { viewBox: `0 0 ${size} ${size}`, width: size, height: size });
  const wrap = h('div', { class: 'chart', style: { width: size + 'px', height: size + 'px', margin: '0 auto' } });
  const tip = attachTip(wrap);
  if (total <= 0) {
    s.append(svg('circle', { cx: c, cy: c, r, fill: 'none', stroke: 'var(--border)', 'stroke-width': sw }));
  } else {
    let a0 = -Math.PI / 2;
    for (const it of items) {
      if (it.value <= 0) continue;
      const frac = it.value / total;
      if (frac > 0.9999) {
        s.append(svg('circle', { cx: c, cy: c, r, fill: 'none', stroke: it.color, 'stroke-width': sw }));
        break;
      }
      const a1 = a0 + frac * Math.PI * 2;
      const large = frac > 0.5 ? 1 : 0;
      const p = (a) => [c + r * Math.cos(a), c + r * Math.sin(a)];
      const [x0, y0] = p(a0), [x1, y1] = p(a1);
      const path = svg('path', { d: `M${x0},${y0} A${r},${r} 0 ${large} 1 ${x1},${y1}`, fill: 'none', stroke: it.color, 'stroke-width': sw });
      path.addEventListener('mousemove', () => tip.show(c, 10, [{ color: it.color, text: `${it.label}: ${(opts.fmt || num)(it.value)} (${num(frac * 100, 1)}%)` }]));
      path.addEventListener('mouseleave', tip.hide);
      s.append(path);
      a0 = a1;
    }
  }
  if (opts.center) {
    s.append(svg('text', { x: c, y: c - 2, 'text-anchor': 'middle', style: 'font-size:17px;font-weight:800;fill:var(--text)' }, opts.center));
    if (opts.centerSub) s.append(svg('text', { x: c, y: c + 16, 'text-anchor': 'middle', style: 'font-size:11px' }, opts.centerSub));
  }
  wrap.prepend(s);
  return wrap;
}

/** Legend list for donut-like data. */
export function legend(items, fmt) {
  return h('div', { class: 'legend' }, items.map((i) => h('span', null, h('i', { style: { background: i.color } }), i.label + (fmt ? ' · ' + fmt(i.value) : ''))));
}

/** 7x24 heatmap; matrix[weekday 0=Sunday][hour] = value. */
export function heatmap(matrix, opts = {}) {
  let max = 0;
  matrix.forEach((r) => r.forEach((v) => { if (v > max) max = v; }));
  const order = isFa() ? [6, 0, 1, 2, 3, 4, 5] : [1, 2, 3, 4, 5, 6, 0];
  const names = (wd) => fmtWeekday(Date.UTC(2023, 0, 1 + wd, 12) / 1000);
  const grid = h('div', { class: 'heat', style: { gridTemplateColumns: '54px repeat(24, minmax(0, 1fr))' } });
  const tip = attachTip(grid);
  grid.style.position = 'relative';
  grid.append(h('div'));
  for (let hr = 0; hr < 24; hr++) grid.append(h('div', { class: 'lab' }, hr % 3 === 0 ? String(hr).padStart(2, '0') : ''));
  for (const wd of order) {
    grid.append(h('div', { class: 'lab day' }, names(wd)));
    for (let hr = 0; hr < 24; hr++) {
      const v = matrix[wd][hr];
      const a = max > 0 ? v / max : 0;
      const cell = h('div', { class: 'cell', style: { background: v > 0 ? `color-mix(in srgb, var(--primary) ${Math.round(12 + a * 88)}%, var(--surface-2))` : '' } });
      cell.addEventListener('mouseenter', () => {
        const r = cell.getBoundingClientRect(), g = grid.getBoundingClientRect();
        tip.show(r.left - g.left + r.width / 2, r.top - g.top - 4, [{ text: `${names(wd)} ${String(hr).padStart(2, '0')}:00 — ${(opts.fmt || ((x) => dur(x)))(v)}` }]);
      });
      cell.addEventListener('mouseleave', tip.hide);
      grid.append(cell);
    }
  }
  return grid;
}

/**
 * Minute strip of one day: chars '0' idle, '1' background traffic,
 * '2' interactive traffic.
 */
export function strip(str, startSec, opts = {}) {
  const n = str.length;
  const H = 46;
  const wrap = h('div', { class: 'chart strip-box', style: { height: H + 20 + 'px' } });
  const tip = attachTip(wrap);
  const colors = { 1: 'var(--warn)', 2: 'var(--primary)' };
  function draw() {
    const W = Math.max(280, wrap.clientWidth);
    wrap.querySelectorAll('svg').forEach((x) => x.remove());
    const s = svg('svg', { width: W, height: H + 20, viewBox: `0 0 ${W} ${H + 20}` });
    s.append(svg('rect', { x: 0, y: 0, width: W, height: H, rx: 8, fill: 'var(--surface-2)', stroke: 'var(--border)' }));
    const mw = W / n;
    let i = 0;
    while (i < n) {
      const c = str[i];
      if (c === '0') { i++; continue; }
      let j = i;
      while (j < n && str[j] === c) j++;
      s.append(svg('rect', { x: i * mw, y: 2, width: Math.max(1.2, (j - i) * mw), height: H - 4, fill: colors[c], rx: 1.5 }));
      i = j;
    }
    for (let hr = 0; hr <= 24; hr += 2) {
      const x = (hr * 60 * W) / n;
      s.append(svg('line', { class: 'axis-line', x1: x, x2: x, y1: H, y2: H + 4 }));
      s.append(svg('text', { x, y: H + 16, 'text-anchor': hr === 0 ? 'start' : hr === 24 ? 'end' : 'middle' }, String(hr % 24).padStart(2, '0')));
    }
    const hit = svg('rect', { x: 0, y: 0, width: W, height: H, fill: 'transparent' });
    hit.addEventListener('mousemove', (e) => {
      const r = s.getBoundingClientRect();
      const m = Math.min(n - 1, Math.max(0, Math.floor(((e.clientX - r.left) / W) * n)));
      const state = { 0: t('strip.idle'), 1: t('strip.bg'), 2: t('strip.inter') }[str[m]];
      tip.show((m + 0.5) * mw, -2, [{ color: colors[str[m]], text: fmtTime(startSec + m * 60) + ' — ' + state }]);
    });
    hit.addEventListener('mouseleave', tip.hide);
    s.append(hit);
    wrap.prepend(s);
  }
  observe(wrap, draw);
  return wrap;
}

/** Rows of bars on a 24h axis. rows: [{label, color, spans:[{start,end,title}]}] (unix seconds). */
export function gantt(rows, startSec, minutes) {
  const total = minutes * 60;
  const wrap = h('div', { class: 'gantt', style: { position: 'relative' } });
  const tip = attachTip(wrap);
  for (const r of rows) {
    const track = h('div', { class: 'g-track' });
    for (const sp of r.spans) {
      const left = Math.max(0, ((sp.start - startSec) / total) * 100);
      const width = Math.max(0.25, ((Math.min(sp.end, startSec + total) - Math.max(sp.start, startSec)) / total) * 100);
      const bar = h('div', { class: 'g-bar', style: { left: left + '%', width: width + '%', background: r.color } });
      bar.addEventListener('mousemove', (e) => {
        const g = wrap.getBoundingClientRect();
        tip.show(e.clientX - g.left, e.clientY - g.top - 8, [{ text: sp.title }]);
      });
      bar.addEventListener('mouseleave', tip.hide);
      track.append(bar);
    }
    wrap.append(h('div', { class: 'g-row' }, h('div', { class: 'g-label mono', title: r.label }, r.label), track));
  }
  return wrap;
}
