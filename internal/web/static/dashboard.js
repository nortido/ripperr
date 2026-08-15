import { fetchJSON, showToast } from './api.js';
import { activeErrorPopover, activeSlotsPopover, bindErrorBadges, bindMobileNameSearch, bindNameFilter, bindNextRunStatCard, bindPageSizeSelect, buildMobileNameSearchSlot, buildNameColumnHeader, buildPageSizeSelect, buildSegmentControl, closePopovers, initSegmentControls, openCustomSelect, openNameFilterWrap, paginationButtons, updateSegmentThumb } from './components.js';
import { bindHistFilterSelect, buildHistFilters, openHistFilterSelect } from './histfilter.js';
import { hydrateIcons, icon } from './icons.js';
import { refreshLogs } from './logs.js';
import { createPoller } from './poller.js';
import { applyDashboardFilter, currentView } from './router.js';
import { historyState } from './state.js';
import { alertIncidentKey, dismissAlertKey, formatDateTime, formatRunSlot, formatTableTime, formatThenSlot, html, linkCell, pageSizeQueryLimit, pruneDismissedAlerts, statusIcon, trackerBadgeHtml, updateRefreshedAt, visibleAlerts } from './util.js';

export const STOP_PENDING_MS = 90000;
// a triggered run needs a moment to be picked up and to fetch the torrent list
// before the server reports it as running; until then the UI trusts the click
export const RUN_PENDING_MS = 12000;


export let stopPending = false;
export let stopPendingTimer = null;

export let runPending = false;
export let runPendingTimer = null;

export let dashboardStatusSlots = [];
export let dashboardRunning = false;
export let dashboardServerRunning = false;
export let dashboardDryRun = false;

export let dashboardFetchGen = 0;
// imported bindings are read-only, so other modules bump the generation through this
export function nextDashboardFetchGen() {
  return ++dashboardFetchGen;
}
export let planForceLocal = Object.create(null);
export let ignoreHashesCache = null;

export let dashboardAlerts = [];

export function setStopPending(active) {
  stopPending = !!active;
  if (stopPendingTimer) {
    clearTimeout(stopPendingTimer);
    stopPendingTimer = null;
  }
  if (stopPending) setRunPending(false);
  if (!stopPending) {
    if (currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
    return;
  }
  stopPendingTimer = setTimeout(function () {
    stopPending = false;
    stopPendingTimer = null;
    if (currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
  }, STOP_PENDING_MS);
  if (currentView === 'dashboard') updateRunStatusUI(effectiveRunning(dashboardServerRunning));
}

export function setRunPending(active) {
  runPending = !!active;
  if (runPendingTimer) {
    clearTimeout(runPendingTimer);
    runPendingTimer = null;
  }
  if (!runPending) return;
  runPendingTimer = setTimeout(function () {
    runPending = false;
    runPendingTimer = null;
    if (currentView === 'dashboard') applyRunStatus(effectiveRunning(dashboardServerRunning));
  }, RUN_PENDING_MS);
}

export function effectiveRunning(serverRunning) {
  if (stopPending && serverRunning) return false;
  if (runPending && !stopPending) return true;
  return !!serverRunning;
}

export function applyRunStatus(running) {
  const wasRunning = dashboardRunning;
  updateRunStatusUI(running);
  if (wasRunning && !running) refreshDashboard(false);
}


export function dashboardHasActiveInteraction() {
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

const dashboardPoller = createPoller('dashboard', [
  { run: pollDashboardStatus, ms: 5000, onResume: true },
  { run: function () { refreshDashboard(false); }, ms: 30000 },
]);
export const stopDashboardPolling = dashboardPoller.stop;
export const ensureDashboardPolling = dashboardPoller.start;

export function isUrlIgnored(urlHash) {
  return !!(urlHash && ignoreHashesCache && ignoreHashesCache.has(urlHash));
}

export async function syncIgnoreState() {
  const items = await fetchJSON('/api/ignore');
  ignoreHashesCache = new Set((items || []).map(function (i) { return i.url_hash; }));
  return items || [];
}

export function updateIgnoredStatCard(count) {
  const cards = document.querySelectorAll('#view-dashboard .stat-grid .stat-card');
  if (cards.length < 4) return;
  const valueEl = cards[3].querySelector('.stat-value');
  if (valueEl) valueEl.textContent = String(count);
}

export function ignoreButtonIconsHtml(ignored) {
  const idleIcon = ignored ? 'eyeOff' : 'eye';
  const hoverIcon = ignored ? 'eye' : 'eyeOff';
  return html`<span class="icon-idle" data-icon="${idleIcon}"></span><span class="icon-hover" data-icon="${hoverIcon}"></span>`;
}

export function updateHistoryIgnoreButtons(root) {
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

export async function refreshIgnoreState() {
  try {
    const items = await syncIgnoreState();
    const dash = document.getElementById('view-dashboard');
    if (dash && dash.querySelector('.page-header')) {
      updateHistoryIgnoreButtons(dash);
      updateIgnoredStatCard(items.length);
    }
  } catch (_) { /* ponytail: best-effort */ }
}

export function planItemForced(item) {
  const hash = item.url_hash || '';
  if (hash && Object.prototype.hasOwnProperty.call(planForceLocal, hash)) {
    return !!planForceLocal[hash];
  }
  return !!item.force_check;
}

export function syncPlanForceLocal(items) {
  planForceLocal = Object.create(null);
  (items || []).forEach(function (item) {
    const hash = item.url_hash || '';
    if (hash) planForceLocal[hash] = !!item.force_check;
  });
}

export function redownloadButtonHtml(hash) {
  const btnTitle = t('dash.redownload');
  return html`<button type="button" class="btn btn-icon-only btn-redownload btn-action-redownload" title="${btnTitle}" aria-label="${btnTitle}" data-hash="${hash}"><span data-icon="rotateCw"></span></button>`;
}

export function planForceButtonHtml(hash, forced) {
  const btnTitle = forced ? t('dash.forceCancel') : t('dash.forceCheck');
  const btnCls = 'btn btn-icon-only btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  const btnIcon = forced ? 'zapOff' : 'zap';
  return html`<button type="button" class="${btnCls}" title="${btnTitle}" aria-label="${btnTitle}" aria-pressed="${forced ? 'true' : 'false'}" data-hash="${hash}" data-force="${forced ? '1' : '0'}"><span data-icon="${btnIcon}"></span></button>`;
}

export function updatePlanForceButton(btn, hash, forced) {
  btn.disabled = false;
  btn.className = 'btn btn-icon-only btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  const btnTitle = forced ? t('dash.forceCancel') : t('dash.forceCheck');
  btn.setAttribute('title', btnTitle);
  btn.setAttribute('aria-label', btnTitle);
  btn.setAttribute('aria-pressed', forced ? 'true' : 'false');
  btn.setAttribute('data-hash', hash);
  btn.setAttribute('data-force', forced ? '1' : '0');
  btn.innerHTML = html`<span data-icon="${forced ? 'zapOff' : 'zap'}"></span>`;
  hydrateIcons(btn);
}

export function updateReleaseRow(hash, force) {
  if (hash) planForceLocal[hash] = !!force;
  const row = document.querySelector('#view-dashboard tr[data-release-hash="' + CSS.escape(hash) + '"]');
  if (!row) return;
  const forced = !!force;
  row.classList.toggle('plan-row-force', forced);
  const recheckCell = row.querySelector('.plan-recheck-cell');
  if (recheckCell) {
    const dateShort = recheckCell.getAttribute('data-recheck-short') || '';
    const dateFull = recheckCell.getAttribute('data-recheck-full') || '';
    recheckCell.innerHTML = html`<span class="plan-recheck-text" title="${dateFull}">${dateShort}</span>`;
  }
  const btn = row.querySelector('.btn-plan-force');
  if (btn) updatePlanForceButton(btn, hash, forced);
}

export function updateRunStatusUI(running) {
  dashboardRunning = !!running;
  const statusEl = document.getElementById('dash-run-status');
  const runBtn = document.getElementById('dash-run-btn');
  const stopBtn = document.getElementById('dash-stop-btn');
  if (statusEl) {
    statusEl.innerHTML = html`<span class="run-status-dot${running ? ' run-status-dot-active' : ''}"></span><span class="run-status-label">${running ? (dashboardDryRun ? t('dash.statusDryRunning') : t('dash.statusRunning')) : t('dash.statusIdle')}</span>`;
  }
  if (runBtn) {
    if (stopPending) {
      runBtn.disabled = true;
      runBtn.className = 'btn btn-primary';
      runBtn.textContent = t('dash.runCheck');
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

export function updateNextRunCard(slots) {
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
    if (sub) sub.innerHTML = html`<span class="stat-then-pill">${t('dash.then', { time: formatThenSlot(runs[1]) })}</span>`;
  } else if (subEl) {
    subEl.remove();
  }
  const hasPopover = runs.length > 2;
  if (hasPopover) card.setAttribute('data-has-slots-popover', '1');
  else card.removeAttribute('data-has-slots-popover');
}

export async function pollDashboardStatus() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  try {
    const status = await fetchJSON('/api/status');
    dashboardServerRunning = !!status.running;
    if (status.running) setRunPending(false);
    setStopPending(!!status.stopping);
    dashboardDryRun = !!status.dry_run;
    applyRunStatus(effectiveRunning(status.running));
    updateNextRunCard(status.next_runs || []);
    dashboardAlerts = status.alerts || [];
    updateDashboardAlerts(dashboardAlerts);
  } catch (_) { /* ponytail: polling is best-effort */ }
}

export function renderAlertsHtml(alerts) {
  const visible = visibleAlerts(alerts);
  if (!visible.length) return '';
  const parts = [];
  visible.forEach(function (alert) {
    const key = alertIncidentKey(alert);
    const sinceTime = formatTableTime(alert.since);
    const name = alert.tracker || t('common.emDash');
    parts.push(html`<div class="dash-alert" data-alert-key="${key}"><span class="dash-alert-icon" data-icon="warning"></span><span class="dash-alert-text">${t('dash.alert.trackerDown', { name: name, time: sinceTime, error: alert.error || '', })}</span><button type="button" class="btn btn-ghost btn-icon btn-sm dash-alert-dismiss" data-alert-key="${key}" title="${t('dash.alert.dismiss')}" aria-label="${t('dash.alert.dismiss')}"><span data-icon="x"></span></button></div>`);
  });
  return html`<div class="dash-alerts">${parts}</div>`;
}

export function updateDashboardAlerts(alerts) {
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

export function bindDashboardAlerts(root) {
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

export async function triggerRunCheck() {
  const runBtn = document.getElementById('dash-run-btn');
  const isRestart = dashboardRunning;
  const prevRunning = dashboardRunning;
  setStopPending(false);
  setRunPending(true);
  updateRunStatusUI(true);
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
      setRunPending(false);
      updateRunStatusUI(prevRunning);
      const msg = data && data.error ? data.error : 'HTTP ' + res.status;
      showToast(msg, 'error');
    }
    pollDashboardStatus();
  } catch (err) {
    setRunPending(false);
    updateRunStatusUI(prevRunning);
    showToast(err.message, 'error');
    pollDashboardStatus();
  } finally {
    if (runBtn && currentView === 'dashboard') updateRunStatusUI(dashboardRunning);
  }
}

export async function triggerStopCheck() {
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


export function statCard(value, label, iconName, variant, filterType) {
  const iconCls = 'stat-icon stat-icon-' + variant;
  let filterCls = '';
  let filterAttr = '';
  if (filterType) {
    filterCls = ' stat-card-filter stat-card-filter-' + variant;
    if (filterType !== 'ignored' && !historyState.ignored && historyState.status === filterType) {
      filterCls += ' stat-card-active';
    } else if (filterType === 'ignored' && historyState.ignored) {
      filterCls += ' stat-card-active';
    }
    filterAttr = html` data-stat-filter="${filterType}" role="button" tabindex="0"`;
  }
  return html`<div class="stat-card${filterCls}"${filterAttr}><div class="stat-card-body"><div class="stat-value">${value}</div><div class="stat-label">${label}</div></div><div class="${iconCls}" data-icon="${iconName}"></div></div>`;
}


export function buildHistoryQuery() {
  const limit = pageSizeQueryLimit(historyState.limit);
  let q = '/api/history?limit=' + limit + '&offset=' + historyState.offset +
    '&sort=' + encodeURIComponent(historyState.sort);
  if (historyState.ignored) q += '&ignored=1';
  else if (historyState.status) q += '&status=' + encodeURIComponent(historyState.status);
  if (historyState.name) q += '&name=' + encodeURIComponent(historyState.name);
  if (historyState.tracker) q += '&tracker=' + encodeURIComponent(historyState.tracker);
  if (historyState.category) q += '&category=' + encodeURIComponent(historyState.category);
  return q;
}

export function buildSortControls() {
  return buildSegmentControl([
    { value: 'checked', icon: 'list', title: t('dash.sort.checked'), dataAttr: html` data-sort="checked"` },
    { value: 'recheck', icon: 'clock', title: t('dash.sort.recheck'), dataAttr: html` data-sort="recheck"` },
  ], historyState.sort, t('dash.sortLabel'));
}

export function nextRunStatCard(slots) {
  const runs = slots || [];
  const first = runs[0] ? formatRunSlot(runs[0]) : t('common.emDash');
  const subLine = runs.length >= 2 ? html`<div class="stat-sub"><span class="stat-then-pill">${t('dash.then', { time: formatThenSlot(runs[1]) })}</span></div>` : '';
  const hasPopover = runs.length > 2;
  const popoverAttr = hasPopover ? html` data-has-slots-popover="1"` : '';
  return html`<div class="stat-card stat-card-next-run"${popoverAttr}><div class="stat-card-body"><div class="stat-value">${first}</div>${subLine}<div class="stat-label">${t('dash.nextRun')}</div></div><div class="stat-icon stat-icon-green" data-icon="clock"></div></div>`;
}

export async function fetchDashboardData() {
  const [status, history, summary, ignoreItems] = await Promise.all([
    fetchJSON('/api/status'),
    fetchJSON(buildHistoryQuery()),
    fetchJSON('/api/history/summary'),
    fetchJSON('/api/ignore'),
  ]);
  ignoreHashesCache = new Set((ignoreItems || []).map(function (i) { return i.url_hash; }));
  return {
    status: status,
    history: history,
    stats: {
      updated: summary.updated || 0,
      errors: summary.errors || 0,
      ignored: (ignoreItems || []).length,
    },
  };
}

export async function loadDashboard() {
  const el = document.getElementById('view-dashboard');
  const hadContent = !!el.querySelector('.page-header');
  if (!hadContent) {
    el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
  }
  const fetchGen = ++dashboardFetchGen;
  try {
    const data = await fetchDashboardData();
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal((data.history && data.history.items) || []);
    renderDashboard(el, data.status, data.history, data.stats);
    updateRefreshedAt('dash-last-updated', 'dash.lastUpdated');
    ensureDashboardPolling();
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export async function refreshDashboard(manual) {
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
    updateRefreshedAt('dash-last-updated', 'dash.lastUpdated');
  } catch (_) { /* ponytail: silent refresh is best-effort */ }

  if (manual) {
    const refreshBtn = document.getElementById('dash-refresh-btn');
    if (refreshBtn) refreshBtn.classList.remove('is-spinning');
  }
}

export async function reloadDashboardTable() {
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

// sort mode picks which of the two time columns is relevant, so only one of
// checked_at/recheck_at is ever rendered, in the header and in every row
export function buildTimeColumnHeader() {
  const recheckMode = historyState.sort === 'recheck';
  const cls = recheckMode ? 'plan-recheck-cell' : 'col-time';
  const fullKey = recheckMode ? 'dash.col.recheck' : 'dash.col.checked';
  const shortKey = recheckMode ? 'dash.col.recheckShort' : 'dash.col.checkedShort';
  return html`<th class="${cls}"><span class="th-label-full">${t(fullKey)}</span><span class="th-label-short">${t(shortKey)}</span></th>`;
}

export function releaseTimeCellHtml(item) {
  if (historyState.sort === 'recheck') {
    const recheckShort = formatTableTime(item.recheck_at);
    const recheckFull = formatDateTime(item.recheck_at);
    return html`<td class="plan-recheck-cell" data-recheck-short="${recheckShort}" data-recheck-full="${recheckFull}"><span class="plan-recheck-text" title="${recheckFull}">${recheckShort}</span></td>`;
  }
  const timeShort = formatTableTime(item.checked_at);
  const timeFull = formatDateTime(item.checked_at);
  return html`<td class="col-time" title="${timeFull}">${timeShort}</td>`;
}

export function buildReleaseRows(histItems) {
  if (!histItems || histItems.length === 0) {
    return html`<tr class="table-empty"><td colspan="5">${t('dash.empty.releases')}</td></tr>`;
  }
  const rows = [];
  histItems.forEach(function (item) {
    const urlHash = item.url_hash || '';
    const ignored = isUrlIgnored(urlHash);
    const forced = planItemForced(item);
    const btnCls = ignored
      ? 'btn btn-icon-only btn-unignore btn-ignore-item btn-action-ignore'
      : 'btn btn-icon-only btn-ignore btn-ignore-item btn-action-ignore';
    const btnTitle = ignored ? t('dash.fromIgnore') : t('dash.toIgnore');
    let rowClsValue = '';
    if (forced) rowClsValue = 'plan-row-force';
    if (item.broken) rowClsValue = rowClsValue ? rowClsValue + ' release-row-broken' : 'release-row-broken';
    const rowCls = rowClsValue ? html` class="${rowClsValue}"` : '';
    const trackerBadge = trackerBadgeHtml(item.tracker_name);
    rows.push(html`<tr${rowCls} data-release-hash="${urlHash}"><td class="col-name"><div class="release-title-wrap">${linkCell(item.name, item.url)}</div><div class="release-foot">${trackerBadge}</div></td><td class="col-tracker">${trackerBadge}</td><td class="col-status">${statusIcon(item.status, item.error, item.error_streak)}</td>${releaseTimeCellHtml(item)}<td class="col-actions"><div class="release-actions">${redownloadButtonHtml(urlHash)}${planForceButtonHtml(urlHash, forced)}<button type="button" class="${btnCls}" title="${btnTitle}" aria-label="${btnTitle}" data-url="${item.url || ''}" data-name="${item.name || ''}" data-hash="${urlHash}">${ignoreButtonIconsHtml(ignored)}</button></div></td></tr>`);
  });
  return html`${rows}`;
}

export function updateDashboardPagination(el, history) {
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

export function updateForceAllButton(el, histItems) {
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

export function updateDashboardTable(el, history) {
  const histItems = history.items || [];
  // sort toggle lives outside tbody, in the thead that this function otherwise
  // never touches, so the time column header needs its own explicit sync
  const theadRow = el.querySelector('.table-dash-releases thead tr');
  const timeTh = theadRow && theadRow.querySelector('.col-time, .plan-recheck-cell');
  const wantCls = historyState.sort === 'recheck' ? 'plan-recheck-cell' : 'col-time';
  if (timeTh && !timeTh.classList.contains(wantCls)) {
    timeTh.outerHTML = buildTimeColumnHeader();
  }
  const tbody = el.querySelector('.table-dash-releases tbody');
  if (tbody) {
    tbody.innerHTML = buildReleaseRows(histItems);
    hydrateIcons(tbody);
    bindDashboardReleaseRows(tbody);
  }
  updateDashboardPagination(el, history);
  updateForceAllButton(el, histItems);
}

export function bindDashboardReleaseRows(root) {
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
      // enabling force starts an out-of-schedule pass, so the run controls
      // switch on the click instead of waiting for the status poll
      if (enable) {
        setRunPending(true);
        updateRunStatusUI(true);
      }
      try {
        const res = await fetchJSON('/api/plan/' + encodeURIComponent(hash) + '/force', {
          method: 'POST',
          body: JSON.stringify({ force: enable }),
        });
        dashboardFetchGen++;
        const queued = !res || res.force_queued !== false;
        if (enable && !queued) {
          // the flag is set, but the force queue was full: no out-of-schedule
          // pass will start, so the optimistic run indicator has to go away
          setRunPending(false);
          updateRunStatusUI(effectiveRunning(dashboardServerRunning));
          showToast(t('dash.forceQueueFull'), 'info');
        } else {
          showToast(enable ? t('dash.forceEnabled') : t('dash.forceDisabled'), 'success');
        }
        updateReleaseRow(hash, !!(res && res.force));
      } catch (err) {
        if (enable) {
          setRunPending(false);
          updateRunStatusUI(effectiveRunning(dashboardServerRunning));
        }
        showToast(err.message, 'error');
        btn.disabled = false;
      }
    });
  });

  el.querySelectorAll('.btn-redownload').forEach(function (btn) {
    if (btn.dataset.redownloadBound) return;
    btn.dataset.redownloadBound = '1';
    btn.addEventListener('click', async function () {
      const hash = btn.getAttribute('data-hash');
      if (!hash) return;
      const row = btn.closest('tr');
      const reEnable = [];
      if (row) {
        row.querySelectorAll('button').forEach(function (b) {
          if (!b.disabled) {
            reEnable.push(b);
            b.disabled = true;
          }
        });
        row.classList.add('is-redownloading');
      } else {
        btn.disabled = true;
      }
      btn.classList.add('is-spinning');
      try {
        await fetchJSON('/api/history/' + encodeURIComponent(hash) + '/redownload', {
          method: 'POST',
        });
        showToast(t('dash.redownloadDone'), 'success');
        await loadDashboard();
      } catch (err) {
        showToast(err.message, 'error');
        btn.classList.remove('is-spinning');
        if (row) row.classList.remove('is-redownloading');
        reEnable.forEach(function (b) { b.disabled = false; });
        if (!row) btn.disabled = false;
      }
    });
  });

  bindErrorBadges(el);
}

export function renderDashboard(el, status, history, stats) {
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

  el.innerHTML = html`<div class="page-header dash-page-header"><div class="page-header-row"><div class="dash-header-top"><h1 class="page-title">${t('dash.title')}</h1><div class="dash-header-status-block"><div class="run-status" id="dash-run-status"></div><span class="dash-meta-sep" aria-hidden="true">·</span><span class="dash-last-updated text-muted" id="dash-last-updated"></span></div></div><div class="page-header-actions dash-header-actions"><div class="page-header-run-group"><button type="button" class="btn btn-stop" id="dash-stop-btn"${effectiveRunning(status.running) ? '' : html` style="display:none"`} data-i18n-aria="dash.stopCheck" aria-label="${t('dash.stopCheck')}" title="${t('dash.stopCheck')}"><span data-icon="stop"></span><span class="btn-stop-label">${t('dash.stop')}</span></button><button type="button" class="btn ${effectiveRunning(status.running) ? 'btn-restart' : 'btn-primary'}" id="dash-run-btn">${effectiveRunning(status.running) ? t('dash.restartCheck') : t('dash.runCheck')}</button></div></div></div></div>${renderAlertsHtml(status.alerts || [])}<div class="stat-grid">${nextRunStatCard(nextRuns)}${statCard(stats.updated, t('dash.filter.updated'), 'check', 'green', 'updated')}${statCard(stats.errors, t('dash.errors'), 'warning', 'danger', 'error')}${statCard(stats.ignored, t('dash.ignored'), 'eyeOff', 'cyan', 'ignored')}</div><div class="card"><div class="card-header releases-card-header${mobileSearch.headerClass}"><div class="card-header-toolbar"><div class="card-header-toolbar-shift"><div class="card-header-left"><h2 class="card-title">${t('dash.releasesTitle')}</h2><button type="button" class="btn btn-sm btn-ghost btn-icon btn-refresh" id="dash-refresh-btn" data-i18n-aria="dash.refresh" aria-label="${t('dash.refresh')}" title="${t('dash.refresh')}"><span data-icon="refresh"></span></button></div><div class="card-toolbar-pre-search">${sortHtml}<button type="button" class="${forceAllCls}" id="dash-force-all-btn" title="${forceAllTitle}" aria-label="${forceAllTitle}" aria-pressed="${allForced ? 'true' : 'false'}"><span data-icon="zapForceAll"></span></button></div></div><div class="card-toolbar-post-search">${mobileSearch.html}${filterHtml}</div></div></div><div class="card-body-flush"><div class="table-wrap"><table class="table table-dash-releases"><thead><tr>${buildNameColumnHeader()}<th class="col-tracker">${t('dash.col.tracker')}</th><th class="col-status"><span class="th-label-full">${t('dash.col.status')}</span><span class="th-label-short">${t('dash.col.statusShort')}</span></th>${buildTimeColumnHeader()}<th class="col-actions"></th></tr></thead><tbody>${releaseRows}</tbody></table></div></div><div class="card-footer"><span class="text-muted">${t('dash.pagination', { from: from, to: to, total: total })}</span><div class="pagination-controls">${buildPageSizeSelect(historyState.limit)}<div class="btn-group pagination-nav">${paginationButtons('hist-prev', 'hist-next', historyState.offset <= 0, historyState.offset + pageLimit >= total)}</div></div></div>`;

  hydrateIcons(el);
  bindDashboardAlerts(el);
  bindNextRunStatCard(el, nextRuns);
  dashboardServerRunning = !!status.running;
  setStopPending(!!status.stopping);
  dashboardDryRun = !!status.dry_run;
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

export function bindStatCardFilters(root) {
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


export async function triggerDryRun(btn) {
  if (btn) btn.disabled = true;
  try {
    const res = await fetch('/api/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dry: true }),
    });
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* ignore */ }
    }
    if (res.status === 202) {
      showToast(t('logs.dryRunStarted'), 'success');
      refreshLogs(true);
    } else if (res.status === 409) {
      showToast((data && data.error) || t('dash.checkAlreadyRunning'), 'info');
    } else {
      showToast(data && data.error ? data.error : 'HTTP ' + res.status, 'error');
    }
  } catch (err) {
    showToast(err.message, 'error');
  } finally {
    updateDryRunButtonState();
  }
}

export async function updateDryRunButtonState() {
  const btn = document.getElementById('logs-dryrun-btn');
  if (!btn) return;
  try {
    const status = await fetchJSON('/api/status');
    const busy = !!(status.running || status.stopping);
    btn.disabled = busy;
    btn.title = busy ? t('logs.dryRunBusy') : t('logs.dryRunHint');
  } catch (_) { /* ponytail: best-effort */ }
}


