// Hand-written SVG/CSS charts: no dependencies, light on the browser.
// Colour discipline: one accent, a lightness ramp of it for further series,
// text never takes the colour of the data it labels.
import { h, svg, t, num, fmtTime, fmtDay, fmtDate, fmtWeekday, isFa, debounce, dur, ramp } from './util.js';

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
 * Time series, stacked bars. A baseline at zero, one y axis, four hairline
 * grid lines. opts: {from, to, bucket, series:[{label,color,points:[{t,v}]}], height, yFmt, labelShift}
 */
export function timeChart(host, opts) {
  const H = opts.height || 220;
  const M = { l: 52, r: 8, t: 8, b: 24 };
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
  const shift = opts.labelShift || 0;

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
    const barMode = steps <= 160;
    const acc = new Array(steps).fill(0);
    vals.forEach((arr, si) => {
      const color = opts.series[si].color;
      if (barMode) {
        const gap = bw > 6 ? 1.5 : 0.5;
        arr.forEach((v, i) => {
          if (v <= 0) return;
          const y0 = y(acc[i]), y1 = y(acc[i] + v);
          s.append(svg('rect', { x: M.l + i * bw + gap / 2, y: y1, width: Math.max(1, bw - gap), height: Math.max(0.5, y0 - y1), fill: color }));
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
        s.append(svg('path', { d, fill: color, 'fill-opacity': 0.85 }));
      }
    });
    const every = Math.max(1, Math.ceil(steps / Math.max(2, Math.floor(pw / 80))));
    let prevDay = '';
    for (let i = 0; i < steps; i += every) {
      const ts = start + i * bucket + shift;
      let label;
      if (bucket >= 86400) label = fmtDay(ts);
      else {
        const d = fmtDay(ts);
        label = fmtTime(ts);
        if (d !== prevDay && (every * bucket >= 3600 * 3 || prevDay === '')) label = d + ' ' + label;
        prevDay = d;
      }
      s.append(svg('text', { x: M.l + (i + 0.5) * bw, y: H - 6, 'text-anchor': 'middle' }, label));
    }
    const hit = svg('rect', { x: M.l, y: M.t, width: pw, height: ph, fill: 'transparent' });
    hit.addEventListener('mousemove', (e) => {
      const r = s.getBoundingClientRect();
      const i = Math.min(steps - 1, Math.max(0, Math.floor((e.clientX - r.left - M.l) / bw)));
      const ts = start + i * bucket + shift;
      const rows = opts.series.map((sr, si) => ({ color: sr.color, text: sr.label + ': ' + yFmt(vals[si][i]) }));
      const title = bucket >= 86400 ? fmtDate(ts) : fmtDay(ts) + ' ' + fmtTime(ts);
      tip.show(M.l + (i + 0.5) * bw, y(totals[i]) - 8, rows, title);
    });
    hit.addEventListener('mouseleave', tip.hide);
    s.append(hit);
    wrap.prepend(s);
  }
  observe(wrap, draw);
  if (opts.series.length > 1) {
    host.append(h('div', { class: 'legend' }, opts.series.map((s) => h('span', null, h('i', { class: 'swatch', style: { background: s.color } }), s.label))));
  }
  return wrap;
}

/** Ranked horizontal bars in the accent. items: [{label, value, text, link}] */
export function rank(items, opts = {}) {
  const max = Math.max(...items.map((i) => i.value), 1);
  return h('div', { class: 'rank' }, items.map((it) => h('div', { class: 'rank-row' },
    h('div', { class: 'name', title: it.label }, it.link ? h('a', { href: it.link }, it.label) : it.label),
    h('div', { class: 'track' }, h('i', { style: { width: Math.max(1.5, (it.value / max) * 100) + '%', background: it.color || null } })),
    h('div', { class: 'val' }, it.text != null ? it.text : (opts.fmt || num)(it.value)))));
}

/**
 * A part-to-whole stacked bar (never a pie) followed by a table that carries
 * the numbers. items: [{label, value}] sorted by value, largest first; the
 * ramp is assigned by rank and everything past the fifth shares the lightest.
 */
export function parts(items, opts = {}) {
  const total = items.reduce((a, b) => a + b.value, 0) || 1;
  const fmt = opts.fmt || num;
  const bar = h('div', { class: 'stackbar' }, items.map((it, i) => h('i', {
    style: { width: (it.value / total) * 100 + '%', background: ramp(Math.min(i, 4)) },
    title: `${it.label}: ${fmt(it.value)} (${num((it.value / total) * 100, 1)}%)`,
  })));
  const table = h('table', { class: 'tbl compact' },
    h('tbody', null, items.map((it, i) => h('tr', null,
      h('td', null, h('i', { class: 'swatch', style: { background: ramp(Math.min(i, 4)) } }), it.label),
      h('td', { class: 'num' }, fmt(it.value)),
      h('td', { class: 'num faint' }, num((it.value / total) * 100, 1) + '%')))));
  return h('div', null, bar, h('div', { class: 'tbl-wrap', style: { marginTop: '6px' } }, table));
}

/** Presence strip: tiny bars, one per slice, for table rows. values: numbers. */
export function sparkline(values, opts = {}) {
  const w = opts.width || 96, hgt = opts.height || 22;
  const max = Math.max(...values, 1);
  const n = values.length;
  const bw = w / n;
  const s = svg('svg', { class: 'spark' + (opts.live ? ' live' : ''), width: w, height: hgt, viewBox: `0 0 ${w} ${hgt}`, role: 'img', 'aria-label': opts.label || '' });
  values.forEach((v, i) => {
    const bh = v > 0 ? Math.max(2, (v / max) * hgt) : 2;
    s.append(svg('rect', { class: v > 0 ? '' : 'off', x: i * bw + 0.5, y: hgt - bh, width: Math.max(1, bw - 1), height: bh }));
  });
  return s;
}

/** 7x24 heatmap; matrix[weekday 0=Sunday][hour] = value. One hue, intensity by value. */
export function heatmap(matrix, opts = {}) {
  let max = 0;
  matrix.forEach((r) => r.forEach((v) => { if (v > max) max = v; }));
  const order = isFa() ? [6, 0, 1, 2, 3, 4, 5] : [1, 2, 3, 4, 5, 6, 0];
  const names = (wd) => fmtWeekday(Date.UTC(2023, 0, 1 + wd, 12) / 1000);
  const grid = h('div', { class: 'heat', style: { gridTemplateColumns: '54px repeat(24, minmax(0, 1fr))' } });
  const tip = attachTip(grid);
  grid.append(h('div'));
  for (let hr = 0; hr < 24; hr++) grid.append(h('div', { class: 'lab' }, hr % 3 === 0 ? String(hr).padStart(2, '0') : ''));
  for (const wd of order) {
    grid.append(h('div', { class: 'lab day' }, names(wd)));
    for (let hr = 0; hr < 24; hr++) {
      const v = matrix[wd][hr];
      const a = max > 0 ? v / max : 0;
      const cell = h('div', { class: 'cell', style: { background: v > 0 ? `color-mix(in srgb, var(--accent) ${Math.round(14 + a * 86)}%, var(--n4))` : '' } });
      cell.addEventListener('mouseenter', () => {
        const r = cell.getBoundingClientRect(), g = grid.getBoundingClientRect();
        tip.show(r.left - g.left + r.width / 2, r.top - g.top - 4, [{ text: `${names(wd)} ${String(hr).padStart(2, '0')}:00, ${(opts.fmt || ((x) => dur(x)))(v)}` }]);
      });
      cell.addEventListener('mouseleave', tip.hide);
      grid.append(cell);
    }
  }
  return grid;
}

/**
 * Minute strip of one day: '0' idle, '1' background traffic, '2' interactive.
 */
export function strip(str, startSec) {
  const n = str.length;
  const H = 40;
  const wrap = h('div', { class: 'chart strip-box', style: { height: H + 20 + 'px' } });
  const tip = attachTip(wrap);
  const colors = { 1: 'var(--n2)', 2: 'var(--accent)' };
  function draw() {
    const W = Math.max(280, wrap.clientWidth);
    wrap.querySelectorAll('svg').forEach((x) => x.remove());
    const s = svg('svg', { width: W, height: H + 20, viewBox: `0 0 ${W} ${H + 20}` });
    s.append(svg('rect', { x: 0, y: 0, width: W, height: H, rx: 4, fill: 'var(--raised)' }));
    const mw = W / n;
    let i = 0;
    while (i < n) {
      const c = str[i];
      if (c === '0') { i++; continue; }
      let j = i;
      while (j < n && str[j] === c) j++;
      s.append(svg('rect', { x: i * mw, y: 2, width: Math.max(1.2, (j - i) * mw), height: H - 4, fill: colors[c] }));
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
      tip.show((m + 0.5) * mw, -2, [{ color: colors[str[m]], text: fmtTime(startSec + m * 60) + ', ' + state }]);
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
  const wrap = h('div', { class: 'gantt' });
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
