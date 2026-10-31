import { api } from '../api.js';
import { h, t, num, ago, flag, empty, tag, panel, rate } from '../util.js';

export async function onlinePage(ctx) {
  const list = await api.get('online');
  const page = h('div', { class: 'stack' });
  const total = h('span', { class: 'faint' });
  page.append(h('div', { class: 'row' }, h('b', null, t('online_n', { n: num(list.length) })), h('span', { class: 'muted' }, t('online_hint')), h('div', { class: 'spacer' }), total));
  if (!list.length) {
    page.append(panel(null, empty(t('nobody_online'), t('nobody_online_hint'))));
    ctx.live((snap) => { if (snap.online.users > 0) ctx.refresh(); });
    return page;
  }
  const speedCells = new Map();
  const rows = list.map((u) => {
    const over = u.limit_ip > 0 && u.conns.length > u.limit_ip;
    const down = h('span', { class: 'mono' }, '-');
    const up = h('span', { class: 'mono faint' }, '');
    speedCells.set(u.email, { down, up });
    return h('div', { class: 'live-row' + (over ? ' over' : '') },
      h('div', null, h('span', { class: 'row', style: { gap: '8px', flexWrap: 'nowrap' } }, h('span', { class: 'dot live' }), h('a', { href: '#/users/' + u.user_id, style: { fontWeight: 500 } }, u.email)),
        h('div', { class: 'muted small' }, t('n_ips', { n: num(u.conns.length) }) + (u.limit_ip ? ' ' + t('of') + ' ' + num(u.limit_ip) : ''))),
      h('div', { class: 'ips' }, u.conns.map((c) => h('div', null,
        h('a', { class: 'mono', href: '#/ips?ip=' + encodeURIComponent(c.ip) }, c.ip),
        c.cdn ? tag(c.cdn, 'warn', t('cdn.hint')) : null,
        c.country ? h('span', null, (flag(c.country) + ' ' + c.country).trim()) : null,
        c.org ? h('span', { class: 'faint small' }, c.org) : null,
        h('span', { class: 'faint small' }, ago(c.last_seen))))),
      h('div', { style: { textAlign: 'end', minWidth: '120px' } }, h('div', null, down), h('div', null, up), over ? tag(t('over_limit'), 'bad') : null));
  });
  page.append(panel(null, h('div', null, rows), null, true));
  page.querySelector('.panel > .pb').style.padding = '0';

  const sig0 = list.map((u) => u.user_id + ':' + u.conns.length).sort().join(',');
  const show = (sp) => {
    const by = new Map(sp.users.map((x) => [x.email, x]));
    for (const [email, cell] of speedCells) {
      const s = by.get(email);
      cell.down.textContent = s ? t('speed.down') + ' ' + rate(s.down) : (sp.available ? t('speed.idle') : '-');
      cell.up.textContent = s ? t('speed.up') + ' ' + rate(s.up) : '';
    }
    total.textContent = sp.available ? t('speed.down') + ' ' + rate(sp.total.down) + '   ' + t('speed.up') + ' ' + rate(sp.total.up) : t('speed.off');
  };
  // new arrivals and departures show up at once instead of at the next refresh
  ctx.live((snap) => {
    if (snap.sig !== sig0) { ctx.refresh(); return; }
    show(snap.speed);
  }, async () => show(await api.get('speed')));
  return page;
}
