import { api } from '../api.js';
import { h, t, num, ago, flag, empty, icon } from '../util.js';

export async function onlinePage() {
  const list = await api.get('online');
  const page = h('div', { class: 'stack' });
  page.append(h('div', { class: 'row' },
    h('span', { class: 'chip ok' }, h('span', { class: 'status-dot on' }), t('online_n', { n: num(list.length) })),
    h('span', { class: 'muted' }, t('online_hint'))));
  if (!list.length) {
    page.append(h('div', { class: 'card' }, empty(t('nobody_online'))));
    return page;
  }
  page.append(h('div', { class: 'grid g3' }, list.map((u) => {
    const over = u.limit_ip > 0 && u.conns.length > u.limit_ip;
    return h('div', { class: 'online-card' + (over ? ' over' : '') },
      h('div', { class: 'row', style: { flexWrap: 'nowrap' } },
        h('span', { class: 'status-dot on' }),
        h('a', { href: '#/users/' + u.user_id, style: { fontWeight: 700 } }, u.email),
        h('div', { class: 'grow' }),
        over ? h('span', { class: 'chip bad' }, icon('warn'), t('over_limit')) : null,
        h('span', { class: 'chip' }, icon('device'), num(u.conns.length) + (u.limit_ip ? ' / ' + num(u.limit_ip) : ''))),
      h('div', { class: 'ips' }, u.conns.map((c) => h('div', null,
        h('a', { class: 'mono', href: '#/ips?ip=' + encodeURIComponent(c.ip) }, c.ip),
        c.cdn ? h('span', { class: 'chip warn', title: t('cdn.hint') }, c.cdn) : null,
        h('span', null, flag(c.country) + ' ' + (c.country || '')),
        h('span', { class: 'faint', style: { overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', maxWidth: '180px' } }, c.org || ''),
        h('span', { class: 'faint', style: { marginInlineStart: 'auto' } }, ago(c.last_seen))))));
  })));
  return page;
}
