import { api } from '../api.js';
import { h, t, setLang, isFa, setTheme, state, icon } from '../util.js';

export function loginPage(onDone, rerender) {
  const user = h('input', { class: 'input', name: 'username', autocomplete: 'username', value: 'admin', dir: 'ltr' });
  const pass = h('input', { class: 'input', name: 'password', type: 'password', autocomplete: 'current-password', dir: 'ltr' });
  const err = h('div', { class: 'alert bad hidden', role: 'alert', style: { marginBottom: '12px' } });
  const btn = h('button', { class: 'btn pri', type: 'submit', style: { width: '100%', justifyContent: 'center' } }, t('login'));
  const form = h('form', { onsubmit: async (e) => {
    e.preventDefault();
    err.classList.add('hidden');
    btn.disabled = true;
    try {
      await api.post('login', { username: user.value.trim(), password: pass.value });
      await onDone();
    } catch (ex) {
      err.textContent = ex.message;
      err.classList.remove('hidden');
    } finally { btn.disabled = false; }
  } },
  err,
  h('div', { class: 'field' }, h('label', { for: 'u' }, t('username')), user),
  h('div', { class: 'field' }, h('label', { for: 'p' }, t('password')), pass),
  btn);

  return h('div', { class: 'login-wrap' },
    h('div', { class: 'panel login' },
      h('div', { class: 'brand' }, h('img', { src: 'favicon.svg', alt: '' }), 'XrayScope'),
      h('p', { class: 'muted', style: { textAlign: 'center', marginTop: 0 } }, t('login.sub')),
      form,
      h('div', { class: 'row', style: { justifyContent: 'center', marginTop: '12px' } },
        h('button', { class: 'btn sm ghost', onclick: () => { setLang(isFa() ? 'en' : 'fa'); rerender(); } }, isFa() ? 'English' : 'فارسی'),
        h('button', { class: 'btn sm ghost icon', 'aria-label': t('theme'), onclick: () => { setTheme(state.theme === 'dark' ? 'light' : 'dark'); rerender(); } }, icon(state.theme === 'dark' ? 'sun' : 'moon')))));
}
