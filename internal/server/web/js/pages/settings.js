import { api, guard } from '../api.js';
import { h, t, toast, state, catName, confirmBox, num, icon } from '../util.js';

const TZS = ['Local', 'Asia/Tehran', 'UTC', 'Europe/Berlin', 'Europe/London', 'Europe/Istanbul', 'Asia/Dubai', 'Asia/Baku', 'Asia/Tashkent', 'America/New_York'];

function sw(label, checked, onchange) {
  const input = h('input', { type: 'checkbox', checked: checked || null });
  input.addEventListener('change', () => onchange(input.checked));
  return h('label', { class: 'switch' }, input, label);
}

function field(label, control, help) {
  return h('div', { class: 'field' }, h('label', null, label), control, help ? h('div', { class: 'help' }, help) : null);
}

function numInput(value, onchange, min, max) {
  const i = h('input', { class: 'input', type: 'number', value: value, min, max, dir: 'ltr' });
  i.addEventListener('input', () => onchange(Number(i.value)));
  return i;
}

export async function settingsPage(ctx) {
  const s = await api.get('settings');
  const cats = state.meta.categories.map((c) => c.id);
  const page = h('div', { class: 'stack', style: { maxWidth: '980px' } });

  // ---- general
  const tz = h('input', { class: 'input', list: 'tzlist', value: s.display_timezone, dir: 'ltr', oninput: (e) => { s.display_timezone = e.target.value.trim(); } });
  const general = h('div', { class: 'card' }, h('h2', null, t('set.general')),
    h('div', { class: 'cols c2' },
      field(t('set.tz'), h('div', null, tz, h('datalist', { id: 'tzlist' }, TZS.map((z) => h('option', { value: z })))), t('set.tz_help')),
      field(t('set.server_lang'), h('select', { class: 'input', onchange: (e) => { s.language = e.target.value; } },
        h('option', { value: 'fa', selected: s.language === 'fa' || null }, 'فارسی'), h('option', { value: 'en', selected: s.language === 'en' || null }, 'English')), t('set.server_lang_help')),
      field(t('set.gap'), numInput(s.session_gap_min, (v) => { s.session_gap_min = v; }, 1, 120), t('set.gap_help')),
      field(t('set.online_window'), numInput(s.online_window_min, (v) => { s.online_window_min = v; }, 1, 60), t('set.online_window_help'))));

  // ---- telegram
  const tg = s.telegram;
  const token = h('input', { class: 'input', type: 'password', dir: 'ltr', placeholder: s.token_set ? '••••••••••  (' + t('set.token_saved') + ')' : '123456:ABC…', autocomplete: 'off', oninput: (e) => { tg.token = e.target.value; } });
  const chats = h('input', { class: 'input', dir: 'ltr', value: (tg.chat_ids || []).join(', '), placeholder: '123456789, -1001234567890', oninput: (e) => { tg.chat_ids = e.target.value.split(/[,\s]+/).filter(Boolean).map(Number).filter((n) => Number.isFinite(n)); } });
  const proxy = h('input', { class: 'input', dir: 'ltr', value: tg.proxy || '', placeholder: 'socks5://user:pass@host:1080', oninput: (e) => { tg.proxy = e.target.value.trim(); } });
  const apiBase = h('input', { class: 'input', dir: 'ltr', value: tg.api_base || '', placeholder: 'https://api.telegram.org', oninput: (e) => { tg.api_base = e.target.value.trim(); } });
  const telegram = h('div', { class: 'card' }, h('h2', null, t('set.telegram')),
    h('div', { style: { marginBottom: '14px' } }, sw(t('set.tg_enabled'), tg.enabled, (v) => { tg.enabled = v; })),
    h('div', { class: 'cols c2' },
      field(t('set.tg_token'), token, t('set.tg_token_help')),
      field(t('set.tg_chats'), chats, t('set.tg_chats_help')),
      field(t('set.tg_proxy'), proxy, t('set.tg_proxy_help')),
      field(t('set.tg_api'), apiBase, t('set.tg_api_help'))),
    h('div', { style: { marginBottom: '12px' } }, sw(t('set.tg_commands'), tg.commands, (v) => { tg.commands = v; })),
    h('div', { class: 'row' },
      h('button', { class: 'btn', onclick: async (e) => {
        e.currentTarget.disabled = true;
        const ok = await save(s, token);
        if (ok) {
          const r = await guard(() => api.post('telegram/test'));
          if (r) toast(t('set.tg_ok', { bot: r.bot || '' }), 'ok');
        }
        e.currentTarget.disabled = false;
      } }, icon('bell'), t('set.tg_test')),
      s.token_set ? h('button', { class: 'btn danger', onclick: () => { s.clear_token = true; tg.enabled = false; toast(t('set.tg_clear_hint')); } }, t('set.tg_clear')) : null));

  // ---- alerts
  const al = s.alerts;
  const rules = [
    ['max_ips', 'set.al.max_ips', 'set.al.max_ips_help', true, true],
    ['new_ip', 'set.al.new_ip', 'set.al.new_ip_help', false, false],
    ['new_country', 'set.al.new_country', 'set.al.new_country_help', false, false],
    ['traffic_spike', 'set.al.spike', 'set.al.spike_help', true, true],
    ['quota_warn', 'set.al.quota', 'set.al.quota_help', true, true],
    ['expiry_warn', 'set.al.expiry', 'set.al.expiry_help', true, true],
    ['node_offline', 'set.al.node', 'set.al.node_help', true, true],
    ['log_stale', 'set.al.log', 'set.al.log_help', true, true],
    ['daily_digest', 'set.al.digest', 'set.al.digest_help', true, false],
  ];
  const alerts = h('div', { class: 'card' }, h('h2', null, t('set.alerts')),
    h('p', { class: 'muted', style: { marginTop: 0 } }, t('set.alerts_intro')),
    rules.map(([key, label, help, hasValue, hasCooldown]) => {
      const a = al[key];
      return h('div', { style: { display: 'grid', gridTemplateColumns: 'minmax(200px, 1.4fr) 110px 110px', gap: '12px', alignItems: 'center', padding: '10px 0', borderTop: '1px solid var(--border)' } },
        h('div', null, sw(t(label), a.enabled, (v) => { a.enabled = v; }), h('div', { class: 'help muted', style: { marginTop: '3px' } }, t(help))),
        hasValue ? h('div', null, numInput(a.value, (v) => { a.value = v; }, 0), h('div', { class: 'faint', style: { fontSize: '11px' } }, t(label + '.unit'))) : h('div'),
        hasCooldown ? h('div', null, numInput(a.cooldown_min, (v) => { a.cooldown_min = v; }, 0), h('div', { class: 'faint', style: { fontSize: '11px' } }, t('set.cooldown'))) : h('div'));
    }),
    h('div', { style: { borderTop: '1px solid var(--border)', paddingTop: '12px', marginTop: '4px' } },
      field(t('set.min_overlap'), numInput(al.min_overlap_min, (v) => { al.min_overlap_min = v; }, 1, 60), t('set.min_overlap_help'))));

  // ---- accuracy: the thresholds behind time, presence and device figures
  const ac = s.accuracy;
  const DEF = { bridge_min: 4, traffic_bridge_max: 45, traffic_share: 70, active_kb: 30, heavy_kb: 1024, min_requests: 2, count_polling: false, separate_ip_changes: false };
  const acBox = h('div');
  const drawAcc = () => acBox.replaceChildren(
    h('div', { class: 'cols c2' },
      field(t('acc.bridge'), numInput(ac.bridge_min, (v) => { ac.bridge_min = v; }, 0, 15), t('acc.bridge_help')),
      field(t('acc.traffic_max'), numInput(ac.traffic_bridge_max, (v) => { ac.traffic_bridge_max = v; }, 0, 180), t('acc.traffic_max_help')),
      field(t('acc.traffic_share'), numInput(ac.traffic_share, (v) => { ac.traffic_share = v; }, 30, 100), t('acc.traffic_share_help')),
      field(t('acc.min_requests'), numInput(ac.min_requests, (v) => { ac.min_requests = v; }, 1, 10), t('acc.min_requests_help')),
      field(t('acc.active_kb'), numInput(ac.active_kb, (v) => { ac.active_kb = v; }, 1, 10240), t('acc.active_kb_help')),
      field(t('acc.heavy_kb'), numInput(ac.heavy_kb, (v) => { ac.heavy_kb = v; }, 1, 102400), t('acc.heavy_kb_help'))),
    h('div', { class: 'stack', style: { gap: '8px' } },
      sw(t('acc.count_polling'), ac.count_polling, (v) => { ac.count_polling = v; }),
      sw(t('acc.separate_ips'), ac.separate_ip_changes, (v) => { ac.separate_ip_changes = v; })));
  drawAcc();
  const accuracy = h('div', { class: 'card' }, h('h2', null, t('acc.title')),
    h('p', { class: 'muted', style: { marginTop: 0 } }, t('acc.intro')), acBox,
    h('div', { class: 'row', style: { marginTop: '12px' } },
      h('button', { class: 'btn', onclick: () => { Object.assign(ac, DEF); drawAcc(); } }, icon('refresh'), t('acc.reset')),
      h('span', { class: 'help muted' }, t('acc.note'))));

  // ---- custom rules
  const ruleBody = h('tbody');
  const drawRules = () => {
    ruleBody.replaceChildren(...s.custom_rules.map((r, i) => h('tr', null,
      h('td', null, h('input', { class: 'input mono', value: r.domain, placeholder: 'example.com', oninput: (e) => { r.domain = e.target.value; } })),
      h('td', null, h('select', { class: 'input', onchange: (e) => { r.category = e.target.value; } }, cats.map((c) => h('option', { value: c, selected: r.category === c || null }, catName(c))))),
      h('td', null, h('input', { class: 'input', value: r.service, placeholder: t('set.rule_service'), oninput: (e) => { r.service = e.target.value; } })),
      h('td', null, h('button', { class: 'btn sm danger', onclick: () => { s.custom_rules.splice(i, 1); drawRules(); } }, icon('trash'))))));
  };
  drawRules();
  const rulesCard = h('div', { class: 'card' }, h('h2', null, t('set.rules')),
    h('p', { class: 'muted', style: { marginTop: 0 } }, t('set.rules_intro')),
    h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' }, h('thead', null, h('tr', null, [t('set.rule_domain'), t('set.rule_cat'), t('set.rule_service'), ''].map((x) => h('th', null, x)))), ruleBody)),
    h('div', { class: 'row', style: { marginTop: '12px' } },
      h('button', { class: 'btn', onclick: () => { s.custom_rules.push({ domain: '', category: 'other', service: '' }); drawRules(); } }, icon('plus'), t('set.rule_add')),
      h('button', { class: 'btn', onclick: async () => {
        if (!(await save(s, token))) return;
        const r = await guard(() => api.post('rules/reclassify'));
        if (r) toast(t('set.reclassified', { n: num(r.changed) }), 'ok');
      } }, icon('refresh'), t('set.reclassify')),
      h('span', { class: 'help muted' }, t('set.reclassify_help'))));

  // ---- sites no rule knows: one click to file them under a category
  const unBody = h('tbody');
  const unCard = h('div', { class: 'card' }, h('h2', null, t('set.unclassified')),
    h('p', { class: 'muted', style: { marginTop: 0 } }, t('set.unclassified_help')),
    h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' }, unBody)));
  api.get('domains/unclassified').then((list) => {
    if (!list.length) { unBody.replaceChildren(h('tr', null, h('td', { class: 'muted' }, t('set.unclassified_none')))); return; }
    unBody.replaceChildren(...list.map((u) => {
      const cat = h('select', { class: 'input' }, cats.filter((c) => c !== 'other').map((c) => h('option', { value: c }, catName(c))));
      const row = h('tr', null,
        h('td', { class: 'mono' }, u.domain), h('td', { class: 'num muted' }, num(u.reqs)), h('td', null, cat),
        h('td', null, h('button', { class: 'btn sm', onclick: async () => {
          const r = await guard(() => api.post('rules/add', { domain: u.domain, category: cat.value, service: '' }));
          if (!r) return;
          s.custom_rules = s.custom_rules.filter((x) => x.domain.toLowerCase() !== r.rule.domain);
          s.custom_rules.push(r.rule);
          drawRules();
          row.remove();
          toast(t('set.rule_added', { n: num(r.changed) }), 'ok');
        } }, icon('plus'), t('set.rule_add'))));
      return row;
    }));
  }).catch(() => {});

  // ---- backup & retention
  const bk = s.backup, rt = s.retention;
  const backups = h('div', { class: 'card' }, h('h2', null, t('set.backup')),
    h('div', { style: { marginBottom: '14px' } }, sw(t('set.bk_enabled'), bk.enabled, (v) => { bk.enabled = v; })),
    h('div', { class: 'cols c2' },
      field(t('set.bk_interval'), numInput(bk.interval_hours, (v) => { bk.interval_hours = v; }, 1)),
      field(t('set.bk_keep'), numInput(bk.keep, (v) => { bk.keep = v; }, 1)),
      field(t('set.rt_visits'), numInput(rt.visits_hourly_days, (v) => { rt.visits_hourly_days = v; }, 2), t('set.rt_visits_help')),
      field(t('set.rt_traffic'), numInput(rt.traffic5_days, (v) => { rt.traffic5_days = v; }, 2), t('set.rt_traffic_help'))),
    sw(t('set.bk_telegram'), bk.send_telegram, (v) => { bk.send_telegram = v; }));

  // ---- account
  const oldPw = h('input', { class: 'input', type: 'password', autocomplete: 'current-password', dir: 'ltr' });
  const newPw = h('input', { class: 'input', type: 'password', autocomplete: 'new-password', dir: 'ltr' });
  const account = h('div', { class: 'card' }, h('h2', null, t('set.account')),
    h('div', { class: 'cols c2' }, field(t('set.old_pw'), oldPw), field(t('set.new_pw'), newPw, t('set.new_pw_help'))),
    h('button', { class: 'btn', onclick: async () => {
      const r = await guard(() => api.post('password', { old: oldPw.value, new: newPw.value }));
      if (r) { toast(t('set.pw_changed'), 'ok'); setTimeout(() => location.reload(), 800); }
    } }, t('set.change_pw')));

  const saveBar = h('div', { class: 'row', style: { position: 'sticky', bottom: '12px', justifyContent: 'flex-end', zIndex: 5 } },
    h('button', { class: 'btn pri', style: { boxShadow: 'var(--shadow)', padding: '10px 22px' }, onclick: async () => {
      if (await save(s, token)) { toast(t('set.saved'), 'ok'); state.meta = await api.get('meta'); }
    } }, t('save')));

  page.append(general, telegram, alerts, accuracy, rulesCard, unCard, backups, account, saveBar);
  return page;
}

async function save(s, tokenInput) {
  const body = JSON.parse(JSON.stringify(s));
  delete body.token_set;
  if (!body.telegram.token) body.telegram.token = '';
  const r = await guard(() => api.put('settings', body));
  if (!r) return false;
  Object.assign(s, r);
  if (tokenInput) { tokenInput.value = ''; }
  return true;
}
