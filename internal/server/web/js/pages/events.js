import { api } from '../api.js';
import { h, t, fmtDateTime, ago, empty } from '../util.js';

const TONE = { max_ips: 'bad', new_ip: 'warn', new_country: 'warn', traffic_spike: 'warn', quota: 'warn', expiry: 'warn', node_offline: 'bad', node_online: 'ok', log_stale: 'bad', backup: 'primary' };

export async function eventsPage() {
  const events = await api.get('events', { limit: 300 });
  const card = h('div', { class: 'card flush' });
  if (!events.length) { card.append(empty(t('no_events'))); return card; }
  card.append(h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, [t('col.time'), t('col.type'), t('col.details')].map((x) => h('th', null, x)))),
    h('tbody', null, events.map((e) => h('tr', null,
      h('td', { class: 'muted', title: fmtDateTime(e.ts) }, ago(e.ts)),
      h('td', null, h('span', { class: 'chip ' + (TONE[e.kind] || '') }, t('ev.' + e.kind))),
      h('td', { style: { whiteSpace: 'normal' } }, e.user_id ? h('a', { href: '#/users/' + e.user_id }, e.text) : e.text)))))));
  return card;
}
