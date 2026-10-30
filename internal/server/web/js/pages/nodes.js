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
        h('td', null, h('b', null, n.name)),
        h('td', null, n.online ? tag(t('online'), 'ok') : n.last_seen ? tag(t('offline'), 'bad') : tag(t('node.never'))),
        h('td', { class: 'muted', title: n.last_seen ? fmtDateTime(n.last_seen) : '' }, ago(n.last_seen)),
        h('td', { class: 'mono' }, n.last_ip || '-'),
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
  modal(t('node.add'), h('div', { class: 'field' }, h('label', null, t('node.name')), name, h('div', { class: 'help' }, t('node.name_help'))), [
    { label: t('cancel') },
    { label: t('create'), kind: 'primary', onclick: async () => {
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
