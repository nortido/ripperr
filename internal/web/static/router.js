import { confirmDialog } from './confirm.js';
import { loadDashboard, stopDashboardPolling } from './dashboard.js';
import { hasUnsavedIntegrations, loadIntegrations } from './integrations.js';
import { closeMobileMenu } from './layout.js';
import { loadLogs, stopLogsPolling } from './logs.js';
import { loadSchedule } from './schedule.js';
import { historyState } from './state.js';
import { closeTrackerDialog, hasUnsavedTrackers, loadTrackers } from './trackers.js';

export const VIEW_HASH = {
  dashboard: '#dashboard',
  schedule: '#settings',
  trackers: '#trackers',
  integrations: '#integrations',
  logs: '#logs',
};

export const HASH_VIEW = {
  dashboard: 'dashboard',
  settings: 'schedule',
  trackers: 'trackers',
  integrations: 'integrations',
  logs: 'logs',
};


export let currentView = null;
let navConfirming = false;

export function parseHash() {
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

export function buildHash(view) {
  const base = VIEW_HASH[view] || '#dashboard';
  if (view !== 'dashboard') return base;
  const params = new URLSearchParams();
  if (historyState.ignored) params.set('filter', 'ignored');
  else if (historyState.status) params.set('filter', historyState.status);
  if (historyState.name) params.set('name', historyState.name);
  const qs = params.toString();
  return qs ? base + '?' + qs : base;
}

export function syncDashboardFilterHash() {
  if (currentView !== 'dashboard') return;
  const hash = buildHash('dashboard');
  if (location.hash !== hash) {
    history.replaceState(null, '', hash);
  }
}

export function applyDashboardFilter(filterType) {
  if (filterType === 'ignored') {
    if (historyState.ignored) {
      historyState.ignored = false;
      historyState.status = '';
    } else {
      historyState.ignored = true;
      historyState.status = '';
    }
  } else if (filterType && filterType !== 'all') {
    const same = !historyState.ignored && historyState.status === filterType;
    historyState.ignored = false;
    historyState.status = same ? '' : filterType;
  } else {
    historyState.ignored = false;
    historyState.status = '';
  }
  historyState.offset = 0;
  syncDashboardFilterHash();
  loadDashboard();
}

export function setActiveNav(view) {
  document.querySelectorAll('[data-view]').forEach(function (a) {
    a.classList.toggle('active', a.getAttribute('data-view') === view);
  });
  document.querySelectorAll('.view').forEach(function (el) {
    el.classList.add('hidden');
  });
  const target = document.getElementById('view-' + view);
  if (target) target.classList.remove('hidden');
}

function unsavedWarning() {
  if (currentView === 'trackers' && hasUnsavedTrackers()) return t('trackers.unsavedWarn');
  if (currentView === 'integrations' && hasUnsavedIntegrations()) return t('integrations.unsavedWarn');
  return '';
}

export function showView(view, updateHash) {
  if (currentView === 'dashboard' && view !== 'dashboard') stopDashboardPolling();
  if (currentView === 'logs' && view !== 'logs') stopLogsPolling();
  if (updateHash !== false) {
    const hash = buildHash(view);
    if (location.hash !== hash) {
      location.hash = hash;
      return;
    }
  }
  // проверка после смены хеша: клик по меню вызывает showView дважды (клик и hashchange),
  // и в начале функции confirm выскочил бы два раза. С currentView не сравниваем: клик по
  // уже открытым «Трекерам» хеш не меняет, но loadTrackers() так же стирает правки
  // пока висит подтверждение, другие переходы (кнопка "назад") игнорируются
  if (navConfirming) return;
  const warn = unsavedWarning();
  if (warn) {
    navConfirming = true;
    const from = currentView;
    confirmDialog({ text: warn, confirmLabel: t('trackers.discard'), danger: true }).finally(function () {
      // и при отказе промиса: иначе флаг застрянет и навигация умрет
      navConfirming = false;
    }).then(function (ok) {
      // хеш мог уйти за время вопроса: возвращаем тот, что соответствует ответу
      const target = buildHash(ok ? view : from);
      if (location.hash !== target) history.replaceState(null, '', target);
      if (ok) enterView(view);
    });
    return;
  }
  enterView(view);
}

function enterView(view) {
  if (currentView === 'trackers') closeTrackerDialog({ force: true });
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

export function initRouter() {
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

  // закрытие вкладки и перезагрузка идут мимо showView.
  // Safari, и на iOS тоже, смотрит на returnValue, одного preventDefault ему мало
  window.addEventListener('beforeunload', function (e) {
    if (!unsavedWarning()) return;
    e.preventDefault();
    e.returnValue = '';
  });

}

