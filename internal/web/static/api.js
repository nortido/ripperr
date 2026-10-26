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

const TOAST_MAX = 3;
const toasts = [];
const mobileToasts = window.matchMedia('(max-width: 900px)');
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
  const i = toasts.indexOf(entry);
  if (i >= 0) toasts.splice(i, 1);
}

function scheduleHide(entry, ms) {
  clearTimeout(entry.timer);
  entry.timer = setTimeout(function () {
    entry.el.style.transition = 'opacity 0.2s';
    entry.el.style.opacity = '0';
    entry.timer = setTimeout(function () { removeToast(entry); }, 200);
  }, ms);
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
  const text = el.querySelector('.toast-msg');
  const actions = document.createElement('div');
  actions.className = 'toast-actions';
  const close = iconButton('toast-close', 'x', t('common.close'));
  actions.appendChild(close);
  el.appendChild(actions);
  close.addEventListener('click', function () { removeToast(entry); });
  // на мобильном у тоста с заголовком пояснение скрыто, открыть его можно только шевроном
  const titledMobile = mobileToasts.matches && el.querySelector('.toast-title') !== null;
  if (titledMobile || text.scrollHeight > text.clientHeight + 1) {
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

function swipeToDismiss(entry) {
  const el = entry.el;
  let start = null;
  let dx = 0;
  let dy = 0;
  function reset() {
    el.style.transition = 'transform 0.15s ease';
    el.style.transform = '';
    start = null;
  }
  el.addEventListener('pointerdown', function (e) {
    if (e.pointerType !== 'touch' || e.target.closest('button')) return;
    start = { x: e.clientX, y: e.clientY, drag: false };
    dx = 0;
    dy = 0;
  });
  el.addEventListener('pointermove', function (e) {
    if (!start) return;
    dx = e.clientX - start.x;
    dy = Math.min(e.clientY - start.y, 0);
    if (!start.drag) {
      if (Math.abs(dx) < 8 && -dy < 8) return;
      start.drag = true;
      el.setPointerCapture(e.pointerId);
      el.style.transition = 'none';
    }
    el.style.transform = 'translate(' + dx + 'px, ' + dy + 'px)';
  });
  el.addEventListener('pointerup', function () {
    if (!start) return;
    if (!start.drag) start = null;
    else if (Math.abs(dx) >= 40 || -dy >= 40) removeToast(entry);
    else reset();
  });
  el.addEventListener('pointercancel', function () { if (start) reset(); });
}

export function showToast(message, type, opts) {
  if (authRedirecting && type === 'error') return;
  if (type === 'error') reportUIError(message);
  const container = document.getElementById('toast-container');
  const cls =
    type === 'success' ? 'toast-success' :
    type === 'error' ? 'toast-error' :
    'toast-info';
  const icon =
    type === 'success' ? 'check' :
    type === 'error' ? 'warning' :
    'info';
  const limit = mobileToasts.matches ? 1 : TOAST_MAX;
  while (toasts.length >= limit) removeToast(toasts[0]);
  const el = document.createElement('div');
  el.className = 'toast ' + cls;
  const badge = document.createElement('span');
  badge.className = 'toast-icon';
  badge.setAttribute('data-icon', icon);
  el.appendChild(badge);
  hydrateIcons(el);
  const text = document.createElement('div');
  text.className = 'toast-text';
  if (opts && opts.title) {
    const title = document.createElement('div');
    title.className = 'toast-title';
    title.textContent = opts.title;
    text.appendChild(title);
  }
  const msg = document.createElement('div');
  msg.className = 'toast-msg';
  msg.textContent = message;
  text.appendChild(msg);
  el.appendChild(text);
  const entry = { el: el, timer: null };
  toasts.push(entry);
  container.appendChild(el);
  addToastControls(entry, message, type);
  el.addEventListener('pointerenter', function (e) {
    if (e.pointerType !== 'mouse') return;
    clearTimeout(entry.timer);
    el.style.opacity = '';
  });
  el.addEventListener('pointerleave', function (e) {
    if (e.pointerType !== 'mouse' || el.classList.contains('is-expanded')) return;
    scheduleHide(entry, 2000);
  });
  if (mobileToasts.matches) swipeToDismiss(entry);
  scheduleHide(entry, type === 'error' ? 7000 : 4000);
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
