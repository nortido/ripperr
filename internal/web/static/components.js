import { ensureQbitCategories, fetchJSON, qbitCategoriesCache, showToast } from './api.js';
import { buildHistoryQuery, dashboardFetchGen, loadDashboard, nextDashboardFetchGen, reloadDashboardTable, syncPlanForceLocal, updateDashboardTable } from './dashboard.js';
import { ICONS, hydrateIcons, icon } from './icons.js';
import { syncDashboardFilterHash } from './router.js';
import { historyState } from './state.js';
import { PAGE_SIZE_OPTIONS, formatRunSlot, html, savePageSize } from './util.js';

export let activeErrorPopover = null;
export let activeSlotsPopover = null;
export let slotsPopoverLeaveTimer = null;

export let openCustomSelect = null;
export let openHistFilterSelect = null;
export let openNameFilterWrap = null;
export let nameFilterDebounceTimer = null;

export function closeOpenCustomSelect() {
  if (!openCustomSelect) return;
  const trigger = openCustomSelect.querySelector('.custom-select-trigger');
  const panel = openCustomSelect.querySelector('.custom-select-panel');
  openCustomSelect.classList.remove('open', 'open-up');
  if (panel) panel.hidden = true;
  if (trigger) trigger.setAttribute('aria-expanded', 'false');
  openCustomSelect = null;
}

export function initCustomSelect(selectEl) {
  if (!selectEl || selectEl.dataset.customSelectInit) return;
  selectEl.dataset.customSelectInit = '1';

  const wrap = document.createElement('div');
  wrap.className = 'custom-select';
  selectEl.parentNode.insertBefore(wrap, selectEl);
  wrap.appendChild(selectEl);
  selectEl.classList.add('custom-select-native');

  const trigger = document.createElement('button');
  trigger.type = 'button';
  trigger.className = 'custom-select-trigger';
  trigger.setAttribute('aria-haspopup', 'listbox');
  trigger.setAttribute('aria-expanded', 'false');
  if (selectEl.id) trigger.id = selectEl.id + '-trigger';

  const labelEl = selectEl.id
    ? document.querySelector('label[for="' + CSS.escape(selectEl.id) + '"]')
    : null;
  if (labelEl && trigger.id) labelEl.setAttribute('for', trigger.id);

  const triggerLabel = document.createElement('span');
  triggerLabel.className = 'custom-select-value';
  const chevron = document.createElement('span');
  chevron.className = 'custom-select-chevron';
  chevron.innerHTML = ICONS.chevron;
  trigger.appendChild(triggerLabel);
  trigger.appendChild(chevron);

  const panel = document.createElement('div');
  panel.className = 'custom-select-panel';
  panel.setAttribute('role', 'listbox');
  panel.hidden = true;

  wrap.appendChild(trigger);
  wrap.appendChild(panel);

  let highlightIdx = selectEl.selectedIndex;

  function renderOptions() {
    panel.innerHTML = '';
    const curVal = selectEl.value;
    Array.from(selectEl.options).forEach(function (opt, idx) {
      const item = document.createElement('div');
      item.className = 'custom-select-option' + (opt.value === curVal ? ' is-selected' : '');
      item.setAttribute('role', 'option');
      item.setAttribute('aria-selected', opt.value === curVal ? 'true' : 'false');
      item.dataset.index = String(idx);
      if (opt.disabled) item.setAttribute('aria-disabled', 'true');
      if (opt.value === curVal) {
        item.innerHTML = html`<span class="custom-select-check">${icon('check')}</span><span>${opt.textContent}</span>`;
      } else {
        item.textContent = opt.textContent;
      }
      if (!opt.disabled) {
        item.addEventListener('click', function (e) {
          e.stopPropagation();
          choose(idx);
        });
      }
      panel.appendChild(item);
    });
    hydrateIcons(panel);
  }

  function syncTrigger() {
    const opt = selectEl.options[selectEl.selectedIndex];
    triggerLabel.textContent = opt ? opt.textContent : '';
    trigger.disabled = selectEl.disabled;
    renderOptions();
  }

  function choose(idx) {
    if (idx < 0 || idx >= selectEl.options.length || selectEl.options[idx].disabled) return;
    selectEl.selectedIndex = idx;
    selectEl.dispatchEvent(new Event('change', { bubbles: true }));
    syncTrigger();
    closePanel();
  }

  function closePanel() {
    wrap.classList.remove('open', 'open-up');
    trigger.setAttribute('aria-expanded', 'false');
    panel.hidden = true;
    if (openCustomSelect === wrap) openCustomSelect = null;
  }

  function positionPanel() {
    wrap.classList.remove('open-up');
    const rect = trigger.getBoundingClientRect();
    const panelH = Math.min(panel.scrollHeight || 320, 320);
    const spaceBelow = window.innerHeight - rect.bottom;
    const spaceAbove = rect.top;
    if (spaceBelow < panelH + 8 && spaceAbove > spaceBelow) wrap.classList.add('open-up');
  }

  function scrollToIndex(idx) {
    const el = panel.querySelector('[data-index="' + idx + '"]');
    if (el) el.scrollIntoView({ block: 'nearest' });
  }

  function highlightOption() {
    panel.querySelectorAll('.custom-select-option').forEach(function (el) {
      el.classList.toggle('is-highlighted', parseInt(el.dataset.index, 10) === highlightIdx);
    });
  }

  function openPanel() {
    closeOpenCustomSelect();
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openCustomSelect = wrap;
    highlightIdx = selectEl.selectedIndex;
    renderOptions();
    positionPanel();
    scrollToIndex(highlightIdx);
    highlightOption();
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
      else choose(highlightIdx);
      return;
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      if (!wrap.classList.contains('open')) {
        openPanel();
        return;
      }
      const step = e.key === 'ArrowDown' ? 1 : -1;
      let next = highlightIdx + step;
      while (next >= 0 && next < selectEl.options.length && selectEl.options[next].disabled) {
        next += step;
      }
      if (next >= 0 && next < selectEl.options.length) {
        highlightIdx = next;
        highlightOption();
        scrollToIndex(highlightIdx);
      }
    }
  });

  selectEl.addEventListener('change', syncTrigger);
  const valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');
  if (valueDesc && valueDesc.set) {
    Object.defineProperty(selectEl, 'value', {
      configurable: true,
      get: function () { return valueDesc.get.call(this); },
      set: function (v) {
        valueDesc.set.call(this, v);
        syncTrigger();
      },
    });
  }
  syncTrigger();
}

export function initCustomSelects(root) {
  (root || document).querySelectorAll('select.select:not([data-custom-select-init])').forEach(initCustomSelect);
}


export function bindErrorBadges(root) {
  root.querySelectorAll('.status-error-badge').forEach(function (badge) {
    badge.addEventListener('click', function (e) {
      e.stopPropagation();
      const err = badge.getAttribute('data-error') || '';
      closePopovers();
      const pop = document.createElement('div');
      pop.className = 'error-popover';
      pop.textContent = err;
      document.body.appendChild(pop);
      const rect = badge.getBoundingClientRect();
      pop.style.top = (rect.bottom + window.scrollY + 4) + 'px';
      pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
      activeErrorPopover = pop;
    });
  });
}

export function showSlotsPopover(card, slots) {
  if (slotsPopoverLeaveTimer) {
    clearTimeout(slotsPopoverLeaveTimer);
    slotsPopoverLeaveTimer = null;
  }
  closePopovers();
  const pop = document.createElement('div');
  pop.className = 'slots-popover';
  slots.forEach(function (s) {
    const item = document.createElement('div');
    item.className = 'slots-popover-item';
    item.textContent = formatRunSlot(s);
    pop.appendChild(item);
  });
  document.body.appendChild(pop);
  const rect = card.getBoundingClientRect();
  pop.style.top = (rect.bottom + window.scrollY + 6) + 'px';
  pop.style.left = Math.min(rect.left + window.scrollX, window.innerWidth - pop.offsetWidth - 8) + 'px';
  activeSlotsPopover = pop;
  pop.addEventListener('mouseenter', function () {
    if (slotsPopoverLeaveTimer) {
      clearTimeout(slotsPopoverLeaveTimer);
      slotsPopoverLeaveTimer = null;
    }
  });
  pop.addEventListener('mouseleave', function () {
    if (activeSlotsPopover) {
      activeSlotsPopover.remove();
      activeSlotsPopover = null;
    }
  });
}

export function bindNextRunStatCard(root, slots) {
  const card = root.querySelector('.stat-card-next-run[data-has-slots-popover]');
  if (!card || !slots || slots.length <= 2) return;

  card.addEventListener('mouseenter', function () {
    showSlotsPopover(card, slots);
  });
  card.addEventListener('mouseleave', function () {
    slotsPopoverLeaveTimer = setTimeout(function () {
      if (activeSlotsPopover) {
        activeSlotsPopover.remove();
        activeSlotsPopover = null;
      }
    }, 120);
  });
  card.addEventListener('click', function (e) {
    e.stopPropagation();
    if (activeSlotsPopover) {
      closePopovers();
    } else {
      showSlotsPopover(card, slots);
    }
  });
}

export function closePopovers() {
  if (activeErrorPopover) {
    activeErrorPopover.remove();
    activeErrorPopover = null;
  }
  if (activeSlotsPopover) {
    activeSlotsPopover.remove();
    activeSlotsPopover = null;
  }
}

export function initGlobalDismiss() {
  // width changes (rotation, devtools, desktop<->mobile breakpoint) can happen
  // while the panel is open; re-run the same logic that open/content-switch
  // already uses so a stale desktop inline max-height never survives into
  // the mobile bottom sheet (or vice versa). Deferred a frame, same as
  // layout.js's own resize handling, so html.mobile-layout has already been
  // flipped (initLayout's listener is bound first, layout.js) by the time
  // this reads it.
  window.addEventListener('resize', function () {
    if (!openHistFilterSelect) return;
    requestAnimationFrame(function () {
      if (openHistFilterSelect) positionHistFilterPanel(openHistFilterSelect);
    });
  });

  document.addEventListener('click', function (e) {
    closePopovers();
    if (openCustomSelect && !openCustomSelect.contains(e.target)) closeOpenCustomSelect();
    if (openHistFilterSelect && !openHistFilterSelect.contains(e.target)) closeOpenHistFilterSelect();
    if (openNameFilterWrap && !openNameFilterWrap.contains(e.target)) closeOpenNameFilter();
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      const histPanel = openHistFilterSelect && openHistFilterSelect.querySelector('.hist-filter-panel');
      if (openCustomSelect) {
        // a select layer open elsewhere on the page absorbs the first Escape
        closeOpenCustomSelect();
      } else if (histPanel && histPanel.dataset.filterGroup) {
        // a group list open inside the filter popup absorbs the first Escape;
        // the popup itself only closes on the next press
        renderHistFilterMain(openHistFilterSelect);
      } else {
        closeOpenHistFilterSelect();
      }
      closeOpenNameFilter();
    }
  });
}


export function initChipInput(chipEl, hintsEl, initialValues, suggestions, onChange) {
  const chips = (initialValues || []).slice();
  const chipsEl = chipEl.querySelector('.chip-input-chips');
  const input = chipEl.querySelector('.chip-input-field');

  function notifyChange() {
    if (onChange) onChange(chips.slice());
  }

  function renderChips() {
    chipsEl.innerHTML = '';
    chips.forEach(function (val) {
      const chip = document.createElement('span');
      chip.className = 'chip';
      chip.innerHTML = html`<span class="chip-label">${val}</span><button type="button" class="chip-remove" aria-label="${t('common.remove')}">&times;</button>`;
      chip.querySelector('.chip-remove').addEventListener('click', function () {
        const i = chips.indexOf(val);
        if (i !== -1) chips.splice(i, 1);
        renderChips();
        renderHints();
        notifyChange();
      });
      chipsEl.appendChild(chip);
    });
  }

  function addChip(raw) {
    const val = String(raw || '').trim();
    if (!val || chips.indexOf(val) !== -1) return;
    chips.push(val);
    renderChips();
    renderHints();
    input.value = '';
    notifyChange();
  }

  function renderHints() {
    if (!hintsEl) return;
    hintsEl.innerHTML = '';
    (suggestions || []).forEach(function (cat) {
      const selected = chips.indexOf(cat) !== -1;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'chip-hint' + (selected ? ' chip-hint-selected' : '');
      btn.textContent = cat;
      btn.disabled = selected;
      if (!selected) {
        btn.addEventListener('click', function () { addChip(cat); });
      }
      hintsEl.appendChild(btn);
    });
  }

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addChip(input.value.replace(/,/g, ''));
    } else if (e.key === 'Backspace' && !input.value && chips.length) {
      chips.pop();
      renderChips();
      renderHints();
      notifyChange();
    }
  });

  input.addEventListener('blur', function () {
    if (input.value.trim()) addChip(input.value);
  });

  renderChips();
  renderHints();

  return {
    getValues: function () { return chips.slice(); },
    setSuggestions: function (list) {
      suggestions = list || [];
      renderHints();
    },
  };
}


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
  // cache not loaded yet (or the tracker was since removed): fall back rather than guess a name
  return found ? found.label : t('common.loading');
}

function histFilterCategoryValueLabel() {
  // category values are the qbit category names themselves, so this reads fine
  // even before qbitCategoriesCache has loaded
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
  // always rendered so the head's height never jumps when filters get set/cleared;
  // when nothing to reset it's just hidden (same trick as .mobile-name-filter-clear)
  const resetActive = histFilterTriggerMeta().showDot;
  const resetClass = 'btn btn-sm btn-ghost btn-icon hist-filter-reset' + (resetActive ? '' : ' is-hidden');
  const resetLabel = t('dash.filterReset');
  const resetHtml = html`<button type="button" class="${resetClass}" title="${resetLabel}" aria-label="${resetLabel}">${icon('x')}</button>`;
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

// how tall the panel is allowed to get is recomputed every time it's shown
// or its content changes, from real available space, not a fixed number
const HIST_FILTER_PANEL_MAX_CEILING = 600;
const HIST_FILTER_PANEL_EDGE_MARGIN = 8;

function positionHistFilterPanel(wrap) {
  const trigger = wrap.querySelector('.hist-filter-trigger');
  const panel = wrap.querySelector('.hist-filter-panel');
  if (!trigger || !panel) return;
  wrap.classList.remove('open-up');
  if (isMobileStackedLayout()) {
    // bottom sheet: CSS alone drives position and the 80vh cap; clear any
    // inline height left over from a desktop session, it would win over CSS
    panel.style.maxHeight = '';
    return;
  }
  const rect = trigger.getBoundingClientRect();
  const spaceBelow = window.innerHeight - rect.bottom;
  const spaceAbove = rect.top;
  // set the inline height cap before reading it back below, so the
  // open-up/open-down decision and the actual rendered height agree
  const budget = Math.max(spaceBelow, spaceAbove) - HIST_FILTER_PANEL_EDGE_MARGIN;
  panel.style.maxHeight = Math.min(Math.max(budget, 160), HIST_FILTER_PANEL_MAX_CEILING) + 'px';
  const maxH = parseFloat(getComputedStyle(panel).maxHeight) || 320;
  const panelH = Math.min(panel.scrollHeight || maxH, maxH);
  if (spaceBelow < panelH + 8 && spaceAbove > spaceBelow) wrap.classList.add('open-up');
}

// hides the bottom fade once scrolled (or trivially, when there's nothing to
// scroll) since scrollHeight - scrollTop - clientHeight is ~0 in both cases
function updateHistFilterScrollFade(wrap) {
  const body = wrap.querySelector('.hist-filter-panel-body');
  if (!body) return;
  const atEnd = body.scrollHeight - body.scrollTop - body.clientHeight <= 1;
  body.classList.toggle('is-scrolled-end', atEnd);
}

function renderHistFilterMain(wrap) {
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
    { v: 'updated', icon: 'check', label: t('dash.filter.updated'), activeClass: 'active-success' },
    { v: 'unchanged', icon: 'minus', label: t('dash.filter.unchanged'), activeClass: 'active-muted' },
    { v: 'unknown', icon: 'help', label: t('dash.filter.unknown'), activeClass: 'active-warning' },
    { v: 'error', icon: 'xCircle', label: t('dash.filter.error'), activeClass: 'active-danger' },
    { v: 'broken', icon: 'warning', label: t('dash.filter.broken'), activeClass: 'active-warning' },
    { v: 'ignored', icon: 'eyeOff', label: t('dash.inIgnore'), activeClass: 'active-ignored', isIgnored: true },
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

export const HIST_FILTER_CLOSE_MS = 130;

// The closing animation needs the panel to stay rendered, so `hidden` is set
// only after it finishes; `is-closing` keeps it visible in the meantime.
export function finishHistFilterClose(wrap) {
  if (!wrap) return;
  if (wrap.histFilterCloseTimer) {
    clearTimeout(wrap.histFilterCloseTimer);
    wrap.histFilterCloseTimer = 0;
  }
  const panel = wrap.querySelector('.hist-filter-panel');
  wrap.classList.remove('open', 'is-closing', 'open-up');
  if (panel) panel.hidden = true;
}

export function startHistFilterClose(wrap) {
  if (!wrap) return;
  // whatever path closes the popup, a select layer left open inside it would
  // otherwise stay open (invisibly) for the next time the popup opens
  if (openCustomSelect && wrap.contains(openCustomSelect)) closeOpenCustomSelect();
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
  wrap.classList.add('is-closing');
  if (wrap.histFilterCloseTimer) clearTimeout(wrap.histFilterCloseTimer);
  wrap.histFilterCloseTimer = setTimeout(function () {
    finishHistFilterClose(wrap);
  }, HIST_FILTER_CLOSE_MS + 30);
}

export function closeOpenHistFilterSelect() {
  if (!openHistFilterSelect) return;
  startHistFilterClose(openHistFilterSelect);
}

export function isMobileStackedLayout() {
  return document.documentElement.classList.contains('mobile-layout');
}

export function nameFilterHasValue() {
  if ((historyState.name || '').trim()) return true;
  if (openNameFilterWrap) {
    const input = openNameFilterWrap.querySelector('.name-filter-input, .mobile-name-filter-input');
    if (input && input.value.trim()) return true;
  }
  return false;
}

export const MOBILE_SEARCH_CLOSE_MS = 320;
export let mobileSearchAnimToken = 0;

export function closeMobileNameSearchPanel(root, force) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  if (!force && nameFilterHasValue()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  if (!header || !slot) return;

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  function finishClose() {
    if (token !== mobileSearchAnimToken) return;
    header.classList.remove('is-search-animating');
    if (openNameFilterWrap === slot) openNameFilterWrap = null;
  }

  if (!header.classList.contains('is-search-expanded') && !slot.classList.contains('is-open')) {
    finishClose();
    return;
  }

  if (btn) btn.setAttribute('aria-expanded', 'false');
  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded');

  if (!isMobileStackedLayout()) {
    finishClose();
    return;
  }

  header.classList.add('is-search-animating');

  const shift = scope.querySelector('.card-header-toolbar-shift');
  let done = false;
  function onShiftTransitionEnd(e) {
    if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
    if (done) return;
    done = true;
    shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }
  if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
  setTimeout(function () {
    if (done) return;
    done = true;
    if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
    finishClose();
  }, MOBILE_SEARCH_CLOSE_MS + 40);
}

export function openMobileNameSearchPanel(root, focus) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || !isMobileStackedLayout()) return;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  if (!header || !slot || !btn || !input) return;
  if (header.classList.contains('is-search-animating')) return;
  closeOpenCustomSelect();
  closeOpenHistFilterSelect();
  if (openNameFilterWrap && !openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeOpenNameFilter(true);
  }

  mobileSearchAnimToken += 1;
  const token = mobileSearchAnimToken;

  if (slot.classList.contains('is-open') && header.classList.contains('is-search-expanded')) {
    openNameFilterWrap = slot;
    input.value = historyState.name;
    btn.setAttribute('aria-expanded', 'true');
    if (focus) {
      input.focus();
      const len = input.value.length;
      input.setSelectionRange(len, len);
    }
    return;
  }

  slot.classList.remove('is-open');
  header.classList.remove('is-search-expanded', 'is-search-animating');
  header.classList.add('is-search-animating');
  btn.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = slot;
  input.value = historyState.name;

  requestAnimationFrame(function () {
    requestAnimationFrame(function () {
      if (token !== mobileSearchAnimToken) return;
      header.classList.add('is-search-expanded');
      slot.classList.add('is-open');
      const shift = scope.querySelector('.card-header-toolbar-shift');
      let done = false;
      function finishOpen() {
        if (token !== mobileSearchAnimToken) return;
        header.classList.remove('is-search-animating');
      }
      function onShiftTransitionEnd(e) {
        if (!shift || e.target !== shift || e.propertyName !== 'transform') return;
        if (done) return;
        done = true;
        shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }
      if (shift) shift.addEventListener('transitionend', onShiftTransitionEnd);
      setTimeout(function () {
        if (done) return;
        done = true;
        if (shift) shift.removeEventListener('transitionend', onShiftTransitionEnd);
        finishOpen();
      }, MOBILE_SEARCH_CLOSE_MS + 40);
      if (focus) {
        input.focus();
        const len = input.value.length;
        input.setSelectionRange(len, len);
      }
    });
  });
}

export function closeDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  if (!wrap) return;
  wrap.classList.remove('is-open');
  if (label) label.setAttribute('aria-expanded', 'false');
  if (openNameFilterWrap === wrap) openNameFilterWrap = null;
}

export function openDesktopNameFilterPanel(root) {
  const scope = root || document.getElementById('view-dashboard');
  if (!scope || isMobileStackedLayout()) return;
  const wrap = scope.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!wrap || !label || !input) return;
  closeMobileNameSearchPanel(scope, true);
  wrap.classList.add('is-open');
  label.setAttribute('aria-expanded', 'true');
  openNameFilterWrap = wrap;
  input.value = historyState.name;
}

export function syncNameFilterLayout() {
  if (!(historyState.name || '').trim()) return;
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) return;
  if (isMobileStackedLayout()) {
    closeDesktopNameFilterPanel(el);
    openMobileNameSearchPanel(el, false);
  } else {
    closeMobileNameSearchPanel(el, true);
    openDesktopNameFilterPanel(el);
  }
  updateNameFilterHeader(el);
}

export function closeOpenNameFilter(force) {
  if (!openNameFilterWrap) return;
  if (!force && nameFilterHasValue()) return;
  if (openNameFilterWrap.classList.contains('mobile-name-search-slot')) {
    closeMobileNameSearchPanel(document.getElementById('view-dashboard'), true);
  } else {
    const label = openNameFilterWrap.querySelector('.th-name-filter-label');
    openNameFilterWrap.classList.remove('is-open');
    if (label) label.setAttribute('aria-expanded', 'false');
    openNameFilterWrap = null;
  }
  if (nameFilterDebounceTimer) {
    clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = null;
  }
}

export async function applyNameFilter(value) {
  const trimmed = (value || '').trim().slice(0, 200);
  if (trimmed === historyState.name) return;
  const keepOpen = openNameFilterWrap !== null;
  historyState.name = trimmed;
  historyState.offset = 0;
  syncDashboardFilterHash();
  const el = document.getElementById('view-dashboard');
  if (!el || !el.querySelector('.table-dash-releases')) {
    loadDashboard();
    return;
  }
  const fetchGen = nextDashboardFetchGen();
  try {
    const history = await fetchJSON(buildHistoryQuery());
    if (fetchGen !== dashboardFetchGen) return;
    syncPlanForceLocal(history.items || []);
    updateDashboardTable(el, history);
    updateNameFilterHeader(el);
    if (keepOpen || trimmed) restoreNameFilterPanel(el);
  } catch (err) {
    showToast(err.message, 'error');
  }
}

export function updateNameFilterHeader(el) {
  const th = el.querySelector('th.col-name');
  const wrap = el.querySelector('.name-filter-wrap');
  const label = wrap && wrap.querySelector('.th-name-filter-label');
  const input = wrap && wrap.querySelector('.name-filter-input');
  if (!th || !wrap || !label) return;
  const active = !!historyState.name;
  th.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  label.setAttribute('title', title);
  if (input) input.value = historyState.name;
  let funnel = label.querySelector('.th-name-filter-icon-active');
  if (active && !funnel) {
    funnel = document.createElement('span');
    funnel.className = 'th-name-filter-icon th-name-filter-icon-active';
    funnel.setAttribute('data-icon', 'funnel');
    funnel.setAttribute('aria-hidden', 'true');
    label.appendChild(funnel);
    hydrateIcons(label);
  } else if (!active && funnel) {
    funnel.remove();
  }
  updateMobileNameSearchUI(el);
}

export function updateMobileNameSearchUI(el) {
  const root = el || document.getElementById('view-dashboard');
  if (!root) return;
  const btn = root.querySelector('#dash-mobile-name-search-btn');
  const slot = root.querySelector('.mobile-name-search-slot');
  const header = root.querySelector('.releases-card-header');
  const input = root.querySelector('.mobile-name-filter-input');
  if (!btn || !slot) return;
  const active = !!historyState.name;
  btn.classList.toggle('is-active', active);
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  btn.setAttribute('title', title);
  btn.setAttribute('aria-label', title);
  let dot = btn.querySelector('.mobile-name-search-dot');
  if (active && !dot) {
    dot = document.createElement('span');
    dot.className = 'mobile-name-search-dot';
    dot.setAttribute('aria-hidden', 'true');
    btn.appendChild(dot);
  } else if (!active && dot) {
    dot.remove();
  }
  if (input) input.value = historyState.name;
  if (header && active && isMobileStackedLayout()) header.classList.add('is-search-expanded');
  else if (header && !active && !slot.classList.contains('is-open')) {
    header.classList.remove('is-search-expanded');
  }
  if (active && isMobileStackedLayout()) slot.classList.add('is-open');
}

export function restoreNameFilterPanel(el) {
  if (isMobileStackedLayout()) {
    openMobileNameSearchPanel(el, true);
    return;
  }
  openDesktopNameFilterPanel(el);
  const input = el.querySelector('.name-filter-input');
  if (input) {
    input.focus();
    const len = input.value.length;
    input.setSelectionRange(len, len);
  }
}

export function buildNameColumnHeader() {
  const active = !!historyState.name;
  const thClass = 'col-name' + (active ? ' is-active' : '');
  const label = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.col.name');
  const activeIconHtml = active
    ? html`<span class="th-name-filter-icon th-name-filter-icon-active" data-icon="funnel" aria-hidden="true"></span>`
    : '';
  return html`<th class="${thClass}"><div class="name-filter-wrap"><span class="th-name-filter-label" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="${label}"><span class="th-name-filter-text">${t('dash.col.name')}</span><span class="th-name-filter-search" data-icon="search" aria-hidden="true"></span>${activeIconHtml}</span><div class="name-filter-inline"><input type="text" class="name-filter-input" value="${historyState.name}" placeholder="${t('dash.nameFilterPlaceholder')}" autocomplete="off"><button type="button" class="name-filter-clear" title="${t('dash.nameFilterClear')}" aria-label="${t('dash.nameFilterClear')}"><span data-icon="x"></span></button></div></div></th>`;
}

export function bindNameFilter(root) {
  const wrap = (root || document).querySelector('.name-filter-wrap');
  if (!wrap || wrap.dataset.nameFilterBound) return;
  wrap.dataset.nameFilterBound = '1';

  const label = wrap.querySelector('.th-name-filter-label');
  const input = wrap.querySelector('.name-filter-input');
  const clearBtn = wrap.querySelector('.name-filter-clear');
  if (!label || !input) return;

  function closePanel(force) {
    if (!force && nameFilterHasValue()) return;
    wrap.classList.remove('is-open');
    label.setAttribute('aria-expanded', 'false');
    if (openNameFilterWrap === wrap) openNameFilterWrap = null;
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
  }

  function openPanel() {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    if (openNameFilterWrap && openNameFilterWrap !== wrap) closeOpenNameFilter();
    wrap.classList.add('is-open');
    label.setAttribute('aria-expanded', 'true');
    openNameFilterWrap = wrap;
    input.value = historyState.name;
    input.focus();
    input.select();
  }

  label.addEventListener('click', function (e) {
    e.stopPropagation();
    if (wrap.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  label.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      if (!wrap.classList.contains('is-open')) openPanel();
      else closePanel();
    }
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && !wrap.classList.contains('is-open') && !isMobileStackedLayout()) {
    openPanel();
  }
}

export function buildMobileNameSearchSlot() {
  const active = !!historyState.name;
  const slotCls = 'mobile-name-search-slot' + (active ? ' is-open' : '');
  const btnCls = 'btn btn-sm btn-ghost btn-icon btn-mobile-name-search' + (active ? ' is-active' : '');
  const title = active
    ? t('dash.nameFilterActive', { value: historyState.name })
    : t('dash.nameFilterPlaceholder');
  const dotHtml = active ? html`<span class="mobile-name-search-dot" aria-hidden="true"></span>` : '';
  const headerExpanded = active ? ' is-search-expanded' : '';
  return {
    headerClass: headerExpanded,
    // .mobile-name-search-toggle is a sibling of .mobile-name-search-slot
    // (not nested inside it): the search button and the close button must
    // sit in a fixed-position box that is completely independent of the
    // search-field's own animated geometry, so the search button is
    // physically unable to move.
    html: html`<div class="${slotCls}"><div class="mobile-name-search-field"><input type="text" class="name-filter-input mobile-name-filter-input" value="${historyState.name}" placeholder="${t('dash.nameFilterPlaceholder')}" autocomplete="off"></div></div><div class="mobile-name-search-toggle"><button type="button" class="btn btn-sm btn-icon name-filter-clear mobile-name-filter-clear" title="${t('dash.nameFilterClear')}" aria-label="${t('dash.nameFilterClear')}"><span data-icon="x"></span></button><button type="button" class="${btnCls}" id="dash-mobile-name-search-btn" aria-haspopup="true" aria-expanded="${active ? 'true' : 'false'}" title="${title}" aria-label="${title}"><span data-icon="search"></span>${dotHtml}</button></div>`,
  };
}

export function bindMobileNameSearch(root) {
  const scope = root || document;
  const header = scope.querySelector('.releases-card-header');
  const slot = scope.querySelector('.mobile-name-search-slot');
  const btn = scope.querySelector('#dash-mobile-name-search-btn');
  const input = scope.querySelector('.mobile-name-filter-input');
  const clearBtn = scope.querySelector('.mobile-name-filter-clear');
  if (!header || !slot || !btn || !input || slot.dataset.mobileNameSearchBound) return;
  slot.dataset.mobileNameSearchBound = '1';

  function closePanel(force) {
    if (nameFilterDebounceTimer) {
      clearTimeout(nameFilterDebounceTimer);
      nameFilterDebounceTimer = null;
    }
    closeMobileNameSearchPanel(scope, force);
  }

  function openPanel() {
    openMobileNameSearchPanel(scope, true);
    input.select();
  }

  btn.addEventListener('click', function (e) {
    e.stopPropagation();
    if (header.classList.contains('is-search-animating')) return;
    if (slot.classList.contains('is-open')) closePanel();
    else openPanel();
  });

  input.addEventListener('input', function () {
    if (nameFilterDebounceTimer) clearTimeout(nameFilterDebounceTimer);
    nameFilterDebounceTimer = setTimeout(function () {
      nameFilterDebounceTimer = null;
      applyNameFilter(input.value);
    }, 400);
  });

  input.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') {
      closePanel();
      return;
    }
    if (e.key === 'Enter') {
      e.preventDefault();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      applyNameFilter(input.value);
      closePanel();
    }
  });

  if (clearBtn) {
    clearBtn.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (nameFilterDebounceTimer) {
        clearTimeout(nameFilterDebounceTimer);
        nameFilterDebounceTimer = null;
      }
      input.value = '';
      await applyNameFilter('');
      closePanel(true);
    });
  }

  if (historyState.name && isMobileStackedLayout()) {
    openMobileNameSearchPanel(scope, false);
  }
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

// keeps the panel open (tracker/category picks) in sync without a full rebuild,
// which would drop the panel's open/hidden state
function refreshHistFilterTrigger(wrap) {
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

  // scroll doesn't bubble, but it does reach capture-phase ancestors; bound
  // once here since renderHistFilterMain/Group replace the body each time
  panel.addEventListener('scroll', function (e) {
    if (e.target.classList && e.target.classList.contains('hist-filter-panel-body')) {
      updateHistFilterScrollFade(wrap);
    }
  }, true);

  // mobile-only scrim behind the bottom sheet (CSS keeps it inert/invisible
  // on desktop); tapping it closes the sheet same as tapping outside it
  if (backdrop) {
    backdrop.addEventListener('click', function () {
      closePanel();
    });
  }

  function closePanel() {
    startHistFilterClose(wrap);
  }

  function openPanel() {
    closeOpenCustomSelect();
    closeOpenHistFilterSelect();
    closeOpenNameFilter();
    if (wrap.histFilterCloseTimer) {
      clearTimeout(wrap.histFilterCloseTimer);
      wrap.histFilterCloseTimer = 0;
    }
    wrap.classList.remove('is-closing');
    wrap.classList.add('open');
    trigger.setAttribute('aria-expanded', 'true');
    panel.hidden = false;
    openHistFilterSelect = wrap;
    // always starts from the main view, even if a previous session left a
    // group list open (see the click-outside/Escape-close handling below)
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

export function paginationButtons(prevId, nextId, prevDisabled, nextDisabled) {
  return html`<button type="button" class="btn btn-sm btn-ghost btn-pagination" id="${prevId}"${prevDisabled ? ' disabled' : ''} aria-label="${t('common.pagePrev')}">‹</button><button type="button" class="btn btn-sm btn-ghost btn-pagination" id="${nextId}"${nextDisabled ? ' disabled' : ''} aria-label="${t('common.pageNext')}">›</button>`;
}

export function buildPageSizeSelect(currentSize) {
  const options = PAGE_SIZE_OPTIONS.map(function (n) {
    const label = n === 0 ? t('common.all') : String(n);
    return html`<option value="${n}"${currentSize === n ? ' selected' : ''}>${label}</option>`;
  });
  return html`<select class="select select-compact page-size-select" id="hist-page-size" aria-label="${t('common.pageSize')}">${options}</select>`;
}

export function buildSegmentControl(options, activeValue, ariaLabel) {
  let activeIdx = 0;
  options.forEach(function (opt, i) {
    if (opt.value === activeValue) activeIdx = i;
  });
  const btnsParts = options.map(function (opt, i) {
    const isActive = i === activeIdx;
    return html`<button type="button" class="seg-ctrl-btn${isActive ? ' active' : ''}" data-value="${opt.value}"${opt.dataAttr || ''} title="${opt.title}" aria-label="${opt.title}" aria-pressed="${isActive ? 'true' : 'false'}">${opt.icon ? html`<span data-icon="${opt.icon}"></span>` : opt.label || ''}</button>`;
  });
  return html`<div class="seg-ctrl" role="group" aria-label="${ariaLabel}" data-active-index="${activeIdx}"><div class="seg-ctrl-thumb" aria-hidden="true"></div>${btnsParts}</div>`;
}

export function updateSegmentThumb(seg) {
  const active = seg.querySelector('.seg-ctrl-btn.active');
  if (!active) return;
  const btns = seg.querySelectorAll('.seg-ctrl-btn');
  let idx = 0;
  btns.forEach(function (b, i) {
    if (b === active) idx = i;
  });
  seg.setAttribute('data-active-index', String(idx));
  seg.style.setProperty('--seg-count', String(btns.length));
  const thumb = seg.querySelector('.seg-ctrl-thumb');
  if (thumb) thumb.style.transform = 'translateX(' + idx * 100 + '%)';
}

export function initSegmentControls(root) {
  (root || document).querySelectorAll('.seg-ctrl').forEach(function (seg) {
    if (seg.dataset.segBound) return;
    seg.dataset.segBound = '1';
    updateSegmentThumb(seg);
    seg.querySelectorAll('.seg-ctrl-btn').forEach(function (btn) {
      btn.addEventListener('click', function () {
        if (btn.classList.contains('active')) return;
        seg.querySelectorAll('.seg-ctrl-btn').forEach(function (b) {
          b.classList.remove('active');
          b.setAttribute('aria-pressed', 'false');
        });
        btn.classList.add('active');
        btn.setAttribute('aria-pressed', 'true');
        updateSegmentThumb(seg);
        seg.dispatchEvent(new CustomEvent('segchange', { bubbles: true, detail: { value: btn.getAttribute('data-value') } }));
      });
    });
  });
}

export function bindPageSizeSelect(root, onChange) {
  const sel = (root || document).querySelector('.page-size-select');
  if (!sel || sel.dataset.pageSizeBound) return;
  sel.dataset.pageSizeBound = '1';
  initCustomSelects(root);
  sel.addEventListener('change', function () {
    const n = parseInt(sel.value, 10);
    const size = PAGE_SIZE_OPTIONS.indexOf(n) >= 0 ? n : 10;
    savePageSize(size);
    onChange(size);
  });
}


export function initPasswordToggles(root) {
  (root || document).querySelectorAll('.input-password-wrap').forEach(function (wrap) {
    if (wrap.dataset.passwordInit) return;
    wrap.dataset.passwordInit = '1';
    const input = wrap.querySelector('.input');
    const btn = wrap.querySelector('.input-password-toggle');
    if (!input || !btn) return;
    btn.addEventListener('click', function () {
      const revealed = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      if (input.classList.contains('input-secure')) {
        input.classList.toggle('input-secure-revealed', !revealed);
      } else {
        input.type = revealed ? 'password' : 'text';
      }
      const nowVisible = input.classList.contains('input-secure-revealed') ||
        (!input.classList.contains('input-secure') && input.type === 'text');
      const iconEl = btn.querySelector('[data-icon]');
      if (iconEl) {
        iconEl.setAttribute('data-icon', nowVisible ? 'eyeOff' : 'eye');
        iconEl.innerHTML = ICONS[nowVisible ? 'eyeOff' : 'eye'];
      }
      btn.setAttribute('aria-label', t(nowVisible ? 'trackers.hideCredentials' : 'trackers.showCredentials'));
    });
  });
}


