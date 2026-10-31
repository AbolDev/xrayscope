// One WebSocket shared by every page. The server pushes a snapshot every few
// seconds; pages subscribe and fall back to polling while the socket is down.
const subs = new Set();
let sock = null;
let up = false;
let last = null;
let retry = 1000;
let timer = null;

function connect() {
  if (sock || document.hidden && subs.size === 0) return;
  const url = new URL('api/live', location.href);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  url.hash = '';
  try { sock = new WebSocket(url); } catch { sock = null; schedule(); return; }
  sock.onopen = () => { up = true; retry = 1000; };
  sock.onmessage = (e) => {
    try { last = JSON.parse(e.data); } catch { return; }
    for (const fn of [...subs]) { try { fn(last); } catch { /* a page's handler must not break the others */ } }
  };
  sock.onclose = () => { sock = null; up = false; if (subs.size) schedule(); };
  sock.onerror = () => { try { sock.close(); } catch { /* already closed */ } };
}

function schedule() {
  clearTimeout(timer);
  timer = setTimeout(connect, retry);
  retry = Math.min(retry * 2, 30000);
}

// subscribe calls fn with every snapshot (and at once with the latest one).
// It returns the function that stops the subscription.
export function subscribe(fn) {
  subs.add(fn);
  connect();
  if (last) { try { fn(last); } catch { /* ignore */ } }
  return () => {
    subs.delete(fn);
    if (!subs.size && sock) { const s = sock; sock = null; up = false; s.onclose = null; s.close(); last = null; }
  };
}

export const isLive = () => up;
