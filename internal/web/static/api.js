import { authRedirecting, gotoLogin } from './auth.js';
import { hydrateIcons } from './icons.js';
import { textDialog } from './confirm.js';

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
    // текст ставим один раз. Если писать его в setOffline, role="status"
    // зачитает «нет связи» как раз в тот момент, когда связь вернулась
    el.textContent = t('common.offline');
    document.body.appendChild(el);
  }
  return el;
}

export function setOffline(next) {
  if (offline === next) return;
  offline = next;
  offlineBanner().classList.toggle('visible', next);
}

export async function fetchJSON(url, options = {}) {
  const headers = { ...options.headers, 'X-Ripperr': '1' };
  if (options.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  let res;
  try {
    res = await fetch(url, { ...options, headers });
  } catch (err) {
    // до сервера не дошли. Ловим здесь, потому что статус дашборда глотает свои ошибки
    // и упавший сервер выглядел живым. Отмена запроса ни о чем не говорит
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
    const err = new Error(msg);
    err.status = res.status;
    err.code = data && data.code;
    throw err;
  }
  return data;
}

let currentToast = null;
let lastFocusOutsideToast = null;

// кнопка тоста после клика получает фокус, а потом тост удаляется: вернуть фокус надо на прежний элемент
document.addEventListener('focusin', function (e) {
  if (e.target instanceof Element && !e.target.closest('#toast-container')) lastFocusOutsideToast = e.target;
}, true);
document.addEventListener('focusout', function (e) {
  // фокус ушел в пустоту (клик по фону): иначе потом вернули бы его в давно брошенное поле
  if (!e.relatedTarget && e.target === lastFocusOutsideToast) lastFocusOutsideToast = null;
}, true);

function removeToast(entry) {
  clearTimeout(entry.timer);
  entry.el.remove();
  if (currentToast === entry) currentToast = null;
}

function reportUIError(text) {
  // сырой fetch: fetchJSON при сбое дергает gotoLogin/setOffline, а отчет не должен ничего показывать
  fetch('/api/events', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Ripperr': '1' },
    body: JSON.stringify({ text: String(text).slice(0, 1000), path: location.hash }),
    keepalive: true,
  }).catch(function () {});
}

function iconButton(className, name, label) {
  const b = document.createElement('button');
  b.type = 'button';
  b.className = className;
  b.setAttribute('aria-label', label);
  b.setAttribute('data-icon', name);
  return b;
}

function addToastControls(entry, message, type) {
  const el = entry.el;
  const text = el.querySelector('.toast-text');
  const actions = document.createElement('div');
  actions.className = 'toast-actions';
  const close = iconButton('toast-close', 'x', t('common.close'));
  actions.appendChild(close);
  el.appendChild(actions);
  close.addEventListener('click', function () { removeToast(entry); });
  if (text.scrollHeight > text.clientHeight + 1) {
    el.classList.add('is-long');
    const expand = iconButton('toast-expand', 'chevron', t('toast.expand'));
    expand.setAttribute('aria-expanded', 'false');
    actions.prepend(expand);
    expand.addEventListener('click', function () {
      const open = el.classList.toggle('is-expanded');
      expand.setAttribute('aria-expanded', open ? 'true' : 'false');
      expand.setAttribute('aria-label', open ? t('toast.collapse') : t('toast.expand'));
      // раскрытый тост читают долго: автоскрытие отключаем насовсем
      clearTimeout(entry.timer);
      el.style.opacity = '';
    });
    if (text.scrollHeight > window.innerHeight * 0.7 - 56) {
      el.classList.add('has-details');
      const more = document.createElement('button');
      more.type = 'button';
      more.className = 'toast-more';
      more.textContent = t('toast.details');
      more.addEventListener('click', function () {
        removeToast(entry);
        // ошибки уже записаны в историю: полный текст и копирование там, остальное в окне
        if (type === 'error') import('./logs.js').then(function (m) { m.openEventsAndFocus(message); });
        else textDialog(message, { returnFocus: lastFocusOutsideToast });
      });
      el.appendChild(more);
    }
  }
  hydrateIcons(actions);
}

export function showToast(message, type) {
  if (authRedirecting && type === 'error') return;
  if (type === 'error') reportUIError(message);
  const container = document.getElementById('toast-container');
  const cls =
    type === 'success' ? 'toast-success' :
    type === 'error' ? 'toast-error' :
    'toast-info';
  if (currentToast) removeToast(currentToast);
  const el = document.createElement('div');
  el.className = 'toast ' + cls;
  const text = document.createElement('div');
  text.className = 'toast-text';
  text.textContent = message;
  el.appendChild(text);
  const entry = { el: el, timer: null };
  currentToast = entry;
  container.appendChild(el);
  addToastControls(entry, message, type);
  entry.timer = setTimeout(function () {
    el.style.opacity = '0';
    el.style.transition = 'opacity 0.2s';
    entry.timer = setTimeout(function () { removeToast(entry); }, 200);
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
