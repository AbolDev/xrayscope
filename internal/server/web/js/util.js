// Small DOM, formatting and state helpers shared by every page.
import { dict } from './lang.js';

export const state = {
  lang: load('xs.lang', 'en'),
  theme: document.documentElement.getAttribute('data-theme') || 'light',
  meta: null,
  rangeKey: load('xs.range', '24h'),
  custom: JSON.parse(load('xs.custom', 'null')),
};

function load(key, def) {
  try { return localStorage.getItem(key) || def; } catch (e) { return def; }
}
export function save(key, val) {
  try { localStorage.setItem(key, val); } catch (e) { /* storage may be blocked */ }
}

// ---------------------------------------------------------------- DOM

// h builds an element. Text is always inserted as text nodes, never HTML:
// domains and e-mails come from user traffic and must not be trusted.
export function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k === 'class') el.className = v;
      else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (v === true) el.setAttribute(k, '');
      else el.setAttribute(k, v);
    }
  }
  append(el, kids);
  return el;
}

export function append(el, kids) {
  for (const k of kids.flat(Infinity)) {
    if (k == null || k === false) continue;
    el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  }
  return el;
}

export function svg(tag, attrs, ...kids) {
  const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null) el.setAttribute(k, v);
  append(el, kids);
  return el;
}

export function clear(el) { el.replaceChildren(); return el; }

// ---------------------------------------------------------------- i18n

export function t(key, vars) {
  let s = (dict[state.lang] && dict[state.lang][key]) || dict.en[key] || key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll('{' + k + '}', v);
  return s;
}
export const isFa = () => state.lang === 'fa';
export const locale = () => (isFa() ? 'fa-IR' : 'en-GB');

export function setLang(lang) {
  state.lang = lang;
  save('xs.lang', lang);
  const root = document.documentElement;
  root.setAttribute('lang', lang);
  root.setAttribute('dir', lang === 'fa' ? 'rtl' : 'ltr');
}
export function setTheme(theme) {
  state.theme = theme;
  save('xs.theme', theme);
  document.documentElement.setAttribute('data-theme', theme);
}

// ---------------------------------------------------------------- numbers and time

const nf = {};
export function num(n, digits = 0) {
  const key = locale() + digits;
  nf[key] = nf[key] || new Intl.NumberFormat(locale(), { maximumFractionDigits: digits });
  return nf[key].format(n);
}

// ltr isolates a left-to-right fragment (numbers with Latin units) inside
// right-to-left text.
export const ltr = (s) => '\u2066' + s + '\u2069';

// digits converts Latin digits to Persian ones when the UI is Persian.
export const digits = (s) => (isFa() ? String(s).replace(/\d/g, (d) => '\u06f0\u06f1\u06f2\u06f3\u06f4\u06f5\u06f6\u06f7\u06f8\u06f9'[d]) : String(s));

export function bytes(n) {
  n = Number(n) || 0;
  if (n < 1024) return ltr(num(n) + ' B');
  const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < units.length - 1);
  return ltr(num(n, n >= 100 ? 0 : n >= 10 ? 1 : 2) + ' ' + units[i]);
}

// duration formats minutes as "2h 30m" / "۲ ساعت و ۳۰ دقیقه".
export function dur(min) {
  min = Math.max(0, Math.round(Number(min) || 0));
  if (min < 1) return isFa() ? '۰ ' + t('u.min') : '0m';
  const d = Math.floor(min / 1440), hh = Math.floor((min % 1440) / 60), m = min % 60;
  const parts = [];
  if (d) parts.push(num(d) + (isFa() ? ' ' + t('u.day') : 'd'));
  if (hh) parts.push(num(hh) + (isFa() ? ' ' + t('u.hour') : 'h'));
  if (m && d === 0) parts.push(num(m) + (isFa() ? ' ' + t('u.min') : 'm'));
  return parts.join(isFa() ? ' ' + t('u.and') + ' ' : ' ');
}

export function tzName() { return state.meta && state.meta.timezone && state.meta.timezone !== 'Local' ? state.meta.timezone : undefined; }

function fmt(opts, sec) {
  const base = isFa() ? 'fa-IR-u-ca-persian' : 'en-GB';
  try {
    return new Intl.DateTimeFormat(base, { timeZone: tzName(), ...opts }).format(new Date(sec * 1000));
  } catch (e) {
    return new Intl.DateTimeFormat(base, opts).format(new Date(sec * 1000));
  }
}
export const fmtTime = (sec) => fmt({ hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }, sec);
export const fmtDate = (sec) => fmt({ year: 'numeric', month: 'short', day: 'numeric' }, sec);
export const fmtDay = (sec) => fmt({ month: 'short', day: 'numeric' }, sec);
export const fmtWeekday = (sec) => fmt({ weekday: 'short' }, sec);
export const fmtDateTime = (sec) => fmtDate(sec) + ' ' + fmtTime(sec);
export const fmtFullDay = (sec) => fmt({ weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }, sec);
// fmtDateStr formats a YYYY-MM-DD calendar date (no time zone involved).
export function fmtDateStr(date, opts) {
  const base = isFa() ? 'fa-IR-u-ca-persian' : 'en-GB';
  return new Intl.DateTimeFormat(base, { timeZone: 'UTC', ...(opts || { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' }) }).format(new Date(date + 'T12:00:00Z'));
}
export const dateStrToSec = (date) => Date.parse(date + 'T12:00:00Z') / 1000;

export function ago(sec) {
  if (!sec) return '—';
  const d = Math.max(0, Math.floor(Date.now() / 1000 - sec));
  if (d < 45) return t('ago.now');
  if (d < 3600) return t('ago.min', { n: num(Math.round(d / 60)) });
  if (d < 86400) return t('ago.hour', { n: num(Math.round(d / 3600)) });
  if (d < 86400 * 60) return t('ago.day', { n: num(Math.round(d / 86400)) });
  return fmtDate(sec);
}

// ymd returns the YYYY-MM-DD date of a unix time in the display timezone.
export function ymd(sec) {
  const p = new Intl.DateTimeFormat('en-CA', { timeZone: tzName(), year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(sec * 1000));
  return p;
}

// ---------------------------------------------------------------- ranges

const DAY = 86400;

// localMidnight returns the unix time of 00:00 in the display timezone for
// the day containing sec.
export function localMidnight(sec) {
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone: tzName(), hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(sec * 1000));
  const g = (type) => Number(parts.find((p) => p.type === type).value);
  return sec - (g('hour') * 3600 + g('minute') * 60 + g('second'));
}

export const RANGES = ['today', '24h', '7d', '30d', '90d', '365d', 'custom'];

export function currentRange() {
  const now = Math.floor(Date.now() / 1000);
  const k = state.rangeKey;
  if (k === 'today') return { from: localMidnight(now), to: now };
  if (k === 'custom' && state.custom) return { from: state.custom.from, to: state.custom.to };
  const days = { '24h': 1, '7d': 7, '30d': 30, '90d': 90, '365d': 365 }[k] || 1;
  return { from: now - days * DAY, to: now };
}
export function setRange(key, custom) {
  state.rangeKey = key;
  save('xs.range', key);
  if (custom) { state.custom = custom; save('xs.custom', JSON.stringify(custom)); }
}

// ---------------------------------------------------------------- misc

// Data colours: one accent and a lightness ramp of it, never a rainbow.
export const ramp = (i) => `var(--d${Math.min(5, Math.max(1, i + 1))})`;

// tag renders a short status label (text, never a bare coloured dot).
export const tag = (text, tone, title) => h('span', { class: 'tag' + (tone ? ' ' + tone : ''), title }, text);

// stats renders the figure strip: [{v, l, s}].
export function stats(items) {
  return h('div', { class: 'stats' }, items.map((x) => h('div', { class: 'stat' },
    h('div', { class: 'v' }, x.v), h('div', { class: 'l' }, x.l), x.s ? h('div', { class: 's' }, x.s) : null)));
}

// panel builds a titled container. aside is optional text on the far side.
export function panel(title, body, aside, flush) {
  return h('section', { class: 'panel' + (flush ? ' flush' : '') },
    title ? h('div', { class: 'ph' }, h('h2', null, title), aside != null ? h('span', { class: 'aside' }, aside) : null) : null,
    h('div', { class: 'pb' }, body));
}

// URL state: filters, tabs and paging live in the hash query so a view can
// be shared and survives a reload. Updates do not re-render the page.
export function queryGet(name, def = '') {
  const q = new URLSearchParams((location.hash.split('?')[1] || ''));
  return q.get(name) ?? def;
}
export function queryPut(values) {
  const [path, query = ''] = location.hash.split('?');
  const q = new URLSearchParams(query);
  for (const [k, v] of Object.entries(values)) {
    if (v === '' || v == null) q.delete(k); else q.set(k, v);
  }
  const s = q.toString();
  history.replaceState(null, '', path + (s ? '?' + s : ''));
}

export const catName = (id) => t('cat.' + id);
export const isBgCat = (id) => { const c = state.meta && state.meta.categories.find((x) => x.id === id); return !!(c && c.background); };

export function flag(cc) {
  if (!cc || cc.length !== 2) return '';
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1a5 + c.charCodeAt(0)));
}

export function debounce(fn, ms) {
  let id;
  return (...a) => { clearTimeout(id); id = setTimeout(() => fn(...a), ms); };
}

export function toast(msg, kind) {
  let wrap = document.querySelector('.toast-wrap');
  if (!wrap) { wrap = h('div', { class: 'toast-wrap' }); document.body.append(wrap); }
  const el = h('div', { class: 'toast ' + (kind || '') }, msg);
  wrap.append(el);
  setTimeout(() => el.remove(), kind === 'bad' ? 7000 : 3500);
}

export function modal(title, body, actions) {
  const close = () => bg.remove();
  const bg = h('div', { class: 'modal-bg', onclick: (e) => { if (e.target === bg) close(); } },
    h('div', { class: 'modal', role: 'dialog' }, h('h3', null, title), body,
      h('div', { class: 'actions' }, (actions || []).map((a) => h('button', {
        class: 'btn ' + (a.kind === 'primary' ? 'pri' : a.kind || ''),
        onclick: async () => { if (!a.onclick || (await a.onclick(close)) !== false) close(); },
      }, a.label)))));
  document.body.append(bg);
  return close;
}

export function confirmBox(title, text, danger) {
  return new Promise((resolve) => {
    modal(title, h('p', null, text), [
      { label: t('cancel'), onclick: () => resolve(false) },
      { label: t('confirm'), kind: danger ? 'danger' : 'primary', onclick: () => resolve(true) },
    ]);
  });
}

// empty says what belongs here and, when there is one, the action that fills it.
export function empty(text, hint, action) {
  return h('div', { class: 'empty' }, h('b', null, text || t('no_data')), hint ? h('div', null, hint) : null, action || null);
}

// failure is the error state of a region: what broke and what to do.
export function failure(message, retry) {
  return h('div', { class: 'empty' }, h('b', null, t('err.title')), h('div', null, message),
    retry ? h('button', { class: 'btn', onclick: retry }, icon('refresh'), t('err.retry')) : null);
}

// Inline SVG icons (stroke style).
const ICONS = {
  dashboard: 'M3 13h8V3H3v10zm0 8h8v-6H3v6zm10 0h8V11h-8v10zm0-18v6h8V3h-8z',
  users: 'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75',
  live: 'M5 12.55a11 11 0 0 1 14.08 0M1.42 9a16 16 0 0 1 21.16 0M8.53 16.11a6 6 0 0 1 6.95 0M12 20h.01',
  globe: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM2 12h20M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z',
  bell: 'M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9M13.73 21a2 2 0 0 1-3.46 0',
  server: 'M2 4h20v6H2zM2 14h20v6H2zM6 7h.01M6 17h.01',
  settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z',
  activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
  upload: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M17 8l-5-5-5 5M12 3v12',
  download: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3',
  clock: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 6v6l4 2',
  moon: 'M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z',
  sun: 'M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10zM12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42',
  search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.35-4.35',
  menu: 'M3 12h18M3 6h18M3 18h18',
  logout: 'M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9',
  inbox: 'M22 12h-6l-2 3h-4l-2-3H2M5.45 5.11L2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z',
  refresh: 'M23 4v6h-6M1 20v-6h6M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15',
  shield: 'M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z',
  database: 'M12 8c4.97 0 9-1.34 9-3s-4.03-3-9-3-9 1.34-9 3 4.03 3 9 3zM21 12c0 1.66-4 3-9 3s-9-1.34-9-3M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5',
  plus: 'M12 5v14M5 12h14',
  trash: 'M3 6h18M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6M10 11v6M14 11v6M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2',
  copy: 'M20 9h-9a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h9a2 2 0 0 0 2-2v-9a2 2 0 0 0-2-2zM5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1',
  chevron: 'M9 18l6-6-6-6',
  device: 'M5 2h14a2 2 0 0 1 2 2v16a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zM12 18h.01',
  warn: 'M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0zM12 9v4M12 17h.01',
};
export function icon(name) {
  return svg('svg', { viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': 1.9, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' },
    svg('path', { d: ICONS[name] || ICONS.activity }));
}
