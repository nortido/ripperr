import { fetchJSON, showToast } from './api.js';
import { buildHistoryQuery, dashboardFetchGen, loadDashboard, nextDashboardFetchGen, syncPlanForceLocal, updateDashboardTable } from './dashboard.js';
import { closeOpenHistFilterSelect, openHistFilterSelect, positionHistFilterPanel, renderHistFilterMain } from './histfilter.js';
import { ICONS, hydrateIcons, icon } from './icons.js';
import { syncDashboardFilterHash } from './router.js';
import { historyState, isMobileStackedLayout, overlayBridge } from './state.js';
import { PAGE_SIZE_OPTIONS, formatRunSlot, html, savePageSize } from './util.js';

export let activeErrorPopover = null;
export let activeSlotsPopover = null;
export let slotsPopoverLeaveTimer = null;

export let openCustomSelect = null;
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
overlayBridge.getOpenCustomSelect = function () { return openCustomSelect; };
overlayBridge.closeCustomSelect = closeOpenCustomSelect;

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
overlayBridge.closeNameFilter = closeOpenNameFilter;

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


