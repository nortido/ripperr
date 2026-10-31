import { fetchJSON, showToast } from './api.js';
import { bindReleaseHistory } from './relhistory.js';
import { dashboardServerRunning, effectiveRunning, loadDashboard, nextDashboardFetchGen, pollDashboardStatus, setRunPending, updateRunStatusUI } from './dashboard.js';
import { confirmDialog } from './confirm.js';
import { hydrateIcons, ICONS, icon } from './icons.js';
import { historyFiltered, historyState, saveHistoryOffset } from './state.js';
import { openRowMenu } from './rowmenu.js';
import { categoryChipHtml, formatDateTime, formatTableTime, html, linkCell, pageSizeQueryLimit, statusIcon, trackerBadgeHtml } from './util.js';

export let planForceLocal = Object.create(null);
export let ignoreHashesCache = null;

let checkingHashes = new Set();
let queuedHashes = new Set();
let redownloadEnabled = true;

export function setRedownloadEnabled(on) {
  redownloadEnabled = !!on;
}

export function setCheckProgress(checking, queued) {
  // подсветку дает опрос статуса, а таблица перерисовывается своим циклом,
  // поэтому набор держим тут и накладываем с обеих сторон
  checkingHashes = new Set(checking || []);
  queuedHashes = new Set(queued || []);
  applyCheckProgress();
}

function applyCheckProgress(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  scope.querySelectorAll('tr[data-release-hash]').forEach(function (row) {
    const hash = row.getAttribute('data-release-hash') || '';
    row.classList.toggle('release-row-checking', checkingHashes.has(hash));
    row.classList.toggle('release-row-queued', !checkingHashes.has(hash) && queuedHashes.has(hash));
  });
}

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
  if (!valueEl) return;
  valueEl.textContent = String(count);
  valueEl.dataset.digits = String(String(count).length);
}

export function ignoreButtonIconsHtml(ignored) {
  // на ховере иконка показывает, что будет после клика
  const idleIcon = ignored ? 'eyeHidden' : 'eye';
  const hoverIcon = ignored ? 'eye' : 'eyeHidden';
  return html`<span class="icon-idle" data-icon="${idleIcon}"></span><span class="icon-hover" data-icon="${hoverIcon}"></span>`;
}

export function forceButtonIconsHtml(forced) {
  const idleIcon = forced ? 'zapFilled' : 'zap';
  const hoverIcon = forced ? 'zap' : 'zapFilled';
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
  } catch (_) { /* кнопки останутся как были */ }
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

export function moreButtonHtml(hash, absent) {
  const btnTitle = t('dash.more');
  const absentAttr = absent ? html` data-absent="1"` : '';
  return html`<button type="button" class="btn btn-sm btn-icon btn-more btn-action-more" title="${btnTitle}" aria-label="${btnTitle}" aria-haspopup="menu" aria-expanded="false" data-hash="${hash}"${absentAttr}><span data-icon="moreVertical"></span></button>`;
}

function setMoreIcon(btn, name) {
  const iconEl = btn.querySelector('[data-icon]');
  if (!iconEl) return;
  iconEl.setAttribute('data-icon', name);
  iconEl.innerHTML = ICONS[name];
}

function releaseRow(hash) {
  // фоновое обновление могло перерисовать таблицу, пока шел запрос или диалог,
  // поэтому строку каждый раз ищем заново по хэшу
  return document.querySelector('tr[data-release-hash="' + CSS.escape(hash) + '"]');
}

function setReleaseBusy(hash, on, icon) {
  const row = releaseRow(hash);
  if (!row) return;
  row.classList.toggle('is-redownloading', on);
  if (on) {
    row.querySelectorAll('button:not(:disabled)').forEach(function (b) {
      b.disabled = true;
      b.dataset.busyOff = '1';
    });
  } else {
    row.querySelectorAll('button[data-busy-off]').forEach(function (b) {
      b.disabled = false;
      delete b.dataset.busyOff;
    });
  }
  const more = row.querySelector('.btn-action-more');
  if (more) {
    more.classList.toggle('is-spinning', on);
    setMoreIcon(more, on ? icon || 'rotateCw' : 'moreVertical');
  }
}

function focusMoreButton(hash) {
  const row = releaseRow(hash);
  const more = row && row.querySelector('.btn-action-more');
  if (more) more.focus({ preventScroll: true });
}

const REMOVE_ERROR_KEYS = {
  torrent_busy: 'dash.redownloadBusy',
  replace_pending: 'dash.removePending',
  torrents_changed: 'dash.removeChanged',
  files_not_allowed: 'dash.removeFilesDenied',
  remove_unconfirmed: 'dash.removeUnconfirmed',
  partial_list: 'dash.partialList',
  runner_unavailable: 'dash.runnerNotReady',
  timeout: 'dash.timeout',
};

function removeErrorText(err) {
  const key = REMOVE_ERROR_KEYS[err.code];
  return key ? t(key) : err.message;
}

async function redownloadRelease(hash, name) {
  const ok = await confirmDialog({
    text: t('dash.redownloadConfirm', { name: name }),
    confirmLabel: t('dash.redownloadAction'),
    danger: true,
  });
  if (!ok) return;
  setReleaseBusy(hash, true);
  try {
    await fetchJSON('/api/history/' + encodeURIComponent(hash) + '/redownload', {
      method: 'POST',
    });
    showToast(t('dash.redownloadDone'), 'success');
    await loadDashboard();
  } catch (err) {
    showToast(err.code === 'torrent_busy' ? t('dash.redownloadBusy') : err.message, 'error');
    setReleaseBusy(hash, false);
  }
}

async function removeRelease(hash, name) {
  const base = '/api/history/' + encodeURIComponent(hash) + '/remove';
  setReleaseBusy(hash, true, 'loader');
  let info;
  try {
    info = await fetchJSON(base);
  } catch (err) {
    showToast(removeErrorText(err), 'error', { title: t('dash.removeFailedTitle') });
    setReleaseBusy(hash, false);
    return;
  }
  setReleaseBusy(hash, false);
  focusMoreButton(hash);
  const count = info.torrents || 0;
  let text;
  if (count === 0) text = t('dash.removeConfirmGone', { name: name });
  else if (count === 1) text = t('dash.removeConfirm', { name: name });
  else text = t('dash.removeConfirmMany', { name: name, count: count });
  // тоггл виден всегда и всегда выключен: когда файлы удалить нельзя, он неактивен с причиной
  const opts = { text: text, confirmLabel: t('common.remove'), danger: true, checkLabel: t('dash.removeWithFiles') };
  if (!info.files) {
    opts.checkDisabled = true;
    opts.checkHint = count === 0 ? t('dash.removeFilesGone') : t('dash.removeFilesOff');
  }
  const res = await confirmDialog(opts);
  if (!res) return;
  setReleaseBusy(hash, true, 'loader');
  try {
    await fetchJSON(base, {
      method: 'POST',
      body: JSON.stringify({ files: !!info.files && !!res.checked, torrents: count }),
    });
    showToast(t('dash.removeDone'), 'success');
    await loadDashboard();
  } catch (err) {
    showToast(removeErrorText(err), 'error', { title: t('dash.removeFailedTitle') });
    setReleaseBusy(hash, false);
  }
}

export function planForceButtonHtml(hash, forced) {
  const btnTitle = forced ? t('dash.forceCancel') : t('dash.forceCheck');
  const btnCls = 'btn btn-sm btn-icon btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  return html`<button type="button" class="${btnCls}" title="${btnTitle}" aria-label="${btnTitle}" aria-pressed="${forced ? 'true' : 'false'}" data-hash="${hash}" data-force="${forced ? '1' : '0'}">${forceButtonIconsHtml(forced)}</button>`;
}

export function updatePlanForceButton(btn, hash, forced) {
  btn.disabled = false;
  btn.className = 'btn btn-sm btn-icon btn-plan-force btn-action-force' + (forced ? ' is-active' : '');
  const btnTitle = forced ? t('dash.forceCancel') : t('dash.forceCheck');
  btn.setAttribute('title', btnTitle);
  btn.setAttribute('aria-label', btnTitle);
  btn.setAttribute('aria-pressed', forced ? 'true' : 'false');
  btn.setAttribute('data-hash', hash);
  btn.setAttribute('data-force', forced ? '1' : '0');
  btn.innerHTML = forceButtonIconsHtml(forced);
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
    // меняем только текст даты: рядом в ячейке чипы, innerHTML стер бы их
    const textEl = recheckCell.querySelector('.plan-recheck-text');
    const nextHtml = planRecheckSpanHtml(dateShort, dateFull, isUrlIgnored(hash));
    if (textEl) textEl.outerHTML = nextHtml;
    else recheckCell.innerHTML = nextHtml;
  }
  const btn = row.querySelector('.btn-plan-force');
  if (btn) updatePlanForceButton(btn, hash, forced);
}

export function buildTimeColumnHeader() {
  const recheckMode = historyState.sort === 'recheck';
  const cls = recheckMode ? 'plan-recheck-cell' : 'col-time';
  const fullKey = recheckMode ? 'dash.col.recheck' : 'dash.col.checked';
  const shortKey = recheckMode ? 'dash.col.recheckShort' : 'dash.col.checkedShort';
  return html`<th class="${cls}"><span class="th-label-full">${t(fullKey)}</span><span class="th-label-short">${t(shortKey)}</span></th>`;
}

// формой повторяет дату "25.10 23:11": тире не спутать со значком статуса
const DATE_STUB = '--.-- --:--';

function dateStubHtml(cls) {
  return html`<span class="${cls} time-stub">${DATE_STUB}</span>`;
}

function planRecheckSpanHtml(short, full, ignored) {
  // игнорируемые не перепроверяются, и дата тут была бы выдумкой.
  // В атрибутах остается настоящая, меняем только то, что видно
  if (ignored || !short) return dateStubHtml('plan-recheck-text');
  return html`<span class="plan-recheck-text" title="${full}">${short}</span>`;
}

function plannedCheckSpanHtml(short, full, ignored) {
  if (ignored) return dateStubHtml('col-time-text');
  return html`<span class="col-time-text time-planned" title="${t('dash.firstCheckAt', { time: full })}">${icon('clock')}${short}</span>`;
}

export function releaseTimeCellHtml(item) {
  const meta = releaseMetaHtml(item);
  const ignored = isUrlIgnored(item.url_hash || '');
  if (historyState.sort === 'recheck') {
    const recheckShort = item.recheck_at ? formatTableTime(item.recheck_at) : '';
    const recheckFull = item.recheck_at ? formatDateTime(item.recheck_at) : '';
    return html`<td class="plan-recheck-cell" data-recheck-short="${recheckShort}" data-recheck-full="${recheckFull}">${planRecheckSpanHtml(recheckShort, recheckFull, ignored)}${meta}</td>`;
  }
  if (!item.checked_at && item.recheck_at) {
    const plannedShort = formatTableTime(item.recheck_at);
    const plannedFull = formatDateTime(item.recheck_at);
    return html`<td class="col-time" data-recheck-short="${plannedShort}" data-recheck-full="${plannedFull}">${plannedCheckSpanHtml(plannedShort, plannedFull, ignored)}${meta}</td>`;
  }
  if (!item.checked_at) {
    return html`<td class="col-time">${dateStubHtml('col-time-text')}${meta}</td>`;
  }
  const timeShort = formatTableTime(item.checked_at);
  const timeFull = formatDateTime(item.checked_at);
  return html`<td class="col-time"><span class="col-time-text" title="${timeFull}">${timeShort}</span>${meta}</td>`;
}

function categoryBadgeHtml(item) {
  const category = (item.category || '').trim();
  return category ? categoryChipHtml(category, item.category_color) : '';
}

function releaseMetaHtml(item) {
  // чипы еще раз рядом с датой: на мобильном колонка трекера скрыта, и внизу
  // карточки они встают в ряд с датой, не разрывая название
  return html`<span class="release-meta">${trackerBadgeHtml(item.tracker_name)}${categoryBadgeHtml(item)}</span>`;
}

export function buildReleaseRows(histItems) {
  if (!histItems || histItems.length === 0) {
    // пустой результат под фильтром не значит пустую базу, текст у них разный
    const key = historyFiltered() ? 'dash.empty.filtered' : 'dash.empty.releases';
    return html`<tr class="table-empty"><td colspan="5">${t(key)}</td></tr>`;
  }
  const rows = [];
  histItems.forEach(function (item) {
    const urlHash = item.url_hash || '';
    const ignored = isUrlIgnored(urlHash);
    const forced = planItemForced(item);
    const btnCls = ignored
      ? 'btn btn-sm btn-icon btn-unignore btn-ignore-item btn-action-ignore'
      : 'btn btn-sm btn-icon btn-ignore btn-ignore-item btn-action-ignore';
    const btnTitle = ignored ? t('dash.fromIgnore') : t('dash.toIgnore');
    let rowClsValue = '';
    if (forced) rowClsValue = 'plan-row-force';
    if (item.broken) rowClsValue = rowClsValue ? rowClsValue + ' release-row-broken' : 'release-row-broken';
    if (checkingHashes.has(urlHash)) rowClsValue += ' release-row-checking';
    else if (queuedHashes.has(urlHash)) rowClsValue += ' release-row-queued';
    if (item.absent) rowClsValue += ' release-row-absent';
    const rowCls = rowClsValue ? html` class="${rowClsValue}"` : '';
    const absentLabel = item.absent ? html`<span class="release-absent" title="${t('dash.absentTitle')}">${t('dash.absent')}</span>` : '';
    const trackerBadge = trackerBadgeHtml(item.tracker_name);
    rows.push(html`<tr${rowCls} data-release-hash="${urlHash}"><td class="col-name"><div class="release-title-wrap">${linkCell(item.name, item.url)}${absentLabel}</div><div class="release-foot">${trackerBadge}</div></td><td class="col-tracker">${trackerBadge}</td><td class="col-category">${categoryBadgeHtml(item)}</td><td class="col-status">${statusIcon(item.status, item.error_streak)}</td>${releaseTimeCellHtml(item)}<td class="col-actions"><div class="release-actions">${planForceButtonHtml(urlHash, forced)}<button type="button" class="${btnCls}" title="${btnTitle}" aria-label="${btnTitle}" data-url="${item.url || ''}" data-name="${item.name || ''}" data-hash="${urlHash}">${ignoreButtonIconsHtml(ignored)}</button>${moreButtonHtml(urlHash, item.absent)}</div></td></tr>`);
  });
  return html`${rows}`;
}

let historyFull = { items: [], total: 0 };

export function getHistoryFull() {
  return historyFull;
}

export function historyPage(history) {
  // весь отфильтрованный список уже в браузере, страница это срез из него
  if (history) historyFull = history;
  const items = historyFull.items || [];
  const total = items.length;
  const pageLimit = pageSizeQueryLimit(historyState.limit);
  // сохраненная страница могла опустеть или прийти от другого размера страницы
  let offset = Math.floor(historyState.offset / pageLimit) * pageLimit;
  if (offset >= total) offset = total === 0 ? 0 : Math.floor((total - 1) / pageLimit) * pageLimit;
  historyState.offset = offset;
  saveHistoryOffset();
  return {
    items: items.slice(offset, offset + pageLimit),
    total: total,
    from: total === 0 ? 0 : offset + 1,
    to: Math.min(offset + pageLimit, total),
    first: offset <= 0,
    last: offset + pageLimit >= total,
  };
}

export function updateDashboardPagination(el, page) {
  const footerText = el.querySelector('.card-footer > .text-muted');
  if (footerText) {
    footerText.textContent = t('dash.pagination', { from: page.from, to: page.to, total: page.total });
  }
  const first = el.querySelector('#hist-first');
  const prev = el.querySelector('#hist-prev');
  const next = el.querySelector('#hist-next');
  if (first) first.disabled = page.first;
  if (prev) prev.disabled = page.first;
  if (next) next.disabled = page.last;
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
  const page = historyPage(history);
  const histItems = page.items;
  // переключатель сортировки в thead, а функция трогает только tbody,
  // поэтому заголовок колонки времени синхронизируем отдельно
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
  updateDashboardPagination(el, page);
  updateForceAllButton(el, historyFull.items);
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
        } else {
          await fetchJSON('/api/ignore', {
            method: 'POST',
            body: JSON.stringify({ url: url, name: name }),
          });
        }
        await refreshIgnoreState();
        if (historyState.ignored) {
          loadDashboard();
        } else {
          // строка остается на месте без полной перезагрузки, так что ячейка с
          // плановой датой должна сразу показать новое состояние игнора.
          // Меняем только текст: рядом чипы, innerHTML стер бы их
          const row = btn.closest('tr');
          const cell = row && row.querySelector('.plan-recheck-cell, .col-time[data-recheck-short]');
          const textEl = cell && cell.querySelector('.plan-recheck-text, .col-time-text');
          if (textEl) {
            const dateShort = cell.getAttribute('data-recheck-short') || '';
            const dateFull = cell.getAttribute('data-recheck-full') || '';
            const planned = cell.classList.contains('plan-recheck-cell') ? planRecheckSpanHtml : plannedCheckSpanHtml;
            textEl.outerHTML = planned(dateShort, dateFull, isUrlIgnored(hash));
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
      // включение форса сразу запускает внеплановый проход, поэтому кнопки
      // переключаем по клику, не дожидаясь опроса статуса
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
          // флаг стоит, но очередь форса была полна и прохода не будет.
          // Убираем оптимистичный индикатор запуска
          setRunPending(false);
          updateRunStatusUI(effectiveRunning(dashboardServerRunning));
          showToast(t('dash.forceQueueFull'), 'info');
        }
        updateReleaseRow(hash, !!(res && res.force));
        // снятый форс убирает раздачу из очереди на сервере: обновляем счетчик и подсветку сразу
        if (!enable) pollDashboardStatus();
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

  el.querySelectorAll('.btn-action-more').forEach(function (btn) {
    if (btn.dataset.moreBound) return;
    btn.dataset.moreBound = '1';
    btn.addEventListener('click', function () {
      const hash = btn.getAttribute('data-hash');
      if (!hash) return;
      const ignoreBtn = btn.closest('tr').querySelector('[data-name]');
      const name = (ignoreBtn && ignoreBtn.getAttribute('data-name')) || hash;
      const absent = btn.getAttribute('data-absent') === '1';
      const items = [];
      if (redownloadEnabled) {
        items.push({
          label: t('dash.redownloadAction'),
          icon: 'download',
          title: absent ? t('dash.absentTitle') : t('dash.redownload'),
          disabled: absent,
          onSelect: function () { redownloadRelease(hash, name); },
        });
      }
      items.push({
        label: t('common.remove'),
        icon: 'trash',
        danger: true,
        onSelect: function () { removeRelease(hash, name); },
      });
      openRowMenu(btn, items);
    });
  });

  bindReleaseHistory(el);
}
