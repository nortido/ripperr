import { authRedirecting, gotoLogin } from './auth.js';

export let qbitCategoriesCache = null;
export let qbitCategoriesUnavailable = false;

export async function fetchJSON(url, options = {}) {
  const headers = { ...options.headers };
  if (options.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(url, { ...options, headers });
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
