'use strict';

const REFRESH_OPTIONS = [1, 2, 3, 4, 6, 8, 12, 24];
const THEME_KEY = 'trackerr-theme';
const SIDEBAR_COLLAPSED_KEY = 'trackerr-sidebar-collapsed';
const PAGE_SCALE_BASE = 480;
const MOBILE_LAYOUT_MAX = 900;
let layoutWasMobile = false;
const DISMISSED_ALERTS_KEY = 'trackerr-dismissed-alerts';
const PAGE_SIZE_KEY = 'trackerr-page-size';
const PAGE_SIZE_OPTIONS = [10, 20, 50, 100, 0];
const PAGE_SIZE_ALL = 1000;
const STOP_PENDING_MS = 90000;

const VIEW_HASH = {
  dashboard: '#dashboard',
  schedule: '#settings',
  trackers: '#trackers',
  integrations: '#integrations',
  logs: '#logs',
};

const HASH_VIEW = {
  dashboard: 'dashboard',
  settings: 'schedule',
  trackers: 'trackers',
  integrations: 'integrations',
  logs: 'logs',
};

const ICONS = {
  sun: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>',
  moon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>',
  menu: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/></svg>',
  clock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>',
  list: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/></svg>',
  warning: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>',
  trash: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
  check: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>',
  ban: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07"/></svg>',
  dashboard: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/></svg>',
  settings: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="3"/><path d="M12 1v2M12 21v2M4.22 4.22l1.42 1.42M18.36 18.36l1.42 1.42M1 12h2M21 12h2M4.22 19.78l1.42-1.42M18.36 5.64l1.42-1.42"/></svg>',
  server: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>',
  plug: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M12 22v-5M9 8V2M15 8V2M6 12H2M22 12h-4"/><rect x="4" y="8" width="16" height="9" rx="2"/></svg>',
  chevron: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="6 9 12 15 18 9"/></svg>',
  chevronLeft: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="15 18 9 12 15 6"/></svg>',
  chevronRight: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="9 18 15 12 9 6"/></svg>',
  refresh: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="23 4 23 10 17 10"/><polyline points="1 20 1 14 7 14"/><path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15"/></svg>',
  eye: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>',
  eyeClosed: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><path d="M5 14.5c2.2-1.5 11.8-1.5 14 0"/></svg>',
  eyeOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"/><line x1="1" y1="1" x2="23" y2="23"/></svg>',
  help: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>',
  zap: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
  zapOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/><line x1="1" y1="1" x2="23" y2="23"/></svg>',
  zapPlus: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/><line x1="19" y1="5" x2="19" y2="11"/><line x1="16" y1="8" x2="22" y2="8"/></svg>',
  zapForceAll: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><g transform="translate(-2.5,3.12) scale(0.84)"><path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"/></g><path d="M19.51 0.72L20.63 3.73 23.38 4.84 20.63 6.07 19.51 8.89 18.39 6.07 15.64 4.84 18.39 3.73Z" fill="currentColor" stroke="none"/></svg>',
  banOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="4.93" y1="4.93" x2="19.07" y2="19.07" stroke-width="2.5"/></svg>',
  wrench: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>',
  logs: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>',
  x: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>',
  minus: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="5" y1="12" x2="19" y2="12"/></svg>',
  xCircle: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>',
  funnel: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/></svg>',
  search: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>',
  activity: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>',
  stop: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><rect x="9" y="9" width="6" height="6" rx="1" fill="currentColor" stroke="none"/></svg>',
};

const SECURE_INPUT_SUPPORTED = (function () {
  const el = document.createElement('input');
  el.style.webkitTextSecurity = 'disc';
  return el.style.webkitTextSecurity === 'disc';
})();

let integrationsCache = null;
let qbitCategoriesCache = null;
let qbitCategoriesUnavailable = false;
let timezonesCache = null;
const historyState = { offset: 0, limit: 10, status: '', ignored: false, sort: 'checked', name: '' };
let stopPending = false;
let stopPendingTimer = null;
let activeErrorPopover = null;
let activeSlotsPopover = null;
let slotsPopoverLeaveTimer = null;
let currentView = null;
let openCustomSelect = null;
let openHistFilterSelect = null;
let openNameFilterWrap = null;
let nameFilterDebounceTimer = null;
let dashboardPollTimer = null;
let dashboardDataRefreshTimer = null;
let dashboardVisibilityBound = false;
let dashboardStatusSlots = [];
let dashboardRunning = false;
let dashboardFetchGen = 0;
let planForceLocal = Object.create(null);
let ignoreHashesCache = null;
let openTrackerKey = null;
let dashboardAlerts = [];
let logsState = { limit: 200 };
let logsPollTimer = null;
let logsVisibilityBound = false;
let logsFetchGen = 0;

function randomFieldName(prefix) {
  return prefix + '-' + Math.random().toString(36).slice(2, 11);
}

function secureInputAttrs(prefix) {
  const name = randomFieldName(prefix);
  if (SECURE_INPUT_SUPPORTED) {
    return ' type="text" class="input input-secure" autocomplete="off" name="' + escapeHtml(name) + '"';
  }
  return ' type="password" class="input" autocomplete="off" name="' + escapeHtml(name) + '"';
}

function getDismissedAlertKeys() {
  try {
    const raw = localStorage.getItem(DISMISSED_ALERTS_KEY);
    if (!raw) return new Set();
    const arr = JSON.parse(raw);
    return new Set(Array.isArray(arr) ? arr : []);
  } catch (_) {
    return new Set();
  }
}

function dismissAlertKey(key) {
  const dismissed = getDismissedAlertKeys();
  dismissed.add(key);
  try {
    localStorage.setItem(DISMISSED_ALERTS_KEY, JSON.stringify(Array.from(dismissed)));
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

function pruneDismissedAlerts(alerts) {
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

function alertIncidentKey(alert) {
  return (alert.tracker || '') + '|' + (alert.since || '');
}

function visibleAlerts(alerts) {
  const dismissed = getDismissedAlertKeys();
  return (alerts || []).filter(function (a) {
    return !dismissed.has(alertIncidentKey(a));
  });
}

function formatTableTime(iso) {
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

function formatLogTime(iso) {
  if (!iso) return '';
  return new Date(iso).toLocaleTimeString(localeTag(), {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
}

function trackerDisplayDomain(tracker) {
  const active = tracker.active_domain || '';
  if (active) return active;
  return tracker.domain || '';
}

const TRACKER_HUE_COUNT = 6;

function trackerNameHueIndex(name) {
  let h = 0;
  const s = name || '';
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) - h + s.charCodeAt(i)) | 0;
  }
  return Math.abs(h) % TRACKER_HUE_COUNT;
}

function loadStoredPageSize(fallback) {
  try {
    const raw = localStorage.getItem(PAGE_SIZE_KEY);
    if (raw === 'all') return 0;
    const n = parseInt(raw, 10);
    if (PAGE_SIZE_OPTIONS.indexOf(n) >= 0) return n;
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
  return fallback;
}

function savePageSize(size) {
  try {
    localStorage.setItem(PAGE_SIZE_KEY, size === 0 ? 'all' : String(size));
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

function pageSizeQueryLimit(size) {
  return size === 0 ? PAGE_SIZE_ALL : size;
}

function setStopPending(active) {
  stopPending = !!active;
  if (stopPendingTimer) {
    clearTimeout(stopPendingTimer);
    stopPendingTimer = null;
  }
  if (!stopPending) {
    if (currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
    return;
  }
  stopPendingTimer = setTimeout(function () {
    stopPending = false;
    stopPendingTimer = null;
    if (currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
  }, STOP_PENDING_MS);
  if (currentView === 'dashboard') updateRunStatusUI(effectiveRunning(dashboardRunning));
}

function effectiveRunning(serverRunning) {
  if (stopPending && serverRunning) return false;
  return !!serverRunning;
}

function trackerBadgeHtml(name) {
  const label = name || t('common.emDash');
  const idx = trackerNameHueIndex(label);
  return '<span class="tracker-badge tracker-badge-hue-' + idx + '">' + escapeHtml(label) + '</span>';
}

function icon(name) {
  return ICONS[name] || '';
}

function hydrateIcons(root) {
  (root || document).querySelectorAll('[data-icon]').forEach(function (el) {
    const n = el.getAttribute('data-icon');
    if (n && ICONS[n]) el.innerHTML = ICONS[n];
  });
}

function closeOpenCustomSelect() {
  if (!openCustomSelect) return;
  const trigger = openCustomSelect.querySelector('.custom-select-trigger');
  const panel = openCustomSelect.querySelector('.custom-select-panel');
  openCustomSelect.classList.remove('open', 'open-up');
  if (panel) panel.hidden = true;
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  openCustomSelect = null;
}

function initCustomSelect(selectEl) {
  if (!selectEl || selectEl.dataset.customSelectInit) return;
  selectEl.dataset.customSelectInit = '1';

  const wrap = document.createElement('div');
  wrap.className = 'custom-select';
  selectEl.parentNode.insertBefore(wrap, selectEl);
  wrap.appendChild(selectEl);
  selectEl.classList.add('custom-select-native');

  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.className = 'custom-select-trigger';
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  if (selectEl.id) trigger.id = selectEl.id + '-trigger';

  const labelEl = selectEl.id
    ? document.querySelector('label[for="' + CSS.escape(selectEl.id) + '"]')
    : null;
  if (labelEl && trigger.id) labelEl.setAttribute('for', trigger.id);

  const triggerLabel = document.createElement('span');
  triggerLabel.className = 'custom-select-value';
  const chevron = document.createElement('span');
  chevron.className = 'custom-select-chevron';
  chevron.innerHTML = ICONS.chevron;
  trigger.appendChild(triggerLabel);
  trigger.appendChild(chevron);

  const panel = document.createElement('div');
  panel.className = 'custom-select-panel';
  panel.setAttribute('role', 'listbox');
  panel.hidden = true;

  wrap.appendChild(trigger);
  wrap.appendChild(panel);

  let highlightIdx = selectEl.selectedIndex;

  function renderOptions() {
    panel.innerHTML = '';
    const curVal = selectEl.value;
    Array.from(selectEl.options).forEach(function (opt, idx) {
      const item = document.createElement('div');
      item.className = 'custom-select-option' + (opt.value === curVal ? ' is-selected' : '');
      item.setAttribute('role', 'option');
      item.setAttribute('aria-selected', opt.value === curVal ? 'true' : 'false');
      item.dataset.index = String(idx);
      if (opt.disabled) item.setAttribute('aria-disabled', 'true');
      if (opt.value === curVal) {
        item.innerHTML =
          '<span class="custom-select-check">' + ICONS.check + '</span>' +
          '<span>' + escapeHtml(opt.textContent) + '</span>';
      } else {
        item.textContent = opt.textContent;
      }
      if (!opt.disabled) {
        item.addEventListener('click', function (e) {
          e.stopPropagation();
          choose(idx);
        });
      }
      panel.appendChild(item);
    });
    hydrateIcons(panel);
  }

  function syncTrigger() {
    const opt = selectEl.options[selectEl.selectedIndex];
    triggerLabel.textContent = opt ? opt.textContent : '';
    trigger.disabled = selectEl.disabled;
    renderOptions();
  }

  function choose(idx) {
    if (idx < 0 || idx >= selectEl.options.length || selectEl.options[idx].disabled) return;
    selectEl.selectedIndex = idx;
    selectEl.dispatchEvent(new Event('change', { bubbles: true }));
    syncTrigger();
    closePanel();
  }

  function closePanel() {
    wrap.classList.remove('open', 'open-up');
    trigger.setAttribute('aria-expanded', 'false');
    panel.hidden = true;
    if (openCustomSelect === wrap) openCustomSelect = null;
  }

  function positionPanel() {
    wrap.classList.remove('open-up');
    const rect = trigger.getBoundingClientRect();
    const panelH = Math.min(panel.scrollHeight || 320, 320);
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;
    if (spaceBelow < panelH + 8 && spaceAbove > spaceBelow) wrap.classList.add('open-up');
  }

  function scrollToIndex(idx) {
    const el = panel.querySelector('[data-index="' + idx + '"]');
    if (el) el.scrollIntoView({ block: 'nearest' });
  }

  function highlightOption() {
    panel.querySelectorAll('.custom-select-option').forEach(function (el) {
      el.classList.toggle('is-highlighted', parseInt(el.dataset.index, 10) === highlightIdx);
    });
  }

  function openPanel() {
    closeOpenCustomSelect();
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openCustomSelect = wrap;
    highlightIdx = selectEl.selectedIndex;
    renderOptions();
    positionPanel();
    scrollToIndex(highlightIdx);
    highlightOption();
  }

  trigger.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('open')) closePanel();
    else openPanel();
  });

  trigger.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) openPanel();
      else choose(highlightIdx);
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) {
        openPanel();
        return;
      }
      const step = e.key === 'ArrowDown' ? 1 : -1;
      let next = highlightIdx + step;
      while (next >= 0 && next < selectEl.options.length && selectEl.options[next].disabled) {
        next += step;
      }
      if (next >= 0 && next < selectEl.options.length) {
        highlightIdx = next;
        highlightOption();
        scrollToIndex(highlightIdx);
      }
    }
  });

  selectEl.addEventListener('change', syncTrigger);
  const valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
  if (valueDesc && valueDesc.set) {
    Object.defineProperty(selectEl, 'value', {
      configurable: true,
      get: function () { return valueDesc.get.call(this); },
      set: function (v) {
        valueDesc.set.call(this, v);
        syncTrigger();
      },
    });
  }
  syncTrigger();
}

function initCustomSelects(root) {
  (root || document).querySelectorAll('select.select:not([data-custom-select-init])').forEach(initCustomSelect);
}

function parseDownloadProxy(url) {
  if (!url) return { type: 'http', address: '' };
  const m = String(url).match(/^(https?|socks5):\/\//i);
  if (m) {
    const scheme = m[1].toLowerCase();
    const type = scheme === 'socks5' ? 'socks5' : (scheme === 'https' ? 'https' : 'http');
    return { type: type, address: url.replace(/^[^:]+:\/\//i, '') };
  }
  return { type: 'http', address: String(url) };
}

function buildDownloadProxy(type, address) {
  const addr = String(address || '').trim();
  if (!addr) return '';
  const scheme = type === 'socks5' ? 'socks5' : (type === 'https' ? 'https' : 'http');
  if (/^(https?|socks5):\/\//i.test(addr)) {
    return addr.replace(/^[^:]+:\/\//i, scheme + '://');
  }
  return scheme + '://' + addr;
}

function getProxyDownloadValue() {
  const typeEl = document.getElementById('proxy-type');
  const addrEl = document.getElementById('proxy-download');
  if (!typeEl || !addrEl) return '';
  return buildDownloadProxy(typeEl.value, addrEl.value);
}

function dashboardHasActiveInteraction() {
  if (activeErrorPopover || activeSlotsPopover || openCustomSelect || openHistFilterSelect || openNameFilterWrap) return true;
  const dash = document.getElementById('view-dashboard');
  if (!dash || dash.classList.contains('hidden')) return false;
  const active = document.activeElement;
  if (!active || !dash.contains(active)) return false;
  if (active.tagName === 'TEXTAREA') return true;
  if (active.tagName === 'INPUT') {
    const type = (active.type || '').toLowerCase();
    return type !== 'button' && type !== 'submit' && type !== 'checkbox' && type !== 'radio';
  }
  if (active.tagName === 'SELECT' && !active.classList.contains('custom-select-native')) return true;
  return false;
}

function stopDashboardPolling() {
  if (dashboardPollTimer) {
    clearInterval(dashboardPollTimer);
    dashboardPollTimer = null;
  }
  if (dashboardDataRefreshTimer) {
    clearInterval(dashboardDataRefreshTimer);
    dashboardDataRefreshTimer = null;
  }
  if (dashboardVisibilityBound) {
    document.removeEventListener('visibilitychange', onDashboardVisibility);
    dashboardVisibilityBound = false;
  }
}

function onDashboardVisibility() {
  if (currentView !== 'dashboard') return;
  if (document.visibilityState === 'visible') {
    pollDashboardStatus();
    ensureDashboardPolling();
  } else {
    if (dashboardPollTimer) {
      clearInterval(dashboardPollTimer);
      dashboardPollTimer = null;
    }
    if (dashboardDataRefreshTimer) {
      clearInterval(dashboardDataRefreshTimer);
      dashboardDataRefreshTimer = null;
    }
  }
}

function ensureDashboardPolling() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  if (!dashboardPollTimer) dashboardPollTimer = setInterval(pollDashboardStatus, 5000);
  if (!dashboardDataRefreshTimer) {
    dashboardDataRefreshTimer = setInterval(function () { refreshDashboard(false); }, 30000);
  }
  if (!dashboardVisibilityBound) {
    document.addEventListener('visibilitychange', onDashboardVisibility);
    dashboardVisibilityBound = true;
  }
}

function updateDashboardRefreshedAt() {
  const el = document.getElementById('dash-last-updated');
  if (!el) return;
  const time = new Date().toLocaleTimeString(localeTag(), {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
  el.textContent = t('dash.lastUpdated', { time: time });
}

function isUrlIgnored(urlHash) {
  return !!(urlHash && ignoreHashesCache && ignoreHashesCache.has(urlHash));
}

async function syncIgnoreState() {
  const items = await fetchJSON('/api/ignore');
  ignoreHashesCache = new Set((items || []).map(function (i) { return i.url_hash; }));
  return items || [];
}

function updateIgnoredStatCard(count) {
  const cards = document.querySelectorAll('#view-dashboard .stat-grid .stat-card');
  if (cards.length < 4) return;
  const valueEl = cards[3].querySelector('.stat-value');
  if (valueEl) valueEl.textContent = String(count);
}

function ignoreButtonIconsHtml(ignored) {
  const idleIcon = ignored ? 'eyeOff' : 'eye';
  const hoverIcon = ignored ? 'eye' : 'eyeOff';
  return (
    '<span class="icon-idle" data-icon="' + idleIcon + '"></span>' +
    '<span class="icon-hover" data-icon="' + hoverIcon + '"></span>'
  );
}

function updateHistoryIgnoreButtons(root) {
  (root || document).querySelectorAll('.btn-ignore-item').forEach(function (btn) {
    const hash = btn.getAttribute('data-hash');
    const ignored = isUrlIgnored(hash);
    btn.disabled = false;
    btn.classList.toggle('btn-ignore', !ignored);
    btn.classList.toggle('btn-unignore', ignored);
    btn.setAttribute('title', ignored ? t('dash.fromIgnore') : t('dash.toIgnore'));
    btn.setAttribute('aria-label', ignored ? t('dash.fromIgnore') : t('dash.toIgnore'));
    btn.innerHTML = ignoreButtonIconsHtml(ignored);
  });
  hydrateIcons(root || document);
}

async function refreshIgnoreState() {
  try {
    const items = await syncIgnoreState();
    const dash = document.getElementById('view-dashboard');
    if (dash && dash.querySelector('.page-header')) {
      updateHistoryIgnoreButtons(dash);
      updateIgnoredStatCard(items.length);
    }
  } catch (_) { /* ponytail: best-effort */ }
}

function planItemForced(item) {
  const hash = item.url_hash || '';
  if (hash && Object.prototype.hasOwnProperty.call(planForceLocal, hash)) {
    return !!planForceLocal[hash];
  }
  return !!item.force_check;
}

function syncPlanForceLocal(items) {
  planForceLocal = Object.create(null);
  (items || []).forEach(function (item) {
    const hash = item.url_hash || '';
    if (hash) planForceLocal[hash] = !!item.force_check;
  });
}

function planForceButtonHtml(hash, forced) {
  const btnTitle = escapeHtml(forced ? t('dash.forceCancel') : t('dash.forceCheck'));
  const btnCls = 'btn btn-icon-only btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  const btnIcon = forced ? 'zapOff' : 'zap';
  return (
    '<button type="button" class="' + btnCls + '" ' +
    'title="' + btnTitle + '" aria-label="' + btnTitle + '" ' +
    'aria-pressed="' + (forced ? 'true' : 'false') + '" ' +
    'data-hash="' + escapeHtml(hash) + '" data-force="' + (forced ? '1' : '0') + '">' +
    '<span data-icon="' + btnIcon + '"></span></button>'
  );
}

function updatePlanForceButton(btn, hash, forced) {
  btn.disabled = false;
  btn.className = 'btn btn-icon-only btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  const btnTitle = forced ? t('dash.forceCancel') : t('dash.forceCheck');
  btn.setAttribute('title', btnTitle);
  btn.setAttribute('aria-label', btnTitle);
  btn.setAttribute('aria-pressed', forced ? 'true' : 'false');
  btn.setAttribute('data-hash', hash);
  btn.setAttribute('data-force', forced ? '1' : '0');
  btn.innerHTML = '<span data-icon="' + (forced ? 'zapOff' : 'zap') + '"></span>';
  hydrateIcons(btn);
}

function updateReleaseRow(hash, force) {
  if (hash) planForceLocal[hash] = !!force;
  const row = document.querySelector('#view-dashboard tr[data-release-hash="' + CSS.escape(hash) + '"]');
  if (!row) return;
  const forced = !!force;
  row.classList.toggle('plan-row-force', forced);
  const recheckCell = row.querySelector('.plan-recheck-cell');
  if (recheckCell) {
    const dateShort = recheckCell.getAttribute('data-recheck-short') || '';
    const dateFull = recheckCell.getAttribute('data-recheck-full') || '';
    recheckCell.innerHTML = '<span class="plan-recheck-text" title="' + escapeHtml(dateFull) + '">' +
      escapeHtml(dateShort) + '</span>';
  }
  const btn = row.querySelector('.btn-plan-force');
  if (btn) updatePlanForceButton(btn, hash, forced);
}

function updateRunStatusUI(running) {
  dashboardRunning = !!running;
  const statusEl = document.getElementById('dash-run-status');
  const runBtn = document.getElementById('dash-run-btn');
  const stopBtn = document.getElementById('dash-stop-btn');
  if (statusEl) {
    statusEl.innerHTML =
      '<span class="run-status-dot' + (running ? ' run-status-dot-active' : '') + '"></span>' +
      '<span class="run-status-label">' + escapeHtml(running ? t('dash.statusRunning') : t('dash.statusIdle')) + '</span>';
  }
  if (runBtn) {
    if (stopPending) {
      runBtn.disabled = true;
      runBtn.title = t('dash.stopPending');
      runBtn.setAttribute('aria-label', t('dash.stopPending'));
    } else {
      runBtn.disabled = false;
      runBtn.removeAttribute('title');
      if (running) {
        runBtn.className = 'btn btn-restart';
        runBtn.textContent = t('dash.restartCheck');
        runBtn.setAttribute('aria-label', t('dash.restartCheck'));
      } else {
        runBtn.className = 'btn btn-primary';
        runBtn.textContent = t('dash.runCheck');
        runBtn.setAttribute('aria-label', t('dash.runCheck'));
      }
    }
  }
  if (stopBtn) {
    stopBtn.style.display = running ? '' : 'none';
    stopBtn.disabled = false;
    stopBtn.setAttribute('aria-label', t('dash.stopCheck'));
    stopBtn.title = t('dash.stopCheck');
  }
}

function updateNextRunCard(slots) {
  dashboardStatusSlots = slots || [];
  const card = document.querySelector('#view-dashboard .stat-card-next-run');
  if (!card) return;
  const runs = dashboardStatusSlots;
  const valueEl = card.querySelector('.stat-value');
  const subEl = card.querySelector('.stat-sub');
  if (valueEl) valueEl.textContent = runs[0] ? formatRunSlot(runs[0]) : t('common.emDash');
  if (runs.length >= 2) {
    if (!subEl) {
      const body = card.querySelector('.stat-card-body');
      if (body && valueEl) {
        const sub = document.createElement('div');
        sub.className = 'stat-sub';
        valueEl.insertAdjacentElement('afterend', sub);
      }
    }
    const sub = card.querySelector('.stat-sub');
    if (sub) sub.innerHTML = '<span class="stat-then-pill">' + escapeHtml(t('dash.then', { time: formatThenSlot(runs[1]) })) + '</span>';
  } else if (subEl) {
    subEl.remove();
  }
  const hasPopover = runs.length > 2;
  if (hasPopover) card.setAttribute('data-has-slots-popover', '1');
  else card.removeAttribute('data-has-slots-popover');
}

async function pollDashboardStatus() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  try {
    const status = await fetchJSON('/api/status');
    if (!status.running) setStopPending(false);
    updateRunStatusUI(effectiveRunning(status.running));
    updateNextRunCard(status.next_runs || []);
    dashboardAlerts = status.alerts || [];
    updateDashboardAlerts(dashboardAlerts);
  } catch (_) { /* ponytail: polling is best-effort */ }
}

function renderAlertsHtml(alerts) {
  const visible = visibleAlerts(alerts);
  if (!visible.length) return '';
  let html = '<div class="dash-alerts">';
  visible.forEach(function (alert) {
    const key = alertIncidentKey(alert);
    const sinceTime = formatTableTime(alert.since);
    const name = alert.tracker || t('common.emDash');
    html +=
      '<div class="dash-alert" data-alert-key="' + escapeHtml(key) + '">' +
      '<span class="dash-alert-icon" data-icon="warning"></span>' +
      '<span class="dash-alert-text">' +
      escapeHtml(t('dash.alert.trackerDown', {
        name: name,
        time: sinceTime,
        error: alert.error || '',
      })) +
      '</span>' +
      '<button type="button" class="btn btn-ghost btn-icon btn-sm dash-alert-dismiss"' +
      ' data-alert-key="' + escapeHtml(key) + '"' +
      ' title="' + escapeHtml(t('dash.alert.dismiss')) + '"' +
      ' aria-label="' + escapeHtml(t('dash.alert.dismiss')) + '">' +
      '<span data-icon="x"></span></button></div>';
  });
  html += '</div>';
  return html;
}

function updateDashboardAlerts(alerts) {
  pruneDismissedAlerts(alerts);
  dashboardAlerts = alerts || [];
  const el = document.getElementById('view-dashboard');
  if (!el) return;
  const existing = el.querySelector('.dash-alerts');
  const html = renderAlertsHtml(dashboardAlerts);
  if (!html) {
    if (existing) existing.remove();
    return;
  }
  if (existing) {
    existing.outerHTML = html;
  } else {
    const statGrid = el.querySelector('.stat-grid');
    if (statGrid) statGrid.insertAdjacentHTML('beforebegin', html);
  }
  bindDashboardAlerts(el);
  hydrateIcons(el.querySelector('.dash-alerts'));
}

function bindDashboardAlerts(root) {
  (root || document).querySelectorAll('.dash-alert-dismiss').forEach(function (btn) {
    if (btn.dataset.alertBound) return;
    btn.dataset.alertBound = '1';
    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      const key = btn.getAttribute('data-alert-key');
      if (key) dismissAlertKey(key);
      const row = btn.closest('.dash-alert');
      if (row) row.remove();
      const wrap = (root || document).querySelector('.dash-alerts');
      if (wrap && !wrap.querySelector('.dash-alert')) wrap.remove();
    });
  });
}

async function triggerRunCheck() {
  const runBtn = document.getElementById('dash-run-btn');
  const isRestart = dashboardRunning;
  const prevRunning = dashboardRunning;
  setStopPending(false);
  updateRunStatusUI(true);
  pollDashboardStatus();
  if (runBtn) runBtn.disabled = true;
  try {
    const opts = { method: 'POST' };
    if (isRestart) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify({ restart: true });
    }
    const res = await fetch('/api/run', opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* ignore */ }
    }
    if (res.status === 202) {
      showToast(isRestart ? t('dash.checkRestarted') : t('dash.checkStarted'), 'success');
      updateRunStatusUI(true);
    } else if (res.status === 409) {
      showToast((data && data.error) || t('dash.checkAlreadyRunning'), 'info');
      updateRunStatusUI(true);
    } else {
      updateRunStatusUI(prevRunning);
      const msg = data && data.error ? data.error : 'HTTP ' + res.status;
      showToast(msg, 'error');
    }
    pollDashboardStatus();
  } catch (err) {
    updateRunStatusUI(prevRunning);
    showToast(err.message, 'error');
    pollDashboardStatus();
  } finally {
    if (runBtn && currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
  }
}

async function triggerStopCheck() {
  const stopBtn = document.getElementById('dash-stop-btn');
  const prevRunning = dashboardRunning;
  updateRunStatusUI(false);
  if (stopBtn) stopBtn.disabled = true;
  try {
    const res = await fetch('/api/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ stop: true }),
    });
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* ignore */ }
    }
    if (res.status === 202) {
      showToast(t('dash.checkStopped'), 'success');
      setStopPending(true);
      updateRunStatusUI(false);
    } else if (res.status === 409) {
      showToast(t('dash.checkNotRunning'), 'info');
      updateRunStatusUI(true);
    } else {
      updateRunStatusUI(prevRunning);
      const msg = data && data.error ? data.error : 'HTTP ' + res.status;
      showToast(msg, 'error');
    }
    pollDashboardStatus();
  } catch (err) {
    updateRunStatusUI(prevRunning);
    showToast(err.message, 'error');
    pollDashboardStatus();
  } finally {
    if (stopBtn) stopBtn.disabled = false;
  }
}

function applyTheme(theme) {
  const t = theme === 'light' ? 'light' : 'dark';
  document.documentElement.setAttribute('data-theme', t);
  document.querySelectorAll('.theme-toggle').forEach(function (btn) {
    btn.setAttribute('aria-pressed', t === 'dark' ? 'true' : 'false');
  });
  try {
    localStorage.setItem(THEME_KEY, t);
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

function initTheme() {
  const theme = document.documentElement.getAttribute('data-theme');
  document.querySelectorAll('.theme-toggle').forEach(function (btn) {
    btn.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
  });
}

initTheme();
applyStaticI18n();
hydrateIcons(document);

function loadSidebarCollapsedPref() {
  try {
    const v = localStorage.getItem(SIDEBAR_COLLAPSED_KEY);
    if (v === null) return null;
    return v === '1';
  } catch (_) {
    return null;
  }
}

function saveSidebarCollapsedPref(collapsed) {
  try {
    localStorage.setItem(SIDEBAR_COLLAPSED_KEY, collapsed ? '1' : '0');
  } catch (_) { /* ponytail: localStorage may be unavailable */ }
}

const SIDEBAR_WIDE_MIN = 1200;
let sidebarOverlayOpen = false;

function isSidebarOverlayWidth() {
  const w = window.innerWidth;
  return w >= 901 && w < SIDEBAR_WIDE_MIN;
}

function effectiveSidebarCollapsed() {
  if (window.innerWidth <= 900) return false;
  if (window.innerWidth < SIDEBAR_WIDE_MIN) return true;
  const pref = loadSidebarCollapsedPref();
  return pref !== null ? pref : false;
}

function closeSidebarOverlay() {
  sidebarOverlayOpen = false;
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar) sidebar.classList.remove('sidebar-overlay-open');
  if (backdrop && isSidebarOverlayWidth()) {
    backdrop.classList.remove('visible');
    backdrop.hidden = true;
  }
  refreshSidebarChrome();
}

function openSidebarOverlay() {
  if (!isSidebarOverlayWidth()) return;
  sidebarOverlayOpen = true;
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  if (sidebar) sidebar.classList.add('sidebar-overlay-open');
  if (backdrop) {
    backdrop.hidden = false;
    requestAnimationFrame(function () { backdrop.classList.add('visible'); });
  }
  refreshSidebarChrome();
}

function refreshSidebarChrome() {
  const app = document.querySelector('.app');
  const collapsed = app && app.classList.contains('sidebar-collapsed');
  const btn = document.getElementById('sidebar-collapse-btn');
  const brand = document.getElementById('sidebar-brand');
  const overlay = isSidebarOverlayWidth() && sidebarOverlayOpen;
  if (btn) {
    const icon = btn.querySelector('[data-icon]');
    if (icon) {
      if (isSidebarOverlayWidth()) {
        icon.setAttribute('data-icon', overlay ? 'chevronLeft' : 'chevronRight');
      } else {
        icon.setAttribute('data-icon', collapsed ? 'chevronRight' : 'chevronLeft');
      }
      hydrateIcons(btn);
    }
  }
  if (brand) {
    const showFull = !collapsed || overlay;
    brand.textContent = showFull ? 'trackerr' : 't';
    brand.setAttribute('title', showFull ? '' : 'trackerr');
  }
  const hideLabels = collapsed && !overlay;
  document.querySelectorAll('.sidebar-nav .nav-link, .sidebar-footer .nav-link').forEach(function (link) {
    const label = link.querySelector('.nav-label');
    if (label) link.setAttribute('title', hideLabels ? label.textContent : '');
  });
}

function applySidebarCollapsed(collapsed) {
  const sidebar = document.getElementById('sidebar');
  const app = document.querySelector('.app');
  const btn = document.getElementById('sidebar-collapse-btn');
  if (sidebar) sidebar.classList.toggle('sidebar-collapsed', collapsed);
  if (app) app.classList.toggle('sidebar-collapsed', collapsed);
  if (btn) btn.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
  if (!collapsed) closeSidebarOverlay();
  refreshSidebarChrome();
}

function refreshSidebarLayout() {
  if (!isSidebarOverlayWidth()) closeSidebarOverlay();
  applySidebarCollapsed(effectiveSidebarCollapsed());
}

function initSidebarCollapse() {
  const btn = document.getElementById('sidebar-collapse-btn');
  if (btn) {
    btn.addEventListener('click', function () {
      const w = window.innerWidth;
      if (w <= 900) return;
      if (w < SIDEBAR_WIDE_MIN) {
        if (sidebarOverlayOpen) closeSidebarOverlay();
        else openSidebarOverlay();
        return;
      }
      const app = document.querySelector('.app');
      const next = !(app && app.classList.contains('sidebar-collapsed'));
      saveSidebarCollapsedPref(next);
      applySidebarCollapsed(next);
    });
  }
  document.querySelectorAll('.sidebar-nav .nav-link').forEach(function (link) {
    link.addEventListener('click', function () {
      if (sidebarOverlayOpen) closeSidebarOverlay();
    });
  });
  refreshSidebarLayout();
  window.addEventListener('resize', refreshSidebarLayout);
}

function applyMobileLayout() {
  const app = document.querySelector('.app');
  if (!app) return;
  const w = window.innerWidth;
  const mobile = w > 0 && w < MOBILE_LAYOUT_MAX;
  const layoutChanged = mobile !== layoutWasMobile;
  if (mobile) {
    const scale = w / PAGE_SCALE_BASE;
    document.documentElement.classList.add('mobile-layout');
    app.classList.add('page-scaled');
    app.style.setProperty('--page-scale', String(scale));
    document.documentElement.style.setProperty('--page-scale-inv', String(1 / scale));
  } else {
    document.documentElement.classList.remove('mobile-layout');
    app.classList.remove('page-scaled');
    app.style.removeProperty('--page-scale');
    document.documentElement.style.removeProperty('--page-scale-inv');
  }
  layoutWasMobile = mobile;
  refreshSidebarLayout();
  if (layoutChanged) syncNameFilterLayout();
}

initSidebarCollapse();
layoutWasMobile = window.innerWidth > 0 && window.innerWidth < MOBILE_LAYOUT_MAX;
applyMobileLayout();
window.addEventListener('resize', applyMobileLayout);

document.querySelectorAll('.theme-toggle').forEach(function (btn) {
  btn.addEventListener('click', function () {
    const current = document.documentElement.getAttribute('data-theme');
    applyTheme(current === 'dark' ? 'light' : 'dark');
  });
});

function closeMobileMenu() {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  const btn = document.getElementById('mobile-menu-btn');
  if (sidebar) sidebar.classList.remove('open');
  if (backdrop) {
    backdrop.classList.remove('visible');
    backdrop.hidden = true;
  }
  if (btn) btn.setAttribute('aria-expanded', 'false');
}

function openMobileMenu() {
  const sidebar = document.getElementById('sidebar');
  const backdrop = document.getElementById('sidebar-backdrop');
  const btn = document.getElementById('mobile-menu-btn');
  if (sidebar) sidebar.classList.add('open');
  if (backdrop) {
    backdrop.hidden = false;
    requestAnimationFrame(function () { backdrop.classList.add('visible'); });
  }
  if (btn) btn.setAttribute('aria-expanded', 'true');
}

const mobileMenuBtn = document.getElementById('mobile-menu-btn');
if (mobileMenuBtn) {
  mobileMenuBtn.addEventListener('click', function () {
    const sidebar = document.getElementById('sidebar');
    if (sidebar && sidebar.classList.contains('open')) closeMobileMenu();
    else openMobileMenu();
  });
}

const sidebarBackdrop = document.getElementById('sidebar-backdrop');
if (sidebarBackdrop) {
  sidebarBackdrop.addEventListener('click', function () {
    if (sidebarOverlayOpen) closeSidebarOverlay();
    else closeMobileMenu();
  });
}

async function fetchJSON(url, options = {}) {
  const headers = { ...options.headers };
  if (options.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch(url, { ...options, headers });
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

function showToast(message, type) {
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

function formatDateTime(iso) {
  if (!iso) return t('common.emDash');
  return new Date(iso).toLocaleString(localeTag());
}

function formatRunSlotTime(iso) {
  return new Date(iso).toLocaleTimeString(localeTag(), { hour: '2-digit', minute: '2-digit' });
}

function formatRunSlot(iso) {
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

function formatThenSlot(iso) {
  const d = new Date(iso);
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const slotDay = new Date(d.getFullYear(), d.getMonth(), d.getDate());
  if (slotDay.getTime() === today.getTime()) return formatRunSlotTime(iso);
  return formatRunSlot(iso);
}

function escapeHtml(s) {
  if (!s) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function isSafeLinkUrl(url) {
  try {
    const scheme = new URL(url).protocol.toLowerCase();
    return scheme === 'http:' || scheme === 'https:' || scheme === 'magnet:';
  } catch {
    return false;
  }
}

function linkCell(name, url) {
  const label = escapeHtml(name || url || t('common.emDash'));
  const titleAttr = name ? ' title="' + escapeHtml(name) + '"' : '';
  if (url && isSafeLinkUrl(url)) {
    return '<a class="release-link" href="' + escapeHtml(url) + '" target="_blank" rel="noopener"' + titleAttr + '>' + label + '</a>';
  }
  return '<span class="release-link"' + titleAttr + '>' + label + '</span>';
}

function statusIcon(status, error) {
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
  return (
    '<span' + dataErr + ' title="' + title + '" aria-label="' + title + '">' +
    icon(iconName) + '</span>'
  );
}

function bindErrorBadges(root) {
  root.querySelectorAll('.status-error-badge').forEach(function (badge) {
    badge.addEventListener('click', function (e) {
      e.stopPropagation();
      const err = badge.getAttribute('data-error') || '';
      closePopovers();
      const pop = document.createElement('div');
      pop.className = 'error-popover';
      pop.textContent = err;
      document.body.appendChild(pop);
      const rect = badge.getBoundingClientRect();
      pop.style.top = (rect.bottom + window.scrollY + 4) + 'px';
      pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
      activeErrorPopover = pop;
    });
  });
}

function showSlotsPopover(card, slots) {
  if (slotsPopoverLeaveTimer) {
    clearTimeout(slotsPopoverLeaveTimer);
    slotsPopoverLeaveTimer = null;
  }
  closePopovers();
  const pop = document.createElement('div');
  pop.className = 'slots-popover';
  slots.forEach(function (s) {
    const item = document.createElement('div');
    item.className = 'slots-popover-item';
    item.textContent = formatRunSlot(s);
    pop.appendChild(item);
  });
  document.body.appendChild(pop);
  const rect = card.getBoundingClientRect();
  pop.style.top = (rect.bottom + window.scrollY + 6) + 'px';
  pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
  activeSlotsPopover = pop;
  pop.addEventListener('mouseenter', function () {
    if (slotsPopoverLeaveTimer) {
      clearTimeout(slotsPopoverLeaveTimer);
      slotsPopoverLeaveTimer = null;
    }
  });
  pop.addEventListener('mouseleave', function () {
    if (activeSlotsPopover) {
      activeSlotsPopover.remove();
      activeSlotsPopover = null;
    }
  });
}

function bindNextRunStatCard(root, slots) {
  const card = root.querySelector('.stat-card-next-run[data-has-slots-popover]');
  if (!card || !slots || slots.length <= 2) return;

  card.addEventListener('mouseenter', function () {
    showSlotsPopover(card, slots);
  });
  card.addEventListener('mouseleave', function () {
    slotsPopoverLeaveTimer = setTimeout(function () {
      if (activeSlotsPopover) {
        activeSlotsPopover.remove();
        activeSlotsPopover = null;
      }
    }, 120);
  });
  card.addEventListener('click', function (e) {
    e.stopPropagation();
    if (activeSlotsPopover) {
      closePopovers();
    } else {
      showSlotsPopover(card, slots);
    }
  });
}

function closePopovers() {
  if (activeErrorPopover) {
    activeErrorPopover.remove();
    activeErrorPopover = null;
  }
  if (activeSlotsPopover) {
    activeSlotsPopover.remove();
    activeSlotsPopover = null;
  }
}

document.addEventListener('click', function (e) {
  closePopovers();
  if (openCustomSelect && !openCustomSelect.contains(e.target)) closeOpenCustomSelect();
  if (openHistFilterSelect && !openHistFilterSelect.contains(e.target)) closeOpenHistFilterSelect();
  if (openNameFilterWrap && !openNameFilterWrap.contains(e.target)) closeOpenNameFilter();
});

document.addEventListener('keydown', function (e) {
  if (e.key === 'Escape') {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    closeOpenNameFilter();
  }
});

function parseHash() {
  const raw = (location.hash || '').replace(/^#/, '');
  const qIdx = raw.indexOf('?');
  const pathPart = qIdx >= 0 ? raw.slice(0, qIdx) : raw;
  const queryPart = qIdx >= 0 ? raw.slice(qIdx + 1) : '';
  const parts = pathPart.split('/');
  const main = parts[0] || 'dashboard';
  if (main === 'ignore') {
    historyState.ignored = true;
    historyState.status = '';
    historyState.offset = 0;
    if (location.hash !== '#dashboard?filter=ignored') {
      history.replaceState(null, '', '#dashboard?filter=ignored');
    }
    return 'dashboard';
  }
  if (queryPart) {
    const params = new URLSearchParams(queryPart);
    const filter = params.get('filter');
    if (filter === 'ignored') {
      historyState.ignored = true;
      historyState.status = '';
    } else if (filter) {
      historyState.ignored = false;
      historyState.status = filter;
    } else {
      historyState.ignored = false;
      historyState.status = '';
    }
    const name = params.get('name');
    historyState.name = name ? name.trim().slice(0, 200) : '';
    historyState.offset = 0;
  } else if ((HASH_VIEW[main] || 'dashboard') === 'dashboard') {
    historyState.ignored = false;
    historyState.status = '';
    historyState.name = '';
    historyState.offset = 0;
  }
  return HASH_VIEW[main] || 'dashboard';
}

function buildHash(view) {
  const base = VIEW_HASH[view] || '#dashboard';
  if (view !== 'dashboard') return base;
  const params = new URLSearchParams();
  if (historyState.ignored) params.set('filter', 'ignored');
  else if (historyState.status) params.set('filter', historyState.status);
  if (historyState.name) params.set('name', historyState.name);
  const qs = params.toString();
  return qs ? base + '?' + qs : base;
}

function syncDashboardFilterHash() {
  if (currentView !== 'dashboard') return;
  const hash = buildHash('dashboard');
  if (location.hash !== hash) {
    history.replaceState(null, '', hash);
  }
}

function applyDashboardFilter(filterType) {
  if (filterType === 'ignored') {
    if (historyState.ignored) {
      historyState.ignored = false;
      historyState.status = '';
    } else {
      historyState.ignored = true;
      historyState.status = '';
    }
  } else if (filterType === 'error') {
    if (!historyState.ignored && historyState.status === 'error') {
      historyState.ignored = false;
      historyState.status = '';
    } else {
      historyState.ignored = false;
      historyState.status = 'error';
    }
  } else {
    historyState.ignored = false;
    historyState.status = '';
  }
  historyState.offset = 0;
  syncDashboardFilterHash();
  loadDashboard();
}

function setActiveNav(view) {
  document.querySelectorAll('[data-view]').forEach(function (a) {
    a.classList.toggle('active', a.getAttribute('data-view') === view);
  });
  document.querySelectorAll('.view').forEach(function (el) {
    el.classList.add('hidden');
  });
  const target = document.getElementById('view-' + view);
  if (target) target.classList.remove('hidden');
}

function showView(view, updateHash) {
  if (currentView === 'dashboard' && view !== 'dashboard') stopDashboardPolling();
  if (currentView === 'logs' && view !== 'logs') stopLogsPolling();
  if (updateHash !== false) {
    const hash = buildHash(view);
    if (location.hash !== hash) {
      location.hash = hash;
      return;
    }
  }
  currentView = view;
  setActiveNav(view);
  closeMobileMenu();
  switch (view) {
    case 'dashboard': loadDashboard(); break;
    case 'schedule': loadSchedule(); break;
    case 'trackers': loadTrackers(); break;
    case 'integrations': loadIntegrations(); break;
    case 'logs': loadLogs(); break;
  }
}

document.querySelectorAll('[data-view]').forEach(function (a) {
  a.addEventListener('click', function (e) {
    e.preventDefault();
    showView(a.getAttribute('data-view'));
  });
});

window.addEventListener('hashchange', function () {
  const view = parseHash();
  if (view !== currentView || view === 'dashboard') {
    showView(view, false);
  }
});

function initChipInput(chipEl, hintsEl, initialValues, suggestions, onChange) {
  const chips = (initialValues || []).slice();
  const chipsEl = chipEl.querySelector('.chip-input-chips');
  const input = chipEl.querySelector('.chip-input-field');

  function notifyChange() {
    if (onChange) onChange(chips.slice());
  }

  function renderChips() {
    chipsEl.innerHTML = '';
    chips.forEach(function (val) {
      const chip = document.createElement('span');
      chip.className = 'chip';
      chip.innerHTML =
        '<span class="chip-label">' + escapeHtml(val) + '</span>' +
        '<button type="button" class="chip-remove" aria-label="' + escapeHtml(t('common.remove')) + '">&times;</button>';
      chip.querySelector('.chip-remove').addEventListener('click', function () {
        const i = chips.indexOf(val);
        if (i !== -1) chips.splice(i, 1);
        renderChips();
        renderHints();
        notifyChange();
      });
      chipsEl.appendChild(chip);
    });
  }

  function addChip(raw) {
    const val = String(raw || '').trim();
    if (!val || chips.indexOf(val) !== -1) return;
    chips.push(val);
    renderChips();
    renderHints();
    input.value = '';
    notifyChange();
  }

  function renderHints() {
    if (!hintsEl) return;
    hintsEl.innerHTML = '';
    (suggestions || []).forEach(function (cat) {
      const selected = chips.indexOf(cat) !== -1;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'chip-hint' + (selected ? ' chip-hint-selected' : '');
      btn.textContent = cat;
      btn.disabled = selected;
      if (!selected) {
        btn.addEventListener('click', function () { addChip(cat); });
      }
      hintsEl.appendChild(btn);
    });
  }

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addChip(input.value.replace(/,/g, ''));
    } else if (e.key === 'Backspace' && !input.value && chips.length) {
      chips.pop();
      renderChips();
      renderHints();
      notifyChange();
    }
  });

  input.addEventListener('blur', function () {
    if (input.value.trim()) addChip(input.value);
  });

  renderChips();
  renderHints();

  return {
    getValues: function () { return chips.slice(); },
    setSuggestions: function (list) {
      suggestions = list || [];
      renderHints();
    },
  };
}

async function ensureQbitCategories() {
  if (qbitCategoriesCache !== null || qbitCategoriesUnavailable) return;
  try {
    const data = await fetchJSON('/api/categories');
    qbitCategoriesCache = data.categories || [];
  } catch (_) {
    qbitCategoriesUnavailable = true;
    qbitCategoriesCache = [];
  }
}

async function ensureTimezones() {
  if (timezonesCache !== null) return;
  try {
    const data = await fetchJSON('/api/timezones');
    timezonesCache = data.timezones || [];
  } catch (_) {
    timezonesCache = [];
  }
}

function statCard(value, label, iconName, variant, filterType) {
  const iconCls = 'stat-icon stat-icon-' + (variant || 'blue');
  let filterCls = '';
  let filterAttr = '';
  if (filterType) {
    filterCls = ' stat-card-filter stat-card-filter-' + variant;
    if (filterType === 'error' && !historyState.ignored && historyState.status === 'error') {
      filterCls += ' stat-card-active';
    } else if (filterType === 'ignored' && historyState.ignored) {
      filterCls += ' stat-card-active';
    }
    filterAttr = ' data-stat-filter="' + filterType + '" role="button" tabindex="0"';
  }
  return (
    '<div class="stat-card' + filterCls + '"' + filterAttr + '>' +
    '<div class="stat-card-body">' +
    '<div class="stat-value">' + escapeHtml(String(value)) + '</div>' +
    '<div class="stat-label">' + escapeHtml(label) + '</div>' +
    '</div>' +
    '<div class="' + iconCls + '" data-icon="' + iconName + '"></div>' +
    '</div>'
  );
}

function getHistFilterOptions() {
  return [
    { v: '', icon: null, label: t('common.all'), activeClass: 'active-neutral' },
    { v: 'updated', icon: 'check', label: t('dash.filter.updated'), activeClass: 'active-success' },
    { v: 'unchanged', icon: 'minus', label: t('dash.filter.unchanged'), activeClass: 'active-muted' },
    { v: 'unknown', icon: 'help', label: t('dash.filter.unknown'), activeClass: 'active-warning' },
    { v: 'error', icon: 'xCircle', label: t('dash.filter.error'), activeClass: 'active-danger' },
    { v: 'ignored', icon: 'eyeOff', label: t('dash.inIgnore'), activeClass: 'active-ignored', isIgnored: true },
  ];
}

function isHistFilterActive(opt) {
  if (opt.isIgnored) return historyState.ignored;
  return !historyState.ignored && historyState.status === opt.v;
}

function getActiveHistFilter() {
  const opts = getHistFilterOptions();
  for (let i = 0; i < opts.length; i++) {
    if (isHistFilterActive(opts[i])) return opts[i];
  }
  return opts[0];
}

function closeOpenHistFilterSelect() {
  if (!openHistFilterSelect) return;
  const trigger = openHistFilterSelect.querySelector('.hist-filter-trigger');
  const panel = openHistFilterSelect.querySelector('.hist-filter-panel');
  openHistFilterSelect.classList.remove('open', 'open-up');
  if (panel) panel.hidden = true;
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  openHistFilterSelect = null;
}

function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

function nameFilterHasValue() {
  if ((historyState.name || '').trim()) return true;
  if (openNameFilterWrap) {
    const input = openNameFilterWrap.querySelector('.name-filter-input, .mobile-name-filter-input');
    if (input && input.value.trim()) return true;
  }
  return false;
}

const MOBILE_SEARCH_CLOSE_MS = 320;
let mobileSearchAnimToken = 0;

function closeMobileNameSearchPanel(root, force) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  if (!force && nameFilterHasValue()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  if (!header || !slot) return;

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  function finishClose() {
    if (token !== mobileSearchAnimToken) return;
    header.classList.remove('is-search-animating');
    if (openNameFilterWrap === slot) openNameFilterWrap = null;
  }

  if (!header.classList.contains('is-search-expanded') && !slot.classList.contains('is-open')) {
    finishClose();
    return;
  }

  if (btn) btn.setAttribute('aria-expanded', 'false');
  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded');

  if (!isMobileStackedLayout()) {
    finishClose();
    return;
  }

  header.classList.add('is-search-animating');

  const shift = scope.querySelector('.card-header-toolbar-shift');
  let done = false;
  function onShiftTransitionEnd(e) {
    if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
    if (done) return;
    done = true;
    shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }
  if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
  setTimeout(function () {
    if (done) return;
    done = true;
    if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }, MOBILE_SEARCH_CLOSE_MS + 40);
}

function openMobileNameSearchPanel(root, focus) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || !isMobileStackedLayout()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  if (!header || !slot || !btn || !input) return;
  if (header.classList.contains('is-search-animating')) return;
  closeOpenCustomSelect();
  closeOpenHistFilterSelect();
  if (openNameFilterWrap && !openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeOpenNameFilter(true);
  }

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  if (slot.classList.contains('is-open') && header.classList.contains('is-search-expanded')) {
    openNameFilterWrap = slot;
    input.value = historyState.name;
    btn.setAttribute('aria-expanded', 'true');
    if (focus) {
      input.focus();
      const len = input.value.length;
      input.setSelectionRange(len, len);
    }
    return;
  }

  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded', 'is-search-animating');
  header.classList.add('is-search-animating');
  btn.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = slot;
  input.value = historyState.name;

  requestAnimationFrame(function () {
    requestAnimationFrame(function () {
      if (token !== mobileSearchAnimToken) return;
      header.classList.add('is-search-expanded');
      slot.classList.add('is-open');
      const shift = scope.querySelector('.card-header-toolbar-shift');
      let done = false;
      function finishOpen() {
        if (token !== mobileSearchAnimToken) return;
        header.classList.remove('is-search-animating');
      }
      function onShiftTransitionEnd(e) {
        if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
        if (done) return;
        done = true;
        shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }
      if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
      setTimeout(function () {
        if (done) return;
        done = true;
        if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }, MOBILE_SEARCH_CLOSE_MS + 40);
      if (focus) {
        input.focus();
        const len = input.value.length;
        input.setSelectionRange(len, len);
      }
    });
  });
}

function closeDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  if (!wrap) return;
  wrap.classList.remove('is-open');
  if (label) label.setAttribute('aria-expanded', 'false');
  if (openNameFilterWrap === wrap) openNameFilterWrap = null;
}

function openDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || isMobileStackedLayout()) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!wrap || !label || !input) return;
  closeMobileNameSearchPanel(scope, true);
  wrap.classList.add('is-open');
  label.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = wrap;
  input.value = historyState.name;
}

function syncNameFilterLayout() {
  if (!(historyState.name || '').trim()) return;
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) return;
  if (isMobileStackedLayout()) {
    closeDesktopNameFilterPanel(el);
    openMobileNameSearchPanel(el, false);
  } else {
    closeMobileNameSearchPanel(el, true);
    openDesktopNameFilterPanel(el);
  }
  updateNameFilterHeader(el);
}

function closeOpenNameFilter(force) {
  if (!openNameFilterWrap) return;
  if (!force && nameFilterHasValue()) return;
  if (openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeMobileNameSearchPanel(document.getElementById('view-dashboard'), true);
  } else {
    const label = openNameFilterWrap.querySelector('.th-name-filter-label');
    openNameFilterWrap.classList.remove('is-open');
    if (label) label.setAttribute('aria-expanded', 'false');
    openNameFilterWrap = null;
  }
  if (nameFilterDebounceTimer) {
    clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = null;
  }
}

async function applyNameFilter(value) {
  const trimmed = (value || '').trim().slice(0, 200);
  if (trimmed === historyState.name) return;
  const keepOpen = openNameFilterWrap !== null;
  historyState.name = trimmed;
  historyState.offset = 0;
  syncDashboardFilterHash();
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) {
    loadDashboard();
    return;
  }
  const fetchGen = ++dashboardFetchGen;
  try {
    const history = await fetchJSON(buildHistoryQuery());
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal(history.items || []);
    updateDashboardTable(el, history);
    updateNameFilterHeader(el);
    if (keepOpen || trimmed) restoreNameFilterPanel(el);
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function updateNameFilterHeader(el) {
  const th = el.querySelector('th.col-name');
  const wrap = el.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!th || !wrap || !label) return;
  const active = !!historyState.name;
  th.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  label.setAttribute('title', title);
  if (input) input.value = historyState.name;
  let funnel = label.querySelector('.th-name-filter-icon-active');
  if (active && !funnel) {
    funnel = document.createElement('span');
    funnel.className = 'th-name-filter-icon th-name-filter-icon-active';
    funnel.setAttribute('data-icon', 'funnel');
    funnel.setAttribute('aria-hidden', 'true');
    label.appendChild(funnel);
    hydrateIcons(label);
  } else if (!active && funnel) {
    funnel.remove();
  }
  updateMobileNameSearchUI(el);
}

function updateMobileNameSearchUI(el) {
  const root = el || document.getElementById('view-dashboard');
  if (!root) return;
  const btn = root.querySelector('#dash-mobile-name-search-btn');
  const slot = root.querySelector('.mobile-name-search-slot');
  const header = root.querySelector('.releases-card-header');
  const input = root.querySelector('.mobile-name-filter-input');
  if (!btn || !slot) return;
  const active = !!historyState.name;
  btn.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  btn.setAttribute('title', title);
  btn.setAttribute('aria-label', title);
  let dot = btn.querySelector('.mobile-name-search-dot');
  if (active && !dot) {
    dot = document.createElement('span');
    dot.className = 'mobile-name-search-dot';
    dot.setAttribute('aria-hidden', 'true');
    btn.appendChild(dot);
  } else if (!active && dot) {
    dot.remove();
  }
  if (input) input.value = historyState.name;
  if (header && active && isMobileStackedLayout()) header.classList.add('is-search-expanded');
  else if (header && !active && !slot.classList.contains('is-open')) {
    header.classList.remove('is-search-expanded');
  }
  if (active && isMobileStackedLayout()) slot.classList.add('is-open');
}

function restoreNameFilterPanel(el) {
  if (isMobileStackedLayout()) {
    openMobileNameSearchPanel(el, true);
    return;
  }
  openDesktopNameFilterPanel(el);
  const input = el.querySelector('.name-filter-input');
  if (input) {
    input.focus();
    const len = input.value.length;
    input.setSelectionRange(len, len);
  }
}

function buildNameColumnHeader() {
  const active = !!historyState.name;
  const thClass = 'col-name' + (active ? ' is-active' : '');
  const label = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  const activeIconHtml = active
    ? '<span class="th-name-filter-icon th-name-filter-icon-active" data-icon="funnel" aria-hidden="true"></span>'
    : '';
  return (
    '<th class="' + thClass + '">' +
    '<div class="name-filter-wrap">' +
    '<span class="th-name-filter-label" role="button" tabindex="0"' +
    ' aria-haspopup="dialog" aria-expanded="false"' +
    ' title="' + escapeHtml(label) + '">' +
    '<span class="th-name-filter-text">' + escapeHtml(t('dash.col.name')) + '</span>' +
    '<span class="th-name-filter-search" data-icon="search" aria-hidden="true"></span>' +
    activeIconHtml +
    '</span>' +
    '<div class="name-filter-inline">' +
    '<input type="text" class="name-filter-input"' +
    ' value="' + escapeHtml(historyState.name) + '"' +
    ' placeholder="' + escapeHtml(t('dash.nameFilterPlaceholder')) + '" autocomplete="off">' +
    '<button type="button" class="name-filter-clear"' +
    ' title="' + escapeHtml(t('dash.nameFilterClear')) + '"' +
    ' aria-label="' + escapeHtml(t('dash.nameFilterClear')) + '">' +
    '<span data-icon="x"></span></button>' +
    '</div></div></th>'
  );
}

function bindNameFilter(root) {
  const wrap = (root || document).querySelector('.name-filter-wrap');
  if (!wrap || wrap.dataset.nameFilterBound) return;
  wrap.dataset.nameFilterBound = '1';

  const label = wrap.querySelector('.th-name-filter-label');
  const input = wrap.querySelector('.name-filter-input');
  const clearBtn = wrap.querySelector('.name-filter-clear');
  if (!label || !input) return;

  function closePanel(force) {
    if (!force && nameFilterHasValue()) return;
    wrap.classList.remove('is-open');
    label.setAttribute('aria-expanded', 'false');
    if (openNameFilterWrap === wrap) openNameFilterWrap = null;
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
  }

  function openPanel() {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    if (openNameFilterWrap && openNameFilterWrap !== wrap) closeOpenNameFilter();
    wrap.classList.add('is-open');
    label.setAttribute('aria-expanded', 'true');
    openNameFilterWrap = wrap;
    input.value = historyState.name;
    input.focus();
    input.select();
  }

  label.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  label.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('is-open')) openPanel();
      else closePanel();
    }
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && !wrap.classList.contains('is-open') && !isMobileStackedLayout()) {
    openPanel();
  }
}

function buildMobileNameSearchSlot() {
  const active = !!historyState.name;
  const slotCls = 'mobile-name-search-slot' + (active ? ' is-open' : '');
  const btnCls = 'btn btn-sm btn-ghost btn-icon btn-mobile-name-search' + (active ? ' is-active' : '');
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  const dotHtml = active ? '<span class="mobile-name-search-dot" aria-hidden="true"></span>' : '';
  const headerExpanded = active ? ' is-search-expanded' : '';
  return {
    headerClass: headerExpanded,
    // .mobile-name-search-toggle is a sibling of .mobile-name-search-slot
    // (not nested inside it): the search button and the close button must
    // sit in a fixed-position box that is completely independent of the
    // search-field's own animated geometry, so the search button is
    // physically unable to move.
    html:
      '<div class="' + slotCls + '">' +
      '<div class="mobile-name-search-field">' +
      '<input type="text" class="name-filter-input mobile-name-filter-input"' +
      ' value="' + escapeHtml(historyState.name) + '"' +
      ' placeholder="' + escapeHtml(t('dash.nameFilterPlaceholder')) + '" autocomplete="off">' +
      '</div></div>' +
      '<div class="mobile-name-search-toggle">' +
      '<button type="button" class="btn btn-sm btn-icon name-filter-clear mobile-name-filter-clear"' +
      ' title="' + escapeHtml(t('dash.nameFilterClear')) + '"' +
      ' aria-label="' + escapeHtml(t('dash.nameFilterClear')) + '">' +
      '<span data-icon="x"></span></button>' +
      '<button type="button" class="' + btnCls + '" id="dash-mobile-name-search-btn"' +
      ' aria-haspopup="true" aria-expanded="' + (active ? 'true' : 'false') + '"' +
      ' title="' + escapeHtml(title) + '" aria-label="' + escapeHtml(title) + '">' +
      '<span data-icon="search"></span>' + dotHtml + '</button>' +
      '</div>',
  };
}

function bindMobileNameSearch(root) {
  const scope = root || document;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  const clearBtn = scope.querySelector('.mobile-name-filter-clear');
  if (!header || !slot || !btn || !input || slot.dataset.mobileNameSearchBound) return;
  slot.dataset.mobileNameSearchBound = '1';

  function closePanel(force) {
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
    closeMobileNameSearchPanel(scope, force);
  }

  function openPanel() {
    openMobileNameSearchPanel(scope, true);
    input.select();
  }

  btn.addEventListener('click', function (e) {
    e.stopPropagation();
    if (header.classList.contains('is-search-animating')) return;
    if (slot.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && isMobileStackedLayout()) {
    openMobileNameSearchPanel(scope, false);
  }
}

function buildHistFilters() {
  const active = getActiveHistFilter();
  const isAll = active.v === '' && !active.isIgnored;
  const triggerClass = 'btn btn-sm btn-ghost btn-icon hist-filter-trigger' +
    (isAll ? '' : ' ' + active.activeClass);
  const dotHtml = isAll ? '' : '<span class="hist-filter-dot" aria-hidden="true"></span>';
  const triggerLabel = isAll ? t('dash.filter') : t('dash.filterActive', { value: active.label });
  let optionsHtml = '';
  getHistFilterOptions().forEach(function (opt) {
    const selected = isHistFilterActive(opt);
    const iconHtml = opt.icon
      ? '<span class="hist-filter-option-icon hist-filter-option-icon-' + (opt.v || 'all') +
        '" data-icon="' + opt.icon + '"></span>'
      : '<span class="hist-filter-option-icon-spacer"></span>';
    const checkHtml = selected
      ? '<span class="hist-filter-check" data-icon="check"></span>'
      : '<span class="hist-filter-check"></span>';
    const dataAttr = opt.isIgnored
      ? ' data-filter-ignored="1"'
      : ' data-status="' + opt.v + '"';
    optionsHtml +=
      '<div class="hist-filter-option' + (selected ? ' is-selected' : '') + '"' +
      ' role="option" aria-selected="' + (selected ? 'true' : 'false') + '"' +
      dataAttr + ' data-active-class="' + opt.activeClass + '">' +
      checkHtml + iconHtml +
      '<span class="hist-filter-option-label">' + escapeHtml(opt.label) + '</span></div>';
  });
  return (
    '<div class="hist-filter-select">' +
    '<button type="button" class="' + triggerClass + '"' +
    ' aria-haspopup="listbox" aria-expanded="false"' +
    ' title="' + escapeHtml(triggerLabel) + '"' +
    ' aria-label="' + escapeHtml(triggerLabel) + '">' +
    '<span data-icon="funnel"></span>' +
    dotHtml +
  '</button>' +
    '<div class="hist-filter-panel" role="listbox" hidden>' + optionsHtml + '</div>' +
    '</div>'
  );
}

function bindHistFilterSelect(root) {
  const wrap = (root || document).querySelector('.hist-filter-select');
  if (!wrap || wrap.dataset.histFilterBound) return;
  wrap.dataset.histFilterBound = '1';

  const trigger = wrap.querySelector('.hist-filter-trigger');
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!trigger || !panel) return;

  function closePanel() {
    wrap.classList.remove('open', 'open-up');
    trigger.setAttribute('aria-expanded', 'false');
    panel.hidden = true;
    if (openHistFilterSelect === wrap) openHistFilterSelect = null;
  }

  function positionPanel() {
    wrap.classList.remove('open-up');
    const rect = trigger.getBoundingClientRect();
    const panelH = Math.min(panel.scrollHeight || 320, 320);
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;
    if (spaceBelow < panelH + 8 && spaceAbove > spaceBelow) wrap.classList.add('open-up');
  }

  function openPanel() {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    closeOpenNameFilter();
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openHistFilterSelect = wrap;
    positionPanel();
  }

  trigger.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('open')) closePanel();
    else openPanel();
  });

  trigger.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) openPanel();
      else closePanel();
    }
  });

  panel.querySelectorAll('.hist-filter-option').forEach(function (opt) {
    opt.addEventListener('click', function (e) {
      e.stopPropagation();
      if (opt.getAttribute('data-filter-ignored') === '1') {
        historyState.ignored = true;
        historyState.status = '';
      } else {
        historyState.ignored = false;
        historyState.status = opt.getAttribute('data-status') || '';
      }
      historyState.offset = 0;
      closePanel();
      syncDashboardFilterHash();
      loadDashboard();
    });
  });
}

function paginationButtons(prevId, nextId, prevDisabled, nextDisabled) {
  return (
    '<button type="button" class="btn btn-sm btn-ghost btn-pagination" id="' + prevId + '"' +
    (prevDisabled ? ' disabled' : '') +
    ' aria-label="' + escapeHtml(t('common.pagePrev')) + '">‹</button>' +
    '<button type="button" class="btn btn-sm btn-ghost btn-pagination" id="' + nextId + '"' +
    (nextDisabled ? ' disabled' : '') +
    ' aria-label="' + escapeHtml(t('common.pageNext')) + '">›</button>'
  );
}

function buildPageSizeSelect(currentSize) {
  let html = '<select class="select select-compact page-size-select" id="hist-page-size" aria-label="' +
    escapeHtml(t('common.pageSize')) + '">';
  PAGE_SIZE_OPTIONS.forEach(function (n) {
    const label = n === 0 ? t('common.all') : String(n);
    html += '<option value="' + n + '"' + (currentSize === n ? ' selected' : '') + '>' +
      escapeHtml(label) + '</option>';
  });
  html += '</select>';
  return html;
}

function buildSegmentControl(options, activeValue, ariaLabel) {
  let activeIdx = 0;
  options.forEach(function (opt, i) {
    if (opt.value === activeValue) activeIdx = i;
  });
  let btns = '';
  options.forEach(function (opt, i) {
    const isActive = i === activeIdx;
    btns +=
      '<button type="button" class="seg-ctrl-btn' + (isActive ? ' active' : '') + '"' +
      ' data-value="' + escapeHtml(opt.value) + '"' +
      (opt.dataAttr || '') +
      ' title="' + escapeHtml(opt.title) + '"' +
      ' aria-label="' + escapeHtml(opt.title) + '"' +
      ' aria-pressed="' + (isActive ? 'true' : 'false') + '">' +
      (opt.icon ? '<span data-icon="' + opt.icon + '"></span>' : escapeHtml(opt.label || '')) +
      '</button>';
  });
  return (
    '<div class="seg-ctrl" role="group" aria-label="' + escapeHtml(ariaLabel) +
    '" data-active-index="' + activeIdx + '">' +
    '<div class="seg-ctrl-thumb" aria-hidden="true"></div>' + btns + '</div>'
  );
}

function updateSegmentThumb(seg) {
  const active = seg.querySelector('.seg-ctrl-btn.active');
  if (!active) return;
  const btns = seg.querySelectorAll('.seg-ctrl-btn');
  let idx = 0;
  btns.forEach(function (b, i) {
    if (b === active) idx = i;
  });
  seg.setAttribute('data-active-index', String(idx));
  seg.style.setProperty('--seg-count', String(btns.length));
  const thumb = seg.querySelector('.seg-ctrl-thumb');
  if (thumb) thumb.style.transform = 'translateX(' + idx * 100 + '%)';
}

function initSegmentControls(root) {
  (root || document).querySelectorAll('.seg-ctrl').forEach(function (seg) {
    if (seg.dataset.segBound) return;
    seg.dataset.segBound = '1';
    updateSegmentThumb(seg);
    seg.querySelectorAll('.seg-ctrl-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        if (btn.classList.contains('active')) return;
        seg.querySelectorAll('.seg-ctrl-btn').forEach(function (b) {
          b.classList.remove('active');
          b.setAttribute('aria-pressed', 'false');
        });
        btn.classList.add('active');
        btn.setAttribute('aria-pressed', 'true');
        updateSegmentThumb(seg);
        seg.dispatchEvent(new CustomEvent('segchange', { bubbles: true, detail: { value: btn.getAttribute('data-value') } }));
      });
    });
  });
}

function bindPageSizeSelect(root, onChange) {
  const sel = (root || document).querySelector('.page-size-select');
  if (!sel || sel.dataset.pageSizeBound) return;
  sel.dataset.pageSizeBound = '1';
  initCustomSelects(root);
  sel.addEventListener('change', function () {
    const n = parseInt(sel.value, 10);
    const size = PAGE_SIZE_OPTIONS.indexOf(n) >= 0 ? n : 10;
    savePageSize(size);
    onChange(size);
  });
}

function buildHistoryQuery() {
  const limit = pageSizeQueryLimit(historyState.limit);
  let q = '/api/history?limit=' + limit + '&offset=' + historyState.offset +
    '&sort=' + encodeURIComponent(historyState.sort);
  if (historyState.ignored) q += '&ignored=1';
  else if (historyState.status) q += '&status=' + encodeURIComponent(historyState.status);
  if (historyState.name) q += '&name=' + encodeURIComponent(historyState.name);
  return q;
}

function buildSortControls() {
  return buildSegmentControl([
    { value: 'checked', icon: 'list', title: t('dash.sort.checked'), dataAttr: ' data-sort="checked"' },
    { value: 'recheck', icon: 'clock', title: t('dash.sort.recheck'), dataAttr: ' data-sort="recheck"' },
  ], historyState.sort, t('dash.sortLabel'));
}

function nextRunStatCard(slots) {
  const runs = slots || [];
  const first = runs[0] ? formatRunSlot(runs[0]) : t('common.emDash');
  let subLine = '';
  const hasPopover = runs.length > 2;
  if (runs.length >= 2) {
    subLine = '<div class="stat-sub"><span class="stat-then-pill">' +
      escapeHtml(t('dash.then', { time: formatThenSlot(runs[1]) })) + '</span></div>';
  }
  const popoverAttr = hasPopover ? ' data-has-slots-popover="1"' : '';
  return (
    '<div class="stat-card stat-card-next-run"' + popoverAttr + '>' +
    '<div class="stat-card-body">' +
    '<div class="stat-value">' + escapeHtml(first) + '</div>' +
  subLine +
    '<div class="stat-label">' + escapeHtml(t('dash.nextRun')) + '</div>' +
    '</div>' +
    '<div class="stat-icon stat-icon-green" data-icon="clock"></div>' +
    '</div>'
  );
}

async function fetchDashboardData() {
  const [status, history, histTotal, histErrors, ignoreItems] = await Promise.all([
    fetchJSON('/api/status'),
    fetchJSON(buildHistoryQuery()),
    fetchJSON('/api/history?limit=1'),
    fetchJSON('/api/history?limit=1&status=error'),
    fetchJSON('/api/ignore'),
  ]);
  ignoreHashesCache = new Set((ignoreItems || []).map(function (i) { return i.url_hash; }));
  return {
    status: status,
    history: history,
    stats: {
      total: histTotal.total || 0,
      errors: histErrors.total || 0,
      ignored: (ignoreItems || []).length,
    },
  };
}

async function loadDashboard() {
  const el = document.getElementById('view-dashboard');
  const hadContent = !!el.querySelector('.page-header');
  if (!hadContent) {
    el.innerHTML = '<div class="loading">' + escapeHtml(t('common.loading')) + '</div>';
  }
  const fetchGen = ++dashboardFetchGen;
  try {
    const data = await fetchDashboardData();
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal((data.history && data.history.items) || []);
    renderDashboard(el, data.status, data.history, data.stats);
    updateDashboardRefreshedAt();
    ensureDashboardPolling();
  } catch (err) {
    el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    showToast(err.message, 'error');
  }
}

async function refreshDashboard(manual) {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.page-header')) return;
  if (!manual && dashboardHasActiveInteraction()) return;

  if (manual) {
    const refreshBtn = document.getElementById('dash-refresh-btn');
    if (refreshBtn) refreshBtn.classList.add('is-spinning');
  }

  const fetchGen = ++dashboardFetchGen;
  try {
    const data = await fetchDashboardData();
    if (fetchGen !== dashboardFetchGen) return;
    if (!manual && dashboardHasActiveInteraction()) return;
    syncPlanForceLocal((data.history && data.history.items) || []);
    renderDashboard(el, data.status, data.history, data.stats);
    updateDashboardRefreshedAt();
  } catch (_) { /* ponytail: silent refresh is best-effort */ }

  if (manual) {
    const refreshBtn = document.getElementById('dash-refresh-btn');
    if (refreshBtn) refreshBtn.classList.remove('is-spinning');
  }
}

async function reloadDashboardTable() {
  const el = document.getElementById('view-dashboard');
  if (!el) return;
  const fetchGen = ++dashboardFetchGen;
  try {
    const data = await fetchDashboardData();
    if (fetchGen !== dashboardFetchGen) return;
    updateDashboardTable(el, data.history);
  } catch (err) {
    showToast(err.message, 'error');
  }
}

function buildReleaseRows(histItems) {
  if (!histItems || histItems.length === 0) {
    return '<tr class="table-empty"><td colspan="6">' + escapeHtml(t('dash.empty.releases')) + '</td></tr>';
  }
  let releaseRows = '';
  histItems.forEach(function (item) {
    const urlHash = item.url_hash || '';
    const ignored = isUrlIgnored(urlHash);
    const forced = planItemForced(item);
    const timeShort = formatTableTime(item.checked_at);
    const timeFull = formatDateTime(item.checked_at);
    const recheckShort = formatTableTime(item.recheck_at);
    const recheckFull = formatDateTime(item.recheck_at);
    const btnCls = ignored
      ? 'btn btn-icon-only btn-unignore btn-ignore-item btn-action-ignore'
      : 'btn btn-icon-only btn-ignore btn-ignore-item btn-action-ignore';
    const btnTitle = ignored ? t('dash.fromIgnore') : t('dash.toIgnore');
    const rowCls = forced ? ' class="plan-row-force"' : '';
    const trackerBadge = trackerBadgeHtml(item.tracker_name);
    releaseRows +=
      '<tr' + rowCls + ' data-release-hash="' + escapeHtml(urlHash) + '">' +
      '<td class="col-name">' +
      '<div class="release-title-wrap">' + linkCell(item.name, item.url) + '</div>' +
      '<div class="release-foot">' + trackerBadge + '</div></td>' +
      '<td class="col-tracker">' + trackerBadge + '</td>' +
      '<td class="col-status">' + statusIcon(item.status, item.error) + '</td>' +
      '<td class="col-time" title="' + escapeHtml(timeFull) + '">' + escapeHtml(timeShort) + '</td>' +
      '<td class="plan-recheck-cell" data-recheck-short="' + escapeHtml(recheckShort) +
      '" data-recheck-full="' + escapeHtml(recheckFull) + '">' +
      '<span class="plan-recheck-text" title="' + escapeHtml(recheckFull) + '">' +
      escapeHtml(recheckShort) + '</span></td>' +
      '<td class="col-actions">' +
      '<div class="release-actions">' +
      planForceButtonHtml(urlHash, forced) +
      '<button type="button" class="' + btnCls + '" ' +
      'title="' + escapeHtml(btnTitle) + '" aria-label="' + escapeHtml(btnTitle) + '" ' +
      'data-url="' + escapeHtml(item.url || '') + '" data-name="' + escapeHtml(item.name || '') +
      '" data-hash="' + escapeHtml(urlHash) + '">' +
      ignoreButtonIconsHtml(ignored) + '</button></div></td>' +
      '</tr>';
  });
  return releaseRows;
}

function updateDashboardPagination(el, history) {
  const total = history.total || 0;
  const from = total === 0 ? 0 : historyState.offset + 1;
  const to = Math.min(historyState.offset + pageSizeQueryLimit(historyState.limit), total);
  const pageLimit = pageSizeQueryLimit(historyState.limit);
  const footerText = el.querySelector('.card-footer > .text-muted');
  if (footerText) {
    footerText.textContent = t('dash.pagination', { from: from, to: to, total: total });
  }
  const prev = el.querySelector('#hist-prev');
  const next = el.querySelector('#hist-next');
  if (prev) prev.disabled = historyState.offset <= 0;
  if (next) next.disabled = historyState.offset + pageLimit >= total;
}

function updateForceAllButton(el, histItems) {
  const btn = el.querySelector('#dash-force-all-btn');
  if (!btn) return;
  const nonIgnored = (histItems || []).filter(function (item) { return !isUrlIgnored(item.url_hash); });
  const allForced = nonIgnored.length > 0 && nonIgnored.every(function (item) { return planItemForced(item); });
  const title = allForced ? t('dash.forceAllClear') : t('dash.forceAll');
  btn.classList.toggle('is-active', allForced);
  btn.setAttribute('aria-pressed', allForced ? 'true' : 'false');
  btn.setAttribute('title', title);
  btn.setAttribute('aria-label', title);
}

function updateDashboardTable(el, history) {
  const histItems = history.items || [];
  const tbody = el.querySelector('.table-dash-releases tbody');
  if (tbody) {
    tbody.innerHTML = buildReleaseRows(histItems);
    hydrateIcons(tbody);
    bindDashboardReleaseRows(tbody);
  }
  updateDashboardPagination(el, history);
  updateForceAllButton(el, histItems);
}

function bindDashboardReleaseRows(root) {
  const el = root || document;
  el.querySelectorAll('.btn-ignore-item').forEach(function (btn) {
    if (btn.dataset.ignoreBound) return;
    btn.dataset.ignoreBound = '1';
    btn.addEventListener('click', async function () {
      const url = btn.getAttribute('data-url');
      const name = btn.getAttribute('data-name');
      const hash = btn.getAttribute('data-hash');
      const ignored = isUrlIgnored(hash);
      if (!ignored && !url) {
        showToast(t('ignore.noUrl'), 'error');
        return;
      }
      if (ignored && !hash) {
        showToast(t('ignore.noUrl'), 'error');
        return;
      }
      btn.disabled = true;
      try {
        if (ignored) {
          await fetchJSON('/api/ignore/' + encodeURIComponent(hash), { method: 'DELETE' });
          showToast(t('ignore.deleted'), 'success');
        } else {
          await fetchJSON('/api/ignore', {
            method: 'POST',
            body: JSON.stringify({ url: url, name: name }),
          });
          showToast(t('ignore.added'), 'success');
        }
        await refreshIgnoreState();
        if (historyState.ignored) loadDashboard();
      } catch (err) {
        showToast(err.message, 'error');
        btn.disabled = false;
      }
    });
  });

  el.querySelectorAll('.btn-plan-force').forEach(function (btn) {
    if (btn.dataset.planForceBound) return;
    btn.dataset.planForceBound = '1';
    btn.addEventListener('click', async function () {
      const hash = btn.getAttribute('data-hash');
      if (!hash) return;
      const enable = btn.getAttribute('data-force') !== '1';
      btn.disabled = true;
      try {
        const res = await fetchJSON('/api/plan/' + encodeURIComponent(hash) + '/force', {
          method: 'POST',
          body: JSON.stringify({ force: enable }),
        });
        dashboardFetchGen++;
        showToast(enable ? t('dash.forceEnabled') : t('dash.forceDisabled'), 'success');
        updateReleaseRow(hash, !!(res && res.force));
      } catch (err) {
        showToast(err.message, 'error');
        btn.disabled = false;
      }
    });
  });

  bindErrorBadges(el);
}

function renderDashboard(el, status, history, stats) {
  closePopovers();
  const nextRuns = status.next_runs || [];

  const histItems = history.items || [];
  const total = history.total || 0;
  const releaseRows = buildReleaseRows(histItems);

  const from = total === 0 ? 0 : historyState.offset + 1;
  const to = Math.min(historyState.offset + pageSizeQueryLimit(historyState.limit), total);
  const pageLimit = pageSizeQueryLimit(historyState.limit);
  const filterHtml = buildHistFilters();
  const sortHtml = buildSortControls();
  const nonIgnored = histItems.filter(function (item) { return !isUrlIgnored(item.url_hash); });
  const allForced = nonIgnored.length > 0 && nonIgnored.every(function (item) { return planItemForced(item); });
  const forceAllTitle = allForced ? t('dash.forceAllClear') : t('dash.forceAll');
  const forceAllCls = 'btn btn-sm btn-ghost btn-icon btn-force-all' + (allForced ? ' is-active' : '');
  const mobileSearch = buildMobileNameSearchSlot();

  el.innerHTML =
    '<div class="page-header dash-page-header">' +
    '<div class="page-header-row">' +
    '<div class="dash-header-top">' +
    '<h1 class="page-title">' + escapeHtml(t('dash.title')) + '</h1>' +
    '<div class="dash-header-status-block">' +
    '<div class="run-status" id="dash-run-status"></div>' +
    '<span class="dash-meta-sep" aria-hidden="true">·</span>' +
    '<span class="dash-last-updated text-muted" id="dash-last-updated"></span>' +
    '</div></div>' +
    '<div class="page-header-actions dash-header-actions">' +
    '<div class="page-header-run-group">' +
    '<button type="button" class="btn btn-stop" id="dash-stop-btn"' +
    (effectiveRunning(status.running) ? '' : ' style="display:none"') +
    ' data-i18n-aria="dash.stopCheck" aria-label="' + escapeHtml(t('dash.stopCheck')) +
    '" title="' + escapeHtml(t('dash.stopCheck')) + '">' +
    '<span data-icon="stop"></span>' +
    '<span class="btn-stop-label">' + escapeHtml(t('dash.stop')) + '</span></button>' +
    '<button type="button" class="btn ' + (effectiveRunning(status.running) ? 'btn-restart' : 'btn-primary') +
    '" id="dash-run-btn">' + escapeHtml(effectiveRunning(status.running) ? t('dash.restartCheck') : t('dash.runCheck')) + '</button>' +
    '</div></div></div></div>' +

    renderAlertsHtml(status.alerts || []) +

    '<div class="stat-grid">' +
    nextRunStatCard(nextRuns) +
    statCard(stats.total, t('dash.historyCount'), 'list', 'blue', 'all') +
    statCard(stats.errors, t('dash.errors'), 'warning', 'danger', 'error') +
    statCard(stats.ignored, t('dash.ignored'), 'eyeOff', 'cyan', 'ignored') +
    '</div>' +

    '<div class="card">' +
    '<div class="card-header releases-card-header' + mobileSearch.headerClass + '">' +
    '<div class="card-header-toolbar">' +
    '<div class="card-header-toolbar-shift">' +
    '<div class="card-header-left">' +
    '<h2 class="card-title">' + escapeHtml(t('dash.releasesTitle')) + '</h2>' +
    '<button type="button" class="btn btn-sm btn-ghost btn-icon btn-refresh" id="dash-refresh-btn"' +
    ' data-i18n-aria="dash.refresh" aria-label="' + escapeHtml(t('dash.refresh')) + '"' +
    ' title="' + escapeHtml(t('dash.refresh')) + '">' +
    '<span data-icon="refresh"></span></button>' +
    '</div>' +
    '<div class="card-toolbar-pre-search">' + sortHtml +
    '<button type="button" class="' + forceAllCls + '" id="dash-force-all-btn"' +
    ' title="' + escapeHtml(forceAllTitle) + '" aria-label="' + escapeHtml(forceAllTitle) + '"' +
    ' aria-pressed="' + (allForced ? 'true' : 'false') + '">' +
    '<span data-icon="zapForceAll"></span></button>' +
    '</div></div>' +
    '<div class="card-toolbar-post-search">' +
    mobileSearch.html +
    filterHtml +
    '</div></div></div>' +
    '<div class="card-body-flush">' +
    '<div class="table-wrap">' +
    '<table class="table table-dash-releases">' +
    '<thead><tr>' + buildNameColumnHeader() + '<th class="col-tracker">' +
    escapeHtml(t('dash.col.tracker')) + '</th><th class="col-status">' +
    '<span class="th-label-full">' + escapeHtml(t('dash.col.status')) + '</span>' +
    '<span class="th-label-short">' + escapeHtml(t('dash.col.statusShort')) + '</span>' +
    '</th><th class="col-time">' +
    '<span class="th-label-full">' + escapeHtml(t('dash.col.checked')) + '</span>' +
    '<span class="th-label-short">' + escapeHtml(t('dash.col.checkedShort')) + '</span>' +
    '</th><th class="plan-recheck-cell">' +
    '<span class="th-label-full">' + escapeHtml(t('dash.col.recheck')) + '</span>' +
    '<span class="th-label-short">' + escapeHtml(t('dash.col.recheckShort')) + '</span>' +
    '</th><th class="col-actions"></th></tr></thead>' +
    '<tbody>' + releaseRows + '</tbody></table></div>' +
    '</div>' +
    '<div class="card-footer">' +
    '<span class="text-muted">' + escapeHtml(t('dash.pagination', { from: from, to: to, total: total })) + '</span>' +
    '<div class="pagination-controls">' +
    buildPageSizeSelect(historyState.limit) +
    '<div class="btn-group pagination-nav">' +
    paginationButtons('hist-prev', 'hist-next', historyState.offset <= 0,
      historyState.offset + pageLimit >= total) +
    '</div></div></div>';

  hydrateIcons(el);
  bindDashboardAlerts(el);
  bindNextRunStatCard(el, nextRuns);
  if (!status.running) setStopPending(false);
  updateRunStatusUI(effectiveRunning(status.running));
  dashboardStatusSlots = nextRuns;
  dashboardAlerts = status.alerts || [];

  const runBtn = el.querySelector('#dash-run-btn');
  if (runBtn) runBtn.addEventListener('click', triggerRunCheck);

  const stopBtn = el.querySelector('#dash-stop-btn');
  if (stopBtn) stopBtn.addEventListener('click', triggerStopCheck);

  const refreshBtn = el.querySelector('#dash-refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', function () {
      refreshDashboard(true);
    });
  }

  el.querySelectorAll('.seg-ctrl').forEach(function (seg) {
    if (!seg.querySelector('.seg-ctrl-btn[data-sort]') || seg.dataset.sortBound) return;
    seg.dataset.sortBound = '1';
    seg.addEventListener('segchange', function (e) {
      const sort = e.detail && e.detail.value;
      if (!sort || sort === historyState.sort) return;
      historyState.sort = sort;
      historyState.offset = 0;
      reloadDashboardTable();
    });
  });
  initSegmentControls(el);
  requestAnimationFrame(function () {
    el.querySelectorAll('.seg-ctrl').forEach(updateSegmentThumb);
  });

  bindPageSizeSelect(el, function (size) {
    historyState.limit = size;
    historyState.offset = 0;
    loadDashboard();
  });

  bindHistFilterSelect(el);
  bindNameFilter(el);
  bindMobileNameSearch(el);
  bindStatCardFilters(el);

  const prev = el.querySelector('#hist-prev');
  if (prev) {
    prev.addEventListener('click', function () {
      historyState.offset = Math.max(0, historyState.offset - pageSizeQueryLimit(historyState.limit));
      loadDashboard();
    });
  }
  const next = el.querySelector('#hist-next');
  if (next) {
    next.addEventListener('click', function () {
      historyState.offset += pageSizeQueryLimit(historyState.limit);
      loadDashboard();
    });
  }

  bindDashboardReleaseRows(el);

  const forceAllBtn = el.querySelector('#dash-force-all-btn');
  if (forceAllBtn) {
    forceAllBtn.addEventListener('click', async function () {
      const enable = !forceAllBtn.classList.contains('is-active');
      forceAllBtn.disabled = true;
      try {
        const res = await fetchJSON('/api/plan/force-all', {
          method: 'POST',
          body: JSON.stringify({ force: enable }),
        });
        const count = (res && res.updated) || 0;
        dashboardFetchGen++;
        showToast(enable ? t('dash.forceAllDone', { count: count }) : t('dash.forceAllCleared', { count: count }), 'success');
        loadDashboard();
      } catch (err) {
        showToast(err.message, 'error');
        forceAllBtn.disabled = false;
      }
    });
  }
}

function bindStatCardFilters(root) {
  (root || document).querySelectorAll('.stat-card-filter').forEach(function (card) {
    if (card.dataset.statFilterBound) return;
    card.dataset.statFilterBound = '1';
    function activate() {
      applyDashboardFilter(card.getAttribute('data-stat-filter'));
    }
    card.addEventListener('click', activate);
    card.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        activate();
      }
    });
  });
}

function hourOptions(selected) {
  let html = '';
  for (let h = 0; h < 24; h++) {
    html += '<option value="' + h + '"' + (h === selected ? ' selected' : '') + '>' +
      String(h).padStart(2, '0') + ':00</option>';
  }
  return html;
}

function refreshOptions(selected) {
  return REFRESH_OPTIONS.map(function (n) {
    return '<option value="' + n + '"' + (n === selected ? ' selected' : '') + '>' + n + '</option>';
  }).join('');
}

function timezoneOptions(timezones, selected) {
  let html = '<option value="">' + escapeHtml(t('settings.timezoneDefault')) + '</option>';
  (timezones || []).forEach(function (tz) {
    const id = typeof tz === 'string' ? tz : tz.id;
    const label = typeof tz === 'string' ? tz : formatTimezoneLabel(tz);
    html += '<option value="' + escapeHtml(id) + '"' +
      (id === selected ? ' selected' : '') + '>' + escapeHtml(label) + '</option>';
  });
  return html;
}

function bindDeleteCascade(el) {
  const deleteEnabled = el.querySelector('#delete_enabled');
  const deleteFilesEnabled = el.querySelector('#delete_files_enabled');
  const deleteSection = el.querySelector('#delete-dependent');
  const deleteFilesGroup = el.querySelector('#delete-files-categories-group');

  function update() {
    const delOn = deleteEnabled.checked;
    deleteFilesEnabled.disabled = !delOn;
    if (deleteSection) deleteSection.classList.toggle('card-disabled', !delOn);
    if (!delOn) deleteFilesEnabled.checked = false;

    const filesOn = delOn && deleteFilesEnabled.checked;
    if (deleteFilesGroup) {
      deleteFilesGroup.style.display = filesOn ? '' : 'none';
    }
  }

  deleteEnabled.addEventListener('change', update);
  deleteFilesEnabled.addEventListener('change', update);
  update();
}

async function loadSchedule() {
  const el = document.getElementById('view-schedule');
  el.innerHTML = '<div class="loading">' + escapeHtml(t('common.loading')) + '</div>';
  try {
    await Promise.all([ensureQbitCategories(), ensureTimezones()]);
    const sch = await fetchJSON('/api/schedule');
    renderSchedule(el, sch, qbitCategoriesCache || [], qbitCategoriesUnavailable, timezonesCache || []);
  } catch (err) {
    el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    showToast(err.message, 'error');
  }
}

function renderSchedule(el, sch, suggestions, qbitDown, timezones) {
  const qbitNote = qbitDown
    ? '<p class="form-hint form-hint-warn">' + escapeHtml(t('settings.qbitUnavailable')) + '</p>'
    : '';

  el.innerHTML =
    '<div class="page-header">' +
    '<h1 class="page-title">' + escapeHtml(t('settings.title')) + '</h1>' +
    '</div>' +
    '<form id="schedule-form" class="stack">' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('settings.interface')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="form-group">' +
    '<label class="form-label" for="ui_lang">' + escapeHtml(t('settings.language')) + '</label>' +
    '<select class="select" id="ui_lang" name="ui_lang">' +
    '<option value="ru"' + (getLang() === 'ru' ? ' selected' : '') + '>' + escapeHtml(t('settings.languageRu')) + '</option>' +
    '<option value="en"' + (getLang() === 'en' ? ' selected' : '') + '>' + escapeHtml(t('settings.languageEn')) + '</option>' +
    '</select></div>' +
    '</div></div>' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('settings.schedule')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="form-row">' +
    '<div class="form-group">' +
    '<label class="form-label" for="start_hour">' + escapeHtml(t('settings.startHour')) + '</label>' +
    '<select class="select" id="start_hour" name="start_hour">' +
    hourOptions(sch.start_hour) + '</select></div>' +
    '<div class="form-group">' +
    '<label class="form-label" for="list_refresh_per_day">' + escapeHtml(t('settings.runsPerDay')) + '</label>' +
    '<select class="select" id="list_refresh_per_day" name="list_refresh_per_day">' +
    refreshOptions(sch.list_refresh_per_day) + '</select></div>' +
    '<div class="form-group">' +
    '<label class="form-label" for="timezone">' + escapeHtml(t('settings.timezone')) + '</label>' +
    '<select class="select" id="timezone" name="timezone">' +
    timezoneOptions(timezones, sch.timezone || '') + '</select></div>' +
    '</div>' +
    '<hr class="form-divider">' +
    '<p class="form-section-title">' + escapeHtml(t('settings.thresholds')) + '</p>' +
    '<div class="form-row">' +
    '<div class="form-group">' +
    '<label class="form-label" for="threshold_month">' + escapeHtml(t('settings.thresholdMonth')) + '</label>' +
    '<input type="number" class="input" id="threshold_month" min="1" value="' +
    sch.threshold_month + '"></div>' +
    '<div class="form-group">' +
    '<label class="form-label" for="threshold_half_year">' + escapeHtml(t('settings.thresholdHalfYear')) + '</label>' +
    '<input type="number" class="input" id="threshold_half_year" min="1" value="' +
    sch.threshold_half_year + '"></div>' +
    '<div class="form-group">' +
    '<label class="form-label" for="threshold_year">' + escapeHtml(t('settings.thresholdYear')) + '</label>' +
    '<input type="number" class="input" id="threshold_year" min="1" value="' +
    sch.threshold_year + '"></div>' +
    '</div>' +
    '<hr class="form-divider">' +
    '<div class="form-group">' +
    '<label class="form-label">' + escapeHtml(t('settings.watchTags')) + '</label>' +
    '<div class="chip-input" id="watch_tags">' +
    '<div class="chip-input-chips"></div>' +
    '<input type="text" class="chip-input-field" autocomplete="off" placeholder="' + escapeHtml(t('common.chipPlaceholder')) + '">' +
    '</div>' +
    '<div class="chip-input-hints" id="watch_tags-hints"></div>' +
    '<p class="form-hint">' + escapeHtml(t('settings.watchTagsHint')) + '</p>' +
    '</div>' +
    '</div></div>' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('settings.categories')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="form-group">' +
    '<div class="chip-input" id="monitored_categories">' +
    '<div class="chip-input-chips"></div>' +
    '<input type="text" class="chip-input-field" autocomplete="off" placeholder="' + escapeHtml(t('common.chipPlaceholder')) + '">' +
    '</div>' +
    '<div class="chip-input-hints" id="monitored_categories-hints"></div>' +
    '<p class="form-hint">' + escapeHtml(t('settings.categoriesHint')) + '</p>' +
    qbitNote +
    '</div></div></div>' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('settings.deletion')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="form-group">' +
    '<label class="form-switch">' +
    '<input type="checkbox" id="delete_enabled"' + (sch.delete_enabled ? ' checked' : '') + '>' +
    '<span>' + escapeHtml(t('settings.deleteEnabled')) + '</span></label>' +
    '<p class="form-hint">' + escapeHtml(t('settings.deleteEnabledHint')) + '</p>' +
    '</div>' +
    '<div id="delete-dependent">' +
    '<div class="form-group">' +
    '<label class="form-switch">' +
    '<input type="checkbox" id="delete_files_enabled"' + (sch.delete_files_enabled ? ' checked' : '') + '>' +
    '<span>' + escapeHtml(t('settings.deleteFiles')) + '</span></label>' +
    '<p class="form-hint">' + escapeHtml(t('settings.deleteFilesHint')) + '</p>' +
    '</div>' +
    '<div class="form-group" id="delete-files-categories-group">' +
    '<label class="form-label">' + escapeHtml(t('settings.deleteCategories')) + '</label>' +
    '<div class="chip-input" id="delete_files_categories">' +
    '<div class="chip-input-chips"></div>' +
    '<input type="text" class="chip-input-field" autocomplete="off" placeholder="' + escapeHtml(t('common.chipPlaceholder')) + '">' +
    '</div>' +
    '<div class="chip-input-hints" id="delete_files_categories-hints"></div>' +
    '<p class="form-hint">' + escapeHtml(t('settings.deleteCategoriesHint')) + '</p>' +
    '</div>' +
    '</div>' +
    '</div></div>' +

    '<div class="form-submit-row">' +
    '<button type="submit" class="btn btn-primary">' + escapeHtml(t('common.save')) + '</button>' +
    '<p class="form-hint" style="margin-top:12px">' + escapeHtml(t('settings.saveHint')) + '</p>' +
    '</div>' +
    '</form>';

  const langSelect = el.querySelector('#ui_lang');
  langSelect.addEventListener('change', function () {
    if (langSelect.value !== getLang()) setLang(langSelect.value);
  });

  const monitoredInput = initChipInput(
    el.querySelector('#monitored_categories'),
    el.querySelector('#monitored_categories-hints'),
    sch.monitored_categories || [],
    suggestions
  );

  const watchTagsInput = initChipInput(
    el.querySelector('#watch_tags'),
    el.querySelector('#watch_tags-hints'),
    sch.watch_tags || [],
    []
  );

  const deleteInput = initChipInput(
    el.querySelector('#delete_files_categories'),
    el.querySelector('#delete_files_categories-hints'),
    sch.delete_files_categories || [],
    suggestions
  );

  bindDeleteCascade(el);
  initCustomSelects(el);

  el.querySelector('#schedule-form').addEventListener('submit', async function (e) {
    e.preventDefault();
    const body = {
      start_hour: parseInt(document.getElementById('start_hour').value, 10),
      list_refresh_per_day: parseInt(document.getElementById('list_refresh_per_day').value, 10),
      threshold_month: parseInt(document.getElementById('threshold_month').value, 10),
      threshold_half_year: parseInt(document.getElementById('threshold_half_year').value, 10),
      threshold_year: parseInt(document.getElementById('threshold_year').value, 10),
      delete_enabled: document.getElementById('delete_enabled').checked,
      delete_files_enabled: document.getElementById('delete_files_enabled').checked,
      monitored_categories: monitoredInput.getValues(),
      delete_files_categories: deleteInput.getValues(),
      watch_tags: watchTagsInput.getValues(),
      timezone: document.getElementById('timezone').value,
    };
    try {
      await fetchJSON('/api/schedule', { method: 'PUT', body: JSON.stringify(body) });
      showToast(t('settings.saved'), 'success');
    } catch (err) {
      showToast(err.message, 'error');
    }
  });
}

function activeDomainOptionsHtml(domain, mirrors, selected) {
  const sel = selected || '';
  let html = '<option value=""' + (sel === '' ? ' selected' : '') + '>' +
    escapeHtml(t('trackers.activeDomainAuto')) + '</option>';
  if (domain) {
    html += '<option value="' + escapeHtml(domain) + '"' +
      (sel === domain ? ' selected' : '') + '>' + escapeHtml(domain) + '</option>';
  }
  (mirrors || []).forEach(function (m) {
    if (!m || m === domain) return;
    html += '<option value="' + escapeHtml(m) + '"' +
      (sel === m ? ' selected' : '') + '>' + escapeHtml(m) + '</option>';
  });
  return html;
}

function syncActiveDomainSelect(card, domain, mirrors) {
  const select = card.querySelector('.tr-active-domain');
  if (!select) return;
  const prev = select.value;
  const mirrorSet = mirrors || [];
  let next = prev;
  if (prev && prev !== '' && prev !== domain && mirrorSet.indexOf(prev) === -1) next = '';
  select.innerHTML = activeDomainOptionsHtml(domain, mirrorSet, next);
  select.value = next;
}

function trackerConfigFromApi(tracker) {
  return {
    enabled: !!tracker.enabled,
    domain: tracker.domain || '',
    hour_limit: tracker.hour_limit || 0,
    proxy_type: tracker.proxy_type || 0,
    login_url: tracker.login_url || '',
    download_url: tracker.download_url || '',
    credentials: tracker.credentials || '',
    cookies: tracker.cookies || '',
    publicity: tracker.publicity || 'public',
    mirrors: (tracker.mirrors || []).slice(),
    active_domain: tracker.active_domain || '',
  };
}

function gatherTrackerConfig(item, mirrorValues) {
  return {
    enabled: item.querySelector('.tr-enabled').checked,
    domain: item.querySelector('.tr-domain').value,
    hour_limit: parseInt(item.querySelector('.tr-hour-limit').value, 10) || 0,
    proxy_type: parseInt(item.querySelector('.tr-proxy-type').value, 10),
    login_url: item.querySelector('.tr-login-url').value,
    download_url: item.querySelector('.tr-download-url').value,
    credentials: item.querySelector('.input-password-wrap .input').value,
    cookies: item.querySelector('.tr-cookies').value,
    publicity: item.querySelector('.tr-publicity').value,
    mirrors: (mirrorValues || []).slice(),
    active_domain: item.querySelector('.tr-active-domain').value,
  };
}

function isTrackerDirty(item, mirrorValues) {
  if (!item._trackerSaved) return false;
  const cur = gatherTrackerConfig(item, mirrorValues);
  const saved = item._trackerSaved;
  return JSON.stringify(cur) !== JSON.stringify(saved);
}

function trackerItemHtml(tracker) {
  const pub = tracker.publicity || 'public';
  const key = tracker.key;
  const mirrors = tracker.mirrors || [];
  const isOpen = openTrackerKey === key;
  const disabledCls = tracker.enabled ? '' : ' tracker-item-disabled';
  const domain = trackerDisplayDomain(tracker);
  return (
    '<div class="tracker-item' + disabledCls + (isOpen ? ' is-open' : '') + '" data-key="' + escapeHtml(key) + '">' +
    '<div class="tracker-row" role="button" tabindex="0" aria-expanded="' + (isOpen ? 'true' : 'false') + '">' +
    '<label class="form-switch tracker-row-toggle" title="' + escapeHtml(t('trackers.enabled')) + '">' +
    '<input class="tr-enabled" type="checkbox"' + (tracker.enabled ? ' checked' : '') + '>' +
    '<span class="sr-only">' + escapeHtml(t('trackers.enabled')) + '</span></label>' +
    '<button type="button" class="btn btn-ghost btn-icon-only btn-test-tracker" data-key="' + escapeHtml(key) + '"' +
    ' title="' + escapeHtml(t('trackers.test')) + '" aria-label="' + escapeHtml(t('trackers.test')) + '">' +
    '<span data-icon="activity"></span></button>' +
    '<div class="tracker-row-main">' +
    '<span class="tracker-row-name">' + escapeHtml(tracker.name || key) + '</span>' +
    (domain ? '<span class="tracker-row-domain">' + escapeHtml(domain) + '</span>' : '') +
    '</div>' +
    '<span class="tracker-chevron" data-icon="chevron"></span>' +
    '</div>' +
    '<div class="tracker-panel">' +
    '<div class="tracker-panel-inner">' +
    '<div class="tracker-form form-row">' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('trackers.domain')) + '</label>' +
    '<input type="text" class="input tr-domain" autocomplete="off" name="' + escapeHtml(randomFieldName('tr-domain')) +
    '" value="' + escapeHtml(tracker.domain) + '"></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('trackers.hourLimit')) + '</label>' +
    '<input type="number" class="input tr-hour-limit" min="1" value="' + tracker.hour_limit + '"></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('trackers.proxy')) + '</label>' +
    '<select class="select tr-proxy-type">' +
    '<option value="0"' + (tracker.proxy_type === 0 ? ' selected' : '') + '>' + escapeHtml(t('trackers.proxyDirect')) + '</option>' +
    '<option value="1"' + (tracker.proxy_type === 1 ? ' selected' : '') + '>' + escapeHtml(t('trackers.proxyFlare')) + '</option>' +
    '</select></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('trackers.publicity')) + '</label>' +
    '<select class="select tr-publicity">' +
    '<option value="public"' + (pub === 'public' ? ' selected' : '') + '>' + escapeHtml(t('trackers.public')) + '</option>' +
    '<option value="private"' + (pub === 'private' ? ' selected' : '') + '>' + escapeHtml(t('trackers.private')) + '</option>' +
    '</select></div>' +
    '<div class="form-group form-group-full">' +
    '<label class="form-label">' + escapeHtml(t('trackers.mirrors')) + '</label>' +
    '<div class="chip-input tr-mirrors">' +
    '<div class="chip-input-chips"></div>' +
    '<input type="text" class="chip-input-field" autocomplete="off" name="' + escapeHtml(randomFieldName('tr-mirror')) +
    '" placeholder="' + escapeHtml(t('trackers.mirrorsPlaceholder')) + '">' +
    '</div></div>' +
    '<div class="form-group form-group-full">' +
    '<label class="form-label" for="tr-active-domain-' + escapeHtml(key) + '">' +
    escapeHtml(t('trackers.activeDomain')) + '</label>' +
    '<select class="select tr-active-domain" id="tr-active-domain-' + escapeHtml(key) + '">' +
    activeDomainOptionsHtml(tracker.domain, mirrors, tracker.active_domain || '') +
    '</select>' +
    '<p class="form-hint">' + escapeHtml(t('trackers.activeDomainHint')) + '</p>' +
    '</div>' +
    '<div class="form-group form-group-full"><label class="form-label">' + escapeHtml(t('trackers.loginUrl')) + '</label>' +
    '<input type="text" class="input tr-login-url" autocomplete="off" name="' + escapeHtml(randomFieldName('tr-login')) +
    '" value="' + escapeHtml(tracker.login_url) + '"></div>' +
    '<div class="form-group form-group-full"><label class="form-label">' + escapeHtml(t('trackers.downloadUrl')) + '</label>' +
    '<input type="text" class="input tr-download-url" autocomplete="off" name="' + escapeHtml(randomFieldName('tr-dl')) +
    '" value="' + escapeHtml(tracker.download_url) + '"></div>' +
    '<div class="form-group form-group-full"><label class="form-label">' + escapeHtml(t('trackers.credentials')) + '</label>' +
    '<div class="input-password-wrap">' +
    '<input' + secureInputAttrs('tr-cred') + ' value="' + escapeHtml(tracker.credentials) + '">' +
    '<button type="button" class="input-password-toggle" aria-label="' + escapeHtml(t('trackers.showCredentials')) + '">' +
    '<span data-icon="eye"></span></button></div></div>' +
    '<div class="form-group form-group-full"><label class="form-label">' + escapeHtml(t('trackers.cookies')) + '</label>' +
    '<input type="text" class="input tr-cookies" autocomplete="off" name="' + escapeHtml(randomFieldName('tr-cookie')) +
    '" value="' + escapeHtml(tracker.cookies) + '"></div>' +
    '</div>' +
    '<div class="form-submit-row tracker-save-row">' +
    '<button type="button" class="btn btn-primary btn-save-tracker">' + escapeHtml(t('common.save')) + '</button>' +
    '</div>' +
    '</div></div></div>'
  );
}

function initPasswordToggles(root) {
  (root || document).querySelectorAll('.input-password-wrap').forEach(function (wrap) {
    if (wrap.dataset.passwordInit) return;
    wrap.dataset.passwordInit = '1';
    const input = wrap.querySelector('.input');
    const btn = wrap.querySelector('.input-password-toggle');
    if (!input || !btn) return;
    btn.addEventListener('click', function () {
      const revealed = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      if (input.classList.contains('input-secure')) {
        input.classList.toggle('input-secure-revealed', !revealed);
      } else {
        input.type = revealed ? 'password' : 'text';
      }
      const nowVisible = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      const iconEl = btn.querySelector('[data-icon]');
      if (iconEl) {
        iconEl.setAttribute('data-icon', nowVisible ? 'eyeOff' : 'eye');
        iconEl.innerHTML = ICONS[nowVisible ? 'eyeOff' : 'eye'];
      }
      btn.setAttribute('aria-label', t(nowVisible ? 'trackers.hideCredentials' : 'trackers.showCredentials'));
    });
  });
}

function setTrackerItemEnabled(item, enabled) {
  item.classList.toggle('tracker-item-disabled', !enabled);
}

function toggleTrackerItem(item, key) {
  const wasOpen = item.classList.contains('is-open');
  item.closest('.tracker-list').querySelectorAll('.tracker-item.is-open').forEach(function (other) {
    if (other !== item) {
      other.classList.remove('is-open');
      const row = other.querySelector('.tracker-row');
      if (row) row.setAttribute('aria-expanded', 'false');
    }
  });
  if (!wasOpen) {
    item.classList.add('is-open');
    openTrackerKey = key;
    const row = item.querySelector('.tracker-row');
    if (row) row.setAttribute('aria-expanded', 'true');
    initCustomSelects(item);
    initPasswordToggles(item);
    hydrateIcons(item);
  } else {
    item.classList.remove('is-open');
    openTrackerKey = null;
    const row = item.querySelector('.tracker-row');
    if (row) row.setAttribute('aria-expanded', 'false');
  }
}

function trackerTestTimeoutMs(item) {
  let proxyType = 0;
  if (item) {
    const sel = item.querySelector('.tr-proxy-type');
    if (sel) proxyType = parseInt(sel.value, 10) || 0;
    else if (item._trackerSaved) proxyType = item._trackerSaved.proxy_type || 0;
  }
  if (proxyType !== 1) return 30000;
  const flareMs = integrationsCache && integrationsCache.flaresolverr &&
    integrationsCache.flaresolverr.timeout_ms;
  return flareMs > 0 ? flareMs : 30000;
}

async function testTracker(key, btn, item, mirrorValues) {
  if (!btn || btn.disabled) return;
  const defaultLabel = t('trackers.test');
  btn.disabled = true;
  btn.classList.add('is-spinning');
  const prevHtml = btn.innerHTML;
  btn.innerHTML = '<span data-icon="refresh"></span>';
  btn.setAttribute('title', defaultLabel);
  btn.setAttribute('aria-label', defaultLabel);
  hydrateIcons(btn);
  let ok = false;
  let errMsg = '';
  const controller = new AbortController();
  const timeoutMs = trackerTestTimeoutMs(item);
  const timeoutId = setTimeout(function () { controller.abort(); }, timeoutMs);
  try {
    const opts = { method: 'POST', signal: controller.signal };
    if (item && item.classList.contains('is-open') && isTrackerDirty(item, mirrorValues)) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body = JSON.stringify({ config: gatherTrackerConfig(item, mirrorValues) });
    }
    const res = await fetch('/api/trackers/' + encodeURIComponent(key) + '/test', opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* ignore */ }
    }
    if (res.status === 404) {
      errMsg = (data && data.error) ? data.error : 'HTTP 404';
    } else if (!res.ok) {
      errMsg = (data && data.error) ? data.error : 'HTTP ' + res.status;
    } else if (data && data.ok) {
      ok = true;
    } else {
      errMsg = (data && data.error) ? data.error : t('trackers.testFail');
    }
  } catch (err) {
    errMsg = err.name === 'AbortError' ? t('trackers.testFail') : err.message;
  }
  clearTimeout(timeoutId);
  btn.classList.remove('is-spinning');
  btn.disabled = false;
  if (ok) {
    const okLabel = t('trackers.testOk');
    btn.classList.add('btn-test-ok');
    btn.classList.remove('btn-test-fail');
    btn.innerHTML = '<span data-icon="check"></span>';
    btn.setAttribute('title', okLabel);
    btn.setAttribute('aria-label', okLabel);
    showToast(okLabel, 'success');
  } else {
    const failLabel = errMsg || t('trackers.testFail');
    btn.classList.add('btn-test-fail');
    btn.classList.remove('btn-test-ok');
    btn.innerHTML = '<span data-icon="xCircle"></span>';
    btn.setAttribute('title', failLabel);
    btn.setAttribute('aria-label', failLabel);
    showToast(failLabel, 'error');
  }
  hydrateIcons(btn);
  setTimeout(function () {
    btn.classList.remove('btn-test-ok', 'btn-test-fail');
    btn.innerHTML = prevHtml;
    btn.setAttribute('title', defaultLabel);
    btn.setAttribute('aria-label', defaultLabel);
    hydrateIcons(btn);
  }, 4000);
}

async function loadTrackers() {
  const el = document.getElementById('view-trackers');
  el.innerHTML = '<div class="loading">' + escapeHtml(t('common.loading')) + '</div>';
  try {
    const fetches = [fetchJSON('/api/trackers')];
    if (!integrationsCache) fetches.push(fetchJSON('/api/integrations'));
    const results = await Promise.all(fetches);
    if (!integrationsCache && results[1]) integrationsCache = results[1];
    renderTrackers(el, results[0] || []);
  } catch (err) {
    el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    showToast(err.message, 'error');
  }
}

function renderTrackers(el, trackers) {
  let list = '';
  if (trackers.length === 0) {
    list = '<p class="text-muted">' + escapeHtml(t('trackers.empty')) + '</p>';
  } else {
    list = '<div class="tracker-list">';
    trackers.forEach(function (tr) { list += trackerItemHtml(tr); });
    list += '</div>';
  }

  el.innerHTML =
    '<div class="page-header">' +
    '<h1 class="page-title">' + escapeHtml(t('trackers.title')) + '</h1></div>' + list;

  const mirrorInputs = {};
  el.querySelectorAll('.tracker-item').forEach(function (item) {
    const key = item.getAttribute('data-key');
    const chipEl = item.querySelector('.tr-mirrors');
    const tracker = trackers.find(function (t) { return t.key === key; });
    if (tracker) item._trackerSaved = trackerConfigFromApi(tracker);
    mirrorInputs[key] = initChipInput(
      chipEl,
      null,
      (tracker && tracker.mirrors) || [],
      null,
      function (mirrors) {
        syncActiveDomainSelect(item, item.querySelector('.tr-domain').value, mirrors);
      }
    );
    const domainInput = item.querySelector('.tr-domain');
    if (domainInput) {
      domainInput.addEventListener('input', function () {
        syncActiveDomainSelect(item, domainInput.value, mirrorInputs[key].getValues());
      });
    }

    const row = item.querySelector('.tracker-row');
    if (row) {
      row.addEventListener('click', function (e) {
        if (e.target.closest('.tracker-row-toggle') || e.target.closest('.btn-test-tracker')) return;
        toggleTrackerItem(item, key);
      });
      row.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' || e.key === ' ') {
          e.preventDefault();
          toggleTrackerItem(item, key);
        }
      });
    }
  });

  el.querySelectorAll('.btn-save-tracker').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const item = btn.closest('.tracker-item');
      const key = item.getAttribute('data-key');
      const body = {
        enabled: item.querySelector('.tr-enabled').checked,
        domain: item.querySelector('.tr-domain').value,
        hour_limit: parseInt(item.querySelector('.tr-hour-limit').value, 10) || 0,
        proxy_type: parseInt(item.querySelector('.tr-proxy-type').value, 10),
        login_url: item.querySelector('.tr-login-url').value,
        download_url: item.querySelector('.tr-download-url').value,
        credentials: item.querySelector('.input-password-wrap .input').value,
        cookies: item.querySelector('.tr-cookies').value,
        publicity: item.querySelector('.tr-publicity').value,
        mirrors: mirrorInputs[key].getValues(),
        active_domain: item.querySelector('.tr-active-domain').value,
      };
      btn.disabled = true;
      try {
        await fetchJSON('/api/trackers/' + encodeURIComponent(key), {
          method: 'PUT',
          body: JSON.stringify(body),
        });
        showToast(t('trackers.saved', { key: key }), 'success');
        item._trackerSaved = body;
        const domainEl = item.querySelector('.tracker-row-domain');
        if (domainEl) {
          const active = body.active_domain || body.domain || '';
          domainEl.textContent = active;
          domainEl.style.display = active ? '' : 'none';
        }
      } catch (err) {
        showToast(err.message, 'error');
      }
      btn.disabled = false;
    });
  });

  el.querySelectorAll('.tr-enabled').forEach(function (checkbox) {
    checkbox.addEventListener('change', async function (e) {
      e.stopPropagation();
      const item = checkbox.closest('.tracker-item');
      const key = item.getAttribute('data-key');
      const enabled = checkbox.checked;
      const prev = !enabled;
      setTrackerItemEnabled(item, enabled);
      checkbox.disabled = true;
      try {
        await fetchJSON('/api/trackers/' + encodeURIComponent(key), {
          method: 'PUT',
          body: JSON.stringify({ enabled: enabled }),
        });
        showToast(t('trackers.enabledSaved', {
          key: key,
          state: enabled ? t('trackers.enabledOn') : t('trackers.enabledOff'),
        }), 'success');
        if (item._trackerSaved) item._trackerSaved.enabled = enabled;
      } catch (err) {
        checkbox.checked = prev;
        setTrackerItemEnabled(item, prev);
        showToast(err.message, 'error');
      }
      checkbox.disabled = false;
    });
  });

  el.querySelectorAll('.btn-test-tracker').forEach(function (btn) {
    btn.addEventListener('click', function (e) {
      e.stopPropagation();
      const item = btn.closest('.tracker-item');
      const key = btn.getAttribute('data-key');
      testTracker(key, btn, item, mirrorInputs[key] ? mirrorInputs[key].getValues() : []);
    });
  });

  el.querySelectorAll('.tracker-row-toggle').forEach(function (label) {
    label.addEventListener('click', function (e) { e.stopPropagation(); });
  });

  if (openTrackerKey) {
    const openItem = el.querySelector('.tracker-item[data-key="' + CSS.escape(openTrackerKey) + '"]');
    if (openItem) {
      initCustomSelects(openItem);
      initPasswordToggles(openItem);
    }
  }

  initPasswordToggles(el);
  hydrateIcons(el);
  initCustomSelects(el);
}

async function loadIntegrations() {
  const el = document.getElementById('view-integrations');
  el.innerHTML = '<div class="loading">' + escapeHtml(t('common.loading')) + '</div>';
  try {
    integrationsCache = await fetchJSON('/api/integrations');
    renderIntegrations(el, integrationsCache);
  } catch (err) {
    el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    showToast(err.message, 'error');
  }
}

function renderIntegrations(el, data) {
  const q = data.qbit || {};
  const f = data.flaresolverr || {};
  const p = data.proxy || {};
  const proxyParts = parseDownloadProxy(p.download_proxy || '');

  el.innerHTML =
    '<div class="page-header">' +
    '<h1 class="page-title">' + escapeHtml(t('integrations.title')) + '</h1></div>' +
    '<div class="grid-2">' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('integrations.qbit')) + '</h2></div>' +
    '<div class="card-body" autocomplete="off">' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('integrations.host')) + '</label>' +
    '<input type="text" class="input" id="qbit-host" autocomplete="off" name="qb-host-field" value="' + escapeHtml(q.host) + '"></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('integrations.username')) + '</label>' +
    '<input type="text" class="input" id="qbit-username" autocomplete="off" name="qb-user-field" value="' + escapeHtml(q.username) + '"></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('integrations.password')) + '</label>' +
    '<div class="input-password-wrap">' +
    '<input id="qbit-password"' + secureInputAttrs('qb-secret') + ' value="' + escapeHtml(q.password) + '">' +
    '<button type="button" class="input-password-toggle" aria-label="' + escapeHtml(t('trackers.showCredentials')) + '">' +
    '<span data-icon="eye"></span></button></div></div>' +
    '<div class="btn-group">' +
    '<button type="button" class="btn btn-primary btn-save-int" data-section="qbit">' + escapeHtml(t('common.save')) + '</button>' +
    '<button type="button" class="btn btn-ghost btn-test-int" data-kind="qbit">' + escapeHtml(t('integrations.test')) + '</button>' +
    '<span class="test-result" data-kind="qbit"></span></div>' +
    '</div></div>' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('integrations.flare')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('integrations.url')) + '</label>' +
    '<input type="text" class="input" id="flare-url" value="' + escapeHtml(f.url) + '"></div>' +
    '<div class="form-group"><label class="form-label">' + escapeHtml(t('integrations.timeout')) + '</label>' +
    '<input type="number" class="input" id="flare-timeout" min="1" value="' + (f.timeout_ms || 0) + '"></div>' +
    '<div class="btn-group">' +
    '<button type="button" class="btn btn-primary btn-save-int" data-section="flaresolverr">' + escapeHtml(t('common.save')) + '</button>' +
    '<button type="button" class="btn btn-ghost btn-test-int" data-kind="flaresolverr">' + escapeHtml(t('integrations.test')) + '</button>' +
    '<span class="test-result" data-kind="flaresolverr"></span></div>' +
    '</div></div>' +

    '<div class="card">' +
    '<div class="card-header"><h2 class="card-title">' + escapeHtml(t('integrations.proxy')) + '</h2></div>' +
    '<div class="card-body">' +
    '<div class="proxy-type-row">' +
    '<div class="form-group">' +
    '<label class="form-label" for="proxy-type">' + escapeHtml(t('integrations.proxyType')) + '</label>' +
    '<select class="select" id="proxy-type">' +
    '<option value="http"' + (proxyParts.type === 'http' ? ' selected' : '') + '>' +
    escapeHtml(t('integrations.proxyTypeHttp')) + '</option>' +
    '<option value="https"' + (proxyParts.type === 'https' ? ' selected' : '') + '>' +
    escapeHtml(t('integrations.proxyTypeHttps')) + '</option>' +
    '<option value="socks5"' + (proxyParts.type === 'socks5' ? ' selected' : '') + '>' +
    escapeHtml(t('integrations.proxyTypeSocks5')) + '</option>' +
    '</select></div>' +
    '<div class="form-group">' +
    '<label class="form-label" for="proxy-download">' + escapeHtml(t('integrations.proxyAddress')) + '</label>' +
    '<input type="text" class="input" id="proxy-download" value="' +
    escapeHtml(proxyParts.address) + '" placeholder="' + escapeHtml(t('integrations.downloadProxyPlaceholder')) + '">' +
    '</div></div>' +
    '<div class="btn-group">' +
    '<button type="button" class="btn btn-primary btn-save-int" data-section="proxy">' + escapeHtml(t('common.save')) + '</button>' +
    '<button type="button" class="btn btn-ghost btn-test-int" data-kind="proxy">' + escapeHtml(t('integrations.test')) + '</button>' +
    '<span class="test-result" data-kind="proxy"></span></div>' +
    '</div></div>' +

    '</div>';

  el.querySelectorAll('.btn-save-int').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const section = btn.getAttribute('data-section');
      const doc = buildIntegrationsDoc();
      if (section === 'qbit') {
        doc.qbit = {
          host: document.getElementById('qbit-host').value,
          username: document.getElementById('qbit-username').value,
          password: document.getElementById('qbit-password').value,
        };
      } else if (section === 'flaresolverr') {
        doc.flaresolverr = {
          url: document.getElementById('flare-url').value,
          timeout_ms: parseInt(document.getElementById('flare-timeout').value, 10) || 0,
        };
      } else if (section === 'proxy') {
        doc.proxy = { download_proxy: getProxyDownloadValue() };
      }
      btn.disabled = true;
      try {
        await fetchJSON('/api/integrations', { method: 'PUT', body: JSON.stringify(doc) });
        integrationsCache = doc;
        showToast(t('integrations.saved'), 'success');
      } catch (err) {
        showToast(err.message, 'error');
      }
      btn.disabled = false;
    });
  });

  el.querySelectorAll('.btn-test-int').forEach(function (btn) {
    btn.addEventListener('click', async function () {
      const kind = btn.getAttribute('data-kind');
      const resultEl = el.querySelector('.test-result[data-kind="' + kind + '"]');
      resultEl.innerHTML = '<span class="text-muted">' + escapeHtml(t('integrations.testing')) + '</span>';
      btn.disabled = true;
      try {
        const config = getIntegrationTestConfig(kind);
        const res = await fetchJSON('/api/integrations/test', {
          method: 'POST',
          body: JSON.stringify({ kind: kind, config: config }),
        });
        if (res.ok) {
          resultEl.innerHTML = '<span class="text-success">' + icon('check') + ' ' + escapeHtml(t('integrations.ok')) + '</span>';
          hydrateIcons(resultEl);
        } else {
          resultEl.innerHTML = '<span class="text-danger">' + escapeHtml(res.error || t('integrations.error')) + '</span>';
        }
      } catch (err) {
        resultEl.innerHTML = '<span class="text-danger">' + escapeHtml(err.message) + '</span>';
      }
      btn.disabled = false;
    });
  });

  initCustomSelects(el);
  initPasswordToggles(el);
  hydrateIcons(el);
}

function stopLogsPolling() {
  if (logsPollTimer) {
    clearInterval(logsPollTimer);
    logsPollTimer = null;
  }
  if (logsVisibilityBound) {
    document.removeEventListener('visibilitychange', onLogsVisibility);
    logsVisibilityBound = false;
  }
}

function onLogsVisibility() {
  if (currentView !== 'logs') return;
  if (document.visibilityState === 'visible') {
    refreshLogs(false);
    ensureLogsPolling();
  } else if (logsPollTimer) {
    clearInterval(logsPollTimer);
    logsPollTimer = null;
  }
}

function ensureLogsPolling() {
  if (currentView !== 'logs' || document.visibilityState !== 'visible') return;
  if (!logsPollTimer) logsPollTimer = setInterval(function () { refreshLogs(false); }, 10000);
  if (!logsVisibilityBound) {
    document.addEventListener('visibilitychange', onLogsVisibility);
    logsVisibilityBound = true;
  }
}

function updateLogsRefreshedAt() {
  const el = document.getElementById('logs-last-updated');
  if (!el) return;
  const time = new Date().toLocaleTimeString(localeTag(), {
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  });
  el.textContent = t('logs.lastUpdated', { time: time });
}

async function loadLogs() {
  const el = document.getElementById('view-logs');
  const hadContent = !!el.querySelector('.page-header');
  if (!hadContent) {
    el.innerHTML = '<div class="loading">' + escapeHtml(t('common.loading')) + '</div>';
  }
  const fetchGen = ++logsFetchGen;
  try {
    const data = await fetchJSON('/api/logs?limit=' + logsState.limit);
    if (fetchGen !== logsFetchGen) return;
    if (hadContent) {
      renderLogsFeed(el, data);
      updateLogsRefreshedAt();
    } else {
      renderLogs(el, data);
      updateLogsRefreshedAt();
    }
    ensureLogsPolling();
  } catch (err) {
    if (!hadContent) {
      el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    }
    showToast(err.message, 'error');
  }
}

async function refreshLogs(manual) {
  if (currentView !== 'logs' || document.visibilityState !== 'visible') return;
  const el = document.getElementById('view-logs');
  if (!el || !el.querySelector('.logs-feed')) return;

  if (manual) {
    const btn = document.getElementById('logs-refresh-btn');
    if (btn) btn.classList.add('is-spinning');
  }

  const fetchGen = ++logsFetchGen;
  try {
    const data = await fetchJSON('/api/logs?limit=' + logsState.limit);
    if (fetchGen !== logsFetchGen) return;
    renderLogsFeed(el, data);
    updateLogsRefreshedAt();
  } catch (_) { /* ponytail: silent refresh is best-effort */ }

  if (manual) {
    const btn = document.getElementById('logs-refresh-btn');
    if (btn) btn.classList.remove('is-spinning');
  }
}

function renderLogLine(item) {
  const level = (item.level || 'INFO').toUpperCase();
  const cls = level === 'ERROR' ? 'log-error' : level === 'WARN' ? 'log-warn' : 'log-info';
  return (
    '<div class="log-line ' + cls + '">' +
    '<span class="log-time">' + escapeHtml(formatLogTime(item.time)) + '</span> ' +
    '<span class="log-level">[' + escapeHtml(level) + ']</span> ' +
    '<span class="log-msg">' + escapeHtml(item.message || '') + '</span></div>'
  );
}

function renderLogsFeed(el, data) {
  const feed = el.querySelector('.logs-feed');
  if (!feed) return;
  const items = (data && data.items) || [];
  if (!items.length) {
    feed.innerHTML = '<div class="log-empty">' + escapeHtml(t('logs.empty')) + '</div>';
    return;
  }
  let html = '';
  items.forEach(function (item) { html += renderLogLine(item); });
  feed.innerHTML = html;
  feed.scrollTop = feed.scrollHeight;
}

function renderLogs(el, data) {
  const limitOpts = [100, 200, 500, 1000];
  let limitHtml = '';
  limitOpts.forEach(function (n) {
    limitHtml += '<option value="' + n + '"' + (logsState.limit === n ? ' selected' : '') + '>' + n + '</option>';
  });

  el.innerHTML =
    '<div class="page-header logs-page-header">' +
    '<div class="page-header-row">' +
    '<h1 class="page-title">' + escapeHtml(t('logs.title')) + '</h1>' +
    '<div class="page-header-actions">' +
    '<span class="text-muted logs-last-updated" id="logs-last-updated"></span>' +
    '<button type="button" class="btn btn-sm btn-ghost btn-icon btn-refresh" id="logs-refresh-btn"' +
    ' aria-label="' + escapeHtml(t('logs.refresh')) + '">' +
    '<span data-icon="refresh"></span></button>' +
    '</div></div></div>' +
    '<div class="card logs-card">' +
    '<div class="card-header logs-card-header">' +
    '<div class="form-group logs-limit-group">' +
    '<label class="form-label" for="logs-limit">' + escapeHtml(t('logs.limit')) + '</label>' +
    '<select class="select" id="logs-limit">' + limitHtml + '</select>' +
    '</div></div>' +
    '<div class="logs-feed card-body-flush"></div></div>';

  renderLogsFeed(el, data);
  hydrateIcons(el);
  initCustomSelects(el);

  const refreshBtn = el.querySelector('#logs-refresh-btn');
  if (refreshBtn) {
    refreshBtn.addEventListener('click', function () { refreshLogs(true); });
  }

  const limitSelect = el.querySelector('#logs-limit');
  if (limitSelect) {
    limitSelect.addEventListener('change', function () {
      logsState.limit = parseInt(limitSelect.value, 10) || 200;
      loadLogs();
    });
  }
}

function getIntegrationTestConfig(kind) {
  if (kind === 'qbit') {
    return {
      host: document.getElementById('qbit-host').value,
      username: document.getElementById('qbit-username').value,
      password: document.getElementById('qbit-password').value,
    };
  }
  if (kind === 'flaresolverr') {
    return {
      url: document.getElementById('flare-url').value,
      timeout_ms: parseInt(document.getElementById('flare-timeout').value, 10) || 0,
    };
  }
  if (kind === 'proxy') {
    return { download_proxy: getProxyDownloadValue() };
  }
  return {};
}

function buildIntegrationsDoc() {
  const base = integrationsCache ? JSON.parse(JSON.stringify(integrationsCache)) : {};
  if (!base.qbit) base.qbit = {};
  if (!base.flaresolverr) base.flaresolverr = {};
  if (!base.proxy) base.proxy = {};
  base.qbit.host = document.getElementById('qbit-host').value;
  base.qbit.username = document.getElementById('qbit-username').value;
  base.qbit.password = document.getElementById('qbit-password').value;
  base.flaresolverr.url = document.getElementById('flare-url').value;
  base.flaresolverr.timeout_ms = parseInt(document.getElementById('flare-timeout').value, 10) || 0;
  base.proxy.download_proxy = getProxyDownloadValue();
  return base;
}

const initialView = parseHash();
historyState.limit = loadStoredPageSize(10);
showView(initialView, false);
