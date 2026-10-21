import { fetchJSON, showToast } from './api.js';
import { triggerDryRun, updateDryRunButtonState } from './dashboard.js';
import { hydrateIcons, icon } from './icons.js';
import { createPoller } from './poller.js';
import { currentView } from './router.js';
import { initCustomSelects } from './select.js';
import { html, formatLogTime, updateRefreshedAt } from './util.js';

export let logsState = { limit: 200, level: '', items: [] };
export let logsFetchGen = 0;

export const LOG_LEVELS = ['ERROR', 'WARN', 'INFO'];

export function visibleLogItems() {
  // сервер отдает записи без фильтра по уровню. Режем уже полученное,
  // отдельная ручка ради этого не нужна
  if (!logsState.level) return logsState.items;
  return logsState.items.filter(function (item) {
    return (item.level || 'INFO').toUpperCase() === logsState.level;
  });
}


const logsPoller = createPoller('logs', [
  { run: function () { refreshLogs(false); }, ms: 10000, onResume: true },
]);
export const stopLogsPolling = logsPoller.stop;
export const ensureLogsPolling = logsPoller.start;

export async function loadLogs() {
  const el = document.getElementById('view-logs');
  const hadContent = !!el.querySelector('.page-header');
  if (!hadContent) {
    el.innerHTML = html`<div class="loading">${t('common.loading')}</div>`;
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
      el.innerHTML = html`<div class="alert alert-danger">${err.message}</div>`;
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
  } catch (_) { /* фоновое обновление, молчим */ }

  if (manual) {
    const btn = document.getElementById('logs-refresh-btn');
    if (btn) btn.classList.remove('is-spinning');
  }
}

export function renderLogLine(item) {
  const level = (item.level || 'INFO').toUpperCase();
  const cls = level === 'ERROR' ? 'log-error' : level === 'WARN' ? 'log-warn' : 'log-info';
  return html`<div class="log-line ${cls}"><span class="log-time">${formatLogTime(item.time)}</span> <span class="log-level">[${level}]</span> <span class="log-msg">${item.message || ''}</span></div>`;
}

export function renderLogsFeed(el, data) {
  const feed = el.querySelector('.logs-feed');
  if (!feed) return;
  // data === null при смене уровня: рисуем из кеша, на сервер не ходим
  if (data) logsState.items = data.items || [];
  const items = visibleLogItems();
  if (!items.length) {
    const key = logsState.items.length ? 'logs.emptyFiltered' : 'logs.empty';
    feed.innerHTML = html`<div class="log-empty">${t(key)}</div>`;
    return;
  }
  let parts = '';
  items.forEach(function (item) { parts += renderLogLine(item); });
  feed.innerHTML = parts;
  feed.scrollTop = feed.scrollHeight;
}

function pad2(n) {
  return String(n).padStart(2, '0');
}

function fileLogTime(iso) {
  const d = new Date(iso);
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) + ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
}

function logItemsText(items, timeFn) {
  return items.map(function (item) {
    return timeFn(item.time) + ' [' + (item.level || 'INFO').toUpperCase() + '] ' + (item.message || '');
  }).join('\n');
}

export async function downloadLogs(btn) {
  if (btn) btn.disabled = true;
  try {
    // в файл идет все, что есть на сервере, без фильтра с экрана: его качают
    // для разбора проблеммы, и фильтр там скорее мешает
    const data = await fetchJSON('/api/logs?limit=1000');
    const items = data.items || [];
    if (!items.length) {
      showToast(t('logs.copyEmpty'), 'info');
      return;
    }
    const stamp = fileLogTime(new Date().toISOString());
    const name = 'ripperr-' + stamp.slice(0, 10) + '-' + stamp.slice(11, 13) + stamp.slice(14, 16) + '.log';
    // на экране только время, а файл живет дольше суток, без даты строки не сопоставить
    const url = URL.createObjectURL(new Blob([logItemsText(items, fileLogTime) + '\n'], { type: 'text/plain;charset=utf-8' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
  } catch (_) {
    showToast(t('logs.downloadFail'), 'error');
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.blur();
    }
  }
}

export async function copyLogs(btn) {
  const text = logItemsText(visibleLogItems(), formatLogTime);
  // когда под фильтром пусто, тихий выход выглядел как сломанная кнопка
  if (!text) {
    showToast(t('logs.copyEmpty'), 'info');
    return;
  }
  try {
    // clipboard API есть только в secure context, а приложение обычно открывают
    // по http на адрес в локалке
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
    } else {
      const ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.cssText = 'position:fixed;top:-1000px;opacity:0';
      document.body.appendChild(ta);
      ta.select();
      const ok = document.execCommand('copy');
      ta.remove();
      if (!ok) throw new Error(t('logs.copyFail'));
    }
    showToast(t('logs.copied'), 'success');
  } catch (_) {
    showToast(t('logs.copyFail'), 'error');
  }
  if (btn) btn.blur();
}

export function renderLogs(el, data) {
  const limitOpts = [100, 200, 500, 1000];
  const limitParts = limitOpts.map(function (n) {
    return html`<option value="${n}"${logsState.limit === n ? ' selected' : ''}>${n}</option>`;
  });

  const levelParts = ['', ...LOG_LEVELS].map(function (lv) {
    const label = lv ? lv : t('logs.levelAll');
    return html`<option value="${lv}"${logsState.level === lv ? ' selected' : ''}>${label}</option>`;
  });

  el.innerHTML = html`<div class="page-header logs-page-header"><div class="page-header-row"><h1 class="page-title">${t('logs.title')}</h1><div class="page-header-actions"><span class="text-muted logs-last-updated" id="logs-last-updated"></span><button type="button" class="btn btn-sm btn-ghost" id="logs-dryrun-btn" title="${t('logs.dryRunHint')}">${t('logs.dryRun')}</button><button type="button" class="btn btn-sm btn-ghost btn-icon" id="logs-copy-btn" title="${t('logs.copy')}" aria-label="${t('logs.copy')}"><span data-icon="copy"></span></button><button type="button" class="btn btn-sm btn-ghost btn-icon" id="logs-download-btn" title="${t('logs.download')}" aria-label="${t('logs.download')}"><span data-icon="download"></span></button><button type="button" class="btn btn-sm btn-ghost btn-icon btn-refresh" id="logs-refresh-btn" aria-label="${t('logs.refresh')}"><span data-icon="refresh"></span></button></div></div></div><div class="card logs-card"><div class="card-header logs-card-header"><div class="form-group logs-limit-group"><label class="form-label" for="logs-level">${t('logs.level')}</label><select class="select" id="logs-level">${levelParts}</select></div><div class="form-group logs-limit-group"><label class="form-label" for="logs-limit">${t('logs.limit')}</label><select class="select" id="logs-limit">${limitParts}</select></div></div><div class="logs-feed card-body-flush"></div></div>`;

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

  const levelSelect = el.querySelector('#logs-level');
  if (levelSelect) {
    levelSelect.addEventListener('change', function () {
      logsState.level = levelSelect.value;
      renderLogsFeed(el, null);
    });
  }

  const copyBtn = el.querySelector('#logs-copy-btn');
  if (copyBtn) {
    copyBtn.addEventListener('click', function () { copyLogs(copyBtn); });
  }
  const downloadBtn = el.querySelector('#logs-download-btn');
  if (downloadBtn) {
    downloadBtn.addEventListener('click', function () { downloadLogs(downloadBtn); });
  }

  const dryRunBtn = el.querySelector('#logs-dryrun-btn');
  if (dryRunBtn) {
    dryRunBtn.addEventListener('click', function () { triggerDryRun(dryRunBtn); });
  }

  updateDryRunButtonState();
}


