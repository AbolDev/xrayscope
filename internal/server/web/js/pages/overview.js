import { api } from '../api.js';
import { h, t, num, bytes, dur, icon, catColor, catName, flag, empty, isBgCat } from '../util.js';
import { timeChart, barList, donut, legend, heatmap } from '../charts.js';

export function kpi(iconName, tone, value, label, sub) {
  return h('div', { class: 'card kpi' },
    h('div', { class: 'ico ' + (tone || '') }, icon(iconName)),
    h('div', null, h('div', { class: 'v' }, value), h('div', { class: 'l' }, label), sub ? h('div', { class: 's' }, sub) : null));
}

export async function overviewPage(ctx) {
  const o = await api.get('overview', { from: ctx.range.from, to: ctx.range.to });
  const T = o.totals;
  const page = h('div', { class: 'stack' });

  page.append(h('div', { class: 'grid g4' },
    kpi('live', 'ok', num(T.online_now), t('kpi.online'), t('kpi.ips_now', { n: num(T.ips_now) })),
    kpi('users', '', num(T.users_active), t('kpi.active_users'), t('kpi.of_total', { n: num(T.users_total) })),
    kpi('download', 'down', bytes(T.down), t('kpi.download')),
    kpi('upload', 'warn', bytes(T.up), t('kpi.upload'))));

  // traffic + activity
  const trafficCard = h('div', { class: 'card' }, h('h2', null, t('chart.traffic'), h('span', { class: 'hint' }, bytes(T.up + T.down))));
  if (o.traffic.points.length) {
    timeChart(trafficCard, {
      from: o.range.from, to: o.range.to, bucket: o.traffic.bucket, yFmt: bytes,
      series: [
        { label: t('download'), color: 'var(--down)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--up)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
  } else trafficCard.append(empty(t('no_traffic')));

  const actCard = h('div', { class: 'card' }, h('h2', null, t('chart.activity'), h('span', { class: 'hint' }, t('chart.activity_hint'))));
  if (o.activity.length) {
    const bucket = o.range.to - o.range.from > 4 * 86400 ? 86400 : 3600;
    timeChart(actCard, {
      from: o.range.from, to: o.range.to, bucket, labelShift: bucket === 86400 ? 43200 : 0, yFmt: (v) => num(v),
      series: [{ label: t('chart.users_active'), color: 'var(--primary)', points: o.activity.map((p) => ({ t: p.t, v: p.users })) }],
    });
  } else actCard.append(empty());

  page.append(h('div', { class: 'grid g2' }, trafficCard, actCard));

  // categories + services
  const cats = o.categories.filter((c) => c.minutes > 0 && !isBgCat(c.category));
  const catCard = h('div', { class: 'card' }, h('h2', null, t('chart.categories'), h('span', { class: 'hint' }, t('chart.categories_hint'))));
  if (cats.length) {
    const items = cats.map((c) => ({ label: catName(c.category), value: c.minutes, color: catColor(c.category) }));
    const total = items.reduce((a, b) => a + b.value, 0);
    catCard.append(h('div', { class: 'grid g2', style: { alignItems: 'center' } },
      donut(items, { fmt: dur, center: dur(total), centerSub: t('user_time') }),
      barList(items.slice(0, 8).map((i) => ({ ...i, text: dur(i.value) })))));
    catCard.append(legend(items.slice(0, 12)));
  } else catCard.append(empty());

  const svcCard = h('div', { class: 'card' }, h('h2', null, t('chart.top_services')));
  const svcs = o.services.filter((s) => s.minutes > 0 && s.service && !isBgCat(s.category));
  svcCard.append(svcs.length ? barList(svcs.slice(0, 10).map((s) => ({ label: s.service, value: s.minutes, text: dur(s.minutes), color: catColor(s.category) }))) : empty());
  page.append(h('div', { class: 'grid g2' }, catCard, svcCard));

  // heat map
  const heatCard = h('div', { class: 'card' }, h('h2', null, t('chart.heat'), h('span', { class: 'hint' }, t('chart.heat_hint'))));
  heatCard.append(heatmap(o.heatmap));
  page.append(heatCard);

  // rankings
  const topT = h('div', { class: 'card' }, h('h2', null, t('top.traffic')));
  topT.append(o.top_traffic.length ? barList(o.top_traffic.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.up + u.down, text: bytes(u.up + u.down), color: 'var(--down)' }))) : empty());
  const topA = h('div', { class: 'card' }, h('h2', null, t('top.active')));
  topA.append(o.top_active.length ? barList(o.top_active.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.minutes, text: dur(u.minutes), color: 'var(--primary)' }))) : empty());
  const topC = h('div', { class: 'card' }, h('h2', null, t('top.countries')));
  topC.append(o.countries.length ? barList(o.countries.slice(0, 8).map((c) => ({ label: flag(c.country) + ' ' + c.country, value: c.minutes, text: t('n_users', { n: num(c.users) }), color: 'var(--ok)' }))) : h('div', { class: 'muted' }, t('geo_hint')));
  page.append(h('div', { class: 'grid g3' }, topT, topA, topC));

  // online now
  if (o.online && o.online.length) {
    const on = h('div', { class: 'card' }, h('h2', null, t('online_now'), h('a', { href: '#/online', class: 'hint' }, t('see_all'))));
    on.append(h('div', { class: 'pill-list' }, o.online.slice(0, 16).map((u) => h('a', { class: 'chip ok', href: '#/users/' + u.user_id, style: { textDecoration: 'none' } },
      h('span', { class: 'status-dot on' }), u.email, u.conns.length > 1 ? ' · ' + num(u.conns.length) + ' IP' : ''))));
    page.append(on);
  }
  return page;
}
