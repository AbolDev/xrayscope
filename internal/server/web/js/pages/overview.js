import { api } from '../api.js';
import { h, t, num, bytes, dur, catName, flag, empty, isBgCat, panel, stats, tag, ago } from '../util.js';
import { timeChart, rank, parts, heatmap } from '../charts.js';

export async function overviewPage(ctx) {
  const o = await api.get('overview', { from: ctx.range.from, to: ctx.range.to });
  const T = o.totals;
  o.online = o.online || [];
  const page = h('div', { class: 'stack' });

  // A thin log shows an empty screen with a way out instead of zeros everywhere.
  if (T.reqs === 0 && T.up + T.down === 0 && T.users_total === 0) {
    page.append(panel(null, empty(t('ov.empty'), t('ov.empty_hint'), h('a', { class: 'btn pri', href: '#/guide' }, t('nav.guide')))));
    return page;
  }

  page.append(stats([
    { v: num(T.online_now), l: t('kpi.online'), s: t('kpi.ips_now', { n: num(T.ips_now) }) },
    { v: num(T.users_active), l: t('kpi.active_users'), s: t('kpi.of_total', { n: num(T.users_total) }) },
    { v: bytes(T.down), l: t('kpi.download') },
    { v: bytes(T.up), l: t('kpi.upload') },
  ]));

  // traffic, with the people online beside it
  const traffic = h('div');
  if (o.traffic.points.length) {
    timeChart(traffic, {
      from: o.range.from, to: o.range.to, bucket: o.traffic.bucket, yFmt: bytes, height: 230,
      series: [
        { label: t('download'), color: 'var(--d1)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--d3)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
  } else traffic.append(empty(t('no_traffic'), t('no_traffic_hint')));

  const live = h('div');
  if (o.online.length) {
    live.append(h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' }, h('tbody', null, o.online.slice(0, 8).map((u) => h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.user_id; } },
      h('td', null, u.email),
      h('td', { class: 'num muted' }, t('n_ips', { n: num(u.conns.length) })),
      h('td', { class: 'num faint' }, ago(u.last_seen))))))));
  } else live.append(empty(t('nobody_online')));

  page.append(h('div', { class: 'cols c-main' },
    panel(t('chart.traffic'), traffic, bytes(T.up + T.down)),
    panel(t('online_now'), live, h('a', { href: '#/online' }, t('see_all')), true)));

  // activity and where the time goes
  const act = h('div');
  if (o.activity.length) {
    const bucket = o.range.to - o.range.from > 4 * 86400 ? 86400 : 3600;
    timeChart(act, {
      from: o.range.from, to: o.range.to, bucket, labelShift: bucket === 86400 ? 43200 : 0, height: 200,
      series: [{ label: t('chart.users_active'), color: 'var(--d1)', points: o.activity.map((p) => ({ t: p.t, v: p.users })) }],
    });
  } else act.append(empty(t('no_data')));

  const cats = o.categories.filter((c) => c.minutes > 0 && !isBgCat(c.category));
  const time = h('div');
  if (cats.length) time.append(parts(cats.slice(0, 7).map((c) => ({ label: catName(c.category), value: c.minutes })), { fmt: dur }));
  else time.append(empty(t('no_data')));
  page.append(h('div', { class: 'cols c2' },
    panel(t('chart.activity'), act, t('chart.activity_hint')),
    panel(t('chart.categories'), time, t('chart.categories_hint'))));

  // rankings
  const svcs = o.services.filter((s) => s.minutes > 0 && s.service && !isBgCat(s.category));
  const topSvc = svcs.length ? rank(svcs.slice(0, 8).map((s) => ({ label: s.service, value: s.minutes, text: dur(s.minutes) }))) : empty(t('no_data'));
  const topT = o.top_traffic.length ? rank(o.top_traffic.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.up + u.down, text: bytes(u.up + u.down) }))) : empty(t('no_data'));
  const topA = o.top_active.length ? rank(o.top_active.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.minutes, text: dur(u.minutes) }))) : empty(t('no_data'));
  const countries = o.countries.length
    ? rank(o.countries.slice(0, 8).map((c) => ({ label: (flag(c.country) + ' ' + c.country).trim(), value: c.minutes, text: t('n_users', { n: num(c.users) }) })))
    : h('p', { class: 'muted', style: { margin: 0 } }, t('geo_hint'));
  page.append(h('div', { class: 'cols c2' }, panel(t('chart.top_services'), topSvc), panel(t('top.traffic'), topT)));
  page.append(h('div', { class: 'cols c2' }, panel(t('top.active'), topA), panel(t('top.countries'), countries)));

  page.append(panel(t('chart.heat'), heatmap(o.heatmap), t('chart.heat_hint')));
  return page;
}
