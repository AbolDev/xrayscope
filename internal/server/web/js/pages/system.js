import { api, guard } from '../api.js';
import { h, t, num, bytes, ago, dur, fmtDateTime, fmtDate, icon, modal, confirmBox, toast, empty } from '../util.js';
import { kpi } from './overview.js';

export async function systemPage(ctx) {
  const [st, bk] = await Promise.all([api.get('status'), api.get('backups')]);
  const page = h('div', { class: 'stack' });

  // collection health
  const logTone = { ok: 'ok', waiting: 'warn', disabled: 'bad', missing: 'bad', no_panel: 'warn' }[st.log.state] || '';
  const logCard = h('div', { class: 'card' }, h('h2', null, t('sys.collection')),
    h('div', { class: 'row', style: { marginBottom: '8px' } },
      h('span', { class: 'chip ' + logTone }, t('sys.log.' + st.log.state)),
      st.log.path ? h('span', { class: 'mono muted' }, st.log.path) : null),
    ['disabled', 'missing', 'no_panel'].includes(st.log.state) ? h('div', { class: 'alert ' + (logTone === 'bad' ? 'bad' : 'warn'), style: { marginBottom: '10px' } }, t('sys.msg.' + st.log.state), st.log.message && st.log.state === 'missing' ? h('div', { class: 'mono faint', style: { marginTop: '4px' } }, st.log.message) : null) : null,
    h('div', { class: 'muted' }, st.last_line ? t('sys.last_line', { t: ago(st.last_line) }) : t('sys.no_lines'), ' · ', t('sys.lines', { n: num(st.lines) })));
  if (st.log.state === 'disabled' || st.log.state === 'missing') {
    logCard.append(h('div', { style: { marginTop: '12px' } }, h('div', { class: 'help muted' }, t('sys.enable_log')),
      h('div', { class: 'code', style: { marginTop: '6px' } }, '"log": {\n  "access": "/var/log/xray/access.log",\n  "loglevel": "warning"\n}')));
  }

  const p = st.panel;
  const panelCard = h('div', { class: 'card' }, h('h2', null, t('sys.panel')),
    h('div', { class: 'row' },
      h('span', { class: 'chip ' + (p.type === 'none' ? '' : p.ok ? 'ok' : 'bad') }, p.type === 'none' ? t('sys.panel_none') : p.ok ? t('sys.panel_ok') : t('sys.panel_err')),
      p.type !== 'none' ? h('span', { class: 'muted' }, p.type + ' · ' + t('n_accounts', { n: num(p.users) })) : null),
    p.error ? h('div', { class: 'alert bad', style: { marginTop: '10px' } }, p.error) : null,
    h('div', { class: 'muted', style: { marginTop: '8px' } }, p.last_ok ? t('sys.panel_last', { t: ago(p.last_ok) }) : ''),
    h('div', { class: 'row', style: { marginTop: '10px' } },
      h('span', { class: 'chip ' + (st.telegram ? 'ok' : '') }, 'Telegram: ' + (st.telegram ? t('on') : t('off'))),
      h('span', { class: 'chip ' + (st.geo ? 'ok' : '') }, 'GeoIP: ' + (st.geo ? t('on') : t('off')))));
  page.append(h('div', { class: 'grid g4' },
    kpi('live', 'ok', num(st.online), t('kpi.online'), t('kpi.ips_now', { n: num(st.online_ips) })),
    kpi('database', '', bytes(st.db.size_bytes), t('sys.db_size'), t('sys.rows', { n: num(st.db.act_rows + st.db.visit_rows) })),
    kpi('users', '', num(st.db.users), t('sys.users'), t('sys.ips_domains', { a: num(st.db.ips), b: num(st.db.domains) })),
    kpi('clock', 'warn', dur(Math.floor(st.uptime / 60)), t('sys.uptime'), 'v' + st.version)));
  if (st.cdn_ips > 0 && st.cdn_ips * 2 >= st.online_ips) {
    page.append(h('div', { class: 'alert warn' }, h('b', null, t('sys.cdn_title')), h('div', { style: { marginTop: '4px' } }, t('sys.cdn_text')),
      h('div', { class: 'code', style: { marginTop: '8px' } }, 'CF-Connecting-IP\nAR-Real-IP\nX-Real-IP\nX-Forwarded-For')));
  }
  page.append(h('div', { class: 'grid g2' }, logCard, panelCard));
  if (st.db.first_hour) {
    page.append(h('div', { class: 'card' }, h('h2', null, t('sys.data_span')), h('div', null, fmtDate(st.db.first_hour * 3600) + ' → ' + fmtDate(st.db.last_hour * 3600 + 3599))));
  }

  // backups
  const card = h('div', { class: 'card flush' });
  const fileInput = h('input', { type: 'file', accept: '.gz,.db', class: 'hidden', onchange: async (e) => {
    const f = e.target.files[0];
    if (!f) return;
    if (!(await confirmBox(t('bk.restore'), t('bk.restore_text'), true))) return;
    const fd = new FormData();
    fd.append('file', f);
    const r = await guard(() => api.post('backups/restore', fd));
    if (r) restartPrompt(ctx);
  } });
  card.append(h('h2', null, t('bk.title'), h('span', { class: 'hint' }, t('bk.count', { n: num(bk.backups.length) }))),
    h('div', { class: 'row', style: { padding: '0 18px 14px' } },
      h('button', { class: 'btn primary', onclick: async (e) => {
        e.currentTarget.disabled = true;
        const r = await guard(() => api.post('backups'));
        if (r) toast(t('bk.created'), 'ok');
        ctx.refresh();
      } }, icon('database'), t('bk.create')),
      h('button', { class: 'btn', onclick: () => fileInput.click() }, icon('upload'), t('bk.upload')), fileInput,
      h('span', { class: 'muted' }, t('bk.hint'))));
  if (bk.pending_restore) {
    card.append(h('div', { class: 'alert warn', style: { margin: '0 18px 14px' } }, t('bk.pending'), ' ', h('button', { class: 'btn sm', onclick: () => restartNow() }, t('restart'))));
  }
  card.append(bk.backups.length ? h('div', { class: 'table-wrap' }, h('table', null,
    h('thead', null, h('tr', null, [t('col.file'), t('col.size'), t('col.created'), ''].map((x) => h('th', null, x)))),
    h('tbody', null, bk.backups.map((b) => h('tr', null,
      h('td', { class: 'mono' }, b.name), h('td', null, bytes(b.size)), h('td', { class: 'muted' }, fmtDateTime(b.created)),
      h('td', null, h('div', { class: 'row', style: { gap: '6px' } },
        h('a', { class: 'btn sm', href: 'api/backups/' + b.name, download: b.name }, icon('download'), t('download')),
        h('button', { class: 'btn sm', onclick: async () => {
          if (!(await confirmBox(t('bk.restore'), t('bk.restore_text'), true))) return;
          const r = await guard(() => api.post('backups/restore?name=' + encodeURIComponent(b.name)));
          if (r) restartPrompt(ctx);
        } }, icon('refresh'), t('bk.restore')),
        h('button', { class: 'btn sm danger', onclick: async () => {
          if (await confirmBox(t('delete'), b.name, true)) { await guard(() => api.del('backups/' + b.name)); ctx.refresh(); }
        } }, icon('trash'))))))))) : empty(t('bk.none')));
  page.append(card);
  return page;
}

function restartPrompt() {
  modal(t('bk.staged'), h('p', null, t('bk.staged_text')), [
    { label: t('later') },
    { label: t('restart'), kind: 'primary', onclick: () => restartNow() },
  ]);
}

async function restartNow() {
  await guard(() => api.post('restart'));
  toast(t('restarting'), 'ok');
  setTimeout(() => location.reload(), 4000);
}
