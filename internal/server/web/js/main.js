import { api, setUnauthorizedHandler, guard } from './api.js';
import {
  h, clear, state, t, setLang, setTheme, icon, RANGES, currentRange, setRange, toast, ymd, isFa, modal, failure, queryPut,
} from './util.js';
import { loginPage } from './pages/login.js';
import { overviewPage } from './pages/overview.js';
import { usersPage } from './pages/users.js';
import { userPage } from './pages/user.js';
import { onlinePage } from './pages/online.js';
import { ipsPage } from './pages/ips.js';
import { eventsPage } from './pages/events.js';
import { nodesPage } from './pages/nodes.js';
import { settingsPage } from './pages/settings.js';
import { systemPage } from './pages/system.js';
import { guidePage } from './pages/guide.js';

const root = document.getElementById('app');

const ROUTES = [
  { path: 'overview', icon: 'dashboard', page: overviewPage, range: true, live: 30 },
  { path: 'users', icon: 'users', page: usersPage, range: true, live: 30 },
  { path: 'online', icon: 'live', page: onlinePage, live: 10, badge: true },
  { path: 'ips', icon: 'globe', page: ipsPage, range: true },
  { path: 'events', icon: 'bell', page: eventsPage, live: 30 },
  { path: 'nodes', icon: 'server', page: nodesPage, live: 30 },
  { path: 'guide', icon: 'shield', page: guidePage, live: 30 },
  { path: 'system', icon: 'database', page: systemPage, live: 30 },
  { path: 'settings', icon: 'settings', page: settingsPage },
];

let timer = null;
let renderToken = 0;

function parseHash() {
  const raw = location.hash.replace(/^#\/?/, '');
  const [pathPart, query = ''] = raw.split('?');
  return { parts: pathPart.split('/').filter(Boolean), query: new URLSearchParams(query) };
}

export function go(path) { location.hash = '#/' + path; }

async function boot() {
  setUnauthorizedHandler(showLogin);
  try {
    state.meta = await api.get('meta');
    setLang(state.lang);
    start();
  } catch (e) {
    if (e.status === 401 || e.status === 0) showLogin(); else toast(e.message, 'bad');
  }
}

function showLogin() {
  clearInterval(timer);
  clearInterval(window.__xsBadge);
  state.meta = state.meta || { categories: [], timezone: 'Local' };
  clear(root).append(loginPage(async () => {
    state.meta = await api.get('meta');
    start();
  }, showLogin));
}

// The range lives in the URL (?range=7d or ?from=..&to=..) and in the
// browser; the URL wins so a link opens the same view.
function adoptRange(query) {
  const r = query.get('range');
  if (r && RANGES.includes(r) && r !== 'custom') setRange(r);
  const from = Number(query.get('from')), to = Number(query.get('to'));
  if (from > 0 && to > from) setRange('custom', { from, to });
}

function start() {
  clear(root);
  const side = h('aside', { class: 'side' });
  const main = h('div', { class: 'main' });
  const content = h('div', { class: 'content' });
  const title = h('h1');
  const topbar = h('div', { class: 'topbar' });
  root.append(h('div', { class: 'shell' }, side, main));
  main.append(topbar, content);

  const menuBtn = h('button', { class: 'btn icon menu-btn', 'aria-label': 'Menu', onclick: () => side.classList.toggle('open') }, icon('menu'));
  const rangeBox = h('div', { class: 'row' });
  const themeBtn = h('button', { class: 'btn icon', title: t('theme'), 'aria-label': t('theme'), onclick: () => {
    setTheme(state.theme === 'dark' ? 'light' : 'dark');
    themeBtn.replaceChildren(icon(state.theme === 'dark' ? 'sun' : 'moon'));
  } }, icon(state.theme === 'dark' ? 'sun' : 'moon'));
  const langBtn = h('button', { class: 'btn', onclick: () => { setLang(isFa() ? 'en' : 'fa'); start(); } }, isFa() ? 'English' : 'فارسی');
  const outBtn = h('button', { class: 'btn icon', title: t('logout'), 'aria-label': t('logout'), onclick: async () => { await guard(() => api.post('logout')); showLogin(); } }, icon('logout'));
  topbar.append(menuBtn, title, h('div', { class: 'spacer' }), rangeBox, langBtn, themeBtn, outBtn);

  const navLinks = [];
  const nav = h('nav', { class: 'nav' });
  for (const r of ROUTES) {
    if (r.path === 'system') nav.append(h('div', { class: 'rule' }));
    const badge = r.badge ? h('span', { class: 'count hidden' }) : null;
    const a = h('a', { href: '#/' + r.path, onclick: () => side.classList.remove('open') }, icon(r.icon), h('span', null, t('nav.' + r.path)), badge);
    r.badgeEl = badge;
    navLinks.push([r, a]);
    nav.append(a);
  }
  side.append(
    h('div', { class: 'brand' }, h('img', { src: 'favicon.svg', alt: '' }), h('div', null, 'XrayScope', h('small', null, 'v' + state.meta.version))),
    nav,
    h('div', { class: 'foot' }, t('foot')),
  );

  async function render() {
    const my = ++renderToken;
    clearInterval(timer);
    const { parts, query } = parseHash();
    adoptRange(query);
    const route = ROUTES.find((r) => r.path === (parts[0] || 'overview')) || ROUTES[0];
    for (const [r, a] of navLinks) a.classList.toggle('on', r === route);
    const isUser = route.path === 'users' && parts[1];
    title.textContent = isUser ? t('user') : t('nav.' + route.path);
    buildRange(rangeBox, route, render);

    const ctx = { parts: parts.slice(1), query, range: currentRange(), go, refresh: () => paint(true) };
    const page = isUser ? userPage : route.page;
    const paint = async (soft) => {
      if (my !== renderToken) return;
      ctx.range = currentRange();
      if (!soft) content.replaceChildren(h('div', { class: 'skeleton', style: { height: '220px' } }));
      try {
        const el = await page(ctx);
        if (my === renderToken) {
          const top = window.scrollY;
          content.replaceChildren(el);
          if (soft) window.scrollTo(0, top);
        }
      } catch (e) {
        if (e.status === 401 || my !== renderToken) return;
        if (!soft) content.replaceChildren(h('div', { class: 'panel' }, failure(e.message || String(e), () => paint(false))));
      }
    };
    await paint(false);
    if (route.live && my === renderToken) timer = setInterval(() => { if (!document.hidden) paint(true); }, route.live * 1000);
    refreshBadge(navLinks);
  }

  window.onhashchange = render;
  clearInterval(window.__xsBadge);
  window.__xsBadge = setInterval(() => { if (!document.hidden) refreshBadge(navLinks); }, 20000);
  if (!location.hash) location.hash = '#/overview';
  else render();
}

async function refreshBadge(navLinks) {
  try {
    const st = await api.get('status');
    for (const [r] of navLinks) {
      if (r.badgeEl) {
        r.badgeEl.textContent = String(st.online);
        r.badgeEl.classList.toggle('hidden', st.online === 0);
      }
    }
  } catch (e) { /* the badge is optional */ }
}

function buildRange(box, route, rerender) {
  clear(box);
  if (!route.range) return;
  const seg = h('div', { class: 'seg' });
  for (const k of RANGES) {
    if (k === 'custom') continue;
    seg.append(h('button', { class: state.rangeKey === k ? 'on' : '', onclick: () => { setRange(k); queryPut({ range: k, from: '', to: '' }); rerender(); } }, t('range.' + k)));
  }
  seg.append(h('button', { class: state.rangeKey === 'custom' ? 'on' : '', onclick: () => customDialog(rerender) }, t('range.custom')));
  box.append(seg);
}

function customDialog(rerender) {
  const now = Math.floor(Date.now() / 1000);
  const from = h('input', { class: 'input', type: 'date', value: ymd(now - 7 * 86400) });
  const to = h('input', { class: 'input', type: 'date', value: ymd(now) });
  modal(t('range.custom'), h('div', { class: 'cols c2' },
    h('div', { class: 'field' }, h('label', null, t('from')), from),
    h('div', { class: 'field' }, h('label', null, t('to')), to)), [
    { label: t('cancel') },
    { label: t('apply'), kind: 'primary', onclick: () => {
      const f = Date.parse(from.value + 'T00:00:00') / 1000, tt = Date.parse(to.value + 'T23:59:59') / 1000;
      if (!(f < tt)) { toast(t('err.range'), 'bad'); return false; }
      setRange('custom', { from: Math.floor(f), to: Math.floor(tt) });
      queryPut({ range: '', from: Math.floor(f), to: Math.floor(tt) });
      rerender();
    } },
  ]);
}

boot();
