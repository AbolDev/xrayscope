import { api } from '../api.js';
import { h, t, num, bytes, dur, ago, catColor, catName, empty, icon, fmtDate, debounce, ltr } from '../util.js';

let sortKey = 'last_seen';
let sortDir = -1;
let query = '';
let filter = 'all';

const cols = [
  { key: 'email', label: 'col.user', get: (u) => u.email.toLowerCase() },
  { key: 'last_seen', label: 'col.last_seen', get: (u) => u.last_seen },
  { key: 'traffic', label: 'col.traffic', num: true, get: (u) => u.up + u.down },
  { key: 'minutes', label: 'col.active', num: true, get: (u) => u.minutes },
  { key: 'interactive', label: 'col.interactive', num: true, get: (u) => u.interactive },
  { key: 'ips', label: 'col.ips', num: true, get: (u) => u.max_ips * 1000 + u.ips },
  { key: 'top', label: 'col.top', get: (u) => u.top_category },
  { key: 'quota', label: 'col.quota', get: (u) => (u.quota ? (u.up_total + u.down_total) / u.quota : -1) },
  { key: 'expiry', label: 'col.expiry', get: (u) => u.expiry || 1e12 },
];

export async function usersPage(ctx) {
  const users = await api.get('users', { from: ctx.range.from, to: ctx.range.to });
  const page = h('div', { class: 'stack' });
  const body = h('tbody');
  const head = h('tr');
  const count = h('span', { class: 'muted' });

  const draw = () => {
    let list = users.filter((u) => u.email.toLowerCase().includes(query.toLowerCase()));
    if (filter === 'online') list = list.filter((u) => u.online);
    if (filter === 'over') list = list.filter((u) => u.limit_ip > 0 && u.max_ips > u.limit_ip);
    if (filter === 'inactive') list = list.filter((u) => u.minutes === 0);
    if (filter === 'disabled') list = list.filter((u) => !u.enabled);
    const col = cols.find((c) => c.key === sortKey) || cols[1];
    list.sort((a, b) => {
      const x = col.get(a), y = col.get(b);
      return (x < y ? -1 : x > y ? 1 : 0) * sortDir;
    });
    count.textContent = t('n_of', { a: num(list.length), b: num(users.length) });
    body.replaceChildren(...list.map(row));
    if (!list.length) body.replaceChildren(h('tr', null, h('td', { colspan: cols.length }, empty())));
    head.replaceChildren(...cols.map((c) => h('th', {
      class: 'sortable' + (c.num ? ' num' : ''),
      onclick: () => { if (sortKey === c.key) sortDir = -sortDir; else { sortKey = c.key; sortDir = c.key === 'email' ? 1 : -1; } draw(); },
    }, t(c.label), sortKey === c.key ? h('span', { class: 'arrow' }, sortDir > 0 ? '▲' : '▼') : null)));
  };

  const search = h('input', { class: 'input', placeholder: t('search_users'), value: query, style: { maxWidth: '280px' }, oninput: debounce((e) => { query = e.target.value; draw(); }, 120) });
  const seg = h('div', { class: 'seg' }, ['all', 'online', 'over', 'inactive', 'disabled'].map((k) =>
    h('button', { class: filter === k ? 'on' : '', onclick: (e) => { filter = k; seg.querySelectorAll('button').forEach((b) => b.classList.remove('on')); e.target.classList.add('on'); draw(); } }, t('filter.' + k))));
  page.append(h('div', { class: 'row' }, search, seg, h('div', { class: 'grow' }), count));
  page.append(h('div', { class: 'card flush' }, h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, head), body))));
  draw();
  return page;
}

function row(u) {
  const over = u.limit_ip > 0 && u.max_ips > u.limit_ip;
  const used = u.up_total + u.down_total;
  const pct = u.quota > 0 ? Math.min(100, (used / u.quota) * 100) : 0;
  const expired = u.expiry > 0 && u.expiry * 1000 < Date.now();
  return h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.id; } },
    h('td', null, h('div', { class: 'row', style: { gap: '9px', flexWrap: 'nowrap' } },
      h('span', { class: 'status-dot' + (u.online ? ' on' : ''), title: u.online ? t('online') : t('offline') }),
      h('b', { style: { fontWeight: 600 } }, u.email),
      !u.enabled ? h('span', { class: 'chip bad' }, t('disabled')) : null)),
    h('td', { class: 'muted' }, u.online ? h('span', { class: 'chip ok' }, t('online_n', { n: num(u.online_ips) })) : ago(u.last_seen)),
    h('td', { class: 'num' }, h('span', { style: { color: 'var(--down)' } }, '↓ ' + bytes(u.down)), ' ', h('span', { class: 'muted' }, '↑ ' + bytes(u.up))),
    h('td', { class: 'num' }, u.minutes ? dur(u.minutes) : '—'),
    h('td', { class: 'num' }, u.interactive ? dur(u.interactive) : '—'),
    h('td', { class: 'num' }, u.ips ? h('span', { class: 'chip' + (over ? ' bad' : '') }, icon('device'), num(u.ips) + (u.max_ips > 1 ? ' · ' + t('max') + ' ' + num(u.max_ips) : '') + (u.limit_ip ? ' / ' + num(u.limit_ip) : '')) : '—'),
    h('td', null, u.top_category ? h('span', { class: 'chip' }, h('span', { class: 'dot', style: { background: catColor(u.top_category) } }), u.top_service || catName(u.top_category)) : '—'),
    h('td', { style: { minWidth: '120px' } }, u.quota > 0 ? h('div', null, h('div', { class: 'progress' + (pct > 95 ? ' bad' : pct > 80 ? ' warn' : '') }, h('i', { style: { width: pct + '%' } })), h('div', { class: 'faint', style: { fontSize: '11px' } }, ltr(bytes(used) + ' / ' + bytes(u.quota)))) : h('span', { class: 'faint' }, t('unlimited'))),
    h('td', { class: expired ? '' : 'muted' }, u.expiry ? h('span', { class: expired ? 'chip bad' : '' }, fmtDate(u.expiry)) : '—'));
}
