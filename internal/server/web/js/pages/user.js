import { api, guard } from '../api.js';
import {
  h, t, num, bytes, dur, ago, flag, icon, empty, catColor, catName, fmtDate, fmtDateTime, fmtTime, fmtDay, fmtDateStr,
  dateStrToSec, ymd, state, toast, modal, confirmBox, debounce, isFa, isBgCat, digits,
} from '../util.js';
import { timeChart, barList, donut, legend, heatmap, strip, gantt } from '../charts.js';
import { kpi } from './overview.js';

const TABS = ['summary', 'timeline', 'sites', 'network', 'sleep'];

export async function userPage(ctx) {
  const id = Number(ctx.parts[0]);
  const tab = TABS.includes(ctx.query.get('tab')) ? ctx.query.get('tab') : 'summary';
  const rg = ctx.range;
  const needsReport = tab === 'summary' || tab === 'network' || tab === 'sleep';
  const rep = needsReport ? await api.get('users/' + id, { from: rg.from, to: rg.to }) : null;
  const info = rep ? null : await api.get('users/' + id + '/info');
  const user = rep ? rep.user : info.user;
  const online = rep ? rep.online : info.online;

  const page = h('div', { class: 'stack' });
  page.append(header(user, online, id, rg, ctx));
  page.append(h('div', { class: 'tabs' }, TABS.map((k) => h('a', { class: tab === k ? 'on' : '', href: `#/users/${id}?tab=${k}` }, t('tab.' + k)))));
  const body = { summary, network, sleep }[tab];
  if (body) page.append(body(rep, ctx));
  else if (tab === 'timeline') page.append(await timeline(id, ctx));
  else if (tab === 'sites') page.append(await sites(id, ctx));
  return page;
}

// ---------------------------------------------------------------- header

function header(u, online, id, rg, ctx) {
  const used = u.up_total + u.down_total;
  const pct = u.quota > 0 ? Math.min(100, (used / u.quota) * 100) : 0;
  const expired = u.expiry > 0 && u.expiry * 1000 < Date.now();
  const exp = (kind) => `api/users/${id}/export?kind=${kind}&from=${rg.from}&to=${rg.to}`;
  return h('div', { class: 'card' },
    h('div', { class: 'row', style: { gap: '14px' } },
      h('a', { class: 'btn icon', href: '#/users', title: t('back') }, icon('chevron')),
      h('span', { class: 'status-dot' + (online ? ' on' : ''), style: { width: '12px', height: '12px' } }),
      h('div', { class: 'grow' },
        h('div', { style: { fontSize: '19px', fontWeight: 800 } }, u.email, !u.enabled ? h('span', { class: 'chip bad', style: { marginInlineStart: '10px' } }, t('disabled')) : null),
        h('div', { class: 'muted' }, online ? t('online_n', { n: num(online.conns.length) }) : t('last_seen_at', { t: u.last_seen ? ago(u.last_seen) : '—' }),
          u.first_seen ? ' · ' + t('first_seen_at', { d: fmtDate(u.first_seen) }) : '')),
      u.quota > 0 ? h('div', { style: { minWidth: '170px' } },
        h('div', { class: 'progress' + (pct > 95 ? ' bad' : pct > 80 ? ' warn' : '') }, h('i', { style: { width: pct + '%' } })),
        h('div', { class: 'faint', style: { fontSize: '11.5px' } }, bytes(used) + ' / ' + bytes(u.quota))) : null,
      u.expiry ? h('span', { class: 'chip ' + (expired ? 'bad' : '') }, icon('clock'), (expired ? t('expired') : t('expires')) + ' ' + fmtDate(u.expiry)) : null,
      h('div', { class: 'row', style: { gap: '6px' } },
        h('a', { class: 'btn sm', href: exp('sites'), download: '' }, icon('download'), 'CSV · ' + t('tab.sites')),
        h('a', { class: 'btn sm', href: exp('daily'), download: '' }, 'CSV · ' + t('csv.daily')),
        h('button', { class: 'btn sm', onclick: () => noteDialog(u, ctx) }, t('note')),
        h('button', { class: 'btn sm danger', title: t('delete_user'), onclick: async () => {
          if (await confirmBox(t('delete_user'), t('delete_user_text', { name: u.email }), true)) {
            await guard(() => api.del('users/' + id));
            location.hash = '#/users';
          }
        } }, icon('trash')))),
    u.note ? h('div', { class: 'alert', style: { marginTop: '12px' } }, u.note) : null);
}

function noteDialog(u, ctx) {
  const ta = h('textarea', { class: 'input', maxlength: 2000 }, u.note || '');
  modal(t('note'), ta, [
    { label: t('cancel') },
    { label: t('save'), kind: 'primary', onclick: async () => {
      await guard(() => api.post('users/' + u.id + '/note', { note: ta.value }));
      ctx.refresh();
    } },
  ]);
}

// ---------------------------------------------------------------- summary

function summary(r, ctx) {
  const a = r.activity;
  const page = h('div', { class: 'stack' });
  page.append(h('div', { class: 'grid g4' },
    kpi('clock', '', dur(a.online_minutes), t('us.online_time'), t('us.active_days', { n: num(a.active_days) })),
    kpi('activity', 'ok', dur(a.interactive_minutes), t('us.interactive'), a.online_minutes ? t('us.share', { p: num((a.interactive_minutes / a.online_minutes) * 100) }) : ''),
    kpi('live', 'warn', num(a.sessions), t('us.sessions'), a.sessions ? t('us.avg_session', { d: dur(a.avg_session_min), l: dur(a.longest_session_min) }) : ''),
    kpi('download', 'down', bytes(r.traffic.down), t('download'), '↑ ' + bytes(r.traffic.up))));

  // traffic + categories
  const tc = h('div', { class: 'card' }, h('h2', null, t('chart.traffic')));
  if (r.traffic.points.length) {
    timeChart(tc, {
      from: r.range.from, to: r.range.to, bucket: r.traffic.bucket, yFmt: bytes, labelShift: r.traffic.bucket === 86400 ? 43200 : 0,
      series: [
        { label: t('download'), color: 'var(--down)', points: r.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--up)', points: r.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
  } else tc.append(empty(t('no_traffic')));

  const cats = r.by_category.filter((c) => c.minutes > 0 && !isBgCat(c.category));
  const cc = h('div', { class: 'card' }, h('h2', null, t('us.time_by_cat'), h('span', { class: 'hint' }, t('chart.categories_hint'))));
  if (cats.length) {
    const items = cats.map((c) => ({ label: catName(c.category), value: c.minutes, color: catColor(c.category) }));
    cc.append(donut(items, { fmt: dur, center: dur(items.reduce((x, y) => x + y.value, 0)), centerSub: t('us.total') }), legend(items.slice(0, 10), dur));
  } else cc.append(empty());
  page.append(h('div', { class: 'grid g-main' }, tc, cc));

  // interests
  const fav = h('div', { class: 'card' }, h('h2', null, t('us.interests'), h('span', { class: 'hint' }, t('us.interests_hint'))));
  const keys = Object.keys(r.favorites);
  keys.sort((x, y) => (r.favorites[y][0]?.minutes || 0) - (r.favorites[x][0]?.minutes || 0));
  if (keys.length) {
    fav.append(h('div', { class: 'fav-grid' }, keys.map((k) => h('div', { class: 'fav' },
      h('div', { class: 't' }, h('i', { style: { background: catColor(k) } }), catName(k)),
      r.favorites[k].map((s) => h('div', { class: 's' }, h('b', null, s.service), h('span', null, dur(s.minutes))))))));
  } else fav.append(empty());
  page.append(fav);

  // daily + hourly activity
  const daily = r.activity.daily || [];
  const dc = h('div', { class: 'card' }, h('h2', null, t('us.daily')));
  if (daily.length) {
    const pts = (key) => daily.map((d) => ({ t: dateStrToSec(d.date), v: d[key] }));
    timeChart(dc, {
      from: dateStrToSec(daily[0].date) - 43200, to: dateStrToSec(daily[daily.length - 1].date) + 43200, bucket: 86400, labelShift: 43200,
      yFmt: (v) => num(v / 60, 1) + (isFa() ? ' ' + t('u.hour') : 'h'),
      series: [
        { label: t('us.interactive'), color: 'var(--primary)', points: pts('interactive') },
        { label: t('us.background'), color: 'var(--warn)', points: daily.map((d) => ({ t: dateStrToSec(d.date), v: Math.max(0, d.online - d.interactive) })) },
      ],
      height: 200,
    });
  } else dc.append(empty());
  page.append(dc);

  if (r.activity.hourly && r.activity.hourly.length) {
    const hc = h('div', { class: 'card' }, h('h2', null, t('us.hourly')));
    timeChart(hc, {
      from: r.range.from, to: r.range.to, bucket: 3600, yFmt: (v) => num(v) + (isFa() ? '' : 'm'),
      series: [
        { label: t('us.interactive'), color: 'var(--primary)', points: r.activity.hourly.map((p) => ({ t: p.hour * 3600, v: p.interactive })) },
        { label: t('us.background'), color: 'var(--warn)', points: r.activity.hourly.map((p) => ({ t: p.hour * 3600, v: Math.max(0, p.online - p.interactive) })) },
      ],
      height: 190,
    });
    page.append(hc);
  }

  page.append(h('div', { class: 'card' }, h('h2', null, t('chart.heat'), h('span', { class: 'hint' }, t('us.heat_hint'))), heatmap(r.activity.heatmap)));

  // top sites
  const top = r.top_sites.filter((s) => s.category !== 'cdn' && s.category !== 'system').slice(0, 12);
  const ts = h('div', { class: 'card flush' }, h('h2', null, t('us.top_sites'), h('a', { class: 'hint', href: `#/users/${r.user.id}?tab=sites` }, t('see_all'))));
  ts.append(top.length ? siteTable(top) : empty());
  page.append(ts);
  return page;
}

function siteTable(list) {
  return h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, ['col.site', 'col.category', 'col.service', 'col.requests', 'col.active', 'col.first', 'col.last'].map((k, i) => h('th', { class: i >= 3 && i <= 4 ? 'num' : '' }, t(k))))),
    h('tbody', null, list.map((s) => h('tr', null,
      h('td', { class: 'mono' }, s.domain),
      h('td', null, h('span', { class: 'chip' }, h('span', { class: 'dot', style: { background: catColor(s.category) } }), catName(s.category))),
      h('td', null, s.service || '—'),
      h('td', { class: 'num' }, num(s.reqs)),
      h('td', { class: 'num' }, dur(s.minutes)),
      h('td', { class: 'muted' }, fmtDateTime(s.first * 60)),
      h('td', { class: 'muted' }, fmtDateTime(s.last * 60)))))));
}

// ---------------------------------------------------------------- timeline

async function timeline(id, ctx) {
  const today = ymd(Math.floor(Date.now() / 1000));
  const date = ctx.query.get('date') || today;
  const t0 = await api.get(`users/${id}/timeline`, { date });
  const page = h('div', { class: 'stack' });

  const shift = (d) => {
    const dt = new Date(date + 'T12:00:00Z');
    dt.setUTCDate(dt.getUTCDate() + d);
    return dt.toISOString().slice(0, 10);
  };
  const nav = (d) => `#/users/${id}?tab=timeline&date=${d}`;
  const picker = h('input', { class: 'input', type: 'date', value: date, max: today, style: { width: '160px' }, onchange: (e) => { if (e.target.value) location.hash = nav(e.target.value); } });
  page.append(h('div', { class: 'row' },
    h('a', { class: 'btn icon', href: nav(shift(-1)) }, icon('chevron')),
    h('div', { style: { fontWeight: 700, minWidth: '230px', textAlign: 'center' } }, fmtDateStr(date)),
    date < today ? h('a', { class: 'btn icon', href: nav(shift(1)), style: { transform: 'scaleX(-1)' } }, icon('chevron')) : h('span', { class: 'btn icon', style: { opacity: .3 } }, icon('chevron')),
    date !== today ? h('a', { class: 'btn', href: nav(today) }, t('today')) : null, picker));

  const idle = (t0.strip.match(/0/g) || []).length;
  const inter = (t0.strip.match(/2/g) || []).length;
  const bg = t0.minutes - idle - inter;
  const strCard = h('div', { class: 'card' }, h('h2', null, t('tl.minutes'), h('span', { class: 'hint' }, dur(inter + bg) + ' ' + t('tl.online'))));
  strCard.append(strip(t0.strip, t0.start));
  strCard.append(h('div', { class: 'legend' },
    h('span', null, h('i', { style: { background: 'var(--primary)' } }), t('strip.inter') + ' · ' + dur(inter)),
    h('span', null, h('i', { style: { background: 'var(--warn)' } }), t('strip.bg') + ' · ' + dur(bg)),
    h('span', null, h('i', { style: { background: 'var(--surface-2)', border: '1px solid var(--border)' } }), t('strip.idle'))));
  page.append(strCard);

  // sessions per IP
  const byIP = new Map();
  for (const s of t0.sessions) {
    if (!byIP.has(s.ip)) byIP.set(s.ip, { country: s.country, spans: [] });
    byIP.get(s.ip).spans.push({ start: s.start, end: s.end, title: `${s.ip} · ${fmtTime(s.start)}–${fmtTime(s.end)} · ${dur(s.active)}` });
  }
  const palette = ['var(--primary)', 'var(--ok)', 'var(--warn)', 'var(--down)', '#ec4899', '#8b5cf6'];
  const sc = h('div', { class: 'card' }, h('h2', null, t('tl.sessions'), h('span', { class: 'hint' }, t('tl.sessions_hint'))));
  if (byIP.size) {
    const rows = [...byIP.entries()].map(([ip, v], i) => ({ label: flag(v.country) + ' ' + ip, color: palette[i % palette.length], spans: v.spans }));
    sc.append(gantt(rows, t0.start, t0.minutes));
    if (byIP.size > 1) sc.append(h('div', { class: 'alert warn', style: { marginTop: '10px' } }, t('tl.multi_ip', { n: num(byIP.size) })));
  } else sc.append(empty(t('tl.none')));
  page.append(sc);

  // traffic
  if (t0.traffic.points.length) {
    const tc = h('div', { class: 'card' }, h('h2', null, t('chart.traffic'), h('span', { class: 'hint' }, '↓ ' + bytes(t0.traffic.down) + '  ↑ ' + bytes(t0.traffic.up))));
    timeChart(tc, {
      from: t0.start, to: t0.start + t0.minutes * 60, bucket: t0.traffic.bucket, yFmt: bytes, height: 190,
      series: [
        { label: t('download'), color: 'var(--down)', points: t0.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--up)', points: t0.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
    page.append(tc);
  }

  // what was visited
  const vc = h('div', { class: 'card' }, h('h2', null, t('tl.visited'), h('span', { class: 'hint' }, t('tl.visited_hint'))));
  if (t0.blocks.length) {
    vc.append(h('div', { class: 'stack', style: { gap: '10px' } }, t0.blocks.map((b) => h('div', { style: { display: 'grid', gridTemplateColumns: '120px 1fr', gap: '12px', padding: '10px 0', borderTop: '1px solid var(--border)' } },
      h('div', null, h('b', { class: 'ltr' }, fmtTime(b.hour) + ' – ' + fmtTime(b.hour + 3600)), h('div', { class: 'faint', style: { fontSize: '11.5px' } }, dur(b.minutes) + ' · ' + num(b.reqs) + ' ' + t('requests'))),
      h('div', { class: 'pill-list' }, b.sites.map((s) => h('span', { class: 'chip', title: s.domain + ' · ' + catName(s.category) },
        h('span', { class: 'dot', style: { background: catColor(s.category) } }), h('span', { class: 'mono', style: { fontSize: '11.5px' } }, s.domain), h('span', { class: 'faint' }, num(s.reqs)))))))));
  } else vc.append(empty());
  page.append(vc);
  return page;
}

// ---------------------------------------------------------------- sites

let siteQuery = '';
let siteCat = '';
async function sites(id, ctx) {
  const page = h('div', { class: 'stack' });
  const body = h('div');
  const limit = 100;
  let offset = 0;
  const load = async () => {
    const r = await api.get(`users/${id}/sites`, { from: ctx.range.from, to: ctx.range.to, q: siteQuery, category: siteCat, limit, offset });
    body.replaceChildren(h('div', { class: 'card flush' },
      r.sites.length ? siteTable(r.sites) : empty(),
      r.total > limit ? h('div', { class: 'row', style: { padding: '12px 18px', justifyContent: 'space-between' } },
        h('span', { class: 'muted' }, t('showing', { a: num(offset + 1), b: num(Math.min(offset + limit, r.total)), c: num(r.total) })),
        h('div', { class: 'row' },
          h('button', { class: 'btn sm', disabled: offset === 0 || null, onclick: () => { offset = Math.max(0, offset - limit); load(); } }, t('prev')),
          h('button', { class: 'btn sm', disabled: offset + limit >= r.total || null, onclick: () => { offset += limit; load(); } }, t('next')))) : null));
  };
  const cats = state.meta.categories.map((c) => c.id);
  page.append(h('div', { class: 'row' },
    h('input', { class: 'input', style: { maxWidth: '300px' }, placeholder: t('sites.search'), value: siteQuery, oninput: debounce((e) => { siteQuery = e.target.value; offset = 0; load(); }, 200) }),
    h('select', { class: 'input', style: { maxWidth: '200px' }, onchange: (e) => { siteCat = e.target.value; offset = 0; load(); } },
      h('option', { value: '' }, t('sites.all_cats')), cats.map((c) => h('option', { value: c, selected: siteCat === c || null }, catName(c))))), body);
  await load();
  return page;
}

// ---------------------------------------------------------------- network

function network(r) {
  const page = h('div', { class: 'stack' });
  const cc = r.concurrency;
  const limit = cc.limit;
  const over = limit > 0 && cc.max > limit;

  page.append(h('div', { class: 'grid g4' },
    kpi('globe', '', num(r.ips.length), t('net.ips'), t('net.in_range')),
    kpi('device', over ? 'bad' : 'ok', num(cc.max), t('net.max_conc'), limit ? t('net.limit', { n: num(limit) }) : t('net.no_limit')),
    kpi('device', '', num(r.devices.length), t('net.devices'), t('net.devices_hint')),
    kpi('warn', cc.overlaps.length ? 'warn' : 'ok', num(cc.overlaps.length), t('net.overlaps'), t('net.overlaps_hint'))));

  // IPs
  const ipc = h('div', { class: 'card flush' }, h('h2', null, t('net.ip_history'), h('span', { class: 'hint' }, t('net.ip_hint'))));
  ipc.append(r.ips.length ? h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, ['col.ip', 'col.country', 'col.network', 'col.first', 'col.last', 'col.active', 'col.sessions', 'col.device'].map((k, i) => h('th', { class: i >= 5 && i <= 6 ? 'num' : '' }, t(k))))),
    h('tbody', null, r.ips.map((ip) => h('tr', null,
      h('td', null, h('span', { class: 'status-dot' + (ip.online ? ' on' : ''), style: { marginInlineEnd: '8px' } }), h('a', { class: 'mono', href: '#/ips?ip=' + encodeURIComponent(ip.ip) }, ip.ip)),
      h('td', null, ip.country ? flag(ip.country) + ' ' + ip.country : '—'),
      h('td', { class: 'muted', style: { maxWidth: '220px', overflow: 'hidden', textOverflow: 'ellipsis' }, title: ip.org }, ip.org || '—'),
      h('td', { class: 'muted' }, fmtDateTime(ip.first)),
      h('td', { class: 'muted' }, fmtDateTime(ip.last)),
      h('td', { class: 'num' }, dur(ip.minutes)),
      h('td', { class: 'num' }, num(ip.sessions)),
      h('td', null, (ip.devices || []).length ? h('div', { class: 'row', style: { gap: '5px' } }, ip.devices.map((d) => deviceChip(d))) : h('span', { class: 'faint' }, '—'))))))) : empty());
  page.append(ipc);

  // devices
  const dv = h('div', { class: 'card' }, h('h2', null, t('net.device_guess'), h('span', { class: 'hint' }, t('net.device_guess_hint'))));
  dv.append(r.devices.length ? h('div', { class: 'pill-list' }, r.devices.map((d) => deviceChip({ ...d }, d.ips))) : h('div', { class: 'muted' }, t('net.no_devices')));
  page.append(dv);

  // overlaps
  const oc = h('div', { class: 'card flush' }, h('h2', null, t('net.overlap_list'), h('span', { class: 'hint' }, over ? t('net.over_limit') : '')));
  oc.append(cc.overlaps.length ? h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, ['col.start', 'col.end', 'col.duration', 'col.ips_at_once', 'col.ips'].map((k) => h('th', null, t(k))))),
    h('tbody', null, cc.overlaps.slice(0, 100).map((o) => h('tr', null,
      h('td', { class: 'muted' }, fmtDateTime(o.start)), h('td', { class: 'muted' }, fmtDateTime(o.end)),
      h('td', null, dur(Math.round((o.end - o.start) / 60))),
      h('td', null, h('span', { class: 'chip ' + (limit && o.peak > limit ? 'bad' : '') }, num(o.peak))),
      h('td', { class: 'mono', style: { whiteSpace: 'normal' } }, o.ips.join(', ')))))))
    : empty(t('net.no_overlaps')));
  page.append(oc);
  return page;
}

function deviceChip(d, count) {
  const label = [d.os + (d.vendor ? ' (' + d.vendor + ')' : ''), d.browser].filter(Boolean).join(' · ');
  return h('span', { class: 'chip primary', title: t('confidence', { p: num(d.confidence * 100) }) }, icon('device'), label,
    h('span', { class: 'faint' }, num(d.confidence * 100) + '%'), count ? h('span', { class: 'faint' }, '× ' + num(count)) : null);
}

// ---------------------------------------------------------------- sleep

function sleep(r) {
  const s = r.sleep;
  const page = h('div', { class: 'stack' });
  if (!s.days_analyzed) {
    page.append(h('div', { class: 'card' }, h('div', { class: 'alert' }, t('sl.not_enough', { n: num(r.sleep_window_days) })), h('p', { class: 'muted' }, t('sl.method'))));
    return page;
  }
  const conf = s.confidence;
  page.append(h('div', { class: 'grid g4' },
    kpi('moon', '', digits(s.typical_start), t('sl.bedtime'), t('sl.std', { m: num(s.std_minutes) })),
    kpi('sun', 'warn', digits(s.typical_end), t('sl.wake'), t('sl.nights', { n: num(s.days_analyzed) })),
    kpi('clock', 'ok', dur(s.avg_minutes), t('sl.duration'), t('sl.window', { n: num(r.sleep_window_days) })),
    kpi('shield', conf >= 0.6 ? 'ok' : 'warn', num(conf * 100) + '%', t('sl.confidence'), t('sl.connected_share', { p: num(s.connected_share * 100) }))));

  page.append(h('div', { class: 'alert' }, t('sl.explain')));

  // nightly rows on a noon-to-noon axis
  const card = h('div', { class: 'card' }, h('h2', null, t('sl.nightly'), h('span', { class: 'hint' }, t('sl.nightly_hint'))));
  const ticks = [12, 15, 18, 21, 0, 3, 6, 9, 12];
  card.append(h('div', { class: 'sleep-axis' }, h('div'), h('div', { class: 'ticks' }, ticks.map((hr, i) => h('span', { style: { left: (i / 8) * 100 + '%' } }, String(hr).padStart(2, '0')))), h('div')));
  const days = [...s.days].reverse();
  for (const d of days) {
    // minutes since the previous noon, using the display timezone clock
    const startSec = d.start * 60, endSec = d.end * 60;
    const noon = noonBefore(d.date);
    const left = Math.max(0, ((startSec - noon) / 86400) * 100);
    const width = Math.max(0.5, ((endSec - startSec) / 86400) * 100);
    card.append(h('div', { class: 'sleep-row' },
      h('div', { class: 'd' }, fmtDateStr(d.date, { month: 'short', day: 'numeric', weekday: 'short' })),
      h('div', { class: 'sleep-track' },
        h('div', { class: 'night', style: { left: '37.5%', width: '33.3%' } }),
        h('div', { class: 'sl ' + d.kind + (d.atypical ? ' atyp' : ''), style: { left: left + '%', width: width + '%' }, title: `${fmtTime(startSec)} → ${fmtTime(endSec)} · ${dur(d.minutes)}` })),
      h('div', { class: 'faint', dir: isFa() ? 'rtl' : 'ltr', style: { fontSize: '11.5px', textAlign: 'start' } },
        h('span', { dir: 'ltr', style: { unicodeBidi: 'isolate' } }, `${fmtTime(startSec)}–${fmtTime(endSec)}`), ' · ', dur(d.minutes))));
  }
  card.append(h('div', { class: 'legend' },
    h('span', null, h('i', { style: { background: 'var(--warn)' } }), t('sl.kind.connected')),
    h('span', null, h('i', { style: { background: 'var(--primary)' } }), t('sl.kind.offline')),
    h('span', null, h('i', { style: { background: 'var(--primary)', opacity: .45 } }), t('sl.kind.atypical'))));
  page.append(card);

  const tc = h('div', { class: 'card flush' }, h('h2', null, t('sl.table')));
  tc.append(h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, ['col.date', 'col.from', 'col.to', 'col.duration', 'sl.kind', 'sl.bg_minutes', 'sl.awakenings'].map((k) => h('th', null, t(k))))),
    h('tbody', null, days.reverse().map((d) => h('tr', null,
      h('td', null, fmtDateStr(d.date, { month: 'short', day: 'numeric' })),
      h('td', null, fmtTime(d.start * 60)), h('td', null, fmtTime(d.end * 60)), h('td', null, dur(d.minutes)),
      h('td', null, h('span', { class: 'chip ' + (d.kind === 'connected' ? 'warn' : '') }, t('sl.kind.' + d.kind)), d.atypical ? h('span', { class: 'chip', style: { marginInlineStart: '6px' } }, t('sl.kind.atypical')) : null),
      h('td', null, num(d.present)), h('td', null, num(d.awakenings))))))));
  page.append(tc);
  return page;
}

// noonBefore returns the unix time of 12:00 (display timezone) on the day
// before the given morning date.
function noonBefore(date) {
  const prev = new Date(date + 'T12:00:00Z');
  prev.setUTCDate(prev.getUTCDate() - 1);
  const ds = prev.toISOString().slice(0, 10);
  // find the instant whose display-timezone clock reads 12:00 on ds
  let guess = Date.parse(ds + 'T12:00:00Z') / 1000;
  const parts = (sec) => {
    const p = new Intl.DateTimeFormat('en-GB', { timeZone: state.meta.timezone && state.meta.timezone !== 'Local' ? state.meta.timezone : undefined, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(sec * 1000));
    return Number(p.find((x) => x.type === 'hour').value) * 60 + Number(p.find((x) => x.type === 'minute').value);
  };
  for (let i = 0; i < 3; i++) {
    const diff = parts(guess) - 12 * 60;
    if (diff === 0) break;
    guess -= (diff > 720 ? diff - 1440 : diff < -720 ? diff + 1440 : diff) * 60;
  }
  return guess;
}
