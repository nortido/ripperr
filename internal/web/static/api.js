import { authRedirecting, gotoLogin } from './auth.js';

export let qbitCategoriesCache = null;
export let qbitCategoriesUnavailable = false;

export let offline = false;

function offlineBanner() {
  let el = document.getElementById('offline-banner');
  if (!el) {
    el = document.createElement('div');
    el.id = 'offline-banner';
    el.className = 'offline-banner';
    el.setAttribute('role', 'status');
    document.body.appendChild(el);
  }
  return el;
}

// вешается на fetchJSON, а не на конкретный поллер: статус дашборда глотает
// свои ошибки молча, поэтому раньше отвалившийся сервер выглядел как живой,
// а таймштамп «Обновлено» просто замирал
export function setOffline(next) {
  if (offline === next) return;
  offline = next;
  const el = offlineBanner();
  el.textContent = t('common.offline');
  el.classList.toggle('visible', next);
}

export async function fetchJSON(url, options = {}) {
  const headers = { ...options.headers };
  if (options.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  let res;
  try {
    res = await fetch(url, { ...options, headers });
  } catch (err) {
    // сам fetch отклонился: до сервера не дошли. Ответ с любым HTTP-кодом
    // означает, что сервер на связи, отменённый запрос ни о чём не говорит
    if (err.name !== 'AbortError') setOffline(true);
    throw err;
  }
  setOffline(false);
  if (res.status === 401 && !url.startsWith('/api/auth/')) {
    gotoLogin();
    throw new Error(t('auth.required'));
  }
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      throw new Error(t('common.invalidResponse'));
    }
  }
  if (!res.ok) {
    const msg = data && data.error ? data.error : 'HTTP ' + res.status;
    throw new Error(msg);
  }
  return data;
}

export function showToast(message, type) {
  if (authRedirecting && type === 'error') return;
  const container = document.getElementById('toast-container');
  const cls =
    type === 'success' ? 'toast-success' :
    type === 'error' ? 'toast-error' :
    'toast-info';
  const el = document.createElement('div');
  el.className = 'toast ' + cls;
  el.textContent = message;
  container.appendChild(el);
  setTimeout(function () {
    el.style.opacity = '0';
    el.style.transition = 'opacity 0.2s';
    setTimeout(function () { el.remove(); }, 200);
  }, 4500);
}


export async function ensureQbitCategories() {
  if (qbitCategoriesCache !== null || qbitCategoriesUnavailable) return;
  try {
    const data = await fetchJSON('/api/categories');
    qbitCategoriesCache = data.categories || [];
  } catch (_) {
    qbitCategoriesUnavailable = true;
    qbitCategoriesCache = [];
  }
}
