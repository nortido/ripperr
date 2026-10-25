import { fetchJSON, showToast } from './api.js';
import { triggerDryRun, updateDryRunButtonState } from './dashboard.js';
import { hydrateIcons, icon } from './icons.js';
import { createPoller } from './poller.js';
import { currentView } from './router.js';
import { closeOpenCustomSelect, initCustomSelects } from './select.js';
import { buildSegmentControl, initSegmentControls } from './components.js';
import { copyText } from './confirm.js';
import { html, formatLogTime, updateRefreshedAt } from './util.js';

export let logsState = { limit: 200, level: '', items: [], tab: 'log', events: [], eventsLoaded: false, eventsKey: '', focusText: '', focusUntil: 0 };
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
    if (logsState.tab === 'events') refreshEvents(false, true);
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
  if (logsState.tab === 'events') return refreshEvents(manual, manual);

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

const EVENT_SUCCESS_KINDS = ['updated', 'tracker_up'];
const EVENT_ERROR_KINDS = ['broken', 'tracker_down', 'check_failed', 'ui_error'];

export function eventLevel(kind) {
  if (EVENT_SUCCESS_KINDS.indexOf(kind) >= 0) return 'success';
  if (EVENT_ERROR_KINDS.indexOf(kind) >= 0) return 'error';
  return 'info';
}

export function renderEventLine(item, i) {
  const level = eventLevel(item.kind);
  const iconName = level === 'success' ? 'check' : level === 'error' ? 'warning' : 'info';
  const subject = item.subject ? html`<span class="event-subject">${item.subject}</span>` : '';
  const source = item.source === 'ui' ? html`<span class="event-source">${t('events.sourceUi')}</span>` : '';
  // ссылка только на внутренний хеш: javascript: и внешние адреса отсекаем
  const path = typeof item.app_path === 'string' ? item.app_path : '';
  // ошибка, случившаяся на самой странице логов, вела бы сюда же и ничего не открывала
  const route = path.slice(1).split('?')[0];
  const link = path.startsWith('#') && route !== 'logs' ? html`<a class="event-link" href="${path}">${t('events.open')}</a>` : '';
  return html`<div class="event-line event-${level}"><span class="log-time">${formatLogTime(item.time)}</span><button type="button" class="event-copy" data-i="${i}" title="${t('events.copy')}" aria-label="${t('events.copy')}"><span data-icon="copy"></span></button><span class="event-icon" data-icon="${iconName}"></span><span class="event-text">${item.text || ''}</span>${subject}${source}${link}</div>`;
}

export function renderEventsFeed(el, data) {
  const feed = el.querySelector('.events-feed');
  if (!feed) return;
  // data === null при возврате на страницу: рисуем из кеша
  if (data) {
    logsState.events = data.items || [];
    logsState.eventsLoaded = true;
    // опрос раз в 10 с перерисовывал ленту и сбрасывал прокрутку к найденному событию
    const key = logsState.events.map(function (item) { return item.id; }).join(',');
    if (key === logsState.eventsKey && feed.querySelector('.event-line')) {
      applyEventFocus(feed);
      return;
    }
    logsState.eventsKey = key;
  }
  if (!logsState.eventsLoaded) return;
  if (!logsState.events.length) {
    feed.innerHTML = html`<div class="log-empty">${t('events.empty')}</div>`;
    return;
  }
  const scroll = feed.scrollTop;
  let parts = '';
  logsState.events.forEach(function (item, i) { parts += renderEventLine(item, i); });
  feed.innerHTML = parts;
  feed.scrollTop = scroll;
  hydrateIcons(feed);
  applyEventFocus(feed);
}

// текст события на сервере обрезан до 1000 символов и без пробелов по краям, как в reportUIError
export function openEventsAndFocus(text) {
  logsState.focusText = String(text).slice(0, 1000).trim();
  logsState.focusUntil = Date.now() + 30000;
  const el = document.getElementById('view-logs');
  const seg = currentView === 'logs' && el ? el.querySelector('.logs-card-header .seg-ctrl') : null;
  if (!seg) {
    logsState.tab = 'events';
    location.hash = '#logs';
    return;
  }
  if (logsState.tab === 'events') {
    refreshEvents(false, true);
    return;
  }
  const btn = seg.querySelector('.seg-ctrl-btn[data-value="events"]');
  if (btn) btn.click();
}

function applyEventFocus(feed) {
  if (!logsState.focusText) return;
  // запись ошибки могла еще не дойти до сервера: ищем на следующих опросах, но недолго
  if (Date.now() > logsState.focusUntil) {
    logsState.focusText = '';
    return;
  }
  const i = logsState.events.findIndex(function (item) {
    return item.source === 'ui' && item.text === logsState.focusText;
  });
  if (i < 0) return;
  logsState.focusText = '';
  const line = feed.children[i];
  if (!line) return;
  line.classList.add('event-focus');
  // длинная ошибка выше ленты: по центру ее начало уходило за верх, крутим к началу записи
  feed.scrollTop += line.getBoundingClientRect().top - feed.getBoundingClientRect().top - 8;
}

async function copyEventText(btn) {
  const item = logsState.events[parseInt(btn.getAttribute('data-i'), 10)];
  if (!item) return;
  try {
    await copyText(item.text || '');
  } catch (_) {
    showToast(t('logs.copyFail'), 'error');
    return;
  }
  const iconEl = btn.querySelector('[data-icon]');
  iconEl.setAttribute('data-icon', 'check');
  hydrateIcons(btn);
  btn.classList.add('is-copied');
  setTimeout(function () {
    iconEl.setAttribute('data-icon', 'copy');
    hydrateIcons(btn);
    btn.classList.remove('is-copied');
  }, 1500);
}

// loud: показать ошибку первой загрузки (переключение вкладки, ручное обновление),
// фоновый опрос молчит
export async function refreshEvents(manual, loud) {
  if (currentView !== 'logs' || document.visibilityState !== 'visible') return;
  const el = document.getElementById('view-logs');
  if (!el || !el.querySelector('.events-feed')) return;

  const btn = manual ? document.getElementById('logs-refresh-btn') : null;
  if (btn) btn.classList.add('is-spinning');

  const fetchGen = ++logsFetchGen;
  try {
    const data = await fetchJSON('/api/events?limit=500');
    if (fetchGen !== logsFetchGen) return;
    renderEventsFeed(el, data);
    updateRefreshedAt('logs-last-updated', 'logs.lastUpdated');
  } catch (err) {
    if (loud && fetchGen === logsFetchGen) {
      const feed = el.querySelector('.events-feed');
      if (feed && !logsState.eventsLoaded) feed.innerHTML = html`<div class="log-empty">${err.message}</div>`;
      showToast(err.message, 'error');
    }
  } finally {
    if (btn) btn.classList.remove('is-spinning');
    updateDryRunButtonState();
  }
}

function applyLogsTab(el) {
  const events = logsState.tab === 'events';
  el.querySelector('.logs-feed').classList.toggle('hidden', events);
  el.querySelector('.events-feed').classList.toggle('hidden', !events);
  // группы только прячем: со скрытием через display шапка сжималась и переключатель прыгал по вертикали
  if (events && el.querySelector('#logs-level-group .custom-select.open, #logs-limit-group .custom-select.open')) closeOpenCustomSelect(true);
  ['logs-level-group', 'logs-limit-group'].forEach(function (id) {
    const node = el.querySelector('#' + id);
    if (!node) return;
    node.classList.toggle('is-concealed', events);
    node.toggleAttribute('inert', events);
  });
  ['logs-copy-btn', 'logs-download-btn'].forEach(function (id) {
    const node = el.querySelector('#' + id);
    if (node) node.classList.toggle('hidden', events);
  });
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

  const tabsControl = buildSegmentControl([
    { value: 'log', label: t('logs.tabLog'), title: t('logs.tabLog') },
    { value: 'events', label: t('logs.tabEvents'), title: t('logs.tabEvents') },
  ], logsState.tab, t('logs.tabsLabel'));

  el.innerHTML = html`<div class="page-header logs-page-header"><div class="page-header-row"><h1 class="page-title">${t('logs.title')}</h1><div class="page-header-actions"><span class="text-muted logs-last-updated" id="logs-last-updated"></span><button type="button" class="btn btn-sm btn-ghost" id="logs-dryrun-btn" title="${t('logs.dryRunHint')}">${t('logs.dryRun')}</button><button type="button" class="btn btn-sm btn-ghost btn-icon" id="logs-copy-btn" title="${t('logs.copy')}" aria-label="${t('logs.copy')}"><span data-icon="copy"></span></button><button type="button" class="btn btn-sm btn-ghost btn-icon" id="logs-download-btn" title="${t('logs.download')}" aria-label="${t('logs.download')}"><span data-icon="download"></span></button><button type="button" class="btn btn-sm btn-ghost btn-icon btn-refresh" id="logs-refresh-btn" aria-label="${t('logs.refresh')}"><span data-icon="refresh"></span></button></div></div></div><div class="card logs-card"><div class="card-header logs-card-header">${tabsControl}<div class="form-group logs-limit-group" id="logs-level-group"><label class="form-label" for="logs-level">${t('logs.level')}</label><select class="select" id="logs-level">${levelParts}</select></div><div class="form-group logs-limit-group" id="logs-limit-group"><label class="form-label" for="logs-limit">${t('logs.limit')}</label><select class="select" id="logs-limit">${limitParts}</select></div></div><div class="logs-feed card-body-flush"></div><div class="events-feed card-body-flush hidden"></div></div>`;

  renderLogsFeed(el, data);
  renderEventsFeed(el, null);
  applyLogsTab(el);
  hydrateIcons(el);
  initCustomSelects(el);
  initSegmentControls(el);

  const eventsFeed = el.querySelector('.events-feed');
  if (eventsFeed) {
    eventsFeed.addEventListener('click', function (e) {
      const btn = e.target.closest('.event-copy');
      if (btn) copyEventText(btn);
    });
  }

  const tabsSeg = el.querySelector('.logs-card-header .seg-ctrl');
  if (tabsSeg) {
    tabsSeg.addEventListener('segchange', function (e) {
      const tab = e.detail && e.detail.value === 'events' ? 'events' : 'log';
      if (tab === logsState.tab) return;
      logsState.tab = tab;
      applyLogsTab(el);
      // загрузка страницы могла быть отменена сменой вкладки до запуска опроса
      ensureLogsPolling();
      if (tab === 'events') refreshEvents(false, true);
      else refreshLogs(false);
    });
  }

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


