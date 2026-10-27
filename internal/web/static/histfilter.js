import { ensureQbitCategories, fetchJSON, qbitCategoriesCache } from './api.js';
import { cancelClose, closeAnimated } from './components.js';
import { loadDashboard, reloadDashboardTable } from './dashboard.js';
import { icon } from './icons.js';
import { syncDashboardFilterHash } from './router.js';
import { historyState, isMobileStackedLayout, overlayBridge } from './state.js';
import { html } from './util.js';

export let openHistFilterSelect = null;

let histFilterTrackersCache = null;

async function ensureHistFilterTrackers() {
  if (histFilterTrackersCache !== null) return;
  try {
    const list = await fetchJSON('/api/trackers');
    histFilterTrackersCache = (list || []).map(function (tr) {
      return { id: tr.id, label: tr.name || tr.key };
    });
  } catch (_) {
    histFilterTrackersCache = [];
  }
}

function histFilterTrackerValueLabel() {
  if (!historyState.tracker) return t('dash.filterAllTrackers');
  const found = (histFilterTrackersCache || []).find(function (tr) { return tr.id === historyState.tracker; });
  // кеша еще нет или трекер удалили: имя не угадываем, пишем «загрузка»
  return found ? found.label : t('common.loading');
}

function histFilterCategoryValueLabel() {
  // значение категории и есть имя из qbit, так что читается и до загрузки qbitCategoriesCache
  return historyState.category || t('dash.filterAllCategories');
}

function histFilterOptionRow(selected, label, attrHtml) {
  const checkHtml = selected
    ? html`<span class="hist-filter-check">${icon('check')}</span>`
    : html`<span class="hist-filter-check"></span>`;
  return html`<div class="hist-filter-option${selected ? ' is-selected' : ''}" role="option" aria-selected="${selected ? 'true' : 'false'}"${attrHtml}>${checkHtml}<span class="hist-filter-option-label">${label}</span></div>`;
}

function histFilterStatusOptionsHtml() {
  return getHistFilterOptions().map(function (opt) {
    const selected = isHistFilterActive(opt);
    const iconHtml = opt.icon
      ? html`<span class="hist-filter-option-icon hist-filter-option-icon-${opt.v || 'all'}">${icon(opt.icon)}</span>`
      : html`<span class="hist-filter-option-icon-spacer"></span>`;
    const checkHtml = selected
      ? html`<span class="hist-filter-check">${icon('check')}</span>`
      : html`<span class="hist-filter-check"></span>`;
    const dataAttr = opt.isIgnored
      ? html` data-filter-ignored="1"`
      : html` data-status="${opt.v}"`;
    return html`<div class="hist-filter-option${selected ? ' is-selected' : ''}" role="option" aria-selected="${selected ? 'true' : 'false'}"${dataAttr} data-active-class="${opt.activeClass}">${checkHtml}${iconHtml}<span class="hist-filter-option-label">${opt.label}</span></div>`;
  });
}

function histFilterRowHtml(group, label, value) {
  return html`<div class="hist-filter-row" data-group="${group}"><span class="hist-filter-row-label">${label}</span><span class="hist-filter-row-value">${value}</span><span class="hist-filter-row-chevron">${icon('chevronRight')}</span></div>`;
}

function histFilterMainHtml() {
  // кнопка есть всегда, чтобы шапка не прыгала при смене фильтров. Когда сбрасывать
  // нечего, она просто скрыта (как .mobile-name-filter-clear)
  const resetActive = histFilterTriggerMeta().showDot;
  const resetClass = 'btn btn-sm btn-ghost btn-icon hist-filter-reset' + (resetActive ? '' : ' is-hidden');
  const resetLabel = t('dash.filterReset');
  const resetHtml = html`<button type="button" class="${resetClass}" title="${resetLabel}" aria-label="${resetLabel}">${icon('funnelX')}</button>`;
  return html`<div class="hist-filter-panel-head"><span class="hist-filter-panel-title">${t('dash.filterTitle')}</span>${resetHtml}</div><div class="hist-filter-panel-body"><div class="hist-filter-group-label">${t('dash.col.status')}</div>${histFilterStatusOptionsHtml()}<div class="hist-filter-divider"></div>${histFilterRowHtml('tracker', t('dash.filterTrackerLabel'), histFilterTrackerValueLabel())}<div class="hist-filter-divider"></div>${histFilterRowHtml('category', t('dash.filterCategoryLabel'), histFilterCategoryValueLabel())}</div>`;
}

function histFilterTrackerGroupOptionsHtml() {
  const rows = [histFilterOptionRow(historyState.tracker === 0, t('dash.filterAllTrackers'), html` data-tracker-id="0"`)];
  (histFilterTrackersCache || []).forEach(function (tr) {
    rows.push(histFilterOptionRow(historyState.tracker === tr.id, tr.label, html` data-tracker-id="${tr.id}"`));
  });
  return rows;
}

function histFilterCategoryGroupOptionsHtml() {
  const rows = [histFilterOptionRow(historyState.category === '', t('dash.filterAllCategories'), html` data-category=""`)];
  (qbitCategoriesCache || []).forEach(function (cat) {
    rows.push(histFilterOptionRow(historyState.category === cat, cat, html` data-category="${cat}"`));
  });
  return rows;
}

function histFilterGroupHtml(group) {
  const label = group === 'tracker' ? t('dash.filterTrackerLabel') : t('dash.filterCategoryLabel');
  const options = group === 'tracker' ? histFilterTrackerGroupOptionsHtml() : histFilterCategoryGroupOptionsHtml();
  return html`<div class="hist-filter-panel-head"><button type="button" class="btn btn-sm btn-ghost btn-icon hist-filter-back" title="${t('dash.filterBack')}" aria-label="${t('dash.filterBack')}">${icon('chevronLeft')}</button><span class="hist-filter-panel-title">${label}</span></div><div class="hist-filter-panel-body">${options}</div>`;
}

const HIST_FILTER_PANEL_MAX_CEILING = 600;
const HIST_FILTER_PANEL_EDGE_MARGIN = 8;

export function positionHistFilterPanel(wrap) {
  const trigger = wrap.querySelector('.hist-filter-trigger');
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!trigger || !panel) return;
  wrap.classList.remove('open-up');
  if (isMobileStackedLayout()) {
    // нижняя шторка: позицию и предел 80vh задает CSS. Инлайновая высота с десктопа
    // перебила бы его, поэтому убираем
    panel.style.maxHeight = '';
    return;
  }
  const rect = trigger.getBoundingClientRect();
  const spaceBelow = window.innerHeight - rect.bottom;
  const spaceAbove = rect.top;
  // предел ставим до чтения высоты ниже, иначе выбор вверх или вниз разойдется
  // с тем, что реально нарисовано
  const budget = Math.max(spaceBelow, spaceAbove) - HIST_FILTER_PANEL_EDGE_MARGIN;
  panel.style.maxHeight = Math.min(Math.max(budget, 160), HIST_FILTER_PANEL_MAX_CEILING) + 'px';
  const maxH = parseFloat(getComputedStyle(panel).maxHeight) || 320;
  const panelH = Math.min(panel.scrollHeight || maxH, maxH);
  if (spaceBelow < panelH + 8 && spaceAbove > spaceBelow) wrap.classList.add('open-up');
}

function updateHistFilterScrollFade(wrap) {
  const body = wrap.querySelector('.hist-filter-panel-body');
  if (!body) return;
  // если прокручивать нечего, разница тоже около нуля и растушевка прячется
  const atEnd = body.scrollHeight - body.scrollTop - body.clientHeight <= 1;
  body.classList.toggle('is-scrolled-end', atEnd);
}

export function renderHistFilterMain(wrap) {
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!panel) return;
  delete panel.dataset.filterGroup;
  panel.innerHTML = histFilterMainHtml();
  positionHistFilterPanel(wrap);
  updateHistFilterScrollFade(wrap);
}

function renderHistFilterGroup(wrap, group) {
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!panel) return;
  panel.dataset.filterGroup = group;
  panel.innerHTML = histFilterGroupHtml(group);
  positionHistFilterPanel(wrap);
  updateHistFilterScrollFade(wrap);
}

async function ensureHistFilterExtras(wrap) {
  await Promise.all([ensureHistFilterTrackers(), ensureQbitCategories()]);
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!panel) return;
  const group = panel.dataset.filterGroup;
  if (group === 'tracker' || group === 'category') renderHistFilterGroup(wrap, group);
  else renderHistFilterMain(wrap);
}

export function getHistFilterOptions() {
  return [
    { v: '', icon: null, label: t('common.all'), activeClass: 'active-neutral' },
    { v: 'new', icon: 'plus', label: t('dash.filter.new'), activeClass: 'active-info' },
    { v: 'updated', icon: 'check', label: t('dash.filter.updated'), activeClass: 'active-success' },
    { v: 'unchanged', icon: 'minus', label: t('dash.filter.unchanged'), activeClass: 'active-muted' },
    { v: 'unknown', icon: 'help', label: t('dash.filter.unknown'), activeClass: 'active-warning' },
    { v: 'error', icon: 'xCircle', label: t('dash.filter.error'), activeClass: 'active-danger' },
    { v: 'broken', icon: 'warning', label: t('dash.filter.broken'), activeClass: 'active-broken' },
    { v: 'ignored', icon: 'eyeClosed', label: t('dash.inIgnore'), activeClass: 'active-neutral', isIgnored: true },
  ];
}

export function isHistFilterActive(opt) {
  if (opt.isIgnored) return historyState.ignored;
  return !historyState.ignored && historyState.status === opt.v;
}

export function getActiveHistFilter() {
  const opts = getHistFilterOptions();
  for (let i = 0; i < opts.length; i++) {
    if (isHistFilterActive(opts[i])) return opts[i];
  }
  return opts[0];
}

export function finishHistFilterClose(wrap) {
  if (!wrap) return;
  cancelClose(wrap);
  const panel = wrap.querySelector('.hist-filter-panel');
  wrap.classList.remove('open', 'open-up');
  // hidden ставим только тут, после анимации: ей нужна нарисованная панель.
  // До тех пор ее держит is-closing
  if (panel) panel.hidden = true;
}

export function startHistFilterClose(wrap) {
  if (!wrap) return;
  // как бы ни закрывали попап, открытый внутри селект иначе висел бы
  // невидимым до следующего открытия
  const openSelect = overlayBridge.getOpenCustomSelect();
  if (openSelect && wrap.contains(openSelect)) overlayBridge.closeCustomSelect();
  const trigger = wrap.querySelector('.hist-filter-trigger');
  const panel = wrap.querySelector('.hist-filter-panel');
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  if (openHistFilterSelect === wrap) openHistFilterSelect = null;
  const wasOpen = wrap.classList.contains('open');
  wrap.classList.remove('open');
  if (!wasOpen || !panel || panel.hidden) {
    finishHistFilterClose(wrap);
    return;
  }
  closeAnimated(wrap, function () { finishHistFilterClose(wrap); });
}

export function closeOpenHistFilterSelect() {
  if (!openHistFilterSelect) return;
  startHistFilterClose(openHistFilterSelect);
}

function histFilterTriggerMeta() {
  const active = getActiveHistFilter();
  const isAllStatus = active.v === '' && !active.isIgnored;
  const extraActive = !!historyState.tracker || !!historyState.category;
  return {
    activeClass: !isAllStatus ? active.activeClass : (extraActive ? 'active-neutral' : ''),
    showDot: !isAllStatus || extraActive,
    label: isAllStatus ? t('dash.filter') : t('dash.filterActive', { value: active.label }),
  };
}

function refreshHistFilterTrigger(wrap) {
  // без полной перерисовки: она сбросила бы открытую панель
  const trigger = wrap.querySelector('.hist-filter-trigger');
  if (!trigger) return;
  const meta = histFilterTriggerMeta();
  trigger.className = 'btn btn-sm btn-ghost btn-icon hist-filter-trigger' +
    (meta.activeClass ? ' ' + meta.activeClass : '');
  trigger.setAttribute('title', meta.label);
  trigger.setAttribute('aria-label', meta.label);
  let dot = trigger.querySelector('.hist-filter-dot');
  if (meta.showDot && !dot) {
    dot = document.createElement('span');
    dot.className = 'hist-filter-dot';
    dot.setAttribute('aria-hidden', 'true');
    trigger.appendChild(dot);
  } else if (!meta.showDot && dot) {
    dot.remove();
  }
}

export function buildHistFilters() {
  const meta = histFilterTriggerMeta();
  const triggerClass = 'btn btn-sm btn-ghost btn-icon hist-filter-trigger' +
    (meta.activeClass ? ' ' + meta.activeClass : '');
  const dotHtml = meta.showDot ? html`<span class="hist-filter-dot" aria-hidden="true"></span>` : '';
  const triggerLabel = meta.label;
  return html`<div class="hist-filter-select"><button type="button" class="${triggerClass}" aria-haspopup="dialog" aria-expanded="false" title="${triggerLabel}" aria-label="${triggerLabel}"><span data-icon="funnel"></span>${dotHtml}</button><div class="hist-filter-backdrop"></div><div class="hist-filter-panel" hidden>${histFilterMainHtml()}</div></div>`;
}

export function bindHistFilterSelect(root) {
  const wrap = (root || document).querySelector('.hist-filter-select');
  if (!wrap || wrap.dataset.histFilterBound) return;
  wrap.dataset.histFilterBound = '1';

  const trigger = wrap.querySelector('.hist-filter-trigger');
  const panel = wrap.querySelector('.hist-filter-panel');
  const backdrop = wrap.querySelector('.hist-filter-backdrop');
  if (!trigger || !panel) return;

  // scroll не всплывает, но предки ловят его на перехвате. Вешаем один раз тут,
  // renderHistFilterMain/Group каждый раз меняют тело
  panel.addEventListener('scroll', function (e) {
    if (e.target.classList && e.target.classList.contains('hist-filter-panel-body')) {
      updateHistFilterScrollFade(wrap);
    }
  }, true);

  // затемнение видно только на мобильных, на десктопе CSS его прячет.
  // Тап по нему закрывает шторку, как тап мимо
  if (backdrop) {
    backdrop.addEventListener('click', function () {
      closePanel();
    });
  }

  function closePanel() {
    startHistFilterClose(wrap);
  }

  function openPanel() {
    overlayBridge.closeCustomSelect();
    closeOpenHistFilterSelect();
    overlayBridge.closeNameFilter();
    cancelClose(wrap);
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openHistFilterSelect = wrap;
    // всегда с главного вида, даже если в прошлый раз остался открыт список групп
    renderHistFilterMain(wrap);
    ensureHistFilterExtras(wrap);
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

  panel.addEventListener('click', function (e) {
    if (e.target.closest('.hist-filter-back')) {
      e.stopPropagation();
      renderHistFilterMain(wrap);
      return;
    }

    if (e.target.closest('.hist-filter-reset')) {
      e.stopPropagation();
      if (!histFilterTriggerMeta().showDot) return;

      historyState.status = '';
      historyState.ignored = false;
      historyState.tracker = 0;
      historyState.category = '';

      closePanel();

      historyState.offset = 0;
      syncDashboardFilterHash();
      loadDashboard();
      return;
    }

    const row = e.target.closest('.hist-filter-row');
    if (row) {
      e.stopPropagation();
      renderHistFilterGroup(wrap, row.getAttribute('data-group'));
      return;
    }

    const opt = e.target.closest('.hist-filter-option');
    if (!opt) return;
    e.stopPropagation();

    if (opt.hasAttribute('data-tracker-id')) {
      const value = parseInt(opt.getAttribute('data-tracker-id'), 10) || 0;
      if (historyState.tracker !== value) {
        historyState.tracker = value;
        historyState.offset = 0;
        refreshHistFilterTrigger(wrap);
        reloadDashboardTable();
      }
      renderHistFilterMain(wrap);
      return;
    }

    if (opt.hasAttribute('data-category')) {
      const value = opt.getAttribute('data-category') || '';
      if (historyState.category !== value) {
        historyState.category = value;
        historyState.offset = 0;
        refreshHistFilterTrigger(wrap);
        reloadDashboardTable();
      }
      renderHistFilterMain(wrap);
      return;
    }

    let changed = false;

    if (opt.getAttribute('data-filter-ignored') === '1') {
      changed = !historyState.ignored;
      historyState.ignored = true;
      historyState.status = '';
    } else if (opt.hasAttribute('data-status')) {
      const status = opt.getAttribute('data-status') || '';
      changed = historyState.ignored || historyState.status !== status;
      historyState.ignored = false;
      historyState.status = status;
    } else {
      return;
    }

    closePanel();
    if (!changed) return;

    historyState.offset = 0;
    syncDashboardFilterHash();
    loadDashboard();
  });
}
