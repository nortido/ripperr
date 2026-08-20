import { fetchJSON, showToast } from './api.js';
import { bindErrorBadges } from './components.js';
import { dashboardServerRunning, effectiveRunning, loadDashboard, nextDashboardFetchGen, setRunPending, updateRunStatusUI } from './dashboard.js';
import { hydrateIcons } from './icons.js';
import { historyFiltered, historyState } from './state.js';
import { formatDateTime, formatTableTime, html, linkCell, pageSizeQueryLimit, statusIcon, trackerBadgeHtml } from './util.js';

export let planForceLocal = Object.create(null);
export let ignoreHashesCache = null;

export function setIgnoreHashesCache(items) {
  ignoreHashesCache = new Set((items || []).map(function (i) { return i.url_hash; }));
}

export function isUrlIgnored(urlHash) {
  return !!(urlHash && ignoreHashesCache && ignoreHashesCache.has(urlHash));
}

export async function syncIgnoreState() {
  const items = await fetchJSON('/api/ignore');
  setIgnoreHashesCache(items);
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
    recheckCell.innerHTML = planRecheckSpanHtml(dateShort, dateFull, isUrlIgnored(hash));
  }
  const btn = row.querySelector('.btn-plan-force');
  if (btn) updatePlanForceButton(btn, hash, forced);
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

function planRecheckSpanHtml(short, full, ignored) {
  // ignored releases are never rechecked, so a recheck date here is fiction;
  // the attributes still carry the real date, only the display is swapped
  const text = ignored ? t('common.emDash') : short;
  const title = ignored ? '' : full;
  return html`<span class="plan-recheck-text" title="${title}">${text}</span>`;
}

export function releaseTimeCellHtml(item) {
  if (historyState.sort === 'recheck') {
    const recheckShort = formatTableTime(item.recheck_at);
    const recheckFull = formatDateTime(item.recheck_at);
    const ignored = isUrlIgnored(item.url_hash || '');
    return html`<td class="plan-recheck-cell" data-recheck-short="${recheckShort}" data-recheck-full="${recheckFull}">${planRecheckSpanHtml(recheckShort, recheckFull, ignored)}</td>`;
  }
  const timeShort = formatTableTime(item.checked_at);
  const timeFull = formatDateTime(item.checked_at);
  return html`<td class="col-time" title="${timeFull}">${timeShort}</td>`;
}

export function buildReleaseRows(histItems) {
  if (!histItems || histItems.length === 0) {
    // пустой результат под фильтром это не пустая база: раньше оба случая
    // показывали «Проверок ещё не было» и это вводило в заблуждение
    const key = historyFiltered() ? 'dash.empty.filtered' : 'dash.empty.releases';
    return html`<tr class="table-empty"><td colspan="5">${t(key)}</td></tr>`;
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
        if (historyState.ignored) {
          loadDashboard();
        } else if (historyState.sort === 'recheck') {
          // row stays put (no full reload), so its recheck cell must catch up
          // with the new ignore state right away instead of showing a stale date
          const row = btn.closest('tr');
          const cell = row && row.querySelector('.plan-recheck-cell');
          if (cell) {
            const dateShort = cell.getAttribute('data-recheck-short') || '';
            const dateFull = cell.getAttribute('data-recheck-full') || '';
            cell.innerHTML = planRecheckSpanHtml(dateShort, dateFull, isUrlIgnored(hash));
          }
        }
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
        nextDashboardFetchGen();
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
