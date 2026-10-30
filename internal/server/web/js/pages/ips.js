import { api, guard } from '../api.js';
import { h, t, num, dur, flag, fmtDateTime, empty, icon } from '../util.js';

export async function ipsPage(ctx) {
  const page = h('div', { class: 'stack' });
  const result = h('div');
  const input = h('input', { class: 'input mono', placeholder: t('ip_search_ph'), value: ctx.query.get('ip') || '', style: { maxWidth: '320px' } });
  const run = async () => {
    const ip = input.value.trim();
    if (!ip) return;
    result.replaceChildren(h('div', { class: 'skeleton', style: { height: '120px' } }));
    const d = await guard(() => api.get('ips/' + encodeURIComponent(ip), { from: ctx.range.from, to: ctx.range.to }));
    if (!d) return result.replaceChildren();
    result.replaceChildren(render(d));
  };
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); });
  page.append(h('div', { class: 'row' }, input, h('button', { class: 'btn primary', onclick: run }, icon('search'), t('search')), h('span', { class: 'muted' }, t('ip_search_hint'))), result);
  if (input.value) run();
  return page;
}

function render(d) {
  const card = h('div', { class: 'card flush' });
  card.append(h('h2', null, h('span', { class: 'mono' }, d.ip.ip), d.ip.country ? h('span', { class: 'chip' }, flag(d.ip.country) + ' ' + d.ip.country) : null,
    d.ip.org ? h('span', { class: 'muted' }, d.ip.org + (d.ip.asn ? ' (AS' + d.ip.asn + ')' : '')) : null));
  if (!d.users.length) { card.append(empty(t('ip_none'))); return card; }
  card.append(h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, [t('col.user'), t('col.first'), t('col.last'), t('col.active'), t('col.sessions')].map((x) => h('th', null, x)))),
    h('tbody', null, d.users.map((u) => h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.user_id; } },
      h('td', null, h('b', null, u.email)), h('td', { class: 'muted' }, fmtDateTime(u.first)), h('td', { class: 'muted' }, fmtDateTime(u.last)),
      h('td', null, dur(u.minutes)), h('td', null, num(u.sessions))))))));
  if (d.users.length > 1) card.prepend(h('div', { class: 'alert warn', style: { margin: '14px 18px 0' } }, t('ip_shared', { n: num(d.users.length) })));
  return card;
}
