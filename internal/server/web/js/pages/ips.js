import { api, guard } from '../api.js';
import { h, t, num, dur, flag, fmtDateTime, empty, icon, tag, panel, queryGet, queryPut } from '../util.js';

export async function ipsPage(ctx) {
  const page = h('div', { class: 'stack' });
  const result = h('div');
  const input = h('input', { class: 'input mono', type: 'search', placeholder: t('ip_search_ph'), value: queryGet('ip'), style: { maxWidth: '320px' }, 'aria-label': t('ip_search_ph') });
  const run = async () => {
    const ip = input.value.trim();
    queryPut({ ip });
    if (!ip) return result.replaceChildren();
    result.replaceChildren(h('div', { class: 'skeleton', style: { height: '120px' } }));
    const d = await guard(() => api.get('ips/' + encodeURIComponent(ip), { from: ctx.range.from, to: ctx.range.to }));
    result.replaceChildren(d ? render(d) : '');
  };
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); });
  page.append(h('div', { class: 'row' }, input, h('button', { class: 'btn pri', onclick: run }, icon('search'), t('search')), h('span', { class: 'muted' }, t('ip_search_hint'))), result);
  if (input.value) run();
  else result.append(panel(null, empty(t('ip_prompt'), t('ip_prompt_hint'))));
  return page;
}

function render(d) {
  const title = h('span', null, h('span', { class: 'mono' }, d.ip.ip), ' ', d.ip.country ? tag((flag(d.ip.country) + ' ' + d.ip.country).trim()) : null,
    d.ip.org ? h('span', { class: 'muted', style: { marginInlineStart: '8px' } }, d.ip.org + (d.ip.asn ? ' (AS' + d.ip.asn + ')' : '')) : null);
  const wrap = h('div');
  if (d.users.length > 1) wrap.append(h('div', { class: 'alert warn', style: { margin: '0 16px 8px' } }, t('ip_shared', { n: num(d.users.length) })));
  wrap.append(d.users.length ? h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, [t('col.user'), t('col.first'), t('col.last'), t('col.active'), t('col.sessions')].map((x) => h('th', null, x)))),
    h('tbody', null, d.users.map((u) => h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.user_id; } },
      h('td', null, h('b', null, u.email)), h('td', { class: 'muted' }, fmtDateTime(u.first)), h('td', { class: 'muted' }, fmtDateTime(u.last)),
      h('td', null, dur(u.minutes)), h('td', null, num(u.sessions))))))) : empty(t('ip_none')));
  const p = panel(null, wrap, null, true);
  p.prepend(h('div', { class: 'ph' }, h('h2', null, title)));
  p.querySelector('.pb').style.padding = '8px 0 0';
  return p;
}
