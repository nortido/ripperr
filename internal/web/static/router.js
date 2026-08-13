import { loadDashboard, stopDashboardPolling } from './dashboard.js';
import { loadIntegrations } from './integrations.js';
import { closeMobileMenu } from './layout.js';
import { loadLogs, stopLogsPolling } from './logs.js';
import { loadSchedule } from './schedule.js';
import { historyState } from './state.js';
import { loadTrackers } from './trackers.js';

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

// nav links and hash changes; called once from main.js
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

}

