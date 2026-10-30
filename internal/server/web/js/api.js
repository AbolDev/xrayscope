import { toast, t } from './util.js';

export class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

let onUnauthorized = () => {};
export function setUnauthorizedHandler(f) { onUnauthorized = f; }

async function call(method, path, { params, body, raw } = {}) {
  let url = 'api/' + path;
  if (params) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') q.set(k, v);
    const s = q.toString();
    if (s) url += '?' + s;
  }
  const opts = { method, headers: { 'X-XS': '1' }, credentials: 'same-origin' };
  if (body instanceof FormData) opts.body = body;
  else if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  let res;
  try {
    res = await fetch(url, opts);
  } catch (e) {
    throw new ApiError(0, t('err.network'));
  }
  if (res.status === 401 && path !== 'login') {
    onUnauthorized();
    throw new ApiError(401, t('err.login_required'));
  }
  if (raw) return res;
  let data = null;
  const text = await res.text();
  if (text) { try { data = JSON.parse(text); } catch (e) { /* not JSON */ } }
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText);
  return data;
}

export const api = {
  get: (path, params) => call('GET', path, { params }),
  post: (path, body, params) => call('POST', path, { body: body === undefined ? {} : body, params }),
  put: (path, body) => call('PUT', path, { body }),
  del: (path) => call('DELETE', path),
  raw: (path, params) => call('GET', path, { params, raw: true }),
};

// guard runs fn and shows API errors as a toast.
export async function guard(fn) {
  try {
    return await fn();
  } catch (e) {
    if (e.status !== 401) toast(e.message, 'bad');
    return undefined;
  }
}
