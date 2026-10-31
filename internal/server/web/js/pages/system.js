import { api, guard } from '../api.js';
import { h, t, num, bytes, ago, dur, fmtDateTime, fmtDate, icon, modal, confirmBox, toast, empty, stats, tag, panel } from '../util.js';

export async function systemPage(ctx) {
  const [st, bk] = await Promise.all([api.get('status'), api.get('backups')]);
  const page = h('div', { class: 'stack' });

  page.append(stats([
    { v: num(st.online), l: t('kpi.online'), s: t('kpi.ips_now', { n: num(st.online_ips) }) },
    { v: bytes(st.db.size_bytes), l: t('sys.db_size'), s: t('sys.rows', { n: num(st.db.act_rows + st.db.visit_rows) }) },
    { v: num(st.db.users), l: t('sys.users'), s: t('sys.ips_domains', { a: num(st.db.ips), b: num(st.db.domains) }) },
    { v: dur(Math.floor(st.uptime / 60)), l: t('sys.uptime'), s: 'v' + st.version },
  ]));

  if (st.cdn_ips > 0 && st.cdn_ips * 2 >= st.online_ips) {
    page.append(h('div', { class: 'alert warn' }, h('b', null, t('sys.cdn_title')), h('div', { style: { margin: '4px 0 8px' } }, t('sys.cdn_text')),
      h('div', { class: 'code' }, 'CF-Connecting-IP\nAR-Real-IP\nX-Real-IP\nX-Forwarded-For'),
      h('a', { href: '#/guide', class: 'btn sm', style: { marginTop: '10px' } }, t('nav.guide'))));
  }

  // collection health
  const logTone = { ok: 'ok', waiting: 'warn', disabled: 'bad', missing: 'bad', no_panel: 'warn' }[st.log.state] || '';
  const logBody = h('div', null,
    h('div', { class: 'row', style: { marginBottom: '8px' } }, tag(t('sys.log.' + st.log.state), logTone), st.log.path ? h('span', { class: 'mono muted' }, st.log.path) : null),
    ['disabled', 'missing', 'no_panel'].includes(st.log.state)
      ? h('div', { class: 'alert ' + (logTone === 'bad' ? 'bad' : 'warn'), style: { marginBottom: '10px' } }, t('sys.msg.' + st.log.state),
        st.log.message && st.log.state === 'missing' ? h('div', { class: 'mono faint', style: { marginTop: '4px' } }, st.log.message) : null,
        h('div', { style: { marginTop: '8px' } }, h('a', { class: 'btn sm', href: '#/guide' }, t('nav.guide')))) : null,
    h('div', { class: 'muted' }, st.last_line ? t('sys.last_line', { t: ago(st.last_line) }) : t('sys.no_lines')),
    h('div', { class: 'muted' }, t('sys.lines', { n: num(st.lines) })));

  const p = st.panel;
  const panelBody = h('div', null,
    h('div', { class: 'row' }, tag(p.type === 'none' ? t('sys.panel_none') : p.ok ? t('sys.panel_ok') : t('sys.panel_err'), p.type === 'none' ? '' : p.ok ? 'ok' : 'bad'),
      p.type !== 'none' ? h('span', { class: 'muted' }, p.type + ', ' + t('n_accounts', { n: num(p.users) })) : null),
    p.error ? h('div', { class: 'alert bad', style: { marginTop: '10px' } }, p.error, h('div', { class: 'small', style: { marginTop: '4px' } }, t('sys.panel_fix'))) : null,
    p.last_ok ? h('div', { class: 'muted', style: { marginTop: '8px' } }, t('sys.panel_last', { t: ago(p.last_ok) })) : null,
    h('div', { class: 'row', style: { marginTop: '10px' } },
      tag('Telegram: ' + (st.telegram ? t('on') : t('off')), st.telegram ? 'ok' : ''), tag('GeoIP: ' + (st.geo ? t('on') : t('off')), st.geo ? 'ok' : ''),
      tag(t('sys.speed') + ': ' + (st.speed.ok ? t('on') : t('off')), st.speed.ok ? 'ok' : '')),
    !st.speed.ok && p.type !== 'none' ? h('div', { class: 'alert', style: { marginTop: '10px' } }, t('sys.speed_err')) : null);
  page.append(h('div', { class: 'cols c2' }, panel(t('sys.collection'), logBody), panel(t('sys.panel'), panelBody)));

  if (st.db.first_hour) {
    page.append(panel(t('sys.data_span'), h('div', null, fmtDate(st.db.first_hour * 3600) + '  -  ' + fmtDate(st.db.last_hour * 3600 + 3599))));
  }

  // backups
  const fileInput = h('input', { type: 'file', accept: '.gz,.db', class: 'hidden', onchange: async (e) => {
    const f = e.target.files[0];
    if (!f) return;
    if (!(await confirmBox(t('bk.restore'), t('bk.restore_text'), true))) return;
    const fd = new FormData();
    fd.append('file', f);
    const r = await guard(() => api.post('backups/restore', fd));
    if (r) restartPrompt();
  } });
  const bar = h('div', { class: 'row', style: { padding: '0 16px 12px' } },
    h('button', { class: 'btn pri', onclick: async (e) => {
      e.currentTarget.disabled = true;
      const r = await guard(() => api.post('backups'));
      if (r) toast(t('bk.created'), 'ok');
      ctx.refresh();
    } }, icon('database'), t('bk.create')),
    h('button', { class: 'btn', onclick: () => fileInput.click() }, icon('upload'), t('bk.upload')), fileInput,
    h('span', { class: 'muted' }, t('bk.hint')));
  const list = bk.backups.length ? h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
    h('thead', null, h('tr', null, [t('col.file'), t('col.size'), t('col.created'), ''].map((x) => h('th', null, x)))),
    h('tbody', null, bk.backups.map((b) => h('tr', null,
      h('td', { class: 'mono' }, b.name), h('td', null, bytes(b.size)), h('td', { class: 'muted' }, fmtDateTime(b.created)),
      h('td', null, h('div', { class: 'row', style: { gap: '6px' } },
        h('a', { class: 'btn sm', href: 'api/backups/' + b.name, download: b.name }, icon('download'), t('download')),
        h('button', { class: 'btn sm', onclick: async () => {
          if (!(await confirmBox(t('bk.restore'), t('bk.restore_text'), true))) return;
          const r = await guard(() => api.post('backups/restore?name=' + encodeURIComponent(b.name)));
          if (r) restartPrompt();
        } }, icon('refresh'), t('bk.restore')),
        h('button', { class: 'btn sm danger', 'aria-label': t('delete'), onclick: async () => {
          if (await confirmBox(t('delete'), b.name, true)) { await guard(() => api.del('backups/' + b.name)); ctx.refresh(); }
        } }, icon('trash'))))))))) : empty(t('bk.none'), t('bk.none_hint'));
  const pending = bk.pending_restore ? h('div', { class: 'alert warn', style: { margin: '0 16px 12px' } }, t('bk.pending'), ' ', h('button', { class: 'btn sm', onclick: () => restartNow() }, t('restart'))) : null;
  const bp = panel(t('bk.title'), h('div', null, bar, pending, list), t('bk.count', { n: num(bk.backups.length) }), true);
  page.append(bp);
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
