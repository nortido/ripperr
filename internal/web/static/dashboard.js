import { openAddReleaseDialog, syncAddButton } from './addrelease.js';
import { bindDashboardAlerts, renderAlertsHtml, setDashboardAlerts, updateDashboardAlerts } from './alerts.js';
import { fetchJSON, showToast } from './api.js';
import { activeErrorPopover, bindPageSizeSelect, buildPageSizeSelect, buildSegmentControl, closePopovers, initSegmentControls, paginationButtons, updateSegmentThumb } from './components.js';
import { bindHistFilterSelect, buildHistFilters, openHistFilterSelect } from './histfilter.js';
import { hydrateIcons } from './icons.js';
import { refreshLogs } from './logs.js';
import { bindMobileNameSearch, bindNameFilter, buildMobileNameSearchSlot, buildNameColumnHeader, openNameFilterWrap } from './namefilter.js';
import { bindOnboardingCard, maybeStartDashboardTour, onboardingCardHtml, refreshOnboarding } from './onboarding.js';
import { createPoller } from './poller.js';
import { bindDashboardReleaseRows, buildReleaseRows, buildTimeColumnHeader, isUrlIgnored, planItemForced, setCheckProgress, setIgnoreHashesCache, setRedownloadEnabled, syncPlanForceLocal, updateDashboardTable } from './releases.js';
import { applyDashboardFilter, currentView } from './router.js';
import { openCustomSelect } from './select.js';
import { historyState } from './state.js';
import { isTourOpen } from './tour.js';
import { formatRunSlot, formatRunSlotTime, formatThenSlot, html, pageSizeQueryLimit } from './util.js';

export const STOP_PENDING_MS = 90000;
// сервер не сразу отчитается о прогоне: его надо подхватить и загрузить торренты.
// До тех пор верим клику
export const RUN_PENDING_MS = 12000;


export let stopPending = false;
export let stopPendingTimer = null;

export let runPending = false;
export let runPendingTimer = null;

export let dashboardRunning = false;
export let dashboardServerRunning = false;
export let dashboardDryRun = false;
let dashboardQueuedTotal = 0;

export let dashboardFetchGen = 0;
export function nextDashboardFetchGen() {
  // импорт только на чтение, поэтому другие модули двигают счетчик через эту функцию
  return ++dashboardFetchGen;
}
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
  if (wasRunning && !running) refreshDashboard();
}


export function dashboardHasActiveInteraction() {
  if (isTourOpen()) return true;
  if (activeErrorPopover || openCustomSelect || openHistFilterSelect || openNameFilterWrap) return true;
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

let newPollBusy = false;
let newPollAt = 0;
let newPollRefreshPending = false;

async function pollNewReleases() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  if (newPollRefreshPending && !dashboardHasActiveInteraction()) {
    newPollRefreshPending = false;
    refreshDashboard();
  }
  if (newPollBusy || Date.now() - newPollAt < 10000) return;
  newPollBusy = true;
  newPollAt = Date.now();
  try {
    const res = await fetchJSON('/api/history/poll', { method: 'POST' });
    if (res && res.changed > 0) {
      if (dashboardHasActiveInteraction()) newPollRefreshPending = true;
      else refreshDashboard();
    }
  } catch (_) { /* 502/503 и обрыв связи: следующий опрос попробует снова */ } finally {
    newPollBusy = false;
  }
}

const dashboardPoller = createPoller('dashboard', [
  { run: pollDashboardStatus, ms: 5000, onResume: true },
  { run: pollNewReleases, ms: 15000, onResume: true },
  { run: function () { refreshDashboard(); }, ms: 30000 },
]);
export const stopDashboardPolling = dashboardPoller.stop;
export const ensureDashboardPolling = dashboardPoller.start;

export function updateRunStatusUI(running) {
  dashboardRunning = !!running;
  const statusEl = document.getElementById('dash-run-status');
  const runBtn = document.getElementById('dash-run-btn');
  const stopBtn = document.getElementById('dash-stop-btn');
  if (statusEl) {
    statusEl.innerHTML = html`<span class="run-status-dot${running ? ' run-status-dot-active' : ''}"></span><span class="run-status-label">${running ? (dashboardDryRun ? t('dash.statusDryRunning') : t('dash.statusRunning')) : t('dash.statusIdle')}</span>${running && dashboardQueuedTotal > 0 ? html`<span class="run-status-left">${t('dash.statusLeft', { count: dashboardQueuedTotal })}</span>` : ''}`;
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
  const card = document.querySelector('#view-dashboard .stat-card-next-run');
  if (!card) return;
  const runs = slots || [];
  const valueEl = card.querySelector('.stat-value');
  if (valueEl) {
    valueEl.innerHTML = nextRunValueHtml(runs);
    fitNextRunValue(valueEl);
  }
  const labelEl = card.querySelector('.stat-label');
  if (!labelEl) return;
  let thenEl = labelEl.querySelector('.stat-then');
  if (runs.length >= 2) {
    if (!thenEl) {
      thenEl = document.createElement('span');
      thenEl.className = 'stat-then';
      labelEl.appendChild(thenEl);
    }
    thenEl.textContent = t('dash.then', { time: formatThenSlot(runs[1]) });
  } else if (thenEl) {
    thenEl.remove();
  }
}

export async function pollDashboardStatus() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  try {
    const status = await fetchJSON('/api/status');
    dashboardServerRunning = !!status.running;
    if (status.running) setRunPending(false);
    setStopPending(!!status.stopping);
    dashboardDryRun = !!status.dry_run;
    dashboardQueuedTotal = status.queued_total || 0;
    setCheckProgress(status.checking, status.queued);
    applyRunStatus(effectiveRunning(status.running));
    updateNextRunCard(status.next_runs || []);
    updateDashboardAlerts(status.alerts || []);
  } catch (_) { /* следующий опрос попробует снова */ }
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
    const opts = { method: 'POST', headers: { 'X-Ripperr': '1' } };
    if (isRestart) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify({ restart: true });
    }
    const res = await fetch('/api/run', opts);
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* не JSON, обойдемся кодом ответа */ }
    }
    if (res.status === 202) {
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
      headers: { 'Content-Type': 'application/json', 'X-Ripperr': '1' },
      body: JSON.stringify({ stop: true }),
    });
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* не JSON, хватит статуса */ }
    }
    if (res.status === 202) {
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
    const active = filterType === 'ignored'
      ? historyState.ignored
      : !historyState.ignored && historyState.status === filterType;
    filterCls = ' stat-card-filter stat-card-filter-' + variant;
    if (active) filterCls += ' stat-card-active';
    // раньше активный фильтр было видно только по рамке, скринридер про него молчал
    filterAttr = html` data-stat-filter="${filterType}" role="button" tabindex="0" aria-pressed="${active ? 'true' : 'false'}"`;
  }
  return html`<div class="stat-card${filterCls}"${filterAttr}><div class="stat-card-body"><div class="stat-value" data-digits="${String(value).length}">${value}</div><div class="stat-label">${label}</div></div><div class="${iconCls}" data-icon="${iconName}"></div></div>`;
}


export function buildHistoryFilterParams() {
  // без пагинации: этот же хвост уходит в форс всем, чтобы кнопка взяла ровно отфильтрованные релизы
  let q = '&sort=' + encodeURIComponent(historyState.sort);
  if (historyState.ignored) q += '&ignored=1';
  else if (historyState.status) q += '&status=' + encodeURIComponent(historyState.status);
  if (historyState.name) q += '&name=' + encodeURIComponent(historyState.name);
  if (historyState.tracker) q += '&tracker=' + encodeURIComponent(historyState.tracker);
  if (historyState.category) q += '&category=' + encodeURIComponent(historyState.category);
  return q;
}

export function buildHistoryQuery() {
  const limit = pageSizeQueryLimit(historyState.limit);
  return '/api/history?limit=' + limit + '&offset=' + historyState.offset + buildHistoryFilterParams();
}

export function buildSortControls() {
  return buildSegmentControl([
    { value: 'checked', icon: 'list', title: t('dash.sort.checked'), dataAttr: html` data-sort="checked"` },
    { value: 'recheck', icon: 'clock', title: t('dash.sort.recheck'), dataAttr: html` data-sort="recheck"` },
  ], historyState.sort, t('dash.sortLabel'));
}

function fitNextRunValue(valueEl) {
  if (!valueEl) return;
  // полную форму меряем в невидимом span на 28px: видимая форма зависит от раскладки,
  // и замер по короткой после расширения окна раздувал надпись под иконку
  const full = valueEl.querySelector('.stat-run-full');
  const probe = document.createElement('span');
  probe.textContent = (full || valueEl).textContent;
  probe.style.cssText = 'position:absolute;left:-9999px;top:0;visibility:hidden;white-space:nowrap;font-size:28px';
  valueEl.appendChild(probe);
  const width = probe.getBoundingClientRect().width;
  probe.remove();
  // +1px: без запаса последняя цифра на дробной ширине обрезается
  if (width > 0) valueEl.style.setProperty('--nr-w28', (width + 1).toFixed(1));
  if (document.fonts && document.fonts.status !== 'loaded') {
    document.fonts.ready.then(function () { fitNextRunValue(valueEl); });
  }
}

function nextRunValueHtml(runs) {
  if (!runs[0]) return html`${t('common.emDash')}`;
  // на телефоне счетчики в одну строку и там только время, без "сегодня".
  // Рисуем обе формы, нужную выбирает CSS
  return html`<span class="stat-run-full">${formatRunSlot(runs[0])}</span><span class="stat-run-short">${formatRunSlotTime(runs[0])}</span>`;
}

export function nextRunStatCard(slots) {
  const runs = slots || [];
  const first = nextRunValueHtml(runs);
  const thenSpan = runs.length >= 2 ? html`<span class="stat-then">${t('dash.then', { time: formatThenSlot(runs[1]) })}</span>` : '';
  return html`<div class="stat-card stat-card-next-run"><div class="stat-card-body"><div class="stat-value">${first}</div><div class="stat-label"><span class="stat-label-text">${t('dash.nextRun')}</span>${thenSpan}</div></div><div class="stat-icon stat-icon-neutral" data-icon="clock"></div></div>`;
}

export async function fetchDashboardData() {
  const [status, history, summary, ignoreItems] = await Promise.all([
    fetchJSON('/api/status'),
    fetchJSON(buildHistoryQuery()),
    fetchJSON('/api/history/summary'),
    fetchJSON('/api/ignore'),
  ]);
  setIgnoreHashesCache(ignoreItems);
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
    const pending = fetchDashboardData();
    refreshOnboarding();
    const data = await pending;
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal((data.history && data.history.items) || []);
    renderDashboard(el, data.status, data.history, data.stats);
    ensureDashboardPolling();
    pollNewReleases();
  } catch (err) {
    el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
    showToast(err.message, 'error');
  }
}

export async function refreshDashboard() {
  if (currentView !== 'dashboard' || document.visibilityState !== 'visible') return;
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.page-header')) return;
  if (dashboardHasActiveInteraction()) return;

  const fetchGen = ++dashboardFetchGen;
  try {
    const data = await fetchDashboardData();
    if (fetchGen !== dashboardFetchGen) return;
    if (dashboardHasActiveInteraction()) return;
    syncPlanForceLocal((data.history && data.history.items) || []);
    renderDashboard(el, data.status, data.history, data.stats);
  } catch (_) { /* фоновое обновление, ошибку не показываем */ }
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

export function renderDashboard(el, status, history, stats) {
  closePopovers();
  const nextRuns = status.next_runs || [];

  const histItems = history.items || [];
  const total = history.total || 0;
  // флаг выставляем до сборки строк: кнопку «Перекачать» клиенты кроме qBittorrent не поддерживают
  setRedownloadEnabled(status.redownload);
  const tableCls = 'table table-dash-releases';
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

  el.innerHTML = html`<div class="page-header dash-page-header"><div class="page-header-row"><div class="dash-header-top"><h1 class="page-title">${t('dash.title')}</h1><div class="dash-header-status-block"><div class="run-status" id="dash-run-status"></div></div></div><div class="page-header-actions dash-header-actions"><div class="page-header-run-group"><button type="button" class="btn btn-stop" id="dash-stop-btn"${effectiveRunning(status.running) ? '' : html` style="display:none"`} data-i18n-aria="dash.stopCheck" aria-label="${t('dash.stopCheck')}" title="${t('dash.stopCheck')}"><span data-icon="stop"></span><span class="btn-stop-label">${t('dash.stop')}</span></button><button type="button" class="btn btn-outline" id="dash-add-btn"><span data-icon="plus"></span><span>${t('dash.addButton')}</span></button><button type="button" class="btn ${effectiveRunning(status.running) ? 'btn-restart' : 'btn-primary'}" id="dash-run-btn">${effectiveRunning(status.running) ? t('dash.restartCheck') : t('dash.runCheck')}</button></div></div></div></div>${onboardingCardHtml()}${renderAlertsHtml(status.alerts || [])}<div class="stat-grid">${nextRunStatCard(nextRuns)}${statCard(stats.updated, t('dash.filter.updated'), 'check', 'green', 'updated')}${statCard(stats.errors, t('dash.errors'), 'warning', 'danger', 'error')}${statCard(stats.ignored, t('dash.ignored'), 'eyeHidden', 'cyan', 'ignored')}</div><div class="card"><div class="card-header releases-card-header${mobileSearch.headerClass}"><div class="card-header-toolbar"><div class="card-header-toolbar-shift"><div class="card-header-left"><h2 class="card-title">${t('dash.releasesTitle')}</h2></div><div class="card-toolbar-pre-search">${sortHtml}<button type="button" class="${forceAllCls}" id="dash-force-all-btn" style="display:none" title="${forceAllTitle}" aria-label="${forceAllTitle}" aria-pressed="${allForced ? 'true' : 'false'}"><span data-icon="zapForceAll"></span></button></div></div><div class="card-toolbar-post-search">${mobileSearch.html}${filterHtml}</div></div></div><div class="card-body-flush"><div class="table-wrap"><table class="${tableCls}"><thead><tr>${buildNameColumnHeader()}<th class="col-tracker">${t('dash.col.tracker')}</th><th class="col-category">${t('dash.col.category')}</th><th class="col-status"><span class="th-label-full">${t('dash.col.status')}</span><span class="th-label-short">${t('dash.col.statusShort')}</span></th>${buildTimeColumnHeader()}<th class="col-actions"></th></tr></thead><tbody>${releaseRows}</tbody></table></div></div><div class="card-footer"><span class="text-muted">${t('dash.pagination', { from: from, to: to, total: total })}</span><div class="pagination-controls">${buildPageSizeSelect(historyState.limit)}<div class="btn-group pagination-nav">${paginationButtons('hist-prev', 'hist-next', historyState.offset <= 0, historyState.offset + pageLimit >= total)}</div></div></div>`;

  hydrateIcons(el);
  bindDashboardAlerts(el);
  bindOnboardingCard(el);
  fitNextRunValue(el.querySelector('.stat-card-next-run .stat-value'));
  dashboardServerRunning = !!status.running;
  setStopPending(!!status.stopping);
  dashboardDryRun = !!status.dry_run;
  dashboardQueuedTotal = status.queued_total || 0;
  setCheckProgress(status.checking, status.queued);
  updateRunStatusUI(effectiveRunning(status.running));
  setDashboardAlerts(status.alerts || []);

  const runBtn = el.querySelector('#dash-run-btn');
  if (runBtn) runBtn.addEventListener('click', triggerRunCheck);

  const stopBtn = el.querySelector('#dash-stop-btn');
  if (stopBtn) stopBtn.addEventListener('click', triggerStopCheck);

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

  const addBtn = el.querySelector('#dash-add-btn');
  if (addBtn) addBtn.addEventListener('click', function () { openAddReleaseDialog(addBtn, loadDashboard); });
  syncAddButton();

  const forceAllBtn = el.querySelector('#dash-force-all-btn');
  if (forceAllBtn) {
    forceAllBtn.addEventListener('click', async function () {
      const enable = !forceAllBtn.classList.contains('is-active');
      forceAllBtn.disabled = true;
      try {
        const res = await fetchJSON('/api/plan/force-all?' + buildHistoryFilterParams().slice(1), {
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

  maybeStartDashboardTour();
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
      headers: { 'Content-Type': 'application/json', 'X-Ripperr': '1' },
      body: JSON.stringify({ dry: true }),
    });
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (_) { /* тело не JSON, покажем HTTP-код */ }
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
  } catch (_) { /* кнопка останется как была */ }
}


