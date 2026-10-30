import { api } from '../api.js';
import { h, t, fmtDateTime, ago, empty, tag, panel } from '../util.js';

const TONE = { max_ips: 'bad', new_ip: 'warn', new_country: 'warn', traffic_spike: 'warn', quota: 'warn', expiry: 'warn', node_offline: 'bad', node_online: 'ok', log_stale: 'bad', backup: 'acc' };

export async function eventsPage() {
  const events = await api.get('events', { limit: 300 });
  if (!events.length) return panel(null, empty(t('no_events'), t('no_events_hint'), h('a', { class: 'btn', href: '#/settings' }, t('nav.settings'))));
  const table = h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, [t('col.time'), t('col.type'), t('col.details')].map((x) => h('th', null, x)))),
    h('tbody', null, events.map((e) => h('tr', null,
      h('td', { class: 'muted', title: fmtDateTime(e.ts) }, ago(e.ts)),
      h('td', null, tag(t('ev.' + e.kind), TONE[e.kind])),
      h('td', { class: 'wrap' }, e.user_id ? h('a', { href: '#/users/' + e.user_id }, e.text) : e.text))))));
  const p = panel(null, table, null, true);
  p.querySelector('.pb').style.padding = '0';
  return p;
}
