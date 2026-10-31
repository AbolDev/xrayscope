import { api, guard } from '../api.js';
import { h, t, num, ago, fmtDateTime, empty, icon, modal, confirmBox, toast, state, tag, panel } from '../util.js';

const INSTALL_CMD = 'bash <(curl -fsSL https://raw.githubusercontent.com/AbolDev/xrayscope/main/install.sh)';

export async function nodesPage(ctx) {
  const nodes = await api.get('nodes');
  const page = h('div', { class: 'stack' });
  page.append(h('div', { class: 'row' },
    h('button', { class: 'btn pri', onclick: () => addNode(ctx) }, icon('plus'), t('node.add')),
    h('span', { class: 'muted' }, t('node.hint'))));

  let body;
  if (!nodes.length) {
    body = empty(t('node.none'), t('node.none_hint'), h('button', { class: 'btn pri', onclick: () => addNode(ctx) }, icon('plus'), t('node.add')));
  } else {
    body = h('div', { class: 'tbl-wrap' }, h('table', { class: 'tbl' },
      h('thead', null, h('tr', null, ['node.name', 'col.status', 'col.last_seen', 'node.ip', 'node.version', 'node.panel', 'node.lines', ''].map((k) => h('th', null, k ? t(k) : '')))),
      h('tbody', null, nodes.map((n) => h('tr', null,
        h('td', null, h('b', null, n.name), h('div', { class: 'muted' }, n.mode === 'pull' ? t('node.mode_pull_short') : t('node.mode_push_short'))),
        h('td', null, n.last_error ? h('span', { title: n.last_error }, tag(t('node.error'), 'bad'), h('div', { class: 'muted' }, n.last_error))
          : n.online ? tag(t('online'), 'ok') : n.last_seen ? tag(t('offline'), 'bad') : tag(t('node.never'))),
        h('td', { class: 'muted', title: n.last_seen ? fmtDateTime(n.last_seen) : '' }, ago(n.last_seen)),
        h('td', { class: 'mono' }, n.mode === 'pull' ? n.url : (n.last_ip || '-')),
        h('td', { class: 'muted' }, n.version || '-'),
        h('td', { class: 'muted' }, n.panel || '-'),
        h('td', { class: 'num' }, num(n.lines_in)),
        h('td', null, h('button', { class: 'btn sm danger', 'aria-label': t('node.delete'), onclick: async () => {
          if (await confirmBox(t('node.delete'), t('node.delete_text', { name: n.name }), true)) {
            await guard(() => api.del('nodes/' + n.id));
            ctx.refresh();
          }
        } }, icon('trash'))))))));
  }
  const p = panel(null, body, null, true);
  p.querySelector('.pb').style.padding = '0';
  page.append(p);
  return page;
}

function addNode(ctx) {
  const name = h('input', { class: 'input', placeholder: 'de-1', dir: 'ltr' });
  const mode = h('select', { class: 'input' },
    h('option', { value: 'pull' }, t('node.mode_pull')),
    h('option', { value: 'push' }, t('node.mode_push')));
  const url = h('input', { class: 'input', placeholder: 'http://203.0.113.5:8686', dir: 'ltr' });
  const user = h('input', { class: 'input', placeholder: 'admin', dir: 'ltr', autocomplete: 'off' });
  const pass = h('input', { class: 'input', type: 'password', dir: 'ltr', autocomplete: 'new-password' });
  const proxy = h('input', { class: 'input', placeholder: 'socks5://127.0.0.1:1080', dir: 'ltr' });
  const insecure = h('input', { type: 'checkbox' });
  const pullFields = h('div', null,
    h('div', { class: 'field' }, h('label', null, t('node.url')), url, h('div', { class: 'help' }, t('node.url_help'))),
    h('div', { class: 'field' }, h('label', null, t('node.user')), user),
    h('div', { class: 'field' }, h('label', null, t('node.pass')), pass, h('div', { class: 'help' }, t('node.pass_help'))),
    h('div', { class: 'field' }, h('label', null, t('node.proxy')), proxy, h('div', { class: 'help' }, t('node.proxy_opt'))),
    h('label', { class: 'row' }, insecure, h('span', null, t('node.insecure'))));
  const sync = () => { pullFields.style.display = mode.value === 'pull' ? '' : 'none'; };
  mode.addEventListener('change', sync);
  sync();
  modal(t('node.add'), h('div', null,
    h('div', { class: 'field' }, h('label', null, t('node.name')), name, h('div', { class: 'help' }, t('node.name_help'))),
    h('div', { class: 'field' }, h('label', null, t('node.mode')), mode, h('div', { class: 'help' }, t('node.mode_help'))),
    pullFields), [
    { label: t('cancel') },
    { label: t('create'), kind: 'primary', onclick: async () => {
      if (mode.value === 'pull') {
        const r = await guard(() => api.post('nodes', {
          name: name.value.trim(), mode: 'pull', url: url.value.trim(), username: user.value.trim(),
          password: pass.value, proxy: proxy.value.trim(), insecure: insecure.checked,
        }));
        if (!r) return false;
        toast(t('node.connected'), 'ok');
        ctx.refresh();
        return;
      }
      const r = await guard(() => api.post('nodes', { name: name.value.trim() }));
      if (!r) return false;
      showToken(r, ctx);
    } },
  ]);
}

function showToken(r, ctx) {
  const server = location.origin + (state.meta.base_path || '');
  const cmd = `${INSTALL_CMD} agent \\\n  --server ${server} \\\n  --token ${r.token} \\\n  --name ${r.node.name}`;
  const manual = `xrayscope init -f -role agent \\\n  -server ${server} \\\n  -token ${r.token} \\\n  -name ${r.node.name}\nsystemctl enable --now xrayscope`;
  modal(t('node.created'), h('div', null,
    h('div', { class: 'alert warn', style: { marginBottom: '12px' } }, t('node.token_once')),
    h('div', { class: 'field' }, h('label', null, t('node.token')), h('div', { class: 'code' }, r.token)),
    h('div', { class: 'field' }, h('label', null, t('node.install')), h('div', { class: 'code' }, cmd)),
    h('div', { class: 'field' }, h('label', null, t('node.manual')), h('div', { class: 'code' }, manual)),
    h('div', { class: 'help muted' }, t('node.proxy_help'))), [
    { label: t('close'), kind: 'primary', onclick: () => { ctx.refresh(); toast(t('node.created'), 'ok'); } },
  ]);
}
