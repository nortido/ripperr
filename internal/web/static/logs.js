import { fetchJSON, showToast } from './api.js';
import { initCustomSelects } from './components.js';
import { triggerDryRun, updateDryRunButtonState } from './dashboard.js';
import { hydrateIcons, icon } from './icons.js';
import { createPoller } from './poller.js';
import { currentView } from './router.js';
import { escapeHtml, formatLogTime, updateRefreshedAt } from './util.js';

export let logsState = { limit: 200 };
export let logsFetchGen = 0;


const logsPoller = createPoller('logs', [
  { run: function () { refreshLogs(false); }, ms: 10000, onResume: true },
]);
export const stopLogsPolling = logsPoller.stop;
export const ensureLogsPolling = logsPoller.start;

export async function loadLogs() {
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
      updateRefreshedAt('logs-last-updated', 'logs.lastUpdated');
    } else {
      renderLogs(el, data);
      updateRefreshedAt('logs-last-updated', 'logs.lastUpdated');
    }
    ensureLogsPolling();
  } catch (err) {
    if (!hadContent) {
      el.innerHTML = '<div class="alert alert-danger">' + escapeHtml(err.message) + '</div>';
    }
    showToast(err.message, 'error');
  }
}

export async function refreshLogs(manual) {
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
    updateRefreshedAt('logs-last-updated', 'logs.lastUpdated');
    updateDryRunButtonState();
  } catch (_) { /* ponytail: silent refresh is best-effort */ }

  if (manual) {
    const btn = document.getElementById('logs-refresh-btn');
    if (btn) btn.classList.remove('is-spinning');
  }
}

export function renderLogLine(item) {
  const level = (item.level || 'INFO').toUpperCase();
  const cls = level === 'ERROR' ? 'log-error' : level === 'WARN' ? 'log-warn' : 'log-info';
  return (
    '<div class="log-line ' + cls + '">' +
    '<span class="log-time">' + escapeHtml(formatLogTime(item.time)) + '</span> ' +
    '<span class="log-level">[' + escapeHtml(level) + ']</span> ' +
    '<span class="log-msg">' + escapeHtml(item.message || '') + '</span></div>'
  );
}

export function renderLogsFeed(el, data) {
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

export function renderLogs(el, data) {
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
    '<button type="button" class="btn btn-sm btn-ghost" id="logs-dryrun-btn" ' +
    'title="' + escapeHtml(t('logs.dryRunHint')) + '">' +
    escapeHtml(t('logs.dryRun')) + '</button>' +
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

  const dryRunBtn = el.querySelector('#logs-dryrun-btn');
  if (dryRunBtn) {
    dryRunBtn.addEventListener('click', function () { triggerDryRun(dryRunBtn); });
  }

  updateDryRunButtonState();
}


