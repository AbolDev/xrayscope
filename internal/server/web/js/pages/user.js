import { api, guard } from '../api.js';
import {
  h, t, num, bytes, dur, ago, flag, icon, empty, catName, fmtDate, fmtDateTime, fmtTime, fmtDateStr,
  dateStrToSec, ymd, state, modal, confirmBox, debounce, isFa, isBgCat, digits, ltr, tag, stats, panel, ramp, queryGet, queryPut, rate,
} from '../util.js';
import { timeChart, rank, parts, heatmap, strip, gantt, sparkline } from '../charts.js';

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
  return panel(null, h('div', null,
    h('div', { class: 'row', style: { gap: '14px', alignItems: 'flex-start' } },
      h('a', { class: 'btn icon', href: '#/users', title: t('back'), 'aria-label': t('back'), style: { transform: isFa() ? 'none' : 'scaleX(-1)' } }, icon('chevron')),
      h('div', { class: 'grow' },
        h('div', { class: 'row', style: { gap: '8px', fontSize: 'var(--t-xl)', fontWeight: 500 } }, h('span', { class: 'dot' + (online ? ' live' : ''), role: 'img', 'aria-label': online ? t('online') : t('offline') }), u.email,
          online ? tag(t('online_n', { n: num(online.conns.length) }), 'ok') : tag(t('offline')), !u.enabled ? tag(t('disabled'), 'bad') : null),
        h('div', { class: 'muted small' }, t('last_seen_at', { t: u.last_seen ? ago(u.last_seen) : '-' })),
        online ? speedLine(id, ctx) : null,
        u.first_seen ? h('div', { class: 'muted small' }, t('first_seen_at', { d: fmtDate(u.first_seen) })) : null),
      u.quota > 0 ? h('div', { style: { minWidth: '180px' } },
        h('div', { class: 'meter' + (pct > 95 ? ' bad' : pct > 80 ? ' warn' : '') }, h('i', { style: { width: pct + '%' } })),
        h('div', { class: 'faint small' }, ltr(bytes(used) + ' / ' + bytes(u.quota)))) : null,
      u.expiry ? h('div', null, tag((expired ? t('expired') : t('expires')) + ' ' + fmtDate(u.expiry), expired ? 'bad' : '')) : null,
      h('div', { class: 'row', style: { gap: '6px' } },
        h('a', { class: 'btn sm', href: exp('sites'), download: '' }, icon('download'), t('csv.sites')),
        h('a', { class: 'btn sm', href: exp('daily'), download: '' }, icon('download'), t('csv.daily')),
        h('button', { class: 'btn sm', onclick: () => noteDialog(u, ctx) }, t('note')),
        h('button', { class: 'btn sm danger', title: t('delete_user'), 'aria-label': t('delete_user'), onclick: async () => {
          if (await confirmBox(t('delete_user'), t('delete_user_text', { name: u.email }), true)) {
            await guard(() => api.del('users/' + id));
            location.hash = '#/users';
          }
        } }, icon('trash')))),
    u.note ? h('div', { class: 'alert', style: { marginTop: '12px' } }, u.note) : null));
}

// speedLine shows the user's live speed with the last minutes as a strip.
function speedLine(id, ctx) {
  const text = h('span', { class: 'mono' });
  const box = h('div', { class: 'row', style: { gap: '12px', marginTop: '4px' } }, text);
  const strip = h('span');
  box.append(strip);
  let pts = [];
  const draw = (up, down) => {
    text.textContent = t('speed.down') + ' ' + rate(down) + '   ' + t('speed.up') + ' ' + rate(up);
    strip.replaceChildren(pts.length > 3 ? sparkline(pts, { width: 120, height: 18, live: true }) : '');
  };
  // the history comes once from the server; the socket then adds one point per push
  api.get(`users/${id}/speed`).then((s) => { pts = s.history.slice(-60).map((p) => p.down); draw(s.up, s.down); }).catch(() => {});
  ctx.live((snap) => {
    const row = snap.speed.users.find((x) => x.user_id === id);
    pts = pts.concat(row ? row.down : 0).slice(-60);
    draw(row ? row.up : 0, row ? row.down : 0);
  }, async () => {
    const s = await api.get(`users/${id}/speed`);
    pts = s.history.slice(-60).map((p) => p.down);
    draw(s.up, s.down);
  });
  return box;
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

function summary(r) {
  const a = r.activity;
  const page = h('div', { class: 'stack' });
  page.append(stats([
    { v: dur(a.online_minutes), l: t('us.online_time'), s: t('us.active_days', { n: num(a.active_days) }) },
    { v: dur(a.interactive_minutes), l: t('us.interactive'), s: a.online_minutes ? t('us.share', { p: num((a.interactive_minutes / a.online_minutes) * 100) }) : '' },
    { v: num(a.sessions), l: t('us.sessions'), s: a.sessions ? t('us.avg_session', { d: dur(a.avg_session_min), l: dur(a.longest_session_min) }) : '' },
    { v: bytes(r.traffic.down), l: t('download'), s: t('upload') + ' ' + bytes(r.traffic.up) },
  ]));

  const tc = h('div');
  if (r.traffic.points.length) {
    timeChart(tc, {
      from: r.range.from, to: r.range.to, bucket: r.traffic.bucket, yFmt: bytes, labelShift: r.traffic.bucket === 86400 ? 43200 : 0,
      series: [
        { label: t('download'), color: 'var(--accent)', points: r.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--n2)', points: r.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
  } else tc.append(empty(t('no_traffic'), t('no_traffic_hint')));

  const cats = r.by_category.filter((c) => c.minutes > 0 && !isBgCat(c.category));
  const cc = cats.length ? parts(cats.slice(0, 7).map((c) => ({ label: catName(c.category), value: c.minutes })), { fmt: dur }) : empty(t('no_data'));
  page.append(h('div', { class: 'cols c-main' }, panel(t('chart.traffic'), tc), panel(t('us.time_by_cat'), cc, t('chart.categories_hint'))));

  // interests
  const keys = Object.keys(r.favorites).sort((x, y) => (r.favorites[y][0]?.minutes || 0) - (r.favorites[x][0]?.minutes || 0));
  const fav = keys.length
    ? h('div', { class: 'interests' }, keys.map((k) => h('div', { class: 'interest' }, h('div', { class: 't' }, catName(k)),
      r.favorites[k].map((s) => h('div', { class: 's' }, h('b', { style: { fontWeight: 500 } }, s.service), h('span', null, dur(s.minutes)))))))
    : empty(t('us.no_interests'), t('us.no_interests_hint'));
  page.append(panel(t('us.interests'), fav, t('us.interests_hint')));

  // activity over time
  const daily = a.daily || [];
  const dc = h('div');
  if (daily.length) {
    const pts = (f) => daily.map((d) => ({ t: dateStrToSec(d.date), v: f(d) }));
    timeChart(dc, {
      from: dateStrToSec(daily[0].date) - 43200, to: dateStrToSec(daily[daily.length - 1].date) + 43200, bucket: 86400, labelShift: 43200, height: 190,
      yFmt: (v) => num(v / 60, 1) + (isFa() ? ' ' + t('u.hour') : 'h'),
      series: [
        { label: t('us.interactive'), color: 'var(--accent)', points: pts((d) => d.interactive) },
        { label: t('us.background'), color: 'var(--n3)', points: pts((d) => Math.max(0, d.online - d.interactive)) },
      ],
    });
  } else dc.append(empty(t('no_data')));
  page.append(panel(t('us.daily'), dc));

  if (a.hourly && a.hourly.length) {
    const hc = h('div');
    timeChart(hc, {
      from: r.range.from, to: r.range.to, bucket: 3600, height: 180, yFmt: (v) => num(v),
      series: [
        { label: t('us.interactive'), color: 'var(--accent)', points: a.hourly.map((p) => ({ t: p.hour * 3600, v: p.interactive })) },
        { label: t('us.background'), color: 'var(--n3)', points: a.hourly.map((p) => ({ t: p.hour * 3600, v: Math.max(0, p.online - p.interactive) })) },
      ],
    });
    page.append(panel(t('us.hourly'), hc, t('us.hourly_hint')));
  }

  page.append(panel(t('chart.heat'), heatmap(a.heatmap), t('us.heat_hint')));

  const top = r.top_sites.filter((s) => !isBgCat(s.category)).slice(0, 12);
  const ts = panel(t('us.top_sites'), top.length ? siteTable(top) : empty(t('no_data')), h('a', { href: `#/users/${r.user.id}?tab=sites` }, t('see_all')), true);
  page.append(ts);
  return page;
}

function siteTable(list) {
  return h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, ['col.site', 'col.category', 'col.service', 'col.requests', 'col.active', 'col.data', 'col.first', 'col.last'].map((k, i) => h('th', { class: i >= 3 && i <= 5 ? 'num' : '', title: k === 'col.data' ? t('col.data_hint') : '' }, t(k))))),
    h('tbody', null, list.map((s) => h('tr', null,
      h('td', { class: 'mono' }, s.domain, s.auto ? h('span', null, ' ', tag(t('site.auto'), '', t('site.auto_hint'))) : null),
      h('td', null, catName(s.category)),
      h('td', null, s.service || h('span', { class: 'faint' }, '-')),
      h('td', { class: 'num' }, num(s.reqs)),
      h('td', { class: 'num' }, s.auto ? h('span', { class: 'faint' }, '-') : dur(s.minutes)),
      h('td', { class: 'num muted' }, s.bytes > 0 ? '~' + bytes(s.bytes) : '-'),
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
  const flip = isFa() ? 'none' : 'scaleX(-1)';
  const picker = h('input', { class: 'input', type: 'date', value: date, max: today, style: { width: '160px' }, 'aria-label': t('tl.pick'), onchange: (e) => { if (e.target.value) location.hash = nav(e.target.value); } });
  page.append(h('div', { class: 'row' },
    h('a', { class: 'btn icon', href: nav(shift(-1)), 'aria-label': t('prev'), style: { transform: flip } }, icon('chevron')),
    h('div', { style: { fontWeight: 500, minWidth: '230px', textAlign: 'center' } }, fmtDateStr(date)),
    date < today ? h('a', { class: 'btn icon', href: nav(shift(1)), 'aria-label': t('next'), style: { transform: isFa() ? 'scaleX(-1)' : 'none' } }, icon('chevron')) : h('span', { class: 'btn icon', style: { opacity: 0.35 } }, icon('chevron')),
    date !== today ? h('a', { class: 'btn', href: nav(today) }, t('today')) : null, picker));

  const idle = (t0.strip.match(/0/g) || []).length;
  const inter = (t0.strip.match(/2/g) || []).length;
  const bg = t0.minutes - idle - inter;
  const strCard = panel(t('tl.minutes'), h('div', null, strip(t0.strip, t0.start),
    h('div', { class: 'legend' },
      h('span', null, h('i', { class: 'swatch', style: { background: 'var(--accent)' } }), t('strip.inter') + ': ' + dur(inter)),
      h('span', null, h('i', { class: 'swatch', style: { background: 'var(--n2)' } }), t('strip.bg') + ': ' + dur(bg)),
      h('span', null, h('i', { class: 'swatch', style: { background: 'var(--raised)', boxShadow: 'inset 0 0 0 1px var(--rule)' } }), t('strip.idle')))),
  dur(inter + bg) + ' ' + t('tl.online'));
  page.append(strCard);

  // connections per IP
  const byIP = new Map();
  for (const s of t0.sessions) {
    if (!byIP.has(s.ip)) byIP.set(s.ip, { country: s.country, spans: [] });
    byIP.get(s.ip).spans.push({ start: s.start, end: s.end, title: `${s.ip}  ${fmtTime(s.start)} - ${fmtTime(s.end)}  ${dur(s.active)}` });
  }
  let sc;
  if (byIP.size) {
    const rows = [...byIP.entries()].map(([ip, v], i) => ({ label: (flag(v.country) + ' ' + ip).trim(), color: ramp(Math.min(i * 1, 3)), spans: v.spans }));
    sc = h('div', null, gantt(rows, t0.start, t0.minutes), byIP.size > 1 ? h('div', { class: 'alert warn', style: { marginTop: '10px' } }, t('tl.multi_ip', { n: num(byIP.size) })) : null);
  } else sc = empty(t('tl.none'));
  page.append(panel(t('tl.sessions'), sc, t('tl.sessions_hint')));

  if (t0.traffic.points.length) {
    const tc = h('div');
    timeChart(tc, {
      from: t0.start, to: t0.start + t0.minutes * 60, bucket: t0.traffic.bucket, yFmt: bytes, height: 180,
      series: [
        { label: t('download'), color: 'var(--accent)', points: t0.traffic.points.map((p) => ({ t: p.t, v: p.down })) },
        { label: t('upload'), color: 'var(--n2)', points: t0.traffic.points.map((p) => ({ t: p.t, v: p.up })) },
      ],
    });
    page.append(panel(t('chart.traffic'), tc, t('download') + ' ' + bytes(t0.traffic.down) + ', ' + t('upload') + ' ' + bytes(t0.traffic.up)));
  }

  // what was visited, hour by hour
  const vc = t0.blocks.length
    ? h('div', null, t0.blocks.map((b) => h('div', { style: { display: 'grid', gridTemplateColumns: '130px minmax(0, 1fr)', gap: '12px', padding: '10px 0', borderTop: '1px solid var(--rule)' } },
      h('div', null, h('b', { class: 'ltr' }, fmtTime(b.hour) + ' - ' + fmtTime(b.hour + 3600)), h('div', { class: 'faint small' }, dur(b.minutes)), h('div', { class: 'faint small' }, num(b.reqs) + ' ' + t('requests'))),
      h('div', { class: 'row', style: { gap: '6px' } }, b.sites.map((s) => h('span', { class: 'tag', title: catName(s.category) + (s.service ? ', ' + s.service : '') },
        h('span', { class: 'mono' }, s.domain), h('span', { class: 'faint' }, num(s.reqs)))))))) : empty(t('no_data'));
  page.append(panel(t('tl.visited'), vc, t('tl.visited_hint')));
  return page;
}

// ---------------------------------------------------------------- sites

async function sites(id, ctx) {
  const page = h('div', { class: 'stack' });
  const body = h('div');
  const limit = 100;
  let q = queryGet('q');
  let cat = queryGet('cat');
  let offset = Number(queryGet('off', '0')) || 0;
  const load = async () => {
    const r = await api.get(`users/${id}/sites`, { from: ctx.range.from, to: ctx.range.to, q, category: cat, limit, offset });
    const inner = h('div', null,
      r.sites.length ? siteTable(r.sites) : empty(t('sites.none'), t('sites.none_hint')),
      r.total > limit ? h('div', { class: 'row', style: { padding: '12px 16px', justifyContent: 'space-between' } },
        h('span', { class: 'muted' }, t('showing', { a: num(offset + 1), b: num(Math.min(offset + limit, r.total)), c: num(r.total) })),
        h('div', { class: 'row' },
          h('button', { class: 'btn sm', disabled: offset === 0 || null, onclick: () => { offset = Math.max(0, offset - limit); queryPut({ off: offset || '' }); load(); } }, t('prev')),
          h('button', { class: 'btn sm', disabled: offset + limit >= r.total || null, onclick: () => { offset += limit; queryPut({ off: offset }); load(); } }, t('next')))) : null);
    const p = panel(null, inner, null, true);
    p.querySelector('.pb').style.padding = '0';
    body.replaceChildren(p);
  };
  const cats = state.meta.categories.map((c) => c.id);
  page.append(h('div', { class: 'row' },
    h('input', { class: 'input', type: 'search', style: { maxWidth: '300px' }, placeholder: t('sites.search'), 'aria-label': t('sites.search'), value: q, oninput: debounce((e) => { q = e.target.value; offset = 0; queryPut({ q, off: '' }); load(); }, 200) }),
    h('select', { class: 'input', style: { maxWidth: '200px' }, 'aria-label': t('sites.all_cats'), onchange: (e) => { cat = e.target.value; offset = 0; queryPut({ cat, off: '' }); load(); } },
      h('option', { value: '' }, t('sites.all_cats')), cats.map((c) => h('option', { value: c, selected: cat === c || null }, catName(c))))), body);
  await load();
  return page;
}

// ---------------------------------------------------------------- network

function network(r) {
  const page = h('div', { class: 'stack' });
  const cc = r.concurrency;
  const limit = cc.limit;
  const over = limit > 0 && cc.max > limit;

  page.append(stats([
    { v: num(r.ips.length), l: t('net.ips'), s: t('net.in_range') },
    { v: num(cc.max), l: t('net.max_conc'), s: limit ? t('net.limit', { n: num(limit) }) + (over ? ', ' + t('net.over_limit') : '') : t('net.no_limit') },
    { v: num(r.devices.length), l: t('net.devices'), s: t('net.devices_hint') },
    { v: num(cc.overlaps.length), l: t('net.overlaps'), s: t('net.overlaps_hint') },
  ]));

  const ipc = r.ips.length ? h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, ['col.ip', 'col.country', 'col.network', 'col.first', 'col.last', 'col.active', 'col.sessions', 'col.device'].map((k, i) => h('th', { class: i >= 5 && i <= 6 ? 'num' : '' }, t(k))))),
    h('tbody', null, r.ips.map((ip) => h('tr', null,
      h('td', null, h('a', { class: 'mono', href: '#/ips?ip=' + encodeURIComponent(ip.ip) }, ip.ip), ' ', ip.online ? tag(t('online'), 'ok') : null, ip.cdn ? [' ', tag(ip.cdn, 'warn', t('cdn.hint'))] : null),
      h('td', null, ip.country ? (flag(ip.country) + ' ' + ip.country).trim() : h('span', { class: 'faint' }, '-')),
      h('td', { class: 'muted', style: { maxWidth: '220px', overflow: 'hidden', textOverflow: 'ellipsis' }, title: ip.org }, ip.org || '-'),
      h('td', { class: 'muted' }, fmtDateTime(ip.first)),
      h('td', { class: 'muted' }, fmtDateTime(ip.last)),
      h('td', { class: 'num' }, dur(ip.minutes)),
      h('td', { class: 'num' }, num(ip.sessions)),
      h('td', null, (ip.devices || []).length ? h('div', { class: 'row', style: { gap: '4px' } }, ip.devices.map((d) => deviceTag(d))) : h('span', { class: 'faint' }, '-'))))))) : empty(t('no_data'));
  page.append(panel(t('net.ip_history'), ipc, t('net.ip_hint'), true));

  const dv = r.devices.length
    ? h('div', { class: 'row', style: { gap: '6px' } }, r.devices.map((d) => deviceTag(d, d.ips)))
    : h('div', { class: 'muted' }, t('net.no_devices'));
  page.append(panel(t('net.device_guess'), dv, t('net.device_guess_hint')));

  const oc = cc.overlaps.length ? h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, ['col.start', 'col.end', 'col.duration', 'col.ips_at_once', 'col.ips'].map((k) => h('th', null, t(k))))),
    h('tbody', null, cc.overlaps.slice(0, 100).map((o) => h('tr', null,
      h('td', { class: 'muted' }, fmtDateTime(o.start)), h('td', { class: 'muted' }, fmtDateTime(o.end)),
      h('td', null, dur(Math.round((o.end - o.start) / 60))),
      h('td', null, limit && o.peak > limit ? tag(num(o.peak), 'bad') : num(o.peak)),
      h('td', { class: 'mono wrap' }, o.ips.join(', ')))))))
    : empty(t('net.no_overlaps'));
  page.append(panel(t('net.overlap_list'), oc, over ? t('net.over_limit') : '', true));
  return page;
}

function deviceTag(d, count) {
  const label = [d.os + (d.vendor ? ' (' + d.vendor + ')' : ''), d.browser].filter(Boolean).join(', ');
  // a guess built on little evidence is shown as a guess
  const weak = d.confidence < 0.35;
  return h('span', { class: weak ? 'tag' : 'tag acc', title: t('confidence', { p: num(d.confidence * 100) }) + (weak ? ' - ' + t('dev.weak') : '') },
    weak ? '? ' + label : label,
    h('span', { class: 'faint' }, num(d.confidence * 100) + '%'), count ? h('span', { class: 'faint' }, 'x' + num(count)) : null);
}

// ---------------------------------------------------------------- sleep

function sleep(r) {
  const s = r.sleep;
  const page = h('div', { class: 'stack' });
  if (!s.days_analyzed) {
    page.append(panel(null, empty(t('sl.not_enough', { n: num(r.sleep_window_days) }), t('sl.method'))));
    return page;
  }
  page.append(stats([
    { v: digits(s.typical_start), l: t('sl.bedtime'), s: t('sl.std', { m: num(s.std_minutes) }) },
    { v: digits(s.typical_end), l: t('sl.wake'), s: t('sl.nights', { n: num(s.days_analyzed) }) },
    { v: dur(s.avg_minutes), l: t('sl.duration'), s: t('sl.window', { n: num(r.sleep_window_days) }) },
    { v: num(s.confidence * 100) + '%', l: t('sl.confidence'), s: t('sl.connected_share', { p: num(s.connected_share * 100) }) },
  ]));
  page.append(h('div', { class: 'alert' }, t('sl.explain')));

  const card = h('div');
  const ticks = [12, 15, 18, 21, 0, 3, 6, 9, 12];
  card.append(h('div', { class: 'sleep-axis' }, h('div'), h('div', { class: 'ticks' }, ticks.map((hr, i) => h('span', { style: { left: (i / 8) * 100 + '%' } }, String(hr).padStart(2, '0')))), h('div')));
  const days = [...s.days].reverse();
  for (const d of days) {
    const startSec = d.start * 60, endSec = d.end * 60;
    const noon = noonBefore(d.date);
    const left = Math.max(0, ((startSec - noon) / 86400) * 100);
    const width = Math.max(0.5, ((endSec - startSec) / 86400) * 100);
    card.append(h('div', { class: 'sleep-row' },
      h('div', { class: 'd' }, fmtDateStr(d.date, { month: 'short', day: 'numeric', weekday: 'short' })),
      h('div', { class: 'sleep-track' },
        h('div', { class: 'night', style: { left: '37.5%', width: '33.3%' } }),
        h('div', { class: 'sl ' + d.kind + (d.atypical ? ' atyp' : ''), style: { left: left + '%', width: width + '%' }, title: `${fmtTime(startSec)} - ${fmtTime(endSec)}, ${dur(d.minutes)}` })),
      h('div', { class: 'faint small', dir: isFa() ? 'rtl' : 'ltr', style: { textAlign: 'start' } },
        h('span', { dir: 'ltr', style: { unicodeBidi: 'isolate' } }, `${fmtTime(startSec)} - ${fmtTime(endSec)}`), '  ', dur(d.minutes))));
  }
  card.append(h('div', { class: 'legend' },
    h('span', null, h('i', { class: 'swatch', style: { background: 'var(--warn)' } }), t('sl.kind.connected')),
    h('span', null, h('i', { class: 'swatch', style: { background: 'var(--accent)' } }), t('sl.kind.offline')),
    h('span', null, h('i', { class: 'swatch', style: { background: 'var(--accent)', opacity: 0.45 } }), t('sl.kind.atypical'))));
  page.append(panel(t('sl.nightly'), card, t('sl.nightly_hint')));

  const tc = h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, ['col.date', 'col.from', 'col.to', 'col.duration', 'sl.kind', 'sl.bg_minutes', 'sl.awakenings'].map((k) => h('th', null, t(k))))),
    h('tbody', null, [...days].reverse().map((d) => h('tr', null,
      h('td', null, fmtDateStr(d.date, { month: 'short', day: 'numeric' })),
      h('td', null, fmtTime(d.start * 60)), h('td', null, fmtTime(d.end * 60)), h('td', null, dur(d.minutes)),
      h('td', null, tag(t('sl.kind.' + d.kind), d.kind === 'connected' ? 'warn' : ''), d.atypical ? [' ', tag(t('sl.kind.atypical'))] : null),
      h('td', null, num(d.present)), h('td', null, num(d.awakenings)))))));
  page.append(panel(t('sl.table'), tc, null, true));
  return page;
}

// noonBefore returns the unix time of 12:00 (display timezone) on the day
// before the given morning date.
function noonBefore(date) {
  const prev = new Date(date + 'T12:00:00Z');
  prev.setUTCDate(prev.getUTCDate() - 1);
  const ds = prev.toISOString().slice(0, 10);
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
