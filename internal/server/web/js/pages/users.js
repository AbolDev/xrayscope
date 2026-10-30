import { api } from '../api.js';
import { h, t, num, bytes, dur, ago, catName, empty, fmtDate, debounce, ltr, tag, queryGet, queryPut, panel } from '../util.js';
import { sparkline } from '../charts.js';

const cols = [
  { key: 'email', label: 'col.user', get: (u) => u.email.toLowerCase() },
  { key: 'spark', label: 'col.presence', nosort: true },
  { key: 'last_seen', label: 'col.last_seen', get: (u) => u.last_seen },
  { key: 'down', label: 'col.download', num: true, get: (u) => u.down },
  { key: 'up', label: 'col.upload', num: true, get: (u) => u.up },
  { key: 'minutes', label: 'col.active', num: true, get: (u) => u.minutes },
  { key: 'ips', label: 'col.ips', num: true, get: (u) => u.max_ips * 1000 + u.ips },
  { key: 'top', label: 'col.top', get: (u) => u.top_category },
  { key: 'quota', label: 'col.quota', get: (u) => (u.quota ? (u.up_total + u.down_total) / u.quota : -1) },
  { key: 'expiry', label: 'col.expiry', get: (u) => u.expiry || 1e12 },
];
const FILTERS = ['all', 'online', 'over', 'inactive', 'disabled'];

export async function usersPage(ctx) {
  const users = await api.get('users', { from: ctx.range.from, to: ctx.range.to });
  let query = queryGet('q');
  let filter = FILTERS.includes(queryGet('f')) ? queryGet('f') : 'all';
  let sortKey = cols.some((c) => c.key === queryGet('sort')) ? queryGet('sort') : 'last_seen';
  let sortDir = queryGet('dir') === 'asc' ? 1 : -1;

  const body = h('tbody');
  const head = h('tr');
  const count = h('span', { class: 'muted small' });
  const tableHost = h('div', { class: 'tbl-wrap' });

  const draw = () => {
    let list = users.filter((u) => u.email.toLowerCase().includes(query.toLowerCase()));
    if (filter === 'online') list = list.filter((u) => u.online);
    if (filter === 'over') list = list.filter((u) => u.limit_ip > 0 && u.max_ips > u.limit_ip);
    if (filter === 'inactive') list = list.filter((u) => u.minutes === 0);
    if (filter === 'disabled') list = list.filter((u) => !u.enabled);
    const col = cols.find((c) => c.key === sortKey) || cols[2];
    if (col.get) list.sort((a, b) => { const x = col.get(a), y = col.get(b); return (x < y ? -1 : x > y ? 1 : 0) * sortDir; });
    count.textContent = t('n_of', { a: num(list.length), b: num(users.length) });
    head.replaceChildren(...cols.map((c) => h('th', {
      class: (c.nosort ? '' : 'sort') + (c.num ? ' num' : ''),
      onclick: c.nosort ? null : () => {
        if (sortKey === c.key) sortDir = -sortDir; else { sortKey = c.key; sortDir = c.key === 'email' ? 1 : -1; }
        queryPut({ sort: sortKey, dir: sortDir > 0 ? 'asc' : 'desc' });
        draw();
      },
    }, t(c.label), sortKey === c.key ? h('span', { class: 'arrow' }, sortDir > 0 ? '▲' : '▼') : null)));
    if (!users.length) {
      tableHost.replaceChildren(empty(t('users.none'), t('users.none_hint'), h('a', { class: 'btn pri', href: '#/guide' }, t('nav.guide'))));
      return;
    }
    if (!list.length) {
      tableHost.replaceChildren(empty(t('users.no_match'), t('users.no_match_hint')));
      return;
    }
    body.replaceChildren(...list.map((u) => row(u)));
    tableHost.replaceChildren(h('table', { class: 'tbl' }, h('thead', null, head), body));
  };

  const search = h('input', {
    class: 'input', type: 'search', placeholder: t('search_users'), value: query, style: { maxWidth: '280px' }, 'aria-label': t('search_users'),
    oninput: debounce((e) => { query = e.target.value; queryPut({ q: query }); draw(); }, 120),
  });
  const seg = h('div', { class: 'seg' }, FILTERS.map((k) => h('button', {
    class: filter === k ? 'on' : '',
    onclick: (e) => { filter = k; queryPut({ f: k === 'all' ? '' : k }); seg.querySelectorAll('button').forEach((b) => b.classList.remove('on')); e.target.classList.add('on'); draw(); },
  }, t('filter.' + k))));

  const page = h('div', { class: 'stack' }, h('div', { class: 'row' }, search, seg, h('div', { class: 'spacer' }), count), panel(null, tableHost, null, true));
  page.querySelector('.panel > .pb').style.padding = '0';
  draw();
  return page;
}

function row(u) {
  const over = u.limit_ip > 0 && u.max_ips > u.limit_ip;
  const used = u.up_total + u.down_total;
  const pct = u.quota > 0 ? Math.min(100, (used / u.quota) * 100) : 0;
  const expired = u.expiry > 0 && u.expiry * 1000 < Date.now();
  return h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.id; } },
    h('td', null, h('span', { class: 'row', style: { gap: '8px', flexWrap: 'nowrap' } },
      h('span', { class: 'dot' + (u.online ? ' live' : ''), role: 'img', 'aria-label': u.online ? t('online') : t('offline'), title: u.online ? t('online') : t('offline') }),
      h('a', { href: '#/users/' + u.id, onclick: (e) => e.stopPropagation(), style: { fontWeight: 500 } }, u.email),
      !u.enabled ? tag(t('disabled'), 'bad') : null)),
    h('td', null, sparkline(u.spark || [], { label: t('col.presence'), live: u.online })),
    h('td', { class: 'muted' }, u.online ? t('online_n', { n: num(u.online_ips) }) : ago(u.last_seen)),
    h('td', { class: 'num' }, u.down ? bytes(u.down) : h('span', { class: 'faint' }, '0')),
    h('td', { class: 'num' }, u.up ? bytes(u.up) : h('span', { class: 'faint' }, '0')),
    h('td', { class: 'num' }, u.minutes ? dur(u.minutes) : h('span', { class: 'faint' }, '0')),
    h('td', { class: 'num' }, u.ips
      ? h('span', null, num(u.ips), u.max_ips > 1 || over ? h('span', { class: 'faint' }, ' ' + t('conc') + ' ' + num(u.max_ips)) : null, over ? [' ', tag(t('over_limit'), 'bad', t('limit_n', { n: num(u.limit_ip) }))] : null)
      : h('span', { class: 'faint' }, '0')),
    h('td', null, u.top_category ? (u.top_service || catName(u.top_category)) : h('span', { class: 'faint' }, '-')),
    h('td', { style: { minWidth: '120px' } }, u.quota > 0
      ? h('div', null, h('div', { class: 'meter' + (pct > 95 ? ' bad' : pct > 80 ? ' warn' : '') }, h('i', { style: { width: pct + '%' } })),
        h('div', { class: 'faint', style: { fontSize: 'var(--t-xs)' } }, ltr(bytes(used) + ' / ' + bytes(u.quota))))
      : h('span', { class: 'faint' }, t('unlimited'))),
    h('td', null, u.expiry ? (expired ? tag(fmtDate(u.expiry), 'bad') : h('span', { class: 'muted' }, fmtDate(u.expiry))) : h('span', { class: 'faint' }, '-')));
}
