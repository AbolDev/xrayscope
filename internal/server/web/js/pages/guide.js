import { api } from '../api.js';
import { h, t, toast, icon, num, isFa } from '../util.js';

const SNIPPETS = {
  1: '"log": {\n  "access": "/var/log/xray/access.log",\n  "loglevel": "warning"\n}',
  2: '"sniffing": {\n  "enabled": true,\n  "destOverride": ["http", "tls", "quic"],\n  "routeOnly": true\n}',
  3: '"sockopt": {\n  "trustedXForwardedFor": [\n    "X-Real-IP",\n    "X-Forwarded-For",\n    "AR-Real-IP",\n    "CF-Connecting-IP"\n  ]\n}',
};
const STEPS = { 1: 3, 2: 5, 3: 5 };

// Each check is derived from live data so the guide shows what still needs work.
function checks(st) {
  const logOk = st.log.state === 'ok';
  let sniff = 'unknown';
  if (st.dest_requests >= 50) sniff = st.dest_ip_requests / st.dest_requests > 0.5 ? 'warn' : 'ok';
  let cdn = 'unknown';
  if (st.online_ips > 0) cdn = st.cdn_ips * 2 >= st.online_ips ? 'warn' : 'ok';
  return {
    1: logOk ? 'ok' : st.log.state === 'waiting' ? 'unknown' : 'warn',
    2: logOk ? sniff : 'unknown',
    3: logOk ? cdn : 'unknown',
  };
}

function detail(n, st, state) {
  if (n === 2 && st.dest_requests >= 50) return t('g.2.stat', { p: num((st.dest_ip_requests / st.dest_requests) * 100) });
  if (n === 3 && st.online_ips > 0) return t('g.3.stat', { a: num(st.cdn_ips), b: num(st.online_ips) });
  if (n === 1 && st.log.state === 'ok') return t('sys.lines', { n: num(st.lines) });
  return state === 'unknown' ? t('g.wait') : '';
}

function copyBtn(text) {
  return h('button', { class: 'btn sm', onclick: async (e) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch (err) {
      const ta = h('textarea', { style: { position: 'fixed', opacity: 0 } }, text);
      document.body.append(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    toast(t('g.copied'), 'ok');
    e.currentTarget.blur();
  } }, icon('copy'), t('g.copy'));
}

export async function guidePage() {
  const st = await api.get('status');
  const res = checks(st);
  const page = h('div', { class: 'stack', style: { maxWidth: '900px' } });
  page.append(h('div', { class: 'alert' }, t('g.intro')));
  for (const n of [1, 2, 3]) {
    const state = res[n];
    const tone = { ok: 'ok', warn: 'warn', unknown: '' }[state];
    const card = h('div', { class: 'card' },
      h('h2', null, h('span', { class: 'chip primary' }, String(n)), t(`g.${n}.title`),
        h('span', { class: 'hint' }, h('span', { class: 'chip ' + tone }, t('g.' + state)), ' ', detail(n, st, state))),
      h('p', { style: { marginTop: 0 } }, t(`g.${n}.text`)),
      h('div', { class: 'muted', style: { fontWeight: 600, marginBottom: '4px' } }, t('g.steps')),
      h('ol', { style: { margin: '0 0 12px', paddingInlineStart: '22px', lineHeight: 1.9 } },
        Array.from({ length: STEPS[n] }, (_, i) => h('li', null, t(`g.${n}.s${i + 1}`)))),
      h('div', { class: 'muted', style: { fontWeight: 600, marginBottom: '4px' } }, t('g.snippet')),
      h('div', { class: 'code' }, SNIPPETS[n]),
      h('div', { class: 'row', style: { marginTop: '8px' } }, copyBtn(SNIPPETS[n]), h('span', { class: 'help muted' }, t(`g.${n}.note`))));
    page.append(card);
  }
  page.append(h('div', { class: 'alert warn' }, t('g.restart')));
  return page;
}
