import { icon } from './icons.js';

export const REFRESH_OPTIONS = [1, 2, 3, 4, 6, 8, 12, 24];

export const DISMISSED_ALERTS_KEY = 'trackerr-dismissed-alerts';
export const PAGE_SIZE_KEY = 'trackerr-page-size';
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100, 0];
export const PAGE_SIZE_ALL = 1000;

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
    return ' type="text" class="input input-secure" autocomplete="off" name="' + escapeHtml(name) + '"';
  }
  return ' type="password" class="input" autocomplete="off" name="' + escapeHtml(name) + '"';
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
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
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
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
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

export function formatTableTime(iso) {
  if (!iso) return t('common.emDash');
  const d = new Date(iso);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const time = d.toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' });
  if (day.getTime() === today.getTime()) return time;
  const dd = String(d.getDate()).padStart(2, '0');
  const mm = String(d.getMonth() + 1).padStart(2, '0');
  return dd + '.' + mm + ' ' + time;
}

export function formatLogTime(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleTimeString(localeTag(), {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

export function trackerDisplayDomain(tracker) {
  const active = tracker.active_domain || '';
  if (active) return active;
  return tracker.domain || '';
}

export const TRACKER_HUE_COUNT = 6;

// the tracker set is fixed, so the hues are assigned by hand: hashing put // rutracker and teamhd on the same one. The four common trackers take the hues
// that are furthest apart (blue, violet, amber, pink), leaving the two adjacent
// blue-greens to the rare ones. 0 teal, 1 cyan, 2 blue, 3 violet, 4 amber, 5 pink
export const TRACKER_HUES = {
  rutracker: 2,
  nnm: 3,
  kinozal: 4,
  rutor: 0,
  teamhd: 1,
};

export function trackerNameHueIndex(name) {
  const known = TRACKER_HUES[(name || '').toLowerCase()];
  if (known !== undefined) return known;
  let h = 0;
  const s = name || '';
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % TRACKER_HUE_COUNT;
}

export function loadStoredPageSize(fallback) {
  try {
    const raw = localStorage.getItem(PAGE_SIZE_KEY);
    if (raw === 'all') return 0;
    const n = parseInt(raw, 10);
    if (PAGE_SIZE_OPTIONS.indexOf(n) >= 0) return n;
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
  return fallback;
}

export function savePageSize(size) {
  try {
    localStorage.setItem(PAGE_SIZE_KEY, size === 0 ? 'all' : String(size));
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

export function pageSizeQueryLimit(size) {
  return size === 0 ? PAGE_SIZE_ALL : size;
}


export function trackerBadgeHtml(name) {
  const label = name || t('common.emDash');
  const idx = trackerNameHueIndex(label);
  return '<span class="tracker-badge tracker-badge-hue-' + idx + '">' + escapeHtml(label) + '</span>';
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
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const tomorrow = new Date(today);
  tomorrow.setDate(tomorrow.getDate() + 1);
  const slotDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  const time = formatRunSlotTime(iso);
  if (slotDay.getTime() === today.getTime()) return t('dash.today', { time: time });
  if (slotDay.getTime() === tomorrow.getTime()) return t('dash.tomorrow', { time: time });
  return d.toLocaleString(localeTag(), {
    day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit',
  });
}

export function formatThenSlot(iso) {
  const d = new Date(iso);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const slotDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  if (slotDay.getTime() === today.getTime()) return formatRunSlotTime(iso);
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

export function isSafeLinkUrl(url) {
  try {
    const scheme = new URL(url).protocol.toLowerCase();
    return scheme === 'http:' || scheme === 'https:' || scheme === 'magnet:';
  } catch {
    return false;
  }
}

export function linkCell(name, url) {
  const label = escapeHtml(name || url || t('common.emDash'));
  const titleAttr = name ? ' title="' + escapeHtml(name) + '"' : '';
  if (url && isSafeLinkUrl(url)) {
    return '<a class="release-link" href="' + escapeHtml(url) + '" target="_blank" rel="noopener"' + titleAttr + '>' + label + '</a>';
  }
  return '<span class="release-link"' + titleAttr + '>' + label + '</span>';
}

export function statusIcon(status, error, streak) {
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
  }
  const err = escapeHtml(error || '');
  const title = status === 'error' && error ? err : escapeHtml(label);
  const dataErr = status === 'error' && error
    ? ' data-error="' + err + '" class="' + cls + ' status-error-badge"'
    : ' class="' + cls + '"';
  const badge = streak > 1
    ? '<span class="streak-badge" title="' + escapeHtml(t('dash.streakTitle').replace('{n}', String(streak))) +
      '">' + escapeHtml(String(streak)) + '</span>'
    : '';
  return (
    '<span' + dataErr + ' title="' + title + '" aria-label="' + title + '">' +
    icon(iconName) + '</span>' + badge
  );
}


export function hourOptions(selected) {
  let html = '';
  for (let h = 0; h < 24; h++) {
    html += '<option value="' + h + '"' + (h === selected ? ' selected' : '') + '>' +
      String(h).padStart(2, '0') + ':00</option>';
  }
  return html;
}

export function refreshOptions(selected) {
  return REFRESH_OPTIONS.map(function (n) {
    return '<option value="' + n + '"' + (n === selected ? ' selected' : '') + '>' + n + '</option>';
  }).join('');
}

export function timezoneOptions(timezones, selected) {
  let html = '<option value="">' + escapeHtml(t('settings.timezoneDefault')) + '</option>';
  (timezones || []).forEach(function (tz) {
    const id = typeof tz === 'string' ? tz : tz.id;
    const label = typeof tz === 'string' ? tz : formatTimezoneLabel(tz);
    html += '<option value="' + escapeHtml(id) + '"' +
      (id === selected ? ' selected' : '') + '>' + escapeHtml(label) + '</option>';
  });
  return html;
}


