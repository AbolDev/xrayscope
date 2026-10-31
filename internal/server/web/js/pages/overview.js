import { api } from '../api.js';
import { h, t, num, bytes, dur, catName, flag, empty, isBgCat, panel, stats, ago, rate } from '../util.js';
import { timeChart, rank, parts, heatmap } from '../charts.js';

// change describes a figure against the period just before the range.
function change(cur, prev) {
  if (!(prev > 0)) return '';
  const pct = ((cur - prev) / prev) * 100;
  if (Math.abs(pct) < 1) return t('vs_same');
  return t('vs_prev', { p: (pct > 0 ? '+' : '-') + num(Math.abs(pct), Math.abs(pct) < 10 ? 1 : 0) + '%' });
}

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

  // the numbers, each with what it compares to; speed updates every few seconds
  const speedV = h('span', null, '-');
  const speedS = h('span', null, '');
  page.append(stats([
    { l: t('kpi.speed'), v: speedV, s: speedS },
    { l: t('kpi.online'), v: num(T.online_now), s: t('kpi.ips_now', { n: num(T.ips_now) }) },
    { l: t('kpi.active_users'), v: num(T.users_active), s: change(T.users_active, o.prev.users_active) || t('kpi.of_total', { n: num(T.users_total) }) },
    { l: t('kpi.download'), v: bytes(T.down), s: change(T.down, o.prev.down) },
    { l: t('kpi.upload'), v: bytes(T.up), s: change(T.up, o.prev.up) },
  ]));

  ctx.poll(async () => {
    const sp = await api.get('speed');
    if (!sp.available) { speedV.textContent = '-'; speedS.textContent = t('speed.off'); return; }
    speedV.textContent = rate(sp.total.down);
    speedS.textContent = t('speed.up') + ' ' + rate(sp.total.up);
  }, 3000);

  // The one rich chart: download in the accent, upload in a neutral step.
  const traffic = h('div');
  if (o.traffic.points.length) {
    timeChart(traffic, {
      from: o.range.from, to: o.range.to, bucket: o.traffic.bucket, yFmt: bytes, height: 300,
      series: [
        { label: t('download'), color: 'var(--accent)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--n2)', points: o.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
  } else traffic.append(empty(t('no_traffic'), t('no_traffic_hint')));

  const live = h('div');
  if (o.online.length) {
    live.append(h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' }, h('tbody', null, o.online.slice(0, 8).map((u) => h('tr', { class: 'click', onclick: () => { location.hash = '#/users/' + u.user_id; } },
      h('td', null, h('span', { class: 'dot live', style: { marginInlineEnd: '8px' } }), u.email),
      h('td', { class: 'num muted' }, t('n_ips', { n: num(u.conns.length) })),
      h('td', { class: 'num faint' }, ago(u.last_seen))))))));
  } else live.append(empty(t('nobody_online')));

  page.append(h('div', { class: 'cols c-main' },
    panel(t('chart.traffic'), traffic, bytes(T.up + T.down)),
    panel(t('online_now'), live, h('a', { class: 'link', href: '#/online' }, t('see_all')), true)));

  // Three compact blocks of different kinds: a small chart and two ranked lists.
  const act = h('div');
  if (o.activity.length) {
    const bucket = o.range.to - o.range.from > 4 * 86400 ? 86400 : 3600;
    timeChart(act, {
      from: o.range.from, to: o.range.to, bucket, labelShift: bucket === 86400 ? 43200 : 0, height: 160,
      series: [{ label: t('chart.users_active'), color: 'var(--accent)', points: o.activity.map((p) => ({ t: p.t, v: p.users })) }],
    });
  } else act.append(empty(t('no_data')));

  const cats = o.categories.filter((c) => c.minutes > 0 && !isBgCat(c.category));
  const time = cats.length ? parts(cats.slice(0, 6).map((c) => ({ label: catName(c.category), value: c.minutes })), { fmt: dur }) : empty(t('no_data'));

  const svcs = o.services.filter((s) => s.minutes > 0 && s.service && !isBgCat(s.category));
  const topSvc = svcs.length ? rank(svcs.slice(0, 6).map((s) => ({ label: s.service, value: s.minutes, text: dur(s.minutes) }))) : empty(t('no_data'));
  page.append(h('div', { class: 'cols c3' },
    panel(t('chart.activity'), act, t('chart.activity_hint')),
    panel(t('chart.categories'), time, t('chart.categories_hint')),
    panel(t('chart.top_services'), topSvc)));

  const topT = o.top_traffic.length ? rank(o.top_traffic.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.up + u.down, text: bytes(u.up + u.down) }))) : empty(t('no_data'));
  const topA = o.top_active.length ? rank(o.top_active.map((u) => ({ label: u.email, link: '#/users/' + u.user_id, value: u.minutes, text: dur(u.minutes) }))) : empty(t('no_data'));
  const countries = o.countries.length
    ? rank(o.countries.slice(0, 8).map((c) => ({ label: (flag(c.country) + ' ' + c.country).trim(), value: c.minutes, text: t('n_users', { n: num(c.users) }) })))
    : h('p', { class: 'muted', style: { margin: 0 } }, t('geo_hint'));
  page.append(h('div', { class: 'cols c3' }, panel(t('top.traffic'), topT), panel(t('top.active'), topA), panel(t('top.countries'), countries)));

  page.append(panel(t('chart.heat'), heatmap(o.heatmap), t('chart.heat_hint')));
  return page;
}
