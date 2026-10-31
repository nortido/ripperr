import { icon } from './icons.js';

export const REFRESH_OPTIONS = [1, 2, 3, 4, 6, 8, 12, 24];

export const DISMISSED_ALERTS_KEY = 'ripperr-dismissed-alerts';
export const PAGE_SIZE_KEY = 'ripperr-page-size';
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100, 0];
export const PAGE_SIZE_ALL = 1000;
export const SECRET_MASK = '********';

export const SECURE_INPUT_SUPPORTED = (function () {
  const el = document.createElement('input');
  el.style.webkitTextSecurity = 'disc';
  return el.style.webkitTextSecurity === 'disc';
})();


export function randomFieldName(prefix) {
  return prefix + '-' + Math.random().toString(36).slice(2, 11);
}

export function secureInputAttrs(prefix) {
  const name = randomFieldName(prefix);
  if (SECURE_INPUT_SUPPORTED) {
    return html` type="text" class="input input-secure" autocomplete="off" name="${name}"`;
  }
  return html` type="password" class="input" autocomplete="off" name="${name}"`;
}

export function getDismissedAlertKeys() {
  try {
    const raw = localStorage.getItem(DISMISSED_ALERTS_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    return new Set(Array.isArray(arr) ? arr : []);
  } catch (_) {
    return new Set();
  }
}

export function dismissAlertKey(key) {
  const dismissed = getDismissedAlertKeys();
  dismissed.add(key);
  try {
    localStorage.setItem(DISMISSED_ALERTS_KEY, JSON.stringify(Array.from(dismissed)));
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function pruneDismissedAlerts(alerts) {
  const active = new Set((alerts || []).map(alertIncidentKey));
  const dismissed = getDismissedAlertKeys();
  let changed = false;
  dismissed.forEach(function (k) {
    if (!active.has(k)) {
      dismissed.delete(k);
      changed = true;
    }
  });
  if (!changed) return;
  try {
    localStorage.setItem(DISMISSED_ALERTS_KEY, JSON.stringify(Array.from(dismissed)));
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function alertIncidentKey(alert) {
  return (alert.tracker || '') + '|' + (alert.since || '');
}

export function visibleAlerts(alerts) {
  const dismissed = getDismissedAlertKeys();
  return (alerts || []).filter(function (a) {
    return !dismissed.has(alertIncidentKey(a));
  });
}

// округляем: из-за перехода на летнее время сутки бывают 23 или 25 часов
function dayDelta(d) {
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  return Math.round((day - today) / 86400000);
}

const TIME_WITH_SECONDS = { hour: '2-digit', minute: '2-digit', second: '2-digit' };

export function formatDate(iso) {
  return new Date(iso).toLocaleDateString(localeTag());
}

export function formatTableTime(iso) {
  if (!iso) return t('common.emDash');
  const d = new Date(iso);
  const time = formatRunSlotTime(iso);
  if (dayDelta(d) === 0) return time;
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  return dd + '.' + mm + ' ' + time;
}

export function formatLogTime(iso) {
  if (!iso) return '';
  // лимит в 200 строк захватывает несколько суток, без даты 00:15 после 23:28 не прочесть
  const d = new Date(iso);
  const tag = localeTag();
  return d.toLocaleDateString(tag, { day: '2-digit', month: '2-digit' }) + ' ' + d.toLocaleTimeString(tag, TIME_WITH_SECONDS);
}

export function trackerDisplayDomain(tracker) {
  const active = tracker.active_domain || '';
  if (active) return active;
  return tracker.domain || '';
}

// у каждого трекера свой номер оттенка из 20, разнесены по кругу; свободные 5, 7, 9, 15
// оставлены новым трекерам и неизвестным именам
const TRACKER_HUES = {
  rustorka: 0,
  aniliberty: 1,
  booktracker: 2,
  tapochek: 3,
  kinozal: 4,
  riperam: 6,
  newstudio: 8,
  rutor: 10,
  teamhd: 11,
  selezen: 12,
  toloka: 13,
  rutracker: 14,
  nnm: 16,
  animelayer: 17,
  anidub: 18,
};
const TRACKER_SPARE_HUES = [5, 7, 9, 15];

// соседние номера категорий получают далекие оттенки
const CATEGORY_HUE_ORDER = [0, 10, 5, 15, 3, 13, 8, 18, 1, 11, 6, 16, 4, 14, 9, 19, 2, 12, 7, 17];
const CATEGORY_HUE_COUNT = CATEGORY_HUE_ORDER.length;

function nameHash(name) {
  let h = 0;
  const s = name || '';
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h);
}

function badgeHue(index, offset) {
  return (8 + 18 * index + offset) % 360;
}

function vanDerCorput(n) {
  let v = 0;
  let denom = 1;
  while (n > 0) {
    denom *= 2;
    v += (n % 2) / denom;
    n = Math.floor(n / 2);
  }
  return v;
}

export function loadStoredPageSize(fallback) {
  try {
    const raw = localStorage.getItem(PAGE_SIZE_KEY);
    if (raw === 'all') return 0;
    const n = parseInt(raw, 10);
    if (PAGE_SIZE_OPTIONS.indexOf(n) >= 0) return n;
  } catch (_) { /* localStorage бывает недоступен */ }
  return fallback;
}

export function savePageSize(size) {
  try {
    localStorage.setItem(PAGE_SIZE_KEY, size === 0 ? 'all' : String(size));
  } catch (_) { /* localStorage бывает недоступен */ }
}

export function pageSizeQueryLimit(size) {
  return size === 0 ? PAGE_SIZE_ALL : size;
}


function badgeHtml(label, tier, hue) {
  // имя в title: в узкой колонке чип режется многоточием, иначе его не дочитать
  return html`<span class="tracker-badge tracker-badge-${tier}" style="--badge-h:${hue}" title="${label}">${label}</span>`;
}

export function trackerBadgeHtml(name) {
  const label = name || t('common.emDash');
  let idx = TRACKER_HUES[label.toLowerCase()];
  if (idx === undefined) idx = TRACKER_SPARE_HUES[nameHash(label) % TRACKER_SPARE_HUES.length];
  return badgeHtml(label, 'a', badgeHue(idx, 0));
}

export function categoryChipHtml(name, colorIndex) {
  const k = Number.isInteger(colorIndex) && colorIndex >= 0 ? colorIndex : nameHash(name) % CATEGORY_HUE_COUNT;
  const round = Math.floor(k / CATEGORY_HUE_COUNT);
  // после 20 категорий оттенки идут между уже занятыми
  const hue = badgeHue(CATEGORY_HUE_ORDER[k % CATEGORY_HUE_COUNT], 18 * vanDerCorput(round));
  return badgeHtml(name, round % 2 ? 'c' : 'b', hue);
}


export function formatDateTime(iso) {
  if (!iso) return t('common.emDash');
  return new Date(iso).toLocaleString(localeTag());
}

export function formatRunSlotTime(iso) {
  return new Date(iso).toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' });
}

export function formatRunSlot(iso) {
  const d = new Date(iso);
  const delta = dayDelta(d);
  const time = formatRunSlotTime(iso);
  if (delta === 0) return time;
  if (delta === 1) return t('dash.tomorrow', { time: time });
  return d.toLocaleString(localeTag(), {
    day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit',
  });
}

export function formatThenSlot(iso) {
  const delta = dayDelta(new Date(iso));
  if (delta === 0 || delta === 1) return formatRunSlotTime(iso);
  return formatRunSlot(iso);
}

export function escapeHtml(s) {
  if (s === null || s === undefined) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

class Html {
  constructor(value) {
    this.value = value;
  }
  toString() {
    return this.value;
  }
}

function htmlValue(v) {
  if (v instanceof Html) return v.value;
  if (Array.isArray(v)) return v.map(htmlValue).join('');
  if (v === null || v === undefined || v === false) return '';
  // сырой HTML проходит только как Html из html``, так что escapeHtml не забудешь
  return escapeHtml(String(v));
}

export function html(strings, ...values) {
  let out = strings[0];
  for (let i = 0; i < values.length; i++) {
    out += htmlValue(values[i]) + strings[i + 1];
  }
  return new Html(out);
}

export function rawHtml(value) {
  // только для разметки прямо из исходника (таблица иконок), не для данных
  return new Html(value == null ? '' : String(value));
}

export function isSafeLinkUrl(url) {
  try {
    const scheme = new URL(url).protocol.toLowerCase();
    return scheme === 'http:' || scheme === 'https:' || scheme === 'magnet:';
  } catch {
    return false;
  }
}

function breakableName(s) {
  // имена без пробелов: <wbr> после точек и дефисов переносит по частям, а не посреди слова
  const parts = String(s).match(/[^._\-\/]*[._\-\/]?/g).filter(Boolean);
  return parts.map(function (p, i) { return i ? html`<wbr>${p}` : html`${p}`; });
}

export function linkCell(name, url) {
  const label = name ? breakableName(name) : (url || t('common.emDash'));
  const titleAttr = name ? html` title="${name}"` : '';
  if (url && isSafeLinkUrl(url)) {
    return html`<a class="release-link" href="${url}" target="_blank" rel="noopener"${titleAttr}>${label}</a>`;
  }
  return html`<span class="release-link"${titleAttr}>${label}</span>`;
}

export function statusMeta(status) {
  let cls = 'status-icon';
  let iconName = 'help';
  let label = status || t('common.emDash');
  if (status === 'updated') {
    cls += ' status-icon-success';
    iconName = 'check';
    label = t('status.updated');
  } else if (status === 'unchanged') {
    cls += ' status-icon-muted';
    iconName = 'minus';
    label = t('status.unchanged');
  } else if (status === 'error') {
    cls += ' status-icon-danger';
    iconName = 'xCircle';
    label = t('status.error');
  } else if (status === 'unknown') {
    cls += ' status-icon-warning';
    iconName = 'help';
    label = t('status.unknown');
  } else if (status === 'new') {
    cls += ' status-icon-info';
    iconName = 'plus';
    label = t('status.new');
  }
  return { cls: cls, iconName: iconName, label: label };
}

export function statusIcon(status, streak) {
  const m = statusMeta(status);
  const badge = streak > 1
    ? html`<span class="streak-badge" title="${t('dash.streakTitle', { n: streak })}">${streak}</span>`
    : '';
  return html`<span class="${m.cls} status-history-badge" role="button" tabindex="0" aria-haspopup="dialog" title="${m.label}" aria-label="${m.label}">${icon(m.iconName)}</span>${badge}`;
}


export function hourOptions(selected) {
  const out = [];
  for (let h = 0; h < 24; h++) {
    out.push(html`<option value="${h}"${h === selected ? ' selected' : ''}>${String(h).padStart(2, '0')}:00</option>`);
  }
  return html`${out}`;
}

export function refreshOptions(selected) {
  return html`${REFRESH_OPTIONS.map(function (n) {
    return html`<option value="${n}"${n === selected ? ' selected' : ''}>${n}</option>`;
  })}`;
}

export function timezoneOptions(timezones, selected) {
  const list = timezones || [];
  const out = list.map(function (tz) {
    const id = typeof tz === 'string' ? tz : tz.id;
    const label = typeof tz === 'string' ? tz : formatTimezoneLabel(tz);
    return html`<option value="${id}"${id === selected ? ' selected' : ''}>${label}</option>`;
  });
  // зоны из БД может не быть в списке (задана руками или ее город выпал при схлопывании
  // по смещению). Без своей строки браузер выберет первый пункт и сохранение формы
  // молча сбросит зону на серверную
  const known = list.some(function (tz) {
    return (typeof tz === 'string' ? tz : tz.id) === selected;
  });
  const extra = selected && !known
    ? html`<option value="${selected}" selected>${selected}</option>`
    : '';
  return html`<option value="">${t('settings.timezoneDefault')}</option>${extra}${out}`;
}

export function updateRefreshedAt(id, key) {
  const el = document.getElementById(id);
  if (!el) return;
  const time = new Date().toLocaleTimeString(localeTag(), TIME_WITH_SECONDS);
  el.textContent = t(key, { time: time });
}
