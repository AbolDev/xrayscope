import { api } from '../api.js';
import { h, t, num, ago, flag, empty, tag, panel } from '../util.js';

export async function onlinePage() {
  const list = await api.get('online');
  const page = h('div', { class: 'stack' });
  page.append(h('div', { class: 'row' }, h('b', null, t('online_n', { n: num(list.length) })), h('span', { class: 'muted' }, t('online_hint'))));
  if (!list.length) {
    page.append(panel(null, empty(t('nobody_online'), t('nobody_online_hint'))));
    return page;
  }
  const rows = list.map((u) => {
    const over = u.limit_ip > 0 && u.conns.length > u.limit_ip;
    return h('div', { class: 'live-row' + (over ? ' over' : '') },
      h('div', null, h('a', { href: '#/users/' + u.user_id, style: { fontWeight: 600 } }, u.email),
        h('div', { class: 'muted small' }, t('n_ips', { n: num(u.conns.length) }) + (u.limit_ip ? ' ' + t('of') + ' ' + num(u.limit_ip) : ''))),
      h('div', { class: 'ips' }, u.conns.map((c) => h('div', null,
        h('a', { class: 'mono', href: '#/ips?ip=' + encodeURIComponent(c.ip) }, c.ip),
        c.cdn ? tag(c.cdn, 'warn', t('cdn.hint')) : null,
        c.country ? h('span', null, (flag(c.country) + ' ' + c.country).trim()) : null,
        c.org ? h('span', { class: 'faint small' }, c.org) : null,
        h('span', { class: 'faint small' }, ago(c.last_seen))))),
      over ? tag(t('over_limit'), 'bad') : h('span'));
  });
  page.append(panel(null, h('div', null, rows), null, true));
  page.querySelector('.panel > .pb').style.padding = '0';
  return page;
}
